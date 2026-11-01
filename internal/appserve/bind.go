package appserve

import (
	"errors"
	"fmt"
	"net"
	"sort"
)

// Listen modes for --listen.
const (
	ListenTailnet      = "tailnet"       // the Tailscale interface only: the default and the release setting
	ListenLocalNetwork = "local-network" // the Mac's LAN interface, for the build phase while Tailscale is stopped
)

// DefaultPort is the service's TCP port.
const DefaultPort = 7392

// IfaceAddr is one address on one interface, as BindAddr reads them.
type IfaceAddr struct {
	Iface string
	IP    net.IP
	Up    bool
}

// SystemAddrs is every address on every interface of this machine.
func SystemAddrs() ([]IfaceAddr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []IfaceAddr
	for _, ifc := range ifs {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				out = append(out, IfaceAddr{Iface: ifc.Name, IP: n.IP, Up: ifc.Flags&net.FlagUp != 0})
			}
		}
	}
	return out, nil
}

var (
	tailnetV4  = mustCIDR("100.64.0.0/10")
	tailnetV6  = mustCIDR("fd7a:115c:a1e0::/48")
	privateV4s = []*net.IPNet{mustCIDR("10.0.0.0/8"), mustCIDR("172.16.0.0/12"), mustCIDR("192.168.0.0/16")}
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ErrNoTailnet is BindAddr's answer in tailnet mode when no interface holds
// a tailnet address: Tailscale is stopped or not installed.
var ErrNoTailnet = errors.New("no tailnet address on this Mac (is Tailscale running?); for the build phase, --listen local-network binds the LAN interface")

// BindAddr is the one address the service listens on: in tailnet mode the
// interface's Tailscale address (100.64.0.0/10, else fd7a:115c:a1e0::/48);
// in local-network mode the LAN's private IPv4 (en0 first). It is never the
// unspecified address and never a loopback one.
func BindAddr(mode string, addrs []IfaceAddr) (net.IP, error) {
	var pick func(net.IP) bool
	switch mode {
	case ListenTailnet:
		pick = func(ip net.IP) bool { return tailnetV4.Contains(ip) || (ip.To4() == nil && tailnetV6.Contains(ip)) }
	case ListenLocalNetwork:
		pick = func(ip net.IP) bool {
			if ip.To4() == nil {
				return false
			}
			for _, n := range privateV4s {
				if n.Contains(ip) {
					return true
				}
			}
			return false
		}
	default:
		return nil, fmt.Errorf("--listen %q: want %s or %s", mode, ListenTailnet, ListenLocalNetwork)
	}
	var cands []IfaceAddr
	for _, a := range addrs {
		if a.Up && !a.IP.IsLoopback() && !a.IP.IsUnspecified() && pick(a.IP) {
			cands = append(cands, a)
		}
	}
	if len(cands) == 0 {
		if mode == ListenTailnet {
			return nil, ErrNoTailnet
		}
		return nil, errors.New("no private LAN address on this Mac for --listen local-network")
	}
	// IPv4 before IPv6, en0 before the rest, then by name: one stable answer.
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if (a.IP.To4() != nil) != (b.IP.To4() != nil) {
			return a.IP.To4() != nil
		}
		if (a.Iface == "en0") != (b.Iface == "en0") {
			return a.Iface == "en0"
		}
		return a.Iface < b.Iface
	})
	return cands[0].IP, nil
}

// Listen opens the service's TCP listener on ip:port, refusing an address
// that would reach beyond one interface (unspecified) or the public port on
// loopback, and checking the bound address again after the bind.
func Listen(ip net.IP, port int) (net.Listener, error) {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
		return nil, fmt.Errorf("refusing to listen on %v: the service binds one interface address", ip)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), fmt.Sprint(port)))
	if err != nil {
		return nil, err
	}
	if ta, ok := ln.Addr().(*net.TCPAddr); !ok || ta.IP.IsUnspecified() || ta.IP.IsLoopback() {
		ln.Close()
		return nil, fmt.Errorf("listener bound %v, not %v", ln.Addr(), ip)
	}
	return ln, nil
}
