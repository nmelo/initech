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
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// connectTimeout is how long to wait for a TCP connection to a remote peer.
const connectTimeout = 5 * time.Second

// connectPeer establishes a yamux connection to a single remote peer, performs
// the hello handshake, reads the stream map, and creates RemotePanes.
func connectPeer(peerName string, remote config.Remote, project *config.Project) ([]PaneView, error) {
	// Dial TCP.
	conn, err := net.DialTimeout("tcp", remote.Addr, connectTimeout)
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
	writeJSON(ctrl, hello)

	// Read hello_ok.
	scanner := bufio.NewScanner(ctrl)
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
		LogWarn("remote", "peer_name mismatch",
			"expected", peerName, "got", serverPeerName)
		// Continue anyway; the operator may have renamed the peer.
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
		stream := rawStream

		rp := NewRemotePane(agentName, serverPeerName, stream, ctrl, 80, 24)
		rp.Start()
		panes = append(panes, rp)
		LogDebug("remote", "agent connected", "peer", serverPeerName, "agent", agentName)
	}

	return panes, nil
}
