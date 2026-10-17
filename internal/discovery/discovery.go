package discovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"any_device/internal/util"
)

const (
	DiscoveryPort   = 38271
	MulticastGroup  = "239.255.77.88"
	BeaconInterval  = 2 * time.Second
	PeerTimeoutSecs = 7
)

// DeviceCapabilities describes what virtual/physical devices this endpoint provides to others.
type DeviceCapabilities struct {
	SystemAudio bool   `json:"system_audio"`
	MicAudio    bool   `json:"mic_audio"`
	Mouse       bool   `json:"mouse"`
	Keyboard    bool   `json:"keyboard"`
	Control     bool   `json:"control"`
	AudioDevice string `json:"audio_device,omitempty"`
	MicDevice   string `json:"mic_device,omitempty"`
	Audio       bool   `json:"audio"` // Backward compatibility
}

// Beacon is the broadcast announcement packet.
type Beacon struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	OS        string             `json:"os"`
	IP        string             `json:"ip"`
	Port      int                `json:"port"`
	Provides  DeviceCapabilities `json:"provides"`
	Timestamp int64              `json:"timestamp"`
}

// DiscoveredPeer represents an active peer in the local mesh network.
type DiscoveredPeer struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	OS        string             `json:"os"`
	IP        string             `json:"ip"`
	Port      int                `json:"port"`
	Target    string             `json:"target"`
	Provides  DeviceCapabilities `json:"provides"`
	LastSeen  time.Time          `json:"last_seen"`
	IsManual  bool               `json:"is_manual"`
	Online    bool               `json:"online"`
}

type Service struct {
	id          string
	name        string
	port        int
	currentIP   string
	provides    DeviceCapabilities
	peers       map[string]*DiscoveredPeer
	mu          sync.RWMutex
	cancel      context.CancelFunc
	onPeerFound func(peer DiscoveredPeer)
}

func GenerateNodeID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func DefaultNodeName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "AnyDevice-Host"
	}
	osName := runtime.GOOS
	switch osName {
	case "darwin":
		osName = "macOS"
	case "windows":
		osName = "Windows"
	case "linux":
		osName = "Linux"
	}
	return fmt.Sprintf("%s (%s)", hostname, osName)
}

func NewService(port int, initialIP string, provides DeviceCapabilities) *Service {
	return &Service{
		id:        GenerateNodeID(),
		name:      DefaultNodeName(),
		port:      port,
		currentIP: initialIP,
		provides:  provides,
		peers:     make(map[string]*DiscoveredPeer),
	}
}

func (s *Service) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

func (s *Service) Name() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.name
}

func (s *Service) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(name) != "" {
		s.name = strings.TrimSpace(name)
	}
}

func (s *Service) SetCurrentIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentIP = ip
}

func (s *Service) Port() int {
	return s.port
}

func (s *Service) GetCurrentIP() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentIP
}

func (s *Service) isSelfUnlocked(id, ip string, port int) bool {
	if id != "" && id == s.id {
		return true
	}
	if port == s.port {
		if ip == s.currentIP || util.IsLocalIP(ip) {
			return true
		}
	}
	if util.IsLocalIP(ip) {
		return true
	}
	return false
}

func (s *Service) IsSelf(id, ip string, port int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isSelfUnlocked(id, ip, port)
}

func (s *Service) UpdateProvides(systemAudio, micAudio, mouse, keyboard, control bool, sysDev, micDev string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.provides = DeviceCapabilities{
		SystemAudio: systemAudio,
		MicAudio:    micAudio,
		Mouse:       mouse,
		Keyboard:    keyboard,
		Control:     control,
		AudioDevice: sysDev,
		MicDevice:   micDev,
		Audio:       systemAudio,
	}
}

func (s *Service) GetProvides() DeviceCapabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.provides
}

func (s *Service) Start() {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.mu.Unlock()

	go s.listenLoop(ctx)
	go s.broadcastLoop(ctx)
	go s.pruneLoop(ctx)
}

func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

func (s *Service) broadcastLoop(ctx context.Context) {
	ticker := time.NewTicker(BeaconInterval)
	defer ticker.Stop()

	// Prepare UDP broadcast destination
	bcastAddr := &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: DiscoveryPort,
	}

	conn, err := net.DialUDP("udp4", nil, bcastAddr)
	if err != nil {
		fmt.Printf("[Discovery] UDP broadcast dial warning: %v\n", err)
	} else {
		defer conn.Close()
	}

	// Also prepare multicast
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP(MulticastGroup),
		Port: DiscoveryPort,
	}
	mconn, _ := net.DialUDP("udp4", nil, mcastAddr)
	if mconn != nil {
		defer mconn.Close()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.RLock()
			beacon := Beacon{
				ID:        s.id,
				Name:      s.name,
				OS:        runtime.GOOS,
				IP:        s.currentIP,
				Port:      s.port,
				Provides:  s.provides,
				Timestamp: time.Now().UnixMilli(),
			}
			s.mu.RUnlock()

			data, err := json.Marshal(beacon)
			if err != nil {
				continue
			}

			if conn != nil {
				_, _ = conn.Write(data)
			}
			if mconn != nil {
				_, _ = mconn.Write(data)
			}
		}
	}
}

func (s *Service) listenLoop(ctx context.Context) {
	// Try listening on Multicast first, fallback to standard broadcast
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP(MulticastGroup),
		Port: DiscoveryPort,
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, mcastAddr)
	if err != nil {
		// Fallback to unicast / broadcast listener
		listenAddr := &net.UDPAddr{
			IP:   net.IPv4zero,
			Port: DiscoveryPort,
		}
		conn, err = net.ListenUDP("udp4", listenAddr)
		if err != nil {
			fmt.Printf("[Discovery] Notice: Auto-discovery port %d unavailable (%v); manual IP connection active.\n", DiscoveryPort, err)
			return
		}
	}
	defer conn.Close()

	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := conn.ReadFrom(buf)
		if err != nil {
			continue
		}

		var b Beacon
		if err := json.Unmarshal(buf[:n], &b); err != nil {
			continue
		}

		// Skip self announcement
		if s.IsSelf(b.ID, b.IP, b.Port) {
			continue
		}

		// Ensure peer IP is populated
		peerIP := b.IP
		if peerIP == "" || util.IsLocalIP(peerIP) {
			if udpAddr, ok := remoteAddr.(*net.UDPAddr); ok {
				peerIP = udpAddr.IP.String()
			}
		}
		if s.IsSelf(b.ID, peerIP, b.Port) {
			continue
		}
		peerTarget := fmt.Sprintf("%s:%d", peerIP, b.Port)

		s.mu.Lock()
		p, exists := s.peers[b.ID]
		if !exists {
			p = &DiscoveredPeer{
				ID:       b.ID,
				Name:     b.Name,
				OS:       b.OS,
				IP:       peerIP,
				Port:     b.Port,
				Target:   peerTarget,
				Provides: b.Provides,
				LastSeen: time.Now(),
				Online:   true,
			}
			s.peers[b.ID] = p
			if s.onPeerFound != nil {
				go s.onPeerFound(*p)
			}
		} else {
			p.Name = b.Name
			p.IP = peerIP
			p.Port = b.Port
			p.Target = peerTarget
			p.Provides = b.Provides
			p.LastSeen = time.Now()
			p.Online = true
		}
		s.mu.Unlock()
	}
}

func (s *Service) pruneLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			s.mu.Lock()
			for id, p := range s.peers {
				if now.Sub(p.LastSeen) > PeerTimeoutSecs*time.Second {
					p.Online = false
					// If offline for over 60 seconds and not manual, prune it
					if !p.IsManual && now.Sub(p.LastSeen) > 60*time.Second {
						delete(s.peers, id)
					}
				}
			}
			s.mu.Unlock()
		}
	}
}

func (s *Service) AddManualPeer(target string, name string) *DiscoveredPeer {
	s.mu.Lock()
	defer s.mu.Unlock()

	target = strings.TrimSpace(target)
	if !strings.Contains(target, ":") {
		target = target + fmt.Sprintf(":%d", s.port)
	}

	parts := strings.Split(target, ":")
	ip := parts[0]
	port := s.port
	fmt.Sscanf(parts[1], "%d", &port)

	if s.isSelfUnlocked("", ip, port) || util.IsLocalIP(ip) || util.IsLocalIP(target) {
		return nil
	}

	id := "manual-" + hex.EncodeToString([]byte(target))
	peerName := name
	if peerName == "" {
		peerName = fmt.Sprintf("外部主机 (%s)", target)
	}

	p := &DiscoveredPeer{
		ID:       id,
		Name:     peerName,
		OS:       "remote",
		IP:       ip,
		Port:     port,
		Target:   target,
		Provides: DeviceCapabilities{
			SystemAudio: true,
			MicAudio:    true,
			Mouse:       true,
			Keyboard:    true,
			Control:     true,
			Audio:       true,
		},
		LastSeen: time.Now(),
		IsManual: true,
		Online:   true,
	}
	s.peers[id] = p
	return p
}

func (s *Service) RegisterInboundClient(id, remoteAddr, userAgent string) *DiscoveredPeer {
	s.mu.Lock()
	defer s.mu.Unlock()

	ip := remoteAddr
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		ip = host
	}
	ip = strings.Trim(ip, "[]")

	// Never register local UI or loopback connections as an external peer!
	if util.IsLocalIP(ip) || s.isSelfUnlocked(id, ip, s.port) {
		return nil
	}

	friendlyName := "外部终端"
	osName := "web"
	if strings.Contains(userAgent, "iPhone") {
		friendlyName = "苹果手机 (iPhone)"
		osName = "iOS"
	} else if strings.Contains(userAgent, "iPad") {
		friendlyName = "苹果平板 (iPad)"
		osName = "iPadOS"
	} else if strings.Contains(userAgent, "Android") {
		friendlyName = "安卓设备 (Android)"
		osName = "Android"
	} else if strings.Contains(userAgent, "Macintosh") {
		friendlyName = "Mac 电脑"
		osName = "macOS"
	} else if strings.Contains(userAgent, "Windows") {
		friendlyName = "Windows 电脑"
		osName = "Windows"
	}

	p, exists := s.peers[id]
	if !exists {
		p = &DiscoveredPeer{
			ID:       id,
			Name:     fmt.Sprintf("%s [%s]", friendlyName, ip),
			OS:       osName,
			IP:       ip,
			Port:     s.port,
			Target:   remoteAddr,
			Provides: DeviceCapabilities{
				SystemAudio: false,
				MicAudio:    false,
				Mouse:       false,
				Keyboard:    false,
				Control:     true,
				Audio:       false,
			},
			LastSeen: time.Now(),
			IsManual: false,
			Online:   true,
		}
		s.peers[id] = p
	} else {
		p.LastSeen = time.Now()
		p.Online = true
	}
	return p
}

func (s *Service) RenamePeer(id, newName string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, exists := s.peers[id]
	if !exists {
		return false
	}
	newName = strings.TrimSpace(newName)
	if newName != "" {
		p.Name = newName
	}
	return true
}

func (s *Service) GetPeers() []DiscoveredPeer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]DiscoveredPeer, 0, len(s.peers))
	for _, p := range s.peers {
		// Strictly filter out self and local loopback/interface IPs
		if s.isSelfUnlocked(p.ID, p.IP, p.Port) || util.IsLocalIP(p.IP) || util.IsLocalIP(p.Target) {
			continue
		}
		result = append(result, *p)
	}
	return result
}
