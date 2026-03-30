// reconnect.go manages persistent connections to remote daemon peers with
// automatic reconnection on failure. Each remote peer gets a background
// goroutine that handles the connect/reconnect lifecycle.
package tui

import (
	"math"
	"sync"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// Reconnect backoff parameters.
const (
	reconnectInitial = 1 * time.Second
	reconnectMax     = 30 * time.Second
	reconnectFactor  = 2.0
)

// backoff returns the next retry delay using exponential backoff with a cap.
func backoff(attempt int) time.Duration {
	f := float64(reconnectInitial) * math.Pow(reconnectFactor, float64(attempt))
	if f > float64(reconnectMax) || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return reconnectMax
	}
	return time.Duration(f)
}

// peerManager manages the connection lifecycle for all remote peers.
// It owns a goroutine per peer that handles connect/reconnect.
type peerManager struct {
	project *config.Project
	mu      sync.Mutex
	// onPanesChanged is called (on any goroutine) when remote panes are
	// added or go offline. The callback receives the peer name and the
	// new set of PaneViews for that peer (nil = all offline).
	onPanesChanged func(peerName string, panes []PaneView)
	quit           chan struct{}
	wg             sync.WaitGroup
}

// newPeerManager creates a manager and starts a goroutine per remote peer.
// All connections (initial and reconnect) happen in the background so the
// TUI renders immediately without blocking on network I/O.
func newPeerManager(project *config.Project, onChange func(string, []PaneView), quit chan struct{}) *peerManager {
	pm := &peerManager{
		project:        project,
		onPanesChanged: onChange,
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
		panes, err := connectPeer(peerName, remote, pm.project)
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
		LogInfo("remote", "connected", "peer", peerName, "agents", len(panes))
		pm.onPanesChanged(peerName, panes)

		// Monitor connection health: wait for all RemotePanes to go dead,
		// which signals the yamux session died.
		pm.waitForDisconnect(peerName, panes)

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
