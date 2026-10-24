package util

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

var virtualKeywords = []string{
	"vmnet", "vbox", "virtualbox", "vethernet", "wsl", "hyper-v",
	"docker", "tailscale", "zerotier", "tap", "tun", "utun",
	"bluetooth", "npcap", "teredo", "cni", "flannel", "veth",
	// macOS 网络共享/虚拟机网桥（bridge100 等）同样是虚拟接口；
	// 之前漏判会导致它被当作物理网卡参与 IP 选择与广播。
	"bridge",
}

// IPInfo contains IP address and its interface name with JSON tags.
type IPInfo struct {
	IP        string `json:"ip"`
	IfaceName string `json:"name"`
	IsVirtual bool   `json:"is_virtual"`
}

// —— 网卡枚举缓存 ——
// net.Interfaces/Addrs 是系统调用，而 IsLocalIP 位于 beacon 处理、/api/peers 等
// 高频路径上，逐包枚举网卡既慢又费 CPU（用户反馈“设备很慢”的成因之一）。
// 这里用短 TTL 缓存统一收敛；切换网卡时由 InvalidateNetCache 立即失效。
const netCacheTTL = 2 * time.Second

var (
	netMu      sync.Mutex
	netCacheAt time.Time
	netIPs     []IPInfo
	netBcasts  []string
	// netV6：本机 IPv6 地址（同时存含 zone 与去 zone 两种形式）。
	// GetAllIPs 只收集 IPv4（广播/主 IP 选择语义均为 v4），IPv6 仅用于
	// IsLocalIP 的本机判断——IPv6 直连访问时 RemoteAddr 是本机 v6 地址
	netV6 []string
)

// refreshNetCacheLocked 重新枚举网卡；调用方必须持有 netMu。
func refreshNetCacheLocked() {
	netIPs = nil
	netBcasts = nil
	netV6 = nil
	netCacheAt = time.Now()

	interfaces, err := net.Interfaces()
	if err != nil {
		return
	}

	bcastSet := make(map[string]bool)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		nameLower := strings.ToLower(iface.Name)
		isVirt := false
		for _, kw := range virtualKeywords {
			if strings.Contains(nameLower, kw) {
				isVirt = true
				break
			}
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				if ipaddr, isAddr := addr.(*net.IPAddr); isAddr {
					ipnet = &net.IPNet{IP: ipaddr.IP}
				} else {
					continue
				}
			}

			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() {
				continue
			}

			netIPs = append(netIPs, IPInfo{
				IP:        ip4.String(),
				IfaceName: iface.Name,
				IsVirtual: isVirt,
			})

			// 本机 IPv6 收集：独立全地址枚举、不做 loopback 过滤——
			// macOS lo0 上也有 fe80::1（链路本地），IsLocalIP 需要它；
			// GetAllIPs/广播语义仍仅 IPv4，互不影响
			if v6addrs, err := net.InterfaceAddrs(); err == nil {
				for _, addr := range v6addrs {
					if ipn, ok := addr.(*net.IPNet); ok {
						if s := ipn.IP.String(); strings.Contains(s, ":") {
							netV6 = append(netV6, s)
						}
					}
				}
			}

			// 定向广播地址 = 网段地址 | 反掩码；
			// 点对点/无掩码接口、链路本地与全网广播没有意义，跳过。
			if len(ipnet.Mask) != 4 || iface.Flags&net.FlagBroadcast == 0 {
				continue
			}
			b := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				b[i] = ip4[i] | ^ipnet.Mask[i]
			}
			if s := b.String(); s != "" && s != "255.255.255.255" && !strings.HasPrefix(s, "169.254.") {
				bcastSet[s] = true
			}
		}
	}

	for s := range bcastSet {
		netBcasts = append(netBcasts, s)
	}
}

func refreshNetCacheIfStaleLocked() {
	if netCacheAt.IsZero() || time.Since(netCacheAt) > netCacheTTL {
		refreshNetCacheLocked()
	}
}

// GetOutboundIP uses OS routing table resolution to find the true primary LAN IP
// that connects to the router/internet. UDP Dial does not send any packets.
func GetOutboundIP() string {
	targets := []string{"223.5.5.5:80", "119.29.29.29:80", "8.8.8.8:80"}
	for _, target := range targets {
		conn, err := net.DialTimeout("udp", target, 500*time.Millisecond)
		if err != nil {
			continue
		}
		localAddr, ok := conn.LocalAddr().(*net.UDPAddr)
		conn.Close() // 用完即关，不积压到函数返回（循环内 defer 是反模式）
		if !ok {
			continue
		}
		ip := localAddr.IP.To4()
		if ip != nil && !ip.IsLoopback() {
			return ip.String()
		}
	}
	return ""
}

// GetAllIPs returns all valid IPv4 addresses categorized by physical vs virtual.
// 带短 TTL 缓存：同一份结果在 TTL 内复用，避免高频路径反复发起系统调用。
func GetAllIPs() []IPInfo {
	netMu.Lock()
	defer netMu.Unlock()
	refreshNetCacheIfStaleLocked()
	out := make([]IPInfo, len(netIPs))
	copy(out, netIPs)
	return out
}

// BroadcastTargets 返回本机各物理网段的定向广播地址（如 192.168.0.255，已去重）。
// 相比只发 255.255.255.255（只走默认路由网卡），定向广播能覆盖
// 其余网卡上的对端，显著改善多网卡机器（虚拟机/WSL 用户）的发现率。
func BroadcastTargets() []string {
	netMu.Lock()
	defer netMu.Unlock()
	refreshNetCacheIfStaleLocked()
	out := make([]string, len(netBcasts))
	copy(out, netBcasts)
	return out
}

// InvalidateNetCache 立即失效网卡缓存；切换通信网卡后调用，
// 让下一个发现心跳立即按新拓扑发送广播。
func InvalidateNetCache() {
	netMu.Lock()
	netCacheAt = time.Time{}
	netMu.Unlock()
}

// GetPrimaryIP selects the best LAN IP address for external device access.
func GetPrimaryIP(userSpecifiedIP string) string {
	if userSpecifiedIP != "" {
		return userSpecifiedIP
	}

	// 1. Try OS routing table lookup (gold standard)
	if outbound := GetOutboundIP(); outbound != "" {
		return outbound
	}

	// 2. Fallback: inspect physical non-virtual network interfaces
	// IsPrivate 精确覆盖 RFC1918（10/8、172.16/12、192.168/16）：
	// 此前的 "172." 前缀会把 172.0.x~172.15.x 的公网地址误判为私网
	all := GetAllIPs()
	for _, info := range all {
		if !info.IsVirtual {
			if ip := net.ParseIP(info.IP); ip != nil && ip.IsPrivate() {
				return info.IP
			}
		}
	}

	// 3. Fallback: any non-loopback IP
	if len(all) > 0 {
		return all[0].IP
	}

	return "127.0.0.1"
}

// PrintNetworkInfo displays primary IP and other detected interfaces.
func PrintNetworkInfo(primaryIP string, port int) {
	all := GetAllIPs()
	var others []string
	for _, info := range all {
		if info.IP != primaryIP {
			tag := ""
			if info.IsVirtual {
				tag = " [虚拟]"
			}
			others = append(others, fmt.Sprintf("%s (%s%s)", info.IP, info.IfaceName, tag))
		}
	}

	if len(others) > 0 {
		fmt.Printf("├─ 其他可选网卡: %s\n", strings.Join(others, ", "))
	}
}

// PrintAllInterfaces lists all interfaces with index numbers for user selection.
func PrintAllInterfaces() {
	all := GetAllIPs()
	fmt.Println("==================================================================")
	fmt.Println("本机检测到的可用网络接口列表：")
	fmt.Println("------------------------------------------------------------------")
	for i, info := range all {
		typeTag := "[物理网卡/推荐]"
		if info.IsVirtual {
			typeTag = "[虚拟网卡/虚拟机]"
		}
		fmt.Printf("[%d] %-15s %-25s %s\n", i, info.IP, info.IfaceName, typeTag)
	}
	fmt.Println("------------------------------------------------------------------")
	fmt.Println("可以通过 -ip 参数指定使用某一个 IP, 例如: any_device -ip 192.168.1.100")
	fmt.Println("==================================================================")
}

// IsLocalIP returns true if the given IP string matches loopback, localhost, or any local NIC IPv4/IPv6.
func IsLocalIP(ipStr string) bool {
	ipStr = strings.TrimSpace(ipStr)
	if host, _, err := net.SplitHostPort(ipStr); err == nil {
		ipStr = host
	}
	ipStr = strings.Trim(ipStr, "[]")
	if ipStr == "" || ipStr == "localhost" || ipStr == "127.0.0.1" || ipStr == "::1" || strings.HasPrefix(ipStr, "127.") {
		return true
	}
	parsed := net.ParseIP(ipStr)
	if parsed != nil && (parsed.IsLoopback() || parsed.IsUnspecified()) {
		return true
	}
	for _, info := range GetAllIPs() {
		if info.IP == ipStr {
			return true
		}
	}

	// 本机 IPv6 匹配：GetAllIPs 只含 IPv4，IPv6 直连访问（如 fe80:: 链路
	// 本地地址）此前会被误判为外部对端。zone（%接口）两侧归一化后比较
	trimmed := ipStr
	if i := strings.LastIndexByte(trimmed, '%'); i >= 0 {
		trimmed = trimmed[:i]
	}
	if strings.Contains(trimmed, ":") {
		for _, v6 := range netV6 {
			if v6 == trimmed || v6 == ipStr {
				return true
			}
		}
	}
	return false
}
