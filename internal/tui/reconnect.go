// reconnect.go manages persistent connections to remote daemon peers with
// automatic reconnection on failure. Each remote peer gets a background
// goroutine that handles the connect/reconnect lifecycle.
package tui

import (
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
	onPanesChanged func(peerName string, panes []PaneView)
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
func newPeerManager(project *config.Project, onChange func(string, []PaneView), onFwd func(string, string, bool) error, quit chan struct{}) *peerManager {
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

// wait blocks until all peer goroutines have exited.
func (pm *peerManager) wait() {
	pm.wg.Wait()
}

// managePeer runs the connect/reconnect loop for a single remote peer.
// It exits when pm.quit is closed.
func (pm *peerManager) managePeer(peerName string, remote config.Remote) {
	defer pm.wg.Done()

	attempt := 0
	for {
		// Try to connect.
		pc, err := connectPeer(peerName, remote, pm.project)
		if err != nil {
			LogWarn("remote", "connection failed", "peer", peerName, "attempt", attempt, "err", err)
			pm.onPanesChanged(peerName, nil)

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
		LogInfo("remote", "connected", "peer", peerName, "agents", len(pc.panes))
		pm.onPanesChanged(peerName, pc.panes)

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
		go pm.consumeEvents(peerName, pc.mux, heartbeatDone)

		// Monitor connection health: wait for all RemotePanes to go dead,
		// which signals the yamux session died (either naturally or via
		// heartbeat closing the session).
		pm.waitForDisconnect(peerName, pc.panes)
		close(heartbeatDone)

		// Close the yamux session and control mux to release goroutines,
		// file descriptors, and the TCP connection.
		pc.Close()

		LogWarn("remote", "disconnected", "peer", peerName)
		pm.onPanesChanged(peerName, nil)

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

// consumeEvents drains unsolicited events from the ControlMux and handles
// forward_send commands by delivering messages to local panes. Exits when
// done or pm.quit is closed.
func (pm *peerManager) consumeEvents(peerName string, mux *ControlMux, done chan struct{}) {
	for {
		select {
		case ev := <-mux.Events():
			if ev.Action == "forward_send" && pm.onForwardSend != nil {
				LogInfo("remote", "forward_send event (id-less)", "peer", peerName, "target", ev.Target)
				pm.onForwardSend(ev.Target, ev.Text, ev.Enter) //nolint:errcheck // id-less events have no caller to report to
			}
		case <-mux.Done():
			return
		case <-done:
			return
		case <-pm.quit:
			return
		}
	}
}

// waitForDisconnect blocks until all panes from a peer are dead (yamux
// session closed) or pm.quit fires.
func (pm *peerManager) waitForDisconnect(peerName string, panes []PaneView) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
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
