// window_server.go implements the multi-monitor attach surface (ini-9ka.2):
// window 1's TUI keeps direct ownership of its PTYs and ADDITIONALLY serves
// the pane-stream protocol in-process, so secondary windows
// (`initech --window N`) can attach and render live panes with full input
// routing.
//
// This is the operator's decided topology (option (a), 2026-08-12). The
// rejected alternative -- window 1 becoming a client of a daemon it spawns --
// was turned down because a window-1 crash would leave a headless daemon
// running the fleet invisibly, precisely the state fold-back exists to
// prevent. Here the UI and the PTYs live in the same process, so that failure
// is atomic and unreachable.
//
// ZERO-CHANGE GUARANTEE: startWindowServer is only ever called when
// project.WindowListen is non-empty. A single-window fleet leaves it empty,
// no listener starts, no artifact is created, and nothing is printed -- the
// session runs today's code path. See census_zerochange_test.go, which
// asserts that against the shipped release's baseline.
package tui

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// Wedged-window detection timeouts (ini-z8o). These apply ONLY to the
// in-process window server's sessions -- cross-machine peers keep
// yamux.DefaultConfig()'s WAN-sized 30s/10s, untouched, because the daemon
// serving them is a different instance that never sets these.
//
// yamux's model (read from v0.1.2 session.go, not assumed): keepalive() pings
// every KeepAliveInterval, and Ping waits for the pong for
// ConnectionWriteTimeout before killing the session. Two different bounds
// follow, and they constrain two different ACs:
//
//   - WORST-case detection = interval + writeTimeout, when a wedge begins
//     just after a successful ping. 5+7 = 12s, inside the <=15s bound.
//     (The defaults' 30+10 = 40s is exactly qa1's measured 40.03s, so this
//     model is calibrated against a real measurement, not just the docs.)
//   - MINIMUM time-to-drop = writeTimeout alone, when a wedge begins exactly
//     as a ping falls due. 7s, above the >=5s transient-stall tolerance, so a
//     SIGSTOP+SIGCONT inside 5s cannot be dropped even at the worst phase.
//
// Tuning the interval alone would have hit the detection bound while leaving
// transient tolerance at 10s -- the two numbers are not interchangeable.
// Deliberately not tighter (e.g. 3+5): the bound is <=15s, not "as low as
// possible", and eagerness is the failure mode this tuning introduces.
const (
	windowKeepAliveInterval      = 5 * time.Second
	windowConnectionWriteTimeout = 7 * time.Second
)

// windowYamuxConfig returns the tightened transport config for window-client
// sessions. Built from DefaultConfig so every field we do NOT tune tracks
// upstream rather than being pinned to a stale copy.
func windowYamuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	cfg.KeepAliveInterval = windowKeepAliveInterval
	cfg.ConnectionWriteTimeout = windowConnectionWriteTimeout
	return cfg
}

// windowServer holds the in-process pane-stream listener that lets secondary
// windows attach to this TUI.
type windowServer struct {
	daemon   *Daemon
	listener net.Listener
}

// startWindowServer binds the multi-window pane-stream listener and serves it
// in the background, exposing the given local panes to attaching windows.
//
// It deliberately reuses Daemon.handleConnection -- the same yamux/hello/
// stream-routing code `initech serve` runs -- rather than reimplementing the
// protocol, so a secondary window is an ordinary client of an ordinary
// server. What differs from RunDaemon is only what this process ALREADY
// owns: the panes exist and are running, the logger, PID file, and local IPC
// socket are already set up by the TUI, so none of that is repeated here.
//
// Returns a cleanup func that stops accepting and detaches the sinks. Callers
// must only invoke this when project.WindowListen is non-empty.
func startWindowServer(project *config.Project, version string, panes []*Pane, safeGo func(func()), onFleetState func(FleetStateCmd) error) (*windowServer, func(), error) {
	if project == nil || project.WindowListen == "" {
		return nil, nil, fmt.Errorf("startWindowServer called without a WindowListen address (single-window fleets must not reach here)")
	}

	d := &Daemon{
		project:        project,
		version:        version,
		ringBufs:       make(map[string]*RingBuf),
		multiSinks:     make(map[string]*MultiSink),
		ownership:      newAgentOwnership(),
		clients:        make(map[string]net.Conn),
		clientSessions: make(map[string]*yamux.Session),
		clientCtrlMu:   make(map[string]*sync.Mutex),
		fwdPending:     make(map[string]chan ControlResp),
		// Scoped here, on THIS daemon instance only (ini-z8o). RunDaemon's
		// instance leaves yamuxCfg nil and keeps the defaults.
		yamuxCfg: windowYamuxConfig(),
		// Window 1 owns fleet state; this is how a secondary's
		// set_fleet_state reaches it (ini-9ka.10).
		onFleetState: onFleetState,
	}

	// Attach a ring buffer + fan-out sink to each already-running pane. The
	// ring buffer gives an attaching window its scrollback replay; the
	// MultiSink lets N windows stream the same pane without any of them
	// owning it. This is the one place the TUI's panes and the daemon's
	// serving model have to meet: the panes were created by the TUI, not by
	// the daemon, so the wiring RunDaemon does at pane-creation time is done
	// here instead.
	for _, p := range panes {
		rb := NewRingBuf(DefaultRingBufSize)
		d.ringBufs[p.Name()] = rb
		ms := NewMultiSink()
		ms.Add(rb)
		d.multiSinks[p.Name()] = ms
		p.SetNetworkSink(ms)
		d.panes = append(d.panes, p)
	}

	ln, err := net.Listen("tcp", project.WindowListen)
	if err != nil {
		// Undo the sink wiring so a failed bind leaves the panes exactly as
		// they were, rather than teeing into sinks nothing will ever read.
		for _, p := range panes {
			p.ClearNetworkSink()
		}
		return nil, nil, fmt.Errorf("window listener on %s: %w", project.WindowListen, err)
	}
	d.listener = ln

	ws := &windowServer{daemon: d, listener: ln}

	LogInfo("window-server", "listening", "addr", ln.Addr().String(), "panes", len(d.panes))

	accept := func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // Listener closed.
			}
			c := conn
			safeGo(func() { d.handleConnection(c) })
		}
	}
	safeGo(accept)

	cleanup := func() {
		ln.Close()
		for _, p := range panes {
			p.ClearNetworkSink()
		}
		LogInfo("window-server", "stopped")
	}
	return ws, cleanup, nil
}

// Addr returns the address the window listener is bound to. Useful for tests
// and for discovery once ini-9ka.6 wires the CLI.
func (w *windowServer) Addr() string {
	if w == nil || w.listener == nil {
		return ""
	}
	return w.listener.Addr().String()
}

// localPanes filters a TUI's pane list down to locally-owned panes. Only
// those have a PTY this process can serve; RemotePane values are streams this
// TUI is itself consuming from somewhere else, and re-serving them would make
// window 1 a relay for panes it does not own (ini-9ka.2 serves window 1's own
// agents; cross-machine windows are explicitly out of scope for v1).
func localPanes(views []PaneView) []*Pane {
	out := make([]*Pane, 0, len(views))
	for _, v := range views {
		if p, ok := v.(*Pane); ok {
			out = append(out, p)
		}
	}
	return out
}

// connectedWindows returns the identities of the secondary windows currently
// attached. This is fold-back's liveness input (ini-9ka.7): a window leaves
// this set the moment its control stream errors, which happens when its
// process dies -- cleanly or by crashing, since either way the kernel closes
// its TCP connection.
//
// Deliberately a snapshot read rather than a subscription: the render path
// calls this each frame and compares against assignment, so fold-back happens
// on the first frame after a disconnect without any event plumbing to lag or
// drop. Returns an empty (non-nil) map when there is no server, which is the
// single-window case -- every secondary window is trivially absent, so window
// 1 renders everything, exactly as it does today.
func (w *windowServer) connectedWindows() map[string]bool {
	out := make(map[string]bool)
	if w == nil || w.daemon == nil {
		return out
	}
	w.daemon.sessionsMu.Lock()
	defer w.daemon.sessionsMu.Unlock()
	for peer := range w.daemon.clients {
		out[peer] = true
	}
	return out
}
