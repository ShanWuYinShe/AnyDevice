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
	// 编码参数/档位：引擎未启动时也应上报默认平衡档（旧前端忽略新增字段）
	if msg.Sys.Bitrate != 128000 || msg.Sys.Complexity != 5 || msg.Sys.Level != 1 {
		t.Fatalf("编码参数默认值应为平衡档 128000/5/1，实际 %+v", msg.Sys)
	}
}

// TestApplySenderLatencyAndNetRtt 验证对端上报的延迟分项/RTT 写入映射状态，
// 非法 JSON 不覆盖旧值、不 panic。
func TestApplySenderLatencyAndNetRtt(t *testing.T) {
	pm := NewPeerManager(nil, nil)
	pm.mappings["p"] = &PeerMappingState{}

	pm.applySenderLatency("p", []byte(`{"type":"audio_stats","sys":{"chunk_ms":10,"encode_ms":24,"send_ms":1,"running":true,"bitrate":96000,"complexity":3,"level":0},"mic":{"chunk_ms":10,"encode_ms":22,"send_ms":1,"running":false,"bitrate":160000,"complexity":7,"level":2}}`))
	m := pm.mappings["p"]
	if m.SenderSysLatency == nil || !m.SenderSysLatency.Running {
		t.Fatalf("sys 延迟分项解析错误: %+v", m.SenderSysLatency)
	}
	if m.SenderSysLatency.ChunkMs != 10 || m.SenderSysLatency.EncodeMs != 24 {
		t.Fatalf("sys 延迟数值错误: %+v", m.SenderSysLatency)
	}
	if m.SenderSysLatency.Bitrate != 96000 || m.SenderSysLatency.Complexity != 3 || m.SenderSysLatency.Level != 0 {
		t.Fatalf("sys 编码参数解析错误: %+v", m.SenderSysLatency)
	}
	if m.SenderMicLatency == nil || m.SenderMicLatency.Running {
		t.Fatalf("mic 延迟分项解析错误: %+v", m.SenderMicLatency)
	}
	if m.SenderMicLatency.Bitrate != 160000 || m.SenderMicLatency.Complexity != 7 || m.SenderMicLatency.Level != 2 {
		t.Fatalf("mic 编码参数解析错误: %+v", m.SenderMicLatency)
	}

	// 旧版对端（无编码参数字段）：新字段为零值，不 panic、不改变旧字段语义
	pm.mappings["old"] = &PeerMappingState{}
	pm.applySenderLatency("old", []byte(`{"type":"audio_stats","sys":{"chunk_ms":10,"encode_ms":24,"send_ms":1,"running":true}}`))
	if old := pm.mappings["old"].SenderSysLatency; old == nil || old.Bitrate != 0 || old.Complexity != 0 || old.Level != 0 {
		t.Fatalf("旧版对端编码参数应缺省为零值: %+v", old)
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

// TestStatusExposesEncoderAdaptiveFields 校验 /api/status 暴露发送端 Opus
// 编码参数、自适应档位与累计丢帧（sys/mic 两路），供 UI/验证脚本读取；
// 新增字段是向后兼容的（旧前端忽略未知 key）。
func TestStatusExposesEncoderAdaptiveFields(t *testing.T) {
	hub := NewHub(func(sys, mic, mouse, kb int) {})
	sysEngine := audio.NewEngine(audio.KindSystem, audio.ModeCapture, -1, func([]byte) {})
	micEngine := audio.NewEngine(audio.KindMic, audio.ModeCapture, -1, func([]byte) {})
	s := NewServer(0, "127.0.0.1", hub, sysEngine, micEngine, nil, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	s.SetupRoutes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/api/status 状态码异常: %d", rec.Code)
	}
	var status map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("解析 /api/status 失败: %v", err)
	}
	for _, key := range []string{
		"sys_audio_bitrate", "sys_audio_complexity", "sys_audio_adaptive_level", "sys_audio_dropped",
		"mic_audio_bitrate", "mic_audio_complexity", "mic_audio_adaptive_level", "mic_audio_dropped",
	} {
		if _, ok := status[key]; !ok {
			t.Fatalf("/api/status 缺少字段 %q: %v", key, status)
		}
	}
	// 未启动时为默认平衡档：128000 bit/s、复杂度 5、档位 1、丢帧 0
	if got := status["sys_audio_bitrate"]; got != float64(128000) {
		t.Fatalf("sys_audio_bitrate 默认应为 128000，实际 %v", got)
	}
	if got := status["mic_audio_complexity"]; got != float64(5) {
		t.Fatalf("mic_audio_complexity 默认应为 5，实际 %v", got)
	}
	if got := status["mic_audio_adaptive_level"]; got != float64(1) {
		t.Fatalf("mic_audio_adaptive_level 默认应为 1（平衡档），实际 %v", got)
	}
	if got := status["sys_audio_dropped"]; got != float64(0) {
		t.Fatalf("sys_audio_dropped 初始应为 0，实际 %v", got)
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
