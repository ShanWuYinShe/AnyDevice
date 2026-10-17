package util

import (
	"net"
	"strings"
)

// GetLocalIPs returns all non-loopback IPv4 addresses found on local network interfaces.
func GetLocalIPs() []string {
	var ips []string
	interfaces, err := net.Interfaces()
	if err != nil {
		return ips
	}

	for _, iface := range interfaces {
		// Skip down or loopback interfaces
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
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

			ip = ip.To4()
			if ip == nil {
				continue // Skip IPv6 for simplicity in browser URL scanning
			}

			ipStr := ip.String()
			// Prioritize standard LAN private subnets
			if strings.HasPrefix(ipStr, "192.168.") || strings.HasPrefix(ipStr, "10.") || strings.HasPrefix(ipStr, "172.") {
				ips = append([]string{ipStr}, ips...)
			} else {
				ips = append(ips, ipStr)
			}
		}
	}
	return ips
}

// GetPrimaryIP returns the most likely LAN IP address, or "localhost" if none found.
func GetPrimaryIP() string {
	ips := GetLocalIPs()
	if len(ips) > 0 {
		return ips[0]
	}
	return "127.0.0.1"
}
