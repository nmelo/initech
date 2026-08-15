// reconnect.go manages persistent connections to remote daemon peers with
// automatic reconnection on failure. Each remote peer gets a background
// goroutine that handles the connect/reconnect lifecycle.
package tui

import (
	"fmt"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// Reconnect backoff parameters.
const (
	reconnectInitial = 1 * time.Second
	reconnectMax     = 30 * time.Second
)

// backoff returns the next retry delay using exponential backoff with a cap.
// Progression: 1s, 2s, 4s, 8s, 16s, 30s, 30s, 30s...
func backoff(attempt int) time.Duration {
	if attempt >= 30 {
		return reconnectMax // Prevent overflow on large attempt values.
	}
	d := reconnectInitial << uint(attempt)
	if d > reconnectMax {
		return reconnectMax
	}
	return d
}

// peerManager manages the connection lifecycle for all remote peers.
// It owns a goroutine per peer that handles connect/reconnect.
type peerManager struct {
	project *config.Project
	// onPanesChanged is called (on any goroutine) when remote panes are
	// added or go offline. The callback receives the peer name and the
	// new set of PaneViews for that peer (nil = all offline).
	// onPanesChanged reports a peer's panes AND whether the peer is currently
	// connected. The flag is explicit because the two facts are independent:
	// a peer can be connected with ZERO agents assigned to this window, which
	// is a perfectly healthy connection that len(panes)==0 cannot distinguish
	// from an offline peer (ini-1ch).
	onPanesChanged func(peerName string, panes []PaneView, connected bool)
	// onPaneAdded is called when a daemon announces a new agent stream
	// mid-session via stream_added (after a configure_agent push). The
	// TUI appends the pane to its existing list for that peer.
	onPaneAdded func(peerName string, pane PaneView)

	// onPaneRemoved is called when a daemon announces an agent's removal
	// (reload --prune), so the TUI drops the pane live.
	onPaneRemoved func(peerName, agentName string)
	// onSessionNotice is called when window 1 broadcasts a session-level
	// notice (ini-9ka.8). Session notices describe the session's shape
	// changing -- a window folding back or reattaching -- rather than one
	// agent's activity, so every attached window renders them.
	onSessionNotice func(text string)

	// onPaneOwnership delivers window 1's ownership decision (ini-x5ob). It is
	// how a secondary learns which panes it renders; it derives nothing
	// itself.
	onPaneOwnership func(owner map[string]string)
	// onEvicted is called AT MOST ONCE, when this client concludes another
	// process has taken over its window identity -- either by the server's
	// explicit verdict or by inference from consecutive immediate evictions
	// (ini-jhm6). The manager's loop for that peer has already exited
	// permanently when this fires: eviction is terminal, not a disconnect.
	//
	// Guarded by cbMu: newPeerManager starts the manager goroutines before
	// SetOnEvicted runs, so an unsynchronized set-then-read is a data race
	// (caught by -race on the war tests, and production wires it in the same
	// order). The other Set* callbacks are registered before any connection
	// can complete, which bounds them in practice, but this one is read on
	// the teardown path that a fast eviction reaches immediately.
	cbMu      sync.Mutex
	onEvicted func(peerName, reason string)
	// onAgentStatus is called when window 1 broadcasts an agent's observed
	// state (ini-9ka.11). primary is AgentStatus.Bead, used only when beads is
	// empty because the peer predates the plural field.
	onAgentStatus func(name string, beads []string, primary, desc string, ws WaitingState)
	// onForwardSend is called when a daemon pushes a forward_send command
	// through the control stream. The TUI delivers the message to the
	// local pane named by target. Returns an error if the pane doesn't exist.
	onForwardSend func(target, text string, enter bool) error
	quit          chan struct{}
	wg            sync.WaitGroup
}

// newPeerManager creates a manager and starts a goroutine per remote peer.
// All connections (initial and reconnect) happen in the background so the
// TUI renders immediately without blocking on network I/O.
func newPeerManager(project *config.Project, onChange func(string, []PaneView, bool), onFwd func(string, string, bool) error, quit chan struct{}) *peerManager {
	pm := &peerManager{
		project:        project,
		onPanesChanged: onChange,
		onForwardSend:  onFwd,
		quit:           quit,
	}
	for peerName, remote := range project.Remotes {
		pm.wg.Add(1)
		go pm.managePeer(peerName, remote)
	}
	return pm
}

// SetOnPaneAdded registers the callback fired when a daemon announces a
// stream-on-create (configure_agent → stream_added) mid-session.
func (pm *peerManager) SetOnPaneAdded(fn func(peerName string, pane PaneView)) {
	pm.onPaneAdded = fn
}

// SetOnPaneRemoved registers the removal twin of SetOnPaneAdded.
func (pm *peerManager) SetOnPaneRemoved(fn func(peerName, agentName string)) {
	pm.onPaneRemoved = fn
}

// wait blocks until all peer goroutines have exited.
// quickEvictionWindow and quickEvictionLimit calibrate the lost-verdict
// inference (ini-jhm6 edge 1). The measured war cycled about once per second,
// so a genuine takeover loss dies well inside 5s of connecting; a session that
// lives longer was real and resets the count. Two in a row rather than one,
// because a single quick death has innocent causes (window 1 restarting the
// instant after accepting); two consecutive is the war's signature.
const (
	quickEvictionWindow = 5 * time.Second
	quickEvictionLimit  = 2
)

// sweepEvictionVerdict drains the control mux after a session ends, looking
// for an identity_taken_over that arrived too late for consumeEvents. The
// mux's reader keeps delivering already-received frames after the close;
// Done() marks the reader finished, after which one final non-blocking sweep
// catches anything still buffered.
func (pm *peerManager) sweepEvictionVerdict(pc *peerConn, budget time.Duration) {
	if pc == nil || pc.mux == nil || pc.evicted.Load() {
		return
	}
	deadline := time.After(budget)
	for {
		select {
		case ev := <-pc.mux.Events():
			if ev.Action == identityTakenOverAction {
				pc.evicted.Store(true)
				return
			}
		case <-pc.mux.Done():
			for {
				select {
				case ev := <-pc.mux.Events():
					if ev.Action == identityTakenOverAction {
						pc.evicted.Store(true)
						return
					}
				default:
					return
				}
			}
		case <-deadline:
			return
		}
	}
}

// concludeEvicted reports the terminal eviction upward exactly once.
func (pm *peerManager) concludeEvicted(peerName, reason string) {
	LogWarn("remote", "eviction is terminal; not reconnecting", "peer", peerName, "reason", reason)
	pm.cbMu.Lock()
	fn := pm.onEvicted
	pm.cbMu.Unlock()
	if fn != nil {
		fn(peerName, reason)
	}
}

func (pm *peerManager) wait() {
	pm.wg.Wait()
}

// managePeer runs the connect/reconnect loop for a single remote peer.
// It exits when pm.quit is closed.
func (pm *peerManager) managePeer(peerName string, remote config.Remote) {
	defer pm.wg.Done()

	attempt := 0
	// Consecutive immediate evictions infer a lost takeover verdict
	// (ini-jhm6): a session that is accepted and then dies within
	// quickEvictionWindow looks like losing a war; one that survives longer
	// was a real session and resets the count. DIAL FAILURES NEVER COUNT --
	// window 1 being down is connection-refused, and inferring takeover from
	// it would turn every window-1 restart into a false eviction.
	quickDeaths := 0

	for {
		// Try to connect.
		pc, err := connectPeer(peerName, remote, pm.project)
		if err != nil {
			LogWarn("remote", "connection failed", "peer", peerName, "attempt", attempt, "err", err)
			pm.onPanesChanged(peerName, nil, false)

			delay := backoff(attempt)
			attempt++
			select {
			case <-time.After(delay):
				continue
			case <-pm.quit:
				return
			}
		}

		// Connected successfully.
		attempt = 0
		connectedAt := time.Now()
		LogInfo("remote", "connected", "peer", peerName, "agents", len(pc.panes))
		// OWNERSHIP BEFORE PANES (ini-x5ob). A window renders only what it has
		// been served, so applying the handshake's ownership map first is what
		// lets it plan its agents on the very first frame. Handing over the
		// panes first would leave a frame with panes and no ownership -- zero
		// planned panes, which is the state that armed the ini-w6z replay
		// crash and the guarantee ini-6m4 bought.
		if pm.onPaneOwnership != nil && pc.helloOwner != nil {
			LogInfo("remote", "pane ownership at handshake", "peer", peerName, "agents", len(pc.helloOwner))
			pm.onPaneOwnership(pc.helloOwner)
		}
		pm.onPanesChanged(peerName, pc.panes, true)

		// Start heartbeat: ping every 30s. On failure, close the session
		// to unblock all stream.Read calls (yamux ignores SetReadDeadline).
		heartbeatDone := make(chan struct{})
		go pm.heartbeat(peerName, pc, heartbeatDone)

		// Register request handler for forward_send with delivery confirmation.
		// When the daemon sends a forward_send WITH an ID, the mux dispatches
		// it here. We deliver to the local pane and respond with OK or error.
		pc.mux.SetRequestHandler(func(req ControlResp) ControlResp {
			if req.Action == "forward_send" && pm.onForwardSend != nil {
				LogInfo("remote", "forward_send request", "peer", peerName, "target", req.Target)
				if err := pm.onForwardSend(req.Target, req.Text, req.Enter); err != nil {
					return ControlResp{Error: err.Error()}
				}
				return ControlResp{OK: true}
			}
			return ControlResp{Error: "unknown action"}
		})

		// Consume unsolicited events from the control stream (backward compat
		// for id-less events from older daemons).
		go pm.consumeEvents(peerName, pc, heartbeatDone)

		// Monitor connection health: wait for all RemotePanes to go dead,
		// which signals the yamux session died (either naturally or via
		// heartbeat closing the session).
		pm.waitForDisconnect(peerName, pc)
		close(heartbeatDone)

		// Close the yamux session and control mux to release goroutines,
		// file descriptors, and the TCP connection.
		pc.Close()

		LogWarn("remote", "disconnected", "peer", peerName)
		pm.onPanesChanged(peerName, nil, false)

		// Sweep for a verdict that raced the teardown (ini-jhm6): the verdict
		// is written just before the server closes the session, so it can sit
		// parsed-but-undelivered in the mux's event buffer at the moment
		// consumeEvents exits on heartbeatDone -- the flag never sets and the
		// eviction reads as transient. MEASURED by the war test: the verdict
		// path redialed once before this sweep existed. Bounded, not polite:
		// the reader has already seen the bytes or never will.
		pm.sweepEvictionVerdict(pc, time.Second)

		// EVICTION IS TERMINAL (ini-jhm6). Two exits, one conclusion:
		// the server's explicit verdict, or -- because the verdict is a
		// best-effort write racing a close -- the inference that being
		// accepted and then dropped almost immediately, twice in a row, means
		// losing a takeover war. Either way this loop ENDS: retrying is what
		// turned one stale process into an all-afternoon 1-second flap.
		if pc.evicted.Load() {
			pm.concludeEvicted(peerName, "another "+peerName+" client took over this identity")
			return
		}
		if time.Since(connectedAt) < quickEvictionWindow {
			quickDeaths++
			if quickDeaths >= quickEvictionLimit {
				pm.concludeEvicted(peerName, fmt.Sprintf(
					"connection accepted and dropped within %s, %d times in a row -- concluding another client took over (eviction verdict lost in flight)",
					quickEvictionWindow, quickDeaths))
				return
			}
		} else {
			quickDeaths = 0
		}

		// Brief pause before reconnecting.
		select {
		case <-time.After(reconnectInitial):
		case <-pm.quit:
			return
		}
	}
}

// heartbeat sends a ping control command every 30s and expects a pong
// within 5s. If the ping fails or times out, it closes the yamux session
// to force all stream.Read calls to return, triggering disconnect detection.
// This is the reliable liveness check: it tests actual end-to-end reachability,
// not TCP/yamux internals that may buffer writes to a dead peer.
func (pm *peerManager) heartbeat(peerName string, pc *peerConn, done chan struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			resp, err := pc.mux.Request(ControlCmd{Action: "ping"})
			if err != nil || !resp.OK {
				LogWarn("remote", "heartbeat failed, closing session", "peer", peerName)
				pc.session.Close()
				return
			}
		case <-done:
			return
		case <-pm.quit:
			return
		}
	}
}

// consumeEvents drains unsolicited events from the ControlMux and handles:
//   - forward_send: delivers a message to a local pane
//   - stream_added: accepts a new yamux stream and creates a RemotePane via
//     onPaneAdded (stream-on-create from configure_agent push)
//
// Exits when mux.Done, the local done chan, or pm.quit fires.
func (pm *peerManager) consumeEvents(peerName string, pc *peerConn, done chan struct{}) {
	for {
		select {
		case ev := <-pc.mux.Events():
			switch ev.Action {
			case "forward_send":
				if pm.onForwardSend != nil {
					LogInfo("remote", "forward_send event (id-less)", "peer", peerName, "target", ev.Target)
					pm.onForwardSend(ev.Target, ev.Text, ev.Enter) //nolint:errcheck // id-less events have no caller to report to
				}
			case agentStatusAction:
				if pm.onAgentStatus != nil {
					pm.onAgentStatus(ev.Name, ev.Beads, ev.Bead, ev.Text, ev.WaitingState)
				}
			case identityTakenOverAction:
				LogWarn("remote", "identity taken over", "peer", peerName, "reason", ev.Text)
				pc.evicted.Store(true)
				// Close now so waitForDisconnect returns promptly; managePeer
				// reads the flag and terminates instead of redialing.
				pc.Close()
			case paneOwnershipAction:
				if pm.onPaneOwnership != nil {
					LogInfo("remote", "pane ownership from window 1", "peer", peerName, "agents", len(ev.Owner))
					pm.onPaneOwnership(ev.Owner)
				}
			case sessionNoticeAction:
				if pm.onSessionNotice != nil {
					LogInfo("remote", "session notice from window 1", "peer", peerName, "text", ev.Text)
					pm.onSessionNotice(ev.Text)
				}
			case "stream_added":
				pm.handleStreamAdded(peerName, pc, ev)
			case "agent_removed":
				pm.handleAgentRemoved(peerName, pc, ev)
			}
		case <-pc.mux.Done():
			return
		case <-done:
			return
		case <-pm.quit:
			return
		}
	}
}

// handleStreamAdded accepts a new yamux stream announced by stream_added,
// creates a RemotePane bound to it, and calls onPaneAdded so the TUI inserts
// the pane into the live grid.
func (pm *peerManager) handleStreamAdded(peerName string, pc *peerConn, ev ControlResp) {
	if pm.onPaneAdded == nil {
		LogWarn("remote", "stream_added but no onPaneAdded callback", "peer", peerName)
		return
	}
	if ev.Name == "" {
		LogWarn("remote", "stream_added missing name", "peer", peerName)
		return
	}
	stream, err := pc.session.Accept()
	if err != nil {
		LogWarn("remote", "stream_added accept failed", "peer", peerName, "agent", ev.Name, "err", err)
		return
	}
	rp := NewRemotePane(ev.Name, peerName, stream, pc.mux, 80, 24)
	rp.Start()
	pc.panesMu.Lock()
	pc.panes = append(pc.panes, rp)
	pc.panesMu.Unlock()
	LogInfo("remote", "stream-on-create attached", "peer", peerName, "agent", ev.Name)
	pm.onPaneAdded(peerName, rp)
}

// handleAgentRemoved drops the named RemotePane after the daemon pruned it —
// the removal twin of handleStreamAdded. The pane closes and the TUI callback
// removes it from the live grid; no reconnect required.
func (pm *peerManager) handleAgentRemoved(peerName string, pc *peerConn, ev ControlResp) {
	if ev.Name == "" {
		LogWarn("remote", "agent_removed missing name", "peer", peerName)
		return
	}
	pc.panesMu.Lock()
	var removed PaneView
	for i, rp := range pc.panes {
		if rp.Name() == ev.Name {
			removed = rp
			pc.panes = append(pc.panes[:i], pc.panes[i+1:]...)
			break
		}
	}
	pc.panesMu.Unlock()
	if c, ok := removed.(interface{ Close() }); ok && removed != nil {
		c.Close()
	}
	LogInfo("remote", "agent removed by daemon", "peer", peerName, "agent", ev.Name)
	if pm.onPaneRemoved != nil {
		pm.onPaneRemoved(peerName, ev.Name)
	}
}

// waitForDisconnect blocks until all panes from a peer are dead (yamux
// session closed) or pm.quit fires.
func (pm *peerManager) waitForDisconnect(peerName string, pc *peerConn) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// The yamux session is the authority on whether the CONNECTION is alive.
	// Pane liveness is a proxy for it, and a proxy that lies when the set is
	// empty (ini-1ch): "every pane is dead" is VACUOUSLY TRUE with no panes, so
	// a window that has been assigned no agents tore down a perfectly healthy
	// session on the first tick, reconnected, and tore it down again -- against
	// a window 1 that was alive the whole time, which is exactly what the
	// operator saw. Watching the session directly is both correct for the empty
	// case and a better signal than the proxy in every other case.
	var closed <-chan struct{}
	if pc != nil && pc.session != nil {
		closed = pc.session.CloseChan()
	}

	for {
		select {
		case <-closed:
			return
		case <-ticker.C:
			pc.panesMu.Lock()
			panes := append([]PaneView(nil), pc.panes...)
			pc.panesMu.Unlock()
			// No panes is not evidence of anything. Only a non-empty set whose
			// members have ALL died says the peer is gone.
			if len(panes) == 0 {
				continue
			}
			allDead := true
			for _, p := range panes {
				if p.IsAlive() {
					allDead = false
					break
				}
			}
			if allDead {
				return
			}
		case <-pm.quit:
			return
		}
	}
}

// SetOnSessionNotice registers the callback fired when window 1 broadcasts a
// session-level notice, so the attached window can surface it locally
// (ini-9ka.8).
// SetOnEvicted registers the terminal-eviction callback (ini-jhm6).
func (pm *peerManager) SetOnEvicted(fn func(peerName, reason string)) {
	pm.cbMu.Lock()
	pm.onEvicted = fn
	pm.cbMu.Unlock()
}

func (pm *peerManager) SetOnSessionNotice(fn func(text string)) {
	pm.onSessionNotice = fn
}

// SetOnPaneOwnership registers the callback fired when window 1 serves its
// ownership decision (ini-x5ob).
func (pm *peerManager) SetOnPaneOwnership(fn func(owner map[string]string)) {
	pm.onPaneOwnership = fn
}

// SetOnAgentStatus registers the callback fired when window 1 broadcasts an
// agent's bead/description state (ini-9ka.11) and its needs-input state
// (ini-35ak).
func (pm *peerManager) SetOnAgentStatus(fn func(name string, beads []string, primary, desc string, ws WaitingState)) {
	pm.onAgentStatus = fn
}
