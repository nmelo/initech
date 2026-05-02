package tui

import "net"

// LANIPv4 returns the first non-loopback IPv4 address from the host's network
// interfaces, suitable for displaying in a connection snippet. Returns
// "0.0.0.0" if no usable address is found (e.g., disconnected machine).
func LANIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "0.0.0.0"
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if ipnet.IP.IsLoopback() {
			continue
		}
		ip4 := ipnet.IP.To4()
		if ip4 == nil {
			continue
		}
		return ip4.String()
	}
	return "0.0.0.0"
}
