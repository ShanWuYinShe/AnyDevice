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

// IPInfo contains IP address and its interface name.
type IPInfo struct {
	IP        string
	IfaceName string
	IsVirtual bool
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
// It prioritizes OS routing detection, followed by non-virtual physical interfaces.
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
			// Prioritize common local subnet
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
		if info.IP != primaryIP && !info.IsVirtual {
			others = append(others, fmt.Sprintf("%s (%s)", info.IP, info.IfaceName))
		}
	}

	if len(others) > 0 {
		fmt.Printf("├─ 其他可选物理网卡: %s\n", strings.Join(others, ", "))
	}
}
