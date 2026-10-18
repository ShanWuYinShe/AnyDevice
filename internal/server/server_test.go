package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPeerManagerAndAPIs(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	s := NewServer(8080, "127.0.0.1", hub, nil, nil, nil, nil)
	handler := s.SetupRoutes()

	// 1. GET /api/self
	req := httptest.NewRequest(http.MethodGet, "/api/self", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/self, got %d", rec.Code)
	}
	// 回归：音频协议标识必须与真实链路一致（捕获链路统一 Opus）
	var selfResp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &selfResp); err != nil {
		t.Fatalf("decode /api/self: %v", err)
	}
	if got := selfResp["audio_format"]; got != "opus-48000-stereo" {
		t.Fatalf("expected audio_format 'opus-48000-stereo', got %v", got)
	}

	// 2. POST /api/self
	f := false
	selfBody, _ := json.Marshal(map[string]interface{}{
		"system_audio": f,
		"mic_audio":    true,
		"mouse":        true,
		"keyboard":     true,
		"control":      f,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/self", bytes.NewReader(selfBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/self, got %d", rec.Code)
	}
	// 保存共享设置会重写广播能力：协议标识不得被打回旧的 f32le
	req = httptest.NewRequest(http.MethodGet, "/api/self", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	selfResp = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &selfResp); err != nil {
		t.Fatalf("decode /api/self after POST: %v", err)
	}
	if provides, ok := selfResp["provides"].(map[string]interface{}); !ok || provides["audio_format"] != "opus-48000-stereo" {
		t.Fatalf("expected provides.audio_format 'opus-48000-stereo' after POST /api/self, got %v", selfResp["provides"])
	}

	// 3. POST /api/peers/add (try adding self - must fail!)
	selfAddBody, _ := json.Marshal(map[string]interface{}{
		"target": "127.0.0.1:8080",
		"name":   "My-Self",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/peers/add", bytes.NewReader(selfAddBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request when adding self, got %d", rec.Code)
	}

	// 3b. POST /api/peers/add (external peer)
	addBody, _ := json.Marshal(map[string]interface{}{
		"target": "192.168.1.200:8080",
		"name":   "Remote-Office-PC",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/peers/add", bytes.NewReader(addBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/peers/add, got %d", rec.Code)
	}

	// 4. GET /api/peers
	req = httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from GET /api/peers, got %d", rec.Code)
	}
	var peersResp struct {
		Peers []UnifiedPeer `json:"peers"`
		Total int           `json:"total"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&peersResp)
	if peersResp.Total != 1 {
		t.Fatalf("expected 1 peer, got %d", peersResp.Total)
	}
	p := peersResp.Peers[0]
	if p.Name != "Remote-Office-PC" {
		t.Fatalf("expected Remote-Office-PC, got %s", p.Name)
	}

	// 5. POST /api/peers/map (test separate audio, mouse and keyboard mapping)
	tr := true
	mapBody, _ := json.Marshal(map[string]interface{}{
		"peer_id":           p.ID,
		"play_system_audio": tr,
		"play_mic_audio":    f,
		"execute_mouse":     tr,
		"execute_keyboard":  tr,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/peers/map", bytes.NewReader(mapBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/peers/map, got %d", rec.Code)
	}

	// 6. POST /api/peers/rename
	renameBody, _ := json.Marshal(map[string]interface{}{
		"peer_id": p.ID,
		"name":    "会议室主屏电脑",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/peers/rename", bytes.NewReader(renameBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from POST /api/peers/rename, got %d", rec.Code)
	}

	// Verify rename in GET /api/peers
	req = httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var peersResp2 struct {
		Peers []UnifiedPeer `json:"peers"`
		Total int           `json:"total"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&peersResp2)
	if len(peersResp2.Peers) == 0 || peersResp2.Peers[0].Name != "会议室主屏电脑" {
		t.Fatalf("expected 会议室主屏电脑, got %+v", peersResp2.Peers)
	}
	if peersResp2.Peers[0].Alias != "会议室主屏电脑" {
		t.Fatalf("expected alias persisted on unified peer, got %+v", peersResp2.Peers[0])
	}
}

// TestViewerSelfFiltering 回归测试：手机/外部浏览器打开 UI 时，
// 自己的入站条目不得出现在自己的设备列表里（用户报告的“列表一直显示自己”）。
func TestViewerSelfFiltering(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	s := NewServer(8080, "127.0.0.1", hub, nil, nil, nil, nil)
	handler := s.SetupRoutes()

	phone := s.discovery.RegisterInboundClient("client-x", "203.0.113.7:51000", "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X)")
	if phone == nil {
		t.Fatal("expected inbound registration for external device")
	}

	// 手机自己的视角：必须看不到自己
	req2 := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	req2.RemoteAddr = "203.0.113.7:51002"
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)
	var fromPhone struct {
		Peers []UnifiedPeer `json:"peers"`
		Total int           `json:"total"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&fromPhone); err != nil {
		t.Fatalf("decode peers failed: %v", err)
	}
	if fromPhone.Total != 0 {
		t.Fatalf("viewer must not see itself, got %d peers", fromPhone.Total)
	}

	// 其他设备的视角：应看到这台手机
	req3 := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	req3.RemoteAddr = "198.51.100.9:6000"
	rec3 := httptest.NewRecorder()
	handler.ServeHTTP(rec3, req3)
	var fromOther struct {
		Peers []UnifiedPeer `json:"peers"`
		Total int           `json:"total"`
	}
	if err := json.NewDecoder(rec3.Body).Decode(&fromOther); err != nil {
		t.Fatalf("decode peers failed: %v", err)
	}
	if fromOther.Total != 1 || fromOther.Peers[0].ID != phone.ID {
		t.Fatalf("other viewer should see the phone once, got %+v", fromOther)
	}
}

// TestMappingHandshake 校验订阅声明内容：音频取系统声/麦克风的并集，
// 并携带本机节点身份（供对端归并设备档案）。
func TestMappingHandshake(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	s := NewServer(8080, "127.0.0.1", hub, nil, nil, nil, nil)
	m := PeerMappingState{PlaySystemAudioLocal: true, ExecuteKeyboardLocal: true}
	h := s.peerMgr.mappingHandshake(m)
	if h["audio"] != true || h["mouse"] != false || h["keyboard"] != true {
		t.Fatalf("unexpected handshake: %+v", h)
	}
	if h["node_id"] == "" || h["node_name"] == "" {
		t.Fatalf("handshake must carry node identity: %+v", h)
	}
	m3 := PeerMappingState{}
	h3 := s.peerMgr.mappingHandshake(m3)
	if h3["audio"] != false || h3["mouse"] != false || h3["keyboard"] != false {
		t.Fatalf("empty mapping must subscribe nothing: %+v", h3)
	}
}
