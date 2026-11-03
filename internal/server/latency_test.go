package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"any_device/internal/audio"
	"any_device/internal/discovery"
)

// TestBuildAudioLatencyStats 验证发送端延迟统计消息的 JSON 结构：
// 只包含有订阅者的流，字段名与前端契约一致。
func TestBuildAudioLatencyStats(t *testing.T) {
	hub := NewHub(func(sys, mic, mouse, kb int) {})
	sysEngine := audio.NewEngine(audio.KindSystem, audio.ModeCapture, -1, func([]byte) {})
	s := NewServer(0, "127.0.0.1", hub, sysEngine, nil, nil, nil)

	data := s.buildAudioLatencyStats(true, false)
	if data == nil {
		t.Fatal("应生成音频延迟统计 JSON")
	}
	var msg struct {
		Type string              `json:"type"`
		Sys  *SenderLatencyStats `json:"sys"`
		Mic  *SenderLatencyStats `json:"mic"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("解析失败: %v (%s)", err, data)
	}
	if msg.Type != "audio_stats" {
		t.Fatalf("type 应为 audio_stats，实际 %q", msg.Type)
	}
	if msg.Sys == nil || msg.Mic != nil {
		t.Fatalf("只应包含 sys 流: %+v", msg)
	}
	if msg.Sys.Running {
		t.Fatal("引擎未 Start 时 running 应为 false")
	}
}

// TestApplySenderLatencyAndNetRtt 验证对端上报的延迟分项/RTT 写入映射状态，
// 非法 JSON 不覆盖旧值、不 panic。
func TestApplySenderLatencyAndNetRtt(t *testing.T) {
	pm := NewPeerManager(nil, nil)
	pm.mappings["p"] = &PeerMappingState{}

	pm.applySenderLatency("p", []byte(`{"type":"audio_stats","sys":{"chunk_ms":10,"encode_ms":24,"send_ms":1,"running":true},"mic":{"chunk_ms":10,"encode_ms":22,"send_ms":1,"running":false}}`))
	m := pm.mappings["p"]
	if m.SenderSysLatency == nil || !m.SenderSysLatency.Running {
		t.Fatalf("sys 延迟分项解析错误: %+v", m.SenderSysLatency)
	}
	if m.SenderSysLatency.ChunkMs != 10 || m.SenderSysLatency.EncodeMs != 24 {
		t.Fatalf("sys 延迟数值错误: %+v", m.SenderSysLatency)
	}
	if m.SenderMicLatency == nil || m.SenderMicLatency.Running {
		t.Fatalf("mic 延迟分项解析错误: %+v", m.SenderMicLatency)
	}

	pm.setNetRtt("p", 4)
	if m.NetRttMs != 4 {
		t.Fatalf("RTT 应写入 4，实际 %d", m.NetRttMs)
	}

	// 非法 JSON：保持旧值、不 panic
	pm.applySenderLatency("p", []byte(`not-json`))
	if m.SenderSysLatency == nil || m.SenderSysLatency.ChunkMs != 10 {
		t.Fatal("非法 JSON 不应覆盖已有延迟分项")
	}
}

// TestProbeManualPeerFillsCapabilities 验证「手动添加 IP」会异步拉取对端 /api/self
// 补全能力声明（此前 audio_format 为空会触发协议防呆、禁用音频映射）与真实 OS。
func TestProbeManualPeerFillsCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/self" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"os":"windows","provides":{"system_audio":true,"audio_format":"f32le-48000-stereo","audio":true}}`))
	}))
	defer srv.Close()

	target := strings.TrimPrefix(srv.URL, "http://")
	disc := discovery.NewService(0, "127.0.0.1", discovery.DeviceCapabilities{})
	pm := NewPeerManager(disc, nil)
	peer, err := pm.AddManualPeer(target, "探测对端")
	if err != nil {
		t.Fatalf("添加手动对端失败: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range disc.GetPeers() {
			if p.ID == peer.ID {
				if p.Provides.SystemAudio && p.Provides.AudioFormat == "f32le-48000-stereo" {
					if p.OS != "windows" {
						t.Fatalf("探测后 OS 应更新为 windows，实际 %q", p.OS)
					}
					return // 探测生效
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("手动对端 provides 未被 /api/self 探测补全: %+v", disc.GetPeers())
}

// TestEwmaInt64 验证 1/4 步长平滑：首样本直接写入，后续按 (prev*3+cur)/4。
func TestEwmaInt64(t *testing.T) {
	var v atomic.Int64
	ewmaInt64(&v, 100)
	if v.Load() != 100 {
		t.Fatalf("首样本应为 100，实际 %d", v.Load())
	}
	ewmaInt64(&v, 200)
	if got := v.Load(); got != 125 { // (100*3+200)/4
		t.Fatalf("平滑值应为 125，实际 %d", got)
	}
	ewmaInt64(&v, -5)               // 负值归零
	if got := v.Load(); got != 93 { // (125*3+0)/4 = 93
		t.Fatalf("负值应归零后平滑为 93，实际 %d", got)
	}
}
