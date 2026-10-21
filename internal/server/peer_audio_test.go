package server

import (
	"testing"
	"time"

	"any_device/internal/audio"
)

// opusChunkForTest 用生产编码器生成一个带 tag 的 Opus 包（20ms 正弦音）。
func opusChunkForTest(t *testing.T, tag []byte) []byte {
	t.Helper()
	var packets [][]byte
	enc, err := audio.NewOpusEncoder(func(p []byte) {
		buf := make([]byte, 4+len(p))
		copy(buf[:4], tag)
		copy(buf[4:], p)
		packets = append(packets, buf)
	})
	if err != nil {
		t.Fatalf("创建 Opus 编码器失败: %v", err)
	}
	defer enc.Destroy()
	frame := make([]float32, 960*2)
	for i := 0; i < 960; i++ {
		frame[i*2] = 0.2
		frame[i*2+1] = 0.2
	}
	if err := enc.Feed(frame); err != nil {
		t.Fatalf("编码失败: %v", err)
	}
	if len(packets) != 1 {
		t.Fatalf("期望 1 个 Opus 包，实际 %d", len(packets))
	}
	return packets[0]
}

// TestForwardPeerAudioRoutesPCMToAudioDecoderlessClients 覆盖 Wails macOS
// WKWebView / Safari 的音频链路：这类客户端没有 WebCodecs AudioDecoder，
// 对端转发的 Opus 音频必须解码成 float32 PCM 再下发（AUPS→AUDS、
// AUPM→AUDM），而有解码能力的客户端仍收原始 Opus 包。
func TestForwardPeerAudioRoutesPCMToAudioDecoderlessClients(t *testing.T) {
	hub := NewHub(nil)
	pcmClient := &Client{send: make(chan OutMessage, 16), audioEnabled: true, audioPCM: true}
	opusClient := &Client{send: make(chan OutMessage, 16), audioEnabled: true}
	hub.clients[pcmClient] = true
	hub.clients[opusClient] = true
	go hub.Run()

	pm := NewPeerManager(nil, hub)
	pc := &peerConnection{decoders: map[string]*audio.OpusDecoder{}}
	defer func() {
		for _, d := range pc.decoders {
			d.Destroy()
		}
	}()

	opusChunk := opusChunkForTest(t, audio.TagMicAudioOpus)
	pm.forwardPeerAudio(pc, opusChunk)

	select {
	case msg := <-pcmClient.send:
		if got := string(msg.data[:4]); got != string(audio.TagMicAudio) {
			t.Fatalf("无解码能力客户端应收到 %q，实际 %q", audio.TagMicAudio, got)
		}
		// 20ms @ 48kHz 立体声 float32 = 960*2*4 字节负载
		if want := 4 + 960*2*4; len(msg.data) != want {
			t.Fatalf("PCM 负载长度错误：期望 %d，实际 %d", want, len(msg.data))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("无解码能力客户端未收到 PCM 音频")
	}

	select {
	case msg := <-opusClient.send:
		if got := string(msg.data[:4]); got != string(audio.TagMicAudioOpus) {
			t.Fatalf("有解码能力客户端应收到 %q，实际 %q", audio.TagMicAudioOpus, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("有解码能力客户端未收到 Opus 音频")
	}
}

// TestOpusOnlyBroadcastSkippedForPCMClients：本机捕获引擎只广播 Opus，
// 无解码能力的客户端不应收到无法解码的 Opus 帧（避免每帧一次失败解码）。
func TestOpusOnlyBroadcastSkippedForPCMClients(t *testing.T) {
	hub := NewHub(nil)
	pcmClient := &Client{send: make(chan OutMessage, 16), audioEnabled: true, audioPCM: true}
	opusClient := &Client{send: make(chan OutMessage, 16), audioEnabled: true}
	hub.clients[pcmClient] = true
	hub.clients[opusClient] = true
	go hub.Run()

	hub.BroadcastAudio(opusChunkForTest(t, audio.TagSystemAudioOpus))

	select {
	case msg := <-opusClient.send:
		if got := string(msg.data[:4]); got != string(audio.TagSystemAudioOpus) {
			t.Fatalf("期望 Opus，实际 %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("有解码能力客户端未收到 Opus 音频")
	}
	select {
	case msg := <-pcmClient.send:
		t.Fatalf("无解码能力客户端不应收到 Opus 帧，实际收到 %q", string(msg.data[:4]))
	case <-time.After(300 * time.Millisecond):
	}
}

// TestKibPerSec 校验 UI 码率换算：只算对端推流到本机的网络字节，
// 窗口长度按实际经过时间归一化（Opus 128kbps ≈ 16 KB/s，float32 PCM ≈ 375 KB/s）。
func TestKibPerSec(t *testing.T) {
	cases := []struct {
		name  string
		bytes int64
		secs  float64
		want  int
	}{
		{"无数据", 0, 2, 0},
		{"Opus 128kbps", 32768, 2, 16},
		{"float32 PCM 48k 立体声", 768000, 2, 375},
		{"负值", -100, 2, 0},
		{"零窗口", 32768, 0, 0},
	}
	for _, c := range cases {
		if got := kibPerSec(c.bytes, c.secs); got != c.want {
			t.Errorf("%s: kibPerSec(%d, %v) = %d，期望 %d", c.name, c.bytes, c.secs, got, c.want)
		}
	}
}

// TestSetRxRatesPerPeer：码率按对端独立写入，不能被全局值覆盖。
func TestSetRxRatesPerPeer(t *testing.T) {
	pm := NewPeerManager(nil, nil)
	pm.mappings["peer-a"] = &PeerMappingState{}
	pm.mappings["peer-b"] = &PeerMappingState{}

	pm.setRxRates("peer-a", 16, 0)
	if got := pm.mappings["peer-a"].RxSystemAudioKBps; got != 16 {
		t.Fatalf("peer-a 系统声码率 = %d，期望 16", got)
	}
	if got := pm.mappings["peer-b"].RxSystemAudioKBps; got != 0 {
		t.Fatalf("peer-b 不应被写入，实际 %d", got)
	}
}
