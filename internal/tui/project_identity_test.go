package tui

// project_identity_test.go — ini-ikz3: a PORT IS NOT AN IDENTITY.
//
// THE INCIDENT, 2026-08-15: hover and initech were both configured
// window_listen :9300. initech's server bound first; hover's bind failed
// non-fatally, and hover's `--window 2` then dialed 9300, completed the
// handshake, and rendered INITECH's six agents under hover's band headers.
// Nothing errored anywhere. Keystrokes typed into that viewer would have gone
// to another project's agents.
//
// The handshake carried a token, a peer name and a protocol version, and every
// one of them matched -- because they were never about WHICH FLEET this is. The
// gate these tests cover adds the missing fact and refuses on it, in both
// directions, for two different harms: the server protects its fleet from a
// stranger, and the client protects itself from rendering a stranger's fleet.

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
)

// ikz3Handshake runs one real hello/hello_ok exchange against a real Daemon
// over a real socket pair, and returns what the server said.
//
// Real socket rather than a hand-rolled fake because the thing under test is a
// PROTOCOL step: a fake that answers hello directly would be a fixture more
// capable than the wire (ini-9isx's lesson), and would pass whatever the daemon
// actually does.
func ikz3Handshake(t *testing.T, serverProject string, clientHello HelloMsg) (HelloOKMsg, ErrorMsg, bool) {
	t.Helper()
	d := &Daemon{
		project: &config.Project{Name: serverProject, PeerName: WindowOnePeerName},
		version: "test",
	}

	srv, cli := net.Pipe()
	t.Cleanup(func() { srv.Close(); cli.Close() })

	go d.handleConnection(srv)

	// Real yamux client and a real control stream: the handshake is a protocol
	// step, so the test speaks the protocol.
	session, err := yamux.Client(cli, yamux.DefaultConfig())
	if err != nil {
		t.Fatalf("yamux client: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	ctrl, err := session.Open()
	if err != nil {
		t.Fatalf("open control stream: %v", err)
	}
	t.Cleanup(func() { ctrl.Close() })

	if err := writeJSON(ctrl, clientHello); err != nil {
		t.Fatalf("send hello: %v", err)
	}
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	scanner := NewIPCScanner(ctrl)
	if !scanner.Scan() {
		t.Fatalf("no response to hello: %v", scanner.Err())
	}
	raw := scanner.Bytes()

	var probe struct {
		Action string `json:"action"`
	}
	json.Unmarshal(raw, &probe)
	if probe.Action == "error" {
		var e ErrorMsg
		json.Unmarshal(raw, &e)
		return HelloOKMsg{}, e, false
	}
	var ok HelloOKMsg
	json.Unmarshal(raw, &ok)
	return ok, ErrorMsg{}, true
}

// TestProjectIdentity_ServerRefusesAViewerFromAnotherProject is the incident,
// red before the fix: hover's window dialing initech's port.
func TestProjectIdentity_ServerRefusesAViewerFromAnotherProject(t *testing.T) {
	_, errMsg, accepted := ikz3Handshake(t, "initech", HelloMsg{
		Action:   "hello",
		Version:  ProtocolVersion,
		PeerName: WindowPeerName(2),
		Project:  "hover",
	})

	if accepted {
		t.Fatal("initech's window server ACCEPTED a viewer belonging to hover. That viewer renders " +
			"initech's agents under hover's headers, and keystrokes typed into it go to another " +
			"project's agents -- the ini-ikz3 incident exactly.")
	}
	// The refusal must NAME both fleets: an operator seeing this needs to know
	// whose session answered and which of their projects is misconfigured.
	if !strings.Contains(errMsg.Error, "initech") || !strings.Contains(errMsg.Error, "hover") {
		t.Errorf("the refusal names neither the serving project nor the caller, so it does not tell "+
			"the operator which config to fix: %q", errMsg.Error)
	}
	if !strings.Contains(errMsg.Error, "port") {
		t.Errorf("the refusal does not mention the port, which is the thing to change: %q", errMsg.Error)
	}
}

// TestProjectIdentity_ServerAcceptsItsOwnViewer is the other direction, and it
// is what keeps the gate from being a wall: same project still attaches.
func TestProjectIdentity_ServerAcceptsItsOwnViewer(t *testing.T) {
	ok, errMsg, accepted := ikz3Handshake(t, "initech", HelloMsg{
		Action:   "hello",
		Version:  ProtocolVersion,
		PeerName: WindowPeerName(2),
		Project:  "initech",
	})
	if !accepted {
		t.Fatalf("a viewer of this project's OWN fleet was refused: %q", errMsg.Error)
	}
	if ok.Project != "initech" {
		t.Errorf("hello_ok does not carry the server's project (%q); the client cannot check the "+
			"identity from its own side", ok.Project)
	}
}

// TestProjectIdentity_ServerRefusesAnIdentitylessViewer covers the pre-upgrade
// client, and the message is the point: config validation REQUIRES a project
// name, so an empty one means an older initech rather than an unnamed fleet.
func TestProjectIdentity_ServerRefusesAnIdentitylessViewer(t *testing.T) {
	_, errMsg, accepted := ikz3Handshake(t, "initech", HelloMsg{
		Action:   "hello",
		Version:  ProtocolVersion,
		PeerName: WindowPeerName(2),
	})
	if accepted {
		t.Fatal("a viewer that sent NO project identity was accepted; on a shared port that is the " +
			"same silent cross-attach with an older client")
	}
	if !strings.Contains(strings.ToLower(errMsg.Error), "older") {
		t.Errorf("the refusal does not tell the operator the attaching window is out of date, which "+
			"is the actual fix: %q", errMsg.Error)
	}
}

// TestProjectIdentity_RefusalDoesNotKillTheServingSession is an explicit edge
// case from the bead. A gate that protects the fleet by taking it down would be
// a worse bug than the one it fixes.
func TestProjectIdentity_RefusalDoesNotKillTheServingSession(t *testing.T) {
	// A stranger is refused...
	if _, _, accepted := ikz3Handshake(t, "initech", HelloMsg{
		Action: "hello", Version: ProtocolVersion, PeerName: WindowPeerName(2), Project: "hover",
	}); accepted {
		t.Fatal("precondition: the stranger should have been refused")
	}
	// ...and the server still serves its own, on a fresh connection, exactly as
	// window 1 would after a rejected attach.
	if _, errMsg, accepted := ikz3Handshake(t, "initech", HelloMsg{
		Action: "hello", Version: ProtocolVersion, PeerName: WindowPeerName(2), Project: "initech",
	}); !accepted {
		t.Errorf("after refusing a stranger, the session stopped accepting its OWN viewers: %q",
			errMsg.Error)
	}
}

// TestProjectIdentity_ClientRefusesToRenderAnotherProjectsFleet is the OTHER
// direction, and it is not redundant with the server gate.
//
// The two checks protect different victims from different harms. The server's
// refusal keeps a stranger out of ITS fleet. This keeps THIS window from
// rendering a stranger's agents as its own — which is what the operator
// actually saw, and which a window that trusted the server's silence would
// still be one upgrade-skew away from.
func TestProjectIdentity_ClientRefusesToRenderAnotherProjectsFleet(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	// A server that answers correctly in every way EXCEPT whose fleet it is --
	// the incident's shape: same protocol, same peer name, different project.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		session, err := yamux.Server(conn, yamux.DefaultConfig())
		if err != nil {
			return
		}
		defer session.Close()
		ctrl, err := session.Accept()
		if err != nil {
			return
		}
		defer ctrl.Close()
		scanner := NewIPCScanner(ctrl)
		if !scanner.Scan() {
			return
		}
		writeJSON(ctrl, HelloOKMsg{
			Action:   "hello_ok",
			Version:  ProtocolVersion,
			PeerName: WindowOnePeerName,
			Project:  "initech", // not ours
		})
		time.Sleep(2 * time.Second)
	}()

	_, err = connectPeer(WindowOnePeerName,
		config.Remote{Addr: ln.Addr().String()},
		&config.Project{Name: "hover", PeerName: WindowPeerName(2)})

	if err == nil {
		t.Fatal("hover's window ATTACHED to a server serving initech and would render its agents " +
			"as hover's own -- the ini-ikz3 incident from the viewer's side")
	}
	if !strings.Contains(err.Error(), "initech") || !strings.Contains(err.Error(), "hover") {
		t.Errorf("the client's refusal names neither fleet, so the operator cannot tell which "+
			"window is misconfigured: %v", err)
	}
}
