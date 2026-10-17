package util

import (
	"fmt"
	"net"
	"strings"
	"time"
)

var virtualKeywords = []string{
	"vmnet", "vbox", "virtualbox", "vethernet", "wsl", "hyper-v",
	"docker", "tailscale", "zerotier", "tap", "tun", "utun",
	"bluetooth", "npcap", "teredo", "cni", "flannel", "veth",
}

// IPInfo contains IP address and its interface name with JSON tags.
type IPInfo struct {
	IP        string `json:"ip"`
	IfaceName string `json:"name"`
	IsVirtual bool   `json:"is_virtual"`
}

// GetOutboundIP uses OS routing table resolution to find the true primary LAN IP
// that connects to the router/internet. UDP Dial does not send any packets.
func GetOutboundIP() string {
	targets := []string{"223.5.5.5:80", "119.29.29.29:80", "8.8.8.8:80"}
	for _, target := range targets {
		conn, err := net.DialTimeout("udp", target, 500*time.Millisecond)
		if err == nil {
			defer conn.Close()
			if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok {
				ip := localAddr.IP.To4()
				if ip != nil && !ip.IsLoopback() {
					return ip.String()
				}
			}
		}
	}
	return ""
}

// GetAllIPs returns all valid IPv4 addresses categorized by physical vs virtual.
func GetAllIPs() []IPInfo {
	var result []IPInfo
	interfaces, err := net.Interfaces()
	if err != nil {
		return result
	}

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
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}

			ipStr := ip4.String()
			result = append(result, IPInfo{
				IP:        ipStr,
				IfaceName: iface.Name,
				IsVirtual: isVirt,
			})
		}
	}
	return result
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
	all := GetAllIPs()
	for _, info := range all {
		if !info.IsVirtual {
			if strings.HasPrefix(info.IP, "192.168.") || strings.HasPrefix(info.IP, "10.") || strings.HasPrefix(info.IP, "172.") {
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
	return false
}
