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

	// 启动突发广播：新节点立即发包并短间隔补发，让对端在 1 秒内感知到本机，
	// 而不是等满一个 BeaconInterval（用户反馈“发现设备很慢”的主因之一）。
	BurstCount    = 3
	BurstInterval = 400 * time.Millisecond

	// 监听 socket 建立失败（如端口被其他进程临时占用）后的重试间隔。
	// 之前监听失败会直接退出且永不恢复，表现为“永远发现不了设备”。
	listenRetryInterval = 5 * time.Second

	// 入站（浏览器/手机 UI）peer 使用按来源 IP 的稳定 ID：同一设备刷新页面或
	// 重连 WebSocket 不会在列表里堆积重复条目（旧的 client-N 自增 ID 每次连接都变）。
	inboundIDPrefix = "inbound-"
)

// PeerSource 标记对端条目来源。
type PeerSource string

const (
	SourceBeacon  PeerSource = "beacon"  // 局域网广播自动发现
	SourceInbound PeerSource = "inbound" // 外部浏览器/手机接入本机 UI
	SourceManual  PeerSource = "manual"  // 用户手动添加 IP
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
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Alias    string             `json:"alias,omitempty"` // 本机备注：优先显示，绝不随对端广播被覆盖
	OS       string             `json:"os"`
	IP       string             `json:"ip"`
	Port     int                `json:"port"`
	Target   string             `json:"target"`
	Provides DeviceCapabilities `json:"provides"`
	LastSeen time.Time          `json:"last_seen"`
	IsManual bool               `json:"is_manual"`
	Online   bool               `json:"online"`
	Source   PeerSource         `json:"source,omitempty"`
}

type Service struct {
	id          string
	name        string
	port        int
	currentIP   string
	provides    DeviceCapabilities
	peers       map[string]*DiscoveredPeer
	aliases     map[string]string
	aliasesByIP map[string]string
	persist     func(util.NodeConfig)
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
		id:          GenerateNodeID(),
		name:        DefaultNodeName(),
		port:        port,
		currentIP:   initialIP,
		provides:    provides,
		peers:       make(map[string]*DiscoveredPeer),
		aliases:     make(map[string]string),
		aliasesByIP: make(map[string]string),
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

// SetPersistHook 设置配置持久化回调（上层启动时接线；nil 表示不持久化，便于单测）。
func (s *Service) SetPersistHook(fn func(util.NodeConfig)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persist = fn
}

// SetAliases 恢复持久化的对端备注（启动时由上层调用一次）：
// byID 按条目 ID 精确恢复；byIP 按设备 IP 恢复（对端重启换 ID / 条目被清除后重连仍生效）。
func (s *Service) SetAliases(byID map[string]string, byIP map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	apply := func(key, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		s.aliases[key] = v
		if p, ok := s.peers[key]; ok && p.Alias == "" {
			p.Alias = v
		}
	}
	for k, v := range byID {
		apply(k, v)
	}
	for k, v := range byIP {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		s.aliasesByIP[k] = v
		if p := s.findByIPLocked(k, ""); p != nil && p.Alias == "" {
			p.Alias = v
		}
	}
}

// snapshotConfigLocked 导出需要持久化的配置；调用方必须持有写锁。
func (s *Service) snapshotConfigLocked() util.NodeConfig {
	aliases := make(map[string]string, len(s.aliases))
	for k, v := range s.aliases {
		aliases[k] = v
	}
	byIP := make(map[string]string, len(s.aliasesByIP))
	for k, v := range s.aliasesByIP {
		byIP[k] = v
	}
	return util.NodeConfig{Name: s.name, Aliases: aliases, AliasesByIP: byIP}
}

// persistLocked 触发持久化；调用方必须持有写锁（改名属低频操作，同步写盘可接受）。
func (s *Service) persistLocked() {
	if s.persist == nil {
		return
	}
	s.persist(s.snapshotConfigLocked())
}

func (s *Service) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(name) != "" {
		s.name = strings.TrimSpace(name)
		// 本机名称持久化：重启后不再是出厂主机名
		s.persistLocked()
	}
}

func (s *Service) SetCurrentIP(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentIP = ip
	// 网卡拓扑可能已变化：立即失效缓存，下一个心跳按新拓扑广播
	util.InvalidateNetCache()
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

// broadcastLoop 周期性广播本机 beacon：
//   - 启动时先突发数包，缩短对端首次发现的等待；
//   - 除受限广播/组播外，再向每个物理网段的定向广播地址各发一份，
//     覆盖多网卡机器上不走默认路由的网卡。
func (s *Service) broadcastLoop(ctx context.Context) {
	// 常规套接字：受限广播 + 组播（保留原行为）
	bcastAddr := &net.UDPAddr{
		IP:   net.IPv4bcast,
		Port: DiscoveryPort,
	}
	bconn, err := net.DialUDP("udp4", nil, bcastAddr)
	if err != nil {
		fmt.Printf("[Discovery] UDP 广播套接字警告: %v\n", err)
	}
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP(MulticastGroup),
		Port: DiscoveryPort,
	}
	mconn, _ := net.DialUDP("udp4", nil, mcastAddr)

	// 定向广播套接字池：按目标地址复用，网卡变化时清理失效项
	directed := make(map[string]*net.UDPConn)

	defer func() {
		if bconn != nil {
			_ = bconn.Close()
		}
		if mconn != nil {
			_ = mconn.Close()
		}
		for _, c := range directed {
			_ = c.Close()
		}
	}()

	send := func() {
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
			return
		}

		targets := util.BroadcastTargets()
		live := make(map[string]bool, len(targets))
		for _, tgt := range targets {
			live[tgt] = true
			conn, ok := directed[tgt]
			if !ok {
				addr := &net.UDPAddr{IP: net.ParseIP(tgt), Port: DiscoveryPort}
				c, derr := net.DialUDP("udp4", nil, addr)
				if derr != nil {
					delete(live, tgt)
					continue
				}
				directed[tgt] = c
				conn = c
			}
			_, _ = conn.Write(data)
		}
		for tgt, c := range directed {
			if !live[tgt] {
				_ = c.Close()
				delete(directed, tgt)
			}
		}

		if bconn != nil {
			_, _ = bconn.Write(data)
		}
		if mconn != nil {
			_, _ = mconn.Write(data)
		}
	}

	// 启动突发：立即 + 短间隔补发
	send()
	for i := 1; i < BurstCount; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(BurstInterval):
		}
		send()
	}

	ticker := time.NewTicker(BeaconInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}

// listenLoop 管理发现监听 socket 的生命周期：建立失败或运行中异常中断时
// 按固定间隔重建，而不是从此静默失效。
func (s *Service) listenLoop(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		conn, err := s.openListener()
		if err != nil {
			fmt.Printf("[Discovery] Notice: Auto-discovery port %d unavailable (%v); manual IP connection active. %v 后重试。\n", DiscoveryPort, err, listenRetryInterval)
			select {
			case <-ctx.Done():
				return
			case <-time.After(listenRetryInterval):
			}
			continue
		}
		fatal := s.readBeacons(ctx, conn)
		_ = conn.Close()
		if ctx.Err() != nil {
			return
		}
		if fatal {
			select {
			case <-ctx.Done():
				return
			case <-time.After(listenRetryInterval):
			}
		}
	}
}

// openListener 优先加入组播组；失败（如组播被网络策略禁止）时回退到
// 通配地址监听，仍能接收广播与单播 beacon。
func (s *Service) openListener() (net.PacketConn, error) {
	mcastAddr := &net.UDPAddr{
		IP:   net.ParseIP(MulticastGroup),
		Port: DiscoveryPort,
	}
	if conn, err := net.ListenMulticastUDP("udp4", nil, mcastAddr); err == nil {
		return conn, nil
	} else {
		mcastErr := err
		listenAddr := &net.UDPAddr{
			IP:   net.IPv4zero,
			Port: DiscoveryPort,
		}
		conn, err := net.ListenUDP("udp4", listenAddr)
		if err != nil {
			return nil, mcastErr
		}
		return conn, nil
	}
}

// readBeacons 读取并分发 beacon 包；返回 true 表示遇到非超时错误需要重建监听。
func (s *Service) readBeacons(ctx context.Context, conn net.PacketConn) bool {
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, remoteAddr, err := conn.ReadFrom(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// 非超时错误（如网卡翻转导致 socket 失效）：重建监听
			return true
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

		s.upsertBeacon(b, peerIP)
	}
}

// upsertBeacon 处理一条对端 beacon：新对端入库，已知对端刷新状态。
// 去重铁律：同一 IP 视为同一台设备，无论来源（beacon/入站连接/手动添加）
// 都归并到既有条目——否则同一台设备会因“自动发现 + 主动连接 + 手动添加”
// 出现多条目。既有条目的 ID 保持不变，映射与备注不失效。
// 注意：广播名只会写进 Name，绝不覆盖本地备注 Alias——
// 否则用户给设备改的备注会在一个心跳周期（2 秒）内被打回原样。
func (s *Service) upsertBeacon(b Beacon, peerIP string) {
	peerTarget := fmt.Sprintf("%s:%d", peerIP, b.Port)

	s.mu.Lock()
	p, exists := s.peers[b.ID]
	if !exists {
		// 同 IP 已有其他来源的条目：归并复用，避免同一设备多条目
		if old := s.findByIPLocked(peerIP, ""); old != nil {
			p = old
			exists = true
		}
	}
	if exists {
		// 同 IP 的其他来源档案（更早的手动/入站记录）并入本条目后清除
		if other := s.findByIPLocked(peerIP, b.ID); other != nil {
			if p.Alias == "" && other.Alias != "" {
				p.Alias = other.Alias
				s.aliases[p.ID] = other.Alias
			}
			if other.IsManual {
				// 用户显式添加过的设备保持 manual 语义（不参与超时清除）
				p.IsManual = true
				p.Source = SourceManual
			}
		}
		p.Name = b.Name
		p.OS = b.OS
		p.IP = peerIP
		p.Port = b.Port
		p.Target = peerTarget
		p.Provides = b.Provides
		p.LastSeen = time.Now()
		p.Online = true
		// 被主动连接过的设备收到 beacon 后升级为完整对端（能力来自广播）；
		// 手动添加的设备保持 manual 语义（不参与超时清除），别名一律保留
		if p.Source == SourceInbound {
			p.Source = SourceBeacon
		}
		if p.Alias == "" {
			p.Alias = s.aliasForLocked(b.ID, peerIP)
		}
	} else {
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
			Source:   SourceBeacon,
			Alias:    s.aliasForLocked(b.ID, peerIP),
		}
		s.peers[b.ID] = p
	}
	snapshot := *p
	onFound := s.onPeerFound
	s.mu.Unlock()

	if !exists && onFound != nil {
		go onFound(snapshot)
	}
}

// findByIPLocked 查找同 IP 的既有条目（ID 不同视为同设备的重复档案）；
// 存在多条历史脏数据时保留优先级最高的一条（beacon 信息最全 > manual 用户显式添加 > inbound），
// 其余直接清除。excludeID 用于跳过自身。
func (s *Service) findByIPLocked(peerIP, excludeID string) *DiscoveredPeer {
	if peerIP == "" {
		return nil
	}
	rank := func(src PeerSource) int {
		switch src {
		case SourceBeacon:
			return 3
		case SourceManual:
			return 2
		default:
			return 1
		}
	}
	var best *DiscoveredPeer
	for id, p := range s.peers {
		if id == excludeID || p.IP != peerIP {
			continue
		}
		if best == nil || rank(p.Source) > rank(best.Source) {
			if best != nil {
				delete(s.peers, best.ID)
			}
			best = p
		} else {
			delete(s.peers, id)
		}
	}
	return best
}

// aliasForLocked 取设备备注：优先按条目 ID，其次按 IP（设备重启换 ID 或
// 条目被超时清除后重连，备注仍可按 IP 恢复）。调用方必须持有写锁。
func (s *Service) aliasForLocked(id, ip string) string {
	if a := s.aliases[id]; a != "" {
		return a
	}
	return s.aliasesByIP[ip]
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

	// 同 IP 已有档案（自动发现/入站连接/旧手动记录）：复用为本次手动条目，
	// 用户显式命名的别名覆盖生效——绝不新建第二条
	if existing := s.findByIPLocked(ip, ""); existing != nil {
		existing.IsManual = true
		existing.Source = SourceManual
		existing.Port = port
		existing.Target = target
		existing.LastSeen = time.Now()
		existing.Online = true
		if custom := strings.TrimSpace(name); custom != "" {
			existing.Alias = custom
			s.aliases[existing.ID] = custom
			s.aliasesByIP[ip] = custom
			s.persistLocked()
		}
		snapshot := *existing
		return &snapshot
	}

	id := "manual-" + hex.EncodeToString([]byte(target))
	peerName := name
	if peerName == "" {
		peerName = fmt.Sprintf("外部主机 (%s)", target)
	}

	p := &DiscoveredPeer{
		ID:     id,
		Name:   peerName,
		OS:     "remote",
		IP:     ip,
		Port:   port,
		Target: target,
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
		Source:   SourceManual,
	}
	// 用户自定义名称视为本地备注并持久化，重启后手动对端仍显示该名称
	if custom := strings.TrimSpace(name); custom != "" {
		p.Alias = custom
		s.aliases[id] = custom
		s.aliasesByIP[ip] = custom
		s.persistLocked()
	}
	s.peers[id] = p
	snapshot := *p
	return &snapshot
}

// RegisterInboundClient 登记一个接入本机 UI 的外部浏览器/手机。
// 使用按来源 IP 的稳定 ID：同一设备刷新或重连只会更新既有条目，
// 不再每次连接都产生一个新 peer（旧实现是列表重复/残留“自己”的成因之一）。
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

	peerID := inboundIDPrefix + ip
	// 同 IP 已有自动发现/手动添加的条目：它就是同一台设备，只刷新在线状态，
	// 绝不再建一个 inbound 条目（否则设备列表里同一台机器会出现多条）
	if existing := s.findByIPLocked(ip, ""); existing != nil {
		existing.LastSeen = time.Now()
		existing.Online = true
		snapshot := *existing
		return &snapshot
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

	p, exists := s.peers[peerID]
	if !exists {
		p = &DiscoveredPeer{
			ID:     peerID,
			Name:   fmt.Sprintf("%s [%s]", friendlyName, ip),
			OS:     osName,
			IP:     ip,
			Port:   s.port,
			Target: remoteAddr,
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
			Source:   SourceInbound,
			Alias:    s.aliases[peerID],
		}
		s.peers[peerID] = p
	} else {
		p.LastSeen = time.Now()
		p.Online = true
		if p.Alias == "" {
			p.Alias = s.aliases[peerID]
		}
	}
	snapshot := *p
	return &snapshot
}

// ReconcileInbound 依据真实的连接集合校正对端在线状态（按 IP 匹配）：
// 有存活连接的设备条目保持在线并刷新心跳（即使其 beacon 已停止也不被清除）；
// 入站条目在连接集合中消失时立即置为离线（不必等 PeerTimeout）。
// beacon 条目离线仍由广播心跳超时管理，不在此处置离线。
func (s *Service) ReconcileInbound(liveIPs map[string]bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.peers {
		if !liveIPs[p.IP] {
			if p.Source == SourceInbound {
				p.Online = false
				p.LastSeen = now.Add(-PeerTimeoutSecs * time.Second)
			}
			continue
		}
		p.LastSeen = now
		p.Online = true
	}
}

// RenamePeer 为对端设置本地备注。备注只存在本机并持久化，
// 优先于对端广播名显示，且不会随后续 beacon 被覆盖。
func (s *Service) RenamePeer(id, newName string) bool {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	p, exists := s.peers[id]
	if !exists {
		return false
	}
	p.Alias = newName
	s.aliases[id] = newName
	// 按 IP 建立二级索引：设备重启换 ID 或条目被清除后重连，备注仍可按 IP 恢复
	s.aliasesByIP[p.IP] = newName
	s.persistLocked()
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
		copied := *p
		// 展示名：本地备注优先，其次对端广播名
		if copied.Alias != "" {
			copied.Name = copied.Alias
		}
		result = append(result, copied)
	}
	return result
}
