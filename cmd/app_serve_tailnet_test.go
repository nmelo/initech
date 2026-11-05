package cmd

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/spf13/cobra"
)

// ini-she: in tailnet mode with no Tailscale address the service waits
// in-process and binds when the address comes, instead of exiting for launchd
// to start it again ten seconds later, forever. Every wait here is on an
// event: the interface reader reports each look, the timer is a channel the
// test fires, and listen reports each bind. No real clock.

var (
	tailnetV4 = net.ParseIP("100.101.102.103")
	tailnetV6 = net.ParseIP("fd7a:115c:a1e0::1")
)

type tailnetRig struct {
	*upgradeRig
	mu     sync.Mutex
	ifaces []appserve.IfaceAddr
	looked chan struct{}  // one per interface read
	tick   chan time.Time // fires the one timer waiting (the wait's or the watch's)
	bound  chan net.IP    // one per listen
	wrote  chan struct{}  // one per log write
}

// notifyingLog writes to the rig's log and signals each write.
type notifyingLog struct{ r *tailnetRig }

func (n notifyingLog) Write(p []byte) (int, error) {
	k, err := n.r.log.Write(p)
	select {
	case n.r.wrote <- struct{}{}:
	default:
	}
	return k, err
}

// logHas waits, on log writes, for line to show n times.
func (r *tailnetRig) logHas(t *testing.T, line string, n int) {
	t.Helper()
	for strings.Count(r.log.String(), line) < n {
		select {
		case <-r.wrote:
		case err := <-r.done:
			t.Fatalf("the service exited (%v) before %q showed %d times; log:\n%s", err, line, n, r.log.String())
		}
	}
}

func newTailnetRig(t *testing.T) *tailnetRig {
	t.Helper()
	r := &tailnetRig{upgradeRig: newUpgradeRig(t), looked: make(chan struct{}, 100), tick: make(chan time.Time), bound: make(chan net.IP, 10), wrote: make(chan struct{}, 1)}
	oldAfter := appServeAfter
	t.Cleanup(func() { appServeAfter = oldAfter })
	appServeListen = appserve.ListenTailnet
	appServeAddrs = func() ([]appserve.IfaceAddr, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.looked <- struct{}{}
		return append([]appserve.IfaceAddr(nil), r.ifaces...), nil
	}
	appServeAfter = func(time.Duration) <-chan time.Time { return r.tick }
	appServeListenOn = func(ip net.IP, _ int) (net.Listener, error) {
		r.bound <- ip
		return net.Listen("tcp", "127.0.0.1:0")
	}
	appServeExecutable = func() (string, error) { return "", errors.New("no path in this test") }
	return r
}

// setTailnet puts Tailscale's two addresses on utun4 at v4, or takes them
// away (nil), with the LAN address always there: the wait must never take it.
func (r *tailnetRig) setTailnet(v4 net.IP) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ifaces = []appserve.IfaceAddr{{Iface: "en0", IP: net.ParseIP("192.168.1.50"), Up: true}}
	if v4 != nil {
		r.ifaces = append(r.ifaces,
			appserve.IfaceAddr{Iface: "utun4", IP: v4, Up: true},
			appserve.IfaceAddr{Iface: "utun4", IP: tailnetV6, Up: true})
	}
}

func (r *tailnetRig) run() {
	c := &cobra.Command{}
	c.SetOut(&bytes.Buffer{})
	c.SetErr(notifyingLog{r})
	go func() { r.done <- runAppServe(c, nil) }()
}

// look waits for the next interface read, failing if the service exits first.
func (r *tailnetRig) look(t *testing.T) {
	t.Helper()
	select {
	case <-r.looked:
	case err := <-r.done:
		t.Fatalf("the service exited (%v) instead of reading the interfaces; log:\n%s", err, r.log.String())
	}
}

// fire fires the one timer waiting, failing if the service exits instead.
func (r *tailnetRig) fire(t *testing.T) {
	t.Helper()
	select {
	case r.tick <- time.Time{}:
	case err := <-r.done:
		t.Fatalf("the service exited (%v) instead of waiting; log:\n%s", err, r.log.String())
	}
}

// bind waits for the next listen, failing if the service exits first.
func (r *tailnetRig) bind(t *testing.T) net.IP {
	t.Helper()
	select {
	case ip := <-r.bound:
		return ip
	case err := <-r.done:
		t.Fatalf("the service exited (%v) instead of binding; log:\n%s", err, r.log.String())
		return nil
	}
}

func (r *tailnetRig) noBind(t *testing.T) {
	t.Helper()
	select {
	case ip := <-r.bound:
		t.Fatalf("bound %v with no tailnet address; log:\n%s", ip, r.log.String())
	default:
	}
}

// No tailnet address at start: the service does not exit and binds nothing
// (not the LAN, not loopback) while it waits, logs one line however many tries
// pass, and binds the tailnet address on the first try after it appears.
func TestAppServe_NoTailnetAtStartWaitsThenBinds(t *testing.T) {
	r := newTailnetRig(t)
	r.setTailnet(nil)
	r.run()
	r.look(t)
	for range 5 {
		r.fire(t)
		r.look(t)
		r.noBind(t)
	}
	r.setTailnet(tailnetV4)
	r.fire(t)
	r.look(t)
	if ip := r.bind(t); !ip.Equal(tailnetV4) {
		t.Fatalf("bound %v, want the tailnet address %v", ip, tailnetV4)
	}
	r.logHas(t, "listening on", 1)
	log := r.log.String()
	if n := strings.Count(log, appserve.TailnetWaitLine); n != 1 {
		t.Errorf("the wait line %d times over six tries, want 1; log:\n%s", n, log)
	}
	sigterm(t)
	if err := <-r.done; err != nil {
		t.Fatalf("exit %v on SIGTERM, want nil", err)
	}
}

// SIGTERM while waiting at start is a clean exit (launchctl bootout).
func TestAppServe_SIGTERMWhileWaitingForTailnetExitsCleanly(t *testing.T) {
	r := newTailnetRig(t)
	r.setTailnet(nil)
	r.run()
	r.look(t)
	sigterm(t)
	if err := <-r.done; err != nil {
		t.Fatalf("exit %v on SIGTERM in the wait, want nil; log:\n%s", err, r.log.String())
	}
	r.noBind(t)
}

// The tailnet address lost after start: the service does not exit; it waits
// the same way, one line, and binds again in the same process when the
// address comes back.
func TestAppServe_TheTailnetLostAfterStartWaitsAndRebinds(t *testing.T) {
	r := newTailnetRig(t)
	r.setTailnet(tailnetV4)
	r.run()
	r.look(t)
	r.bind(t)
	r.setTailnet(nil)
	r.fire(t) // the address watch looks: gone
	r.look(t)
	r.look(t) // the wait's first try
	r.fire(t)
	r.look(t)
	r.noBind(t)
	r.setTailnet(tailnetV4)
	r.fire(t)
	r.look(t)
	if ip := r.bind(t); !ip.Equal(tailnetV4) {
		t.Fatalf("bound %v again, want %v", ip, tailnetV4)
	}
	r.logHas(t, "listening on", 2)
	log := r.log.String()
	if strings.Count(log, appserve.TailnetWaitLine) != 1 || strings.Count(log, "listening on") != 2 {
		t.Errorf("want one wait line and two listening lines; log:\n%s", log)
	}
	sigterm(t)
	if err := <-r.done; err != nil {
		t.Fatalf("exit %v on SIGTERM, want nil", err)
	}
}

// A move to another tailnet address is unchanged: a planned restart, so
// launchd starts the service on the new one.
func TestAppServe_AMovedTailnetAddressStillRestarts(t *testing.T) {
	r := newTailnetRig(t)
	r.setTailnet(tailnetV4)
	r.run()
	r.look(t)
	r.bind(t)
	r.setTailnet(net.ParseIP("100.101.102.200"))
	r.fire(t)
	if err := <-r.done; !errors.Is(err, appserve.ErrPlannedRestart) {
		t.Fatalf("exit %v, want the planned restart; log:\n%s", err, r.log.String())
	}
}
