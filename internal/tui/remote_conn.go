// remote_conn.go manages outbound connections to headless daemon peers.
// On TUI startup, it dials each configured remote, performs the yamux+hello
// handshake, and returns RemotePane instances that the TUI adds to its pane
// list. Failures are logged and skipped (graceful degradation).
package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// connectTimeout is how long to wait for a TCP connection to a remote peer.
const connectTimeout = 5 * time.Second

// peerConn holds all resources for a single remote peer connection.
// The caller must call Close() when the connection is no longer needed
// to release the yamux session, control mux, and underlying TCP connection.
type peerConn struct {
	session *yamux.Session
	mux     *ControlMux
	panesMu sync.Mutex // Protects panes slice (mutated by stream_added handler).
	panes   []PaneView
	// evicted is set by the control-event handler when the server's
	// identity_taken_over verdict arrives (ini-jhm6), and read by managePeer
	// after the session dies to decide terminal-vs-retry. Atomic because the
	// writer is the mux consumer goroutine and the reader is the manager.
	evicted atomic.Bool
	// helloOwner is the ownership map window 1 served in the handshake
	// (ini-x5ob). Carried on the connection so the manager can apply it the
	// moment the peer is established -- before the panes are handed over, so
	// this window plans its agents on the first frame instead of rendering
	// nothing until the first broadcast arrives.
	helloOwner map[string]string
}

// Close releases connection resources: control mux, yamux session (which
// closes all streams and the TCP connection). Callers must close individual
// RemotePanes first (which waits for background goroutines) before calling
// Close, since closing the yamux session tears down the streams they read from.
func (pc *peerConn) Close() {
	if pc.mux != nil {
		pc.mux.Close()
	}
	if pc.session != nil {
		pc.session.Close()
	}
}

// connectPeer establishes a yamux connection to a single remote peer, performs
// the hello handshake, reads the stream map, and creates RemotePanes.
func connectPeer(peerName string, remote config.Remote, project *config.Project) (*peerConn, error) {
	// Dial TCP with OS-level keepalive to detect dead peers faster than
	// the default 2-hour TCP keepalive. yamux has its own keepalive, but
	// after kill -9 the TCP write may buffer locally without failing.
	dialer := net.Dialer{Timeout: connectTimeout, KeepAlive: 15 * time.Second}
	conn, err := dialer.Dial("tcp", remote.Addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", remote.Addr, err)
	}

	// Wrap in yamux client.
	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("yamux client: %w", err)
	}

	// Open the control stream (stream 0).
	ctrl, err := session.Open()
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("open control stream: %w", err)
	}

	// Send hello.
	token := remote.Token
	if token == "" {
		token = project.Token
	}
	hello := HelloMsg{
		Action:   "hello",
		Version:  ProtocolVersion,
		Token:    token,
		PeerName: project.PeerName,
		Project:  project.Name, // ini-ikz3: a port is not an identity.
	}
	if err := writeJSON(ctrl, hello); err != nil {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("send hello: %w", err)
	}

	// Read hello_ok.
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("no hello_ok response")
	}

	var helloOK HelloOKMsg
	if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("invalid hello_ok: %w", err)
	}
	if helloOK.Action == "error" {
		// Server sent an error instead of hello_ok.
		var errMsg ErrorMsg
		json.Unmarshal(scanner.Bytes(), &errMsg)
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("server rejected: %s", errMsg.Error)
	}
	if helloOK.Action != "hello_ok" {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("unexpected response action: %q", helloOK.Action)
	}

	// PROJECT IDENTITY, CLIENT SIDE (ini-ikz3). The server refuses a mismatched
	// viewer, and this checks the same fact from the other end.
	//
	// Both directions on purpose: the server's refusal protects the SERVER's
	// fleet from a stranger, and this protects THIS window from rendering a
	// stranger's fleet. They are different harms with different victims, and a
	// window that trusted the server's silence would still be one upgrade-skew
	// away from displaying another project's agents as its own.
	if helloOK.Project != "" && project.Name != "" && helloOK.Project != project.Name {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf(
			"refusing to attach: %s is serving project %q, but this window belongs to %q. "+
				"Two projects are almost certainly sharing a window_listen port",
			remote.Addr, helloOK.Project, project.Name)
	}

	serverPeerName := helloOK.PeerName
	if serverPeerName != peerName {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("peer_name mismatch: expected %q, got %q", peerName, serverPeerName)
	}

	streamMap, err := readStreamMap(scanner, peerName, &helloOK)
	if err != nil {
		ctrl.Close()
		session.Close()
		return nil, err
	}

	// Create a single ControlMux for all RemotePanes from this peer.
	// The mux owns the reader goroutine and routes responses by ID.
	mux := NewControlMux(ctrl)

	// Build a reverse map: stream ID -> agent name.
	agentByStreamID := streamMap.Streams

	// Index the handshake's agent status by name so each RemotePane can be
	// seeded with what the server already told us (ini-9ka.11).
	statusByAgent := make(map[string]AgentStatus, len(helloOK.Agents))
	// The hello_ok agent list is ORDERED: it is window 1's pane creation order,
	// snapshotted before any layout reordering. That position is the fleet's
	// canonical pane number (ini-6m4) -- the one number both windows' modals
	// display, so grab-by-number means the same agent everywhere regardless of
	// how either window has arranged its own panes.
	fleetIdxByAgent := make(map[string]int, len(helloOK.Agents))
	for i, ag := range helloOK.Agents {
		statusByAgent[ag.Name] = ag
		fleetIdxByAgent[ag.Name] = i
	}

	// Accept yamux streams opened by the server (one per agent).
	var panes []PaneView
	for range agentByStreamID {
		rawStream, err := session.Accept()
		if err != nil {
			LogWarn("remote", "stream accept failed", "peer", peerName, "err", err)
			break
		}
		yStream, ok := rawStream.(*yamux.Stream)
		if !ok {
			LogWarn("remote", "unexpected stream type", "peer", peerName)
			rawStream.Close()
			continue
		}
		agentName, ok := agentByStreamID[yStream.StreamID()]
		if !ok {
			LogWarn("remote", "unknown stream ID", "peer", peerName, "id", yStream.StreamID())
			rawStream.Close()
			continue
		}

		rp := NewRemotePane(agentName, serverPeerName, rawStream, mux, 80, 24)
		if i, ok := fleetIdxByAgent[agentName]; ok {
			rp.fleetNum = i + 1
		}
		// Apply the status the handshake ALREADY carried (ini-9ka.11). Before
		// this, helloOK.Agents was received and handed only to
		// pushRolesToPeer, so the bead arrived on the wire and was discarded --
		// which is why a secondary window showed no bead rather than a stale
		// one. This is also what makes a LATE attach correct: a window opened
		// after an agent claimed a bead sees it immediately, without waiting
		// for the next change.
		if st, ok := statusByAgent[agentName]; ok {
			beads := st.Beads
			if len(beads) == 0 && st.Bead != "" {
				// Peer predates the Beads field: fall back to the primary so
				// an older window 1 still populates something correct.
				beads = []string{st.Bead}
			}
			rp.ApplyStatus(beads, st.Desc)
			// And the needs-input state (ini-35ak), for the same reason the
			// bead was seeded here: a window that attaches while an agent is
			// ALREADY waiting must show it on its first frame. Without this
			// the viewer stays blind until the next TRANSITION, which for a
			// question already on screen may never come -- the operator
			// answers it, and window 2 never knew to tell anyone.
			rp.ApplyWaiting(st.WaitingState)
		}
		rp.Start()
		panes = append(panes, rp)
		LogDebug("remote", "agent connected", "peer", serverPeerName, "agent", agentName)
	}

	// Zero-config push: if remote.Roles is configured, send configure_agent
	// for each role and stop_agent for orphans (running but no longer in
	// config). The daemon's idempotent configure_agent handles same-owner
	// re-pushes by refreshing CLAUDE.md without disrupting running agents.
	//
	// Note: agents created by configure_agent in this call do not get yamux
	// streams in the current connection — the existing handshake already
	// allocated streams for the pre-existing pane set. They appear on the
	// next reconnect via hello_ok's running list. (Stream-on-create wiring
	// is tracked in ini-4q9.2.1.)
	if len(remote.Roles) > 0 {
		owned := make(map[string]bool, len(helloOK.Agents))
		for _, ag := range helloOK.Agents {
			owned[ag.Name] = true
		}
		configured, stopped := pushRolesToPeer(mux, peerName, remote, project, helloOK.Agents, owned)
		LogInfo("remote", "push complete", "peer", peerName, "configured", configured, "stopped", stopped)
	}

	return &peerConn{session: session, mux: mux, panes: panes, helloOwner: helloOK.Owner}, nil
}

// readStreamMap reads control frames until the stream map arrives, matching on
// ACTION rather than on position (ini-x5ob).
//
// The previous form took whatever frame came next and unmarshalled it as a
// StreamMapMsg. JSON ignores unknown fields, so ANY other control frame parsed
// "successfully" into a StreamMapMsg with a nil Streams map, and the caller
// then built ZERO panes without a single error -- a viewer holding a correct
// ownership map and showing an empty monitor, permanently, because one
// unrelated message overtook one expected message. Nothing retried.
//
// It was reachable because window 1 broadcasts on its own events and one of
// those events is a window attaching -- precisely the gap between hello_ok and
// stream_map. Measured at 4 failures in 6 composed runs before the fix.
//
// An ownership frame arriving here is kept rather than dropped: it is NEWER
// than the one hello_ok carried, so it replaces it.
func readStreamMap(scanner *bufio.Scanner, peerName string, helloOK *HelloOKMsg) (StreamMapMsg, error) {
	var streamMap StreamMapMsg
	const maxPreamble = 64 // a bound, not an expectation
	for i := 0; i < maxPreamble; i++ {
		if !scanner.Scan() {
			return streamMap, fmt.Errorf("no stream_map response")
		}
		var probe struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &probe); err != nil {
			// Names the stream map deliberately: this is the point in the
			// handshake where it is expected, and an unparseable frame here
			// is indistinguishable from a malformed stream map from the
			// caller's side. Keeping that wording also keeps the pre-existing
			// contract test meaningful rather than editing it to match new
			// prose.
			return streamMap, fmt.Errorf("invalid stream_map (unparseable control frame): %w", err)
		}
		switch probe.Action {
		case "stream_map":
			if err := json.Unmarshal(scanner.Bytes(), &streamMap); err != nil {
				return streamMap, fmt.Errorf("invalid stream_map: %w", err)
			}
			return streamMap, nil
		case paneOwnershipAction:
			var ev ControlResp
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil && ev.Owner != nil && helloOK != nil {
				helloOK.Owner = ev.Owner
				LogInfo("remote", "ownership arrived before stream_map; kept",
					"peer", peerName, "agents", len(ev.Owner))
			}
		default:
			LogInfo("remote", "skipping control frame before stream_map",
				"peer", peerName, "action", probe.Action)
		}
	}
	return streamMap, fmt.Errorf("no stream_map in the first %d control frames", maxPreamble)
}
