package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHubInboundBatch(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	// Fake inbound clients
	c1 := &Client{
		id:           "client-1",
		hub:          hub,
		remoteAddr:   "192.168.1.50:5000",
		deviceType:   "Chrome / macOS",
		joinedAt:     time.Now(),
		audioEnabled: true,
		mouseEnabled: true,
		keyboardEnabled: true,
		allowControl: false,
		send:         make(chan OutMessage, 10),
	}
	c2 := &Client{
		id:           "client-2",
		hub:          hub,
		remoteAddr:   "192.168.1.51:5000",
		deviceType:   "Safari / iOS (iPhone)",
		joinedAt:     time.Now(),
		audioEnabled: true,
		mouseEnabled: true,
		keyboardEnabled: true,
		allowControl: false,
		send:         make(chan OutMessage, 10),
	}

	hub.clients[c1] = true
	hub.clients[c2] = true

	if count := len(hub.GetInboundClients()); count != 2 {
		t.Fatalf("expected 2 clients, got %d", count)
	}

	// Test UpdateAllInboundClients - disable audio for all
	f := false
	hub.UpdateAllInboundClients(&f, nil, nil, nil)

	clients := hub.GetInboundClients()
	for _, c := range clients {
		if c.AudioEnabled {
			t.Errorf("expected client %s audio to be disabled", c.ID)
		}
		if !c.MouseEnabled {
			t.Errorf("expected client %s mouse to still be enabled", c.ID)
		}
	}

	// Test UpdateAllInboundClients - enable allowControl for all
	tr := true
	hub.UpdateAllInboundClients(nil, nil, nil, &tr)
	clients = hub.GetInboundClients()
	for _, c := range clients {
		if !c.AllowControl {
			t.Errorf("expected client %s allowControl to be enabled", c.ID)
		}
	}

	// Test DisconnectAllInboundClients (safely closes connections without panic)
	hub.DisconnectAllInboundClients()
}

func TestOutboundBatch(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	mgr := NewOutboundManager(hub)

	// Add manual peers
	p1 := &OutboundPeer{
		ID:           "peer-1",
		Target:       "192.168.1.100:8080",
		Status:       "connected",
		ReceiveAudio: true,
		ReceiveMouse: true,
		SendMouse:    false,
		cancel:       func() {},
	}
	p2 := &OutboundPeer{
		ID:           "peer-2",
		Target:       "192.168.1.101:8080",
		Status:       "connected",
		ReceiveAudio: true,
		ReceiveMouse: false,
		SendMouse:    false,
		cancel:       func() {},
	}

	mgr.mu.Lock()
	mgr.peers[p1.ID] = p1
	mgr.peers[p2.ID] = p2
	mgr.mu.Unlock()

	if peers := mgr.ListPeers(); len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	// Test UpdateAll - toggle receive audio off
	f := false
	mgr.UpdateAll(&f, nil, nil)
	peers := mgr.ListPeers()
	for _, p := range peers {
		if p.ReceiveAudio {
			t.Errorf("expected peer %s ReceiveAudio to be false", p.ID)
		}
	}

	// Test UpdateAll - toggle send mouse on
	tr := true
	mgr.UpdateAll(nil, nil, &tr)
	peers = mgr.ListPeers()
	for _, p := range peers {
		if !p.SendMouse {
			t.Errorf("expected peer %s SendMouse to be true", p.ID)
		}
	}

	// Test DisconnectAll
	mgr.DisconnectAll()
	if peers := mgr.ListPeers(); len(peers) != 0 {
		t.Fatalf("expected 0 peers after DisconnectAll, got %d", len(peers))
	}
}

func TestBatchAPIs(t *testing.T) {
	hub := NewHub(func(a, m, k int) {})
	s := NewServer(8080, "127.0.0.1", hub, nil, nil, nil, nil)
	handler := s.SetupRoutes()

	// 1. POST /api/inbound/batch
	batchBody, _ := json.Marshal(map[string]interface{}{
		"action": "audio_off",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/inbound/batch", bytes.NewReader(batchBody))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/inbound/batch, got %d", rec.Code)
	}

	// 2. POST /api/outbound/batch
	outboundBody, _ := json.Marshal(map[string]interface{}{
		"action": "audio_off",
	})
	req2 := httptest.NewRequest(http.MethodPost, "/api/outbound/batch", bytes.NewReader(outboundBody))
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /api/outbound/batch, got %d", rec2.Code)
	}
}

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

	// 3. POST /api/peers/add
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
}
