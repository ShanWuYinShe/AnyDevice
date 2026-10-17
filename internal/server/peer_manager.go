package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"any_device/internal/discovery"
	"any_device/internal/keyboard"
	"any_device/internal/mouse"
	"any_device/internal/util"

	"github.com/gorilla/websocket"
)

// PeerMappingState stores what the local user chooses to map from a specific remote peer.
type PeerMappingState struct {
	PlaySystemAudioLocal bool   `json:"play_system_audio_local"`
	PlayMicAudioLocal    bool   `json:"play_mic_audio_local"`
	ExecuteMouseLocal    bool   `json:"execute_mouse_local"`
	ExecuteKeyboardLocal bool   `json:"execute_keyboard_local"`
	ConnectionStatus     string `json:"connection_status"` // "connected", "connecting", "disconnected", "error"
	LatencyMs            int64  `json:"latency_ms"`
	LastError            string `json:"last_error,omitempty"`

	// Backward compatibility fields
	PlayAudioLocal      bool `json:"play_audio_local"`
	RemoteControlActive bool `json:"remote_control_active,omitempty"`
}

// UnifiedPeer is the clean, discoverer-centric peer model exposed to the UI.
type UnifiedPeer struct {
	ID        string                        `json:"id"`
	Name      string                        `json:"name"`
	OS        string                        `json:"os"`
	IP        string                        `json:"ip"`
	Port      int                           `json:"port"`
	Target    string                        `json:"target"`
	Online    bool                          `json:"online"`
	Provides  discovery.DeviceCapabilities  `json:"provides"`
	Mapping   PeerMappingState              `json:"mapping"`
	LastSeen  string                        `json:"last_seen"`
	IsManual  bool                          `json:"is_manual"`
}

type peerConnection struct {
	conn      *websocket.Conn
	cancel    context.CancelFunc
	startTime time.Time
}

type PeerManager struct {
	discovery   *discovery.Service
	hub         *Hub
	mappings    map[string]*PeerMappingState
	conns       map[string]*peerConnection
	mu          sync.RWMutex
}

func NewPeerManager(disc *discovery.Service, hub *Hub) *PeerManager {
	return &PeerManager{
		discovery: disc,
		hub:       hub,
		mappings:  make(map[string]*PeerMappingState),
		conns:     make(map[string]*peerConnection),
	}
}

// ListPeers returns all discovered peers with their current local mapping settings.
func (pm *PeerManager) ListPeers() []UnifiedPeer {
	discPeers := pm.discovery.GetPeers()

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]UnifiedPeer, 0, len(discPeers))
	for _, dp := range discPeers {
		if pm.discovery.IsSelf(dp.ID, dp.IP, dp.Port) || util.IsLocalIP(dp.IP) || util.IsLocalIP(dp.Target) {
			continue
		}

		mapping, exists := pm.mappings[dp.ID]
		if !exists {
			mapping = &PeerMappingState{
				ConnectionStatus: "disconnected",
			}
		}

		result = append(result, UnifiedPeer{
			ID:       dp.ID,
			Name:     dp.Name,
			OS:       dp.OS,
			IP:       dp.IP,
			Port:     dp.Port,
			Target:   dp.Target,
			Online:   dp.Online,
			Provides: dp.Provides,
			Mapping:  *mapping,
			LastSeen: dp.LastSeen.Format("15:04:05"),
			IsManual: dp.IsManual,
		})
	}
	return result
}

// AddManualPeer adds a peer by IP:port and returns it.
func (pm *PeerManager) AddManualPeer(target, name string) (UnifiedPeer, error) {
	dp := pm.discovery.AddManualPeer(target, name)
	if dp == nil {
		return UnifiedPeer{}, fmt.Errorf("无法添加该地址 (不能添加本机作为对端设备)")
	}
	return UnifiedPeer{
		ID:       dp.ID,
		Name:     dp.Name,
		OS:       dp.OS,
		IP:       dp.IP,
		Port:     dp.Port,
		Target:   dp.Target,
		Online:   dp.Online,
		Provides: dp.Provides,
		Mapping: PeerMappingState{
			ConnectionStatus: "disconnected",
		},
		LastSeen: dp.LastSeen.Format("15:04:05"),
		IsManual: true,
	}, nil
}

// RenamePeer assigns a custom name or alias to a peer.
func (pm *PeerManager) RenamePeer(peerID, newName string) error {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return fmt.Errorf("设备名称不能为空")
	}
	if !pm.discovery.RenamePeer(peerID, newName) {
		return fmt.Errorf("未找到该对端设备")
	}
	return nil
}

// SetMapping updates the discoverer's local mapping decisions for a peer.
func (pm *PeerManager) SetMapping(peerID string, playSystemAudio, playMicAudio, executeMouse, executeKeyboard *bool) error {
	pm.mu.Lock()
	mapping, exists := pm.mappings[peerID]
	if !exists {
		mapping = &PeerMappingState{
			ConnectionStatus: "disconnected",
		}
		pm.mappings[peerID] = mapping
	}

	if playSystemAudio != nil {
		mapping.PlaySystemAudioLocal = *playSystemAudio
		mapping.PlayAudioLocal = *playSystemAudio
	}
	if playMicAudio != nil {
		mapping.PlayMicAudioLocal = *playMicAudio
	}
	if executeMouse != nil {
		mapping.ExecuteMouseLocal = *executeMouse
	}
	if executeKeyboard != nil {
		mapping.ExecuteKeyboardLocal = *executeKeyboard
	}

	needsConn := mapping.PlaySystemAudioLocal || mapping.PlayMicAudioLocal || mapping.ExecuteMouseLocal || mapping.ExecuteKeyboardLocal
	pm.mu.Unlock()

	// Find peer target
	var target string
	for _, dp := range pm.discovery.GetPeers() {
		if dp.ID == peerID {
			target = dp.Target
			break
		}
	}
	if target == "" {
		return fmt.Errorf("找不到该对端设备信息")
	}

	if needsConn {
		pm.ensureConnected(peerID, target)
	} else {
		pm.disconnect(peerID)
	}

	return nil
}

func (pm *PeerManager) ensureConnected(peerID, target string) {
	pm.mu.Lock()
	if _, exists := pm.conns[peerID]; exists {
		pm.mu.Unlock()
		return
	}

	mapping := pm.mappings[peerID]
	mapping.ConnectionStatus = "connecting"
	mapping.LastError = ""
	pm.mu.Unlock()

	go pm.connectAndLoop(peerID, target)
}

func (pm *PeerManager) disconnect(peerID string) {
	pm.mu.Lock()
	connObj, exists := pm.conns[peerID]
	if exists {
		delete(pm.conns, peerID)
		if connObj.cancel != nil {
			connObj.cancel()
		}
		if connObj.conn != nil {
			_ = connObj.conn.Close()
		}
	}

	if mapping, ok := pm.mappings[peerID]; ok {
		mapping.ConnectionStatus = "disconnected"
	}
	pm.mu.Unlock()
}

func (pm *PeerManager) connectAndLoop(peerID, target string) {
	clean := strings.TrimPrefix(target, "ws://")
	clean = strings.TrimPrefix(clean, "http://")
	if !strings.Contains(clean, ":") {
		clean = clean + ":8080"
	}

	u := url.URL{Scheme: "ws", Host: clean, Path: "/ws"}
	dialer := websocket.Dialer{
		HandshakeTimeout: 4 * time.Second,
	}

	t0 := time.Now()
	conn, _, err := dialer.Dial(u.String(), nil)
	latency := time.Since(t0).Milliseconds()

	pm.mu.Lock()
	mapping, ok := pm.mappings[peerID]
	if !ok {
		pm.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		return
	}

	if err != nil {
		mapping.ConnectionStatus = "error"
		mapping.LastError = fmt.Sprintf("连接失败: %v", err)
		pm.mu.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	connObj := &peerConnection{
		conn:      conn,
		cancel:    cancel,
		startTime: time.Now(),
	}
	pm.conns[peerID] = connObj
	mapping.ConnectionStatus = "connected"
	mapping.LatencyMs = latency
	mapping.LastError = ""
	pm.mu.Unlock()

	defer func() {
		cancel()
		_ = conn.Close()
		pm.mu.Lock()
		delete(pm.conns, peerID)
		if m, ok := pm.mappings[peerID]; ok {
			if m.PlaySystemAudioLocal || m.PlayMicAudioLocal || m.ExecuteMouseLocal || m.ExecuteKeyboardLocal {
				m.ConnectionStatus = "disconnected"
			}
		}
		pm.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := conn.ReadMessage()
		if err != nil {
			pm.mu.Lock()
			if m, ok := pm.mappings[peerID]; ok {
				m.ConnectionStatus = "error"
				m.LastError = fmt.Sprintf("连接中断: %v", err)
			}
			pm.mu.Unlock()
			return
		}

		pm.mu.RLock()
		curMapping := *pm.mappings[peerID]
		pm.mu.RUnlock()

		if msgType == websocket.BinaryMessage {
			if len(data) >= 4 {
				tag := string(data[:4])
				if tag == "AUDS" {
					if curMapping.PlaySystemAudioLocal {
						pm.hub.BroadcastAudio(data)
					}
				} else if tag == "AUDM" {
					if curMapping.PlayMicAudioLocal {
						pm.hub.BroadcastAudio(data)
					}
				} else {
					// Fallback for legacy untagged stream
					if curMapping.PlaySystemAudioLocal || curMapping.PlayMicAudioLocal {
						pm.hub.BroadcastAudio(data)
					}
				}
			}
		} else if msgType == websocket.TextMessage {
			var baseEvt struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &baseEvt)

			if (baseEvt.Type == "" || baseEvt.Type == "mouse") && curMapping.ExecuteMouseLocal {
				var evt mouse.MouseEvent
				if err := json.Unmarshal(data, &evt); err == nil {
					// Execute remote mouse movements & clicks directly on local OS!
					mouse.SimulateAbsolute(evt.X, evt.Y)
					if evt.Action == "down" || evt.Action == "up" {
						btn := "left"
						if evt.RightDown {
							btn = "right"
						} else if evt.MiddleDown {
							btn = "middle"
						}
						mouse.SimulateButton(btn, evt.Action == "down")
					}
				}

				// Also forward with source metadata so local UI can visualize cursor position
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					obj["source_peer"] = target
					obj["peer_id"] = peerID
					if enriched, err := json.Marshal(obj); err == nil {
						data = enriched
					}
				}
				pm.hub.BroadcastMouse(data)
			} else if baseEvt.Type == "keyboard" && curMapping.ExecuteKeyboardLocal {
				var evt keyboard.KeyEvent
				if err := json.Unmarshal(data, &evt); err == nil {
					// Execute remote physical keyboard events directly on local OS!
					keyboard.SimulateKey(evt.Key, evt.Action == "down")
				}

				// Also forward with source metadata so local UI can visualize active key telemetry
				var obj map[string]interface{}
				if err := json.Unmarshal(data, &obj); err == nil {
					obj["source_peer"] = target
					obj["peer_id"] = peerID
					if enriched, err := json.Marshal(obj); err == nil {
						data = enriched
					}
				}
				pm.hub.BroadcastKeyboard(data)
			}
		}
	}
}

// SendRemoteControl sends a control command to a remote peer.
func (pm *PeerManager) SendRemoteControl(peerID string, cmd interface{}) error {
	pm.mu.RLock()
	connObj, exists := pm.conns[peerID]
	pm.mu.RUnlock()

	if !exists || connObj == nil || connObj.conn == nil {
		return fmt.Errorf("尚未与该对端建立控制连接")
	}

	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}

	return connObj.conn.WriteMessage(websocket.TextMessage, data)
}
