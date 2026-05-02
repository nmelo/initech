// remote_conn.go manages outbound connections to headless daemon peers.
// On TUI startup, it dials each configured remote, performs the yamux+hello
// handshake, and returns RemotePane instances that the TUI adds to its pane
// list. Failures are logged and skipped (graceful degradation).
package tui

import (
	"encoding/json"
	"fmt"
	"net"
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
	panes   []PaneView
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
		Version:  1,
		Token:    token,
		PeerName: project.PeerName,
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

	serverPeerName := helloOK.PeerName
	if serverPeerName != peerName {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("peer_name mismatch: expected %q, got %q", peerName, serverPeerName)
	}

	// Read stream_map.
	if !scanner.Scan() {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("no stream_map response")
	}
	var streamMap StreamMapMsg
	if err := json.Unmarshal(scanner.Bytes(), &streamMap); err != nil {
		ctrl.Close()
		session.Close()
		return nil, fmt.Errorf("invalid stream_map: %w", err)
	}

	// Create a single ControlMux for all RemotePanes from this peer.
	// The mux owns the reader goroutine and routes responses by ID.
	mux := NewControlMux(ctrl)

	// Build a reverse map: stream ID -> agent name.
	agentByStreamID := streamMap.Streams

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

	return &peerConn{session: session, mux: mux, panes: panes}, nil
}
