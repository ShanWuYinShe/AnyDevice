package server

import (
	"testing"
	"time"

	"any_device/internal/audio"
)

// TestForwardPeerAudioSkipsDecodeWithoutPCMClients：没有需要 PCM 形态的客户端时
// 转发端不解码（省 CPU 与每帧 7.7KB 分配），直接按 Opus 转发。
func TestForwardPeerAudioSkipsDecodeWithoutPCMClients(t *testing.T) {
	hub := NewHub(nil)
	opusClient := &Client{send: make(chan OutMessage, 16), sysAudioEnabled: true}
	hub.clients[opusClient] = true
	go hub.Run()

	pm := NewPeerManager(nil, hub)
	pc := &peerConnection{decoders: map[string]*audio.OpusDecoder{}}
	pm.forwardPeerAudio(pc, opusChunkForTest(t, audio.TagSystemAudioOpus))

	if len(pc.decoders) != 0 {
		t.Fatalf("无 PCM 客户端时不应创建解码器，实际 %d 个", len(pc.decoders))
	}
	select {
	case msg := <-opusClient.send:
		if got := string(msg.data[:4]); got != string(audio.TagSystemAudioOpus) {
			t.Fatalf("应收到 Opus 帧，实际 %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("有解码能力客户端未收到 Opus 音频")
	}
}

// TestForwardPeerAudioDecodesOnlyForMappedStream：PCM 客户端只订阅了系统声时，
// 麦克风流（AUPM）不应触发解码，也不应下发。
func TestForwardPeerAudioDecodesOnlyForMappedStream(t *testing.T) {
	hub := NewHub(nil)
	pcmSys := &Client{send: make(chan OutMessage, 16), sysAudioEnabled: true, audioPCM: true}
	hub.clients[pcmSys] = true
	go hub.Run()

	pm := NewPeerManager(nil, hub)
	pc := &peerConnection{decoders: map[string]*audio.OpusDecoder{}}
	defer func() {
		for _, d := range pc.decoders {
			d.Destroy()
		}
	}()

	pm.forwardPeerAudio(pc, opusChunkForTest(t, audio.TagMicAudioOpus))
	if len(pc.decoders) != 0 {
		t.Fatal("只订阅系统声时不应为麦克风流创建解码器")
	}
	select {
	case msg := <-pcmSys.send:
		t.Fatalf("未订阅麦克风的客户端不应收到麦克风帧，实际 %q", string(msg.data[:4]))
	case <-time.After(200 * time.Millisecond):
	}
}

// TestPCMClientCountByStream：按流统计需要 PCM 的订阅客户端（转发端解码门控依据）。
func TestPCMClientCountByStream(t *testing.T) {
	hub := NewHub(nil)
	hub.clients[&Client{audioPCM: true, sysAudioEnabled: true}] = true                        // 仅系统声 PCM
	hub.clients[&Client{audioPCM: true, micAudioEnabled: true}] = true                        // 仅麦克风 PCM
	hub.clients[&Client{sysAudioEnabled: true, micAudioEnabled: true}] = true                 // 无解码能力
	hub.clients[&Client{audioPCM: true, sysAudioEnabled: true, micAudioEnabled: true}] = true // 两路 PCM

	if got := hub.PCMSysAudioClientCount(); got != 2 {
		t.Fatalf("系统声 PCM 客户端数应为 2，实际 %d", got)
	}
	if got := hub.PCMMicAudioClientCount(); got != 2 {
		t.Fatalf("麦克风 PCM 客户端数应为 2，实际 %d", got)
	}
	if got := hub.PCMAudioClientCount(); got != 3 {
		t.Fatalf("PCM 客户端总数应为 3，实际 %d", got)
	}
}
