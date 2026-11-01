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

// BindAddr is the one address the service listens on.
//
// In tailnet mode it is an address on Tailscale's interface, which is told
// apart by the fd7a:115c:a1e0::/48 address Tailscale gives every node: an
// address in 100.64.0.0/10 alone proves nothing, since carrier NAT and phone
// hotspots hand out that range on ordinary LAN interfaces. The interface's
// 100.64.0.0/10 address is preferred, else its fd7a: one. No interface
// holding an fd7a:115c:a1e0::/48 address is ErrNoTailnet.
//
// In local-network mode it is the LAN's private IPv4 (en0 first).
//
// It is never the unspecified address and never a loopback one.
func BindAddr(mode string, addrs []IfaceAddr) (net.IP, error) {
	usable := func(a IfaceAddr) bool { return a.Up && !a.IP.IsLoopback() && !a.IP.IsUnspecified() }
	switch mode {
	case ListenTailnet:
		ts := map[string]bool{}
		for _, a := range addrs {
			if usable(a) && a.IP.To4() == nil && tailnetV6.Contains(a.IP) {
				ts[a.Iface] = true
			}
		}
		var v4, v6 []IfaceAddr
		for _, a := range addrs {
			if !usable(a) || !ts[a.Iface] {
				continue
			}
			switch {
			case a.IP.To4() != nil && tailnetV4.Contains(a.IP):
				v4 = append(v4, a)
			case a.IP.To4() == nil && tailnetV6.Contains(a.IP):
				v6 = append(v6, a)
			}
		}
		for _, c := range [][]IfaceAddr{v4, v6} {
			if len(c) > 0 {
				sort.SliceStable(c, func(i, j int) bool { return c[i].Iface < c[j].Iface })
				return c[0].IP, nil
			}
		}
		return nil, ErrNoTailnet
	case ListenLocalNetwork:
		var cands []IfaceAddr
		for _, a := range addrs {
			if !usable(a) || a.IP.To4() == nil {
				continue
			}
			for _, n := range privateV4s {
				if n.Contains(a.IP) {
					cands = append(cands, a)
					break
				}
			}
		}
		if len(cands) == 0 {
			return nil, errors.New("no private LAN address on this Mac for --listen local-network")
		}
		sort.SliceStable(cands, func(i, j int) bool {
			a, b := cands[i], cands[j]
			if (a.Iface == "en0") != (b.Iface == "en0") {
				return a.Iface == "en0"
			}
			return a.Iface < b.Iface
		})
		return cands[0].IP, nil
	}
	return nil, fmt.Errorf("--listen %q: want %s or %s", mode, ListenTailnet, ListenLocalNetwork)
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
