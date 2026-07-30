package tui

import (
	"bufio"
	"net"
	"strings"
	"testing"
)

// TestHandleIPCBead_NotAliveReturnsError is a regression test for ini-cs7:
// handleIPCBead used to find a dead pane and call SetBead on it anyway,
// returning OK. A restart replaces the pane via NewPane, which starts with
// no bead state, so the update is silently lost the moment the pane comes
// back -- indistinguishable from the request never having been sent.
func TestHandleIPCBead_NotAliveReturnsError(t *testing.T) {
	p := testPane("qa4")
	p.alive = false
	tu := newTestTUI(p)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		tu.handleIPCBead(serverConn, IPCRequest{Action: "bead", Target: "qa4", Text: "ini-svv7"})
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if !scanner.Scan() {
		t.Fatalf("no response: %v", scanner.Err())
	}
	resp := scanner.Text()
	<-done

	if !strings.Contains(resp, `"ok":false`) {
		t.Errorf("response = %s, want ok:false for a dead target pane", resp)
	}
	if !strings.Contains(resp, "not alive") {
		t.Errorf("response = %s, want an error mentioning the pane is not alive", resp)
	}
	if got := p.BeadID(); got != "" {
		t.Errorf("BeadID() = %q, want empty -- a dead pane's bead must not be set", got)
	}
}

// TestHandleIPCBead_AlivePaneStillWorks is a control for the above: a live
// pane must still succeed, proving the IsAlive check doesn't over-reject.
func TestHandleIPCBead_AlivePaneStillWorks(t *testing.T) {
	p := testPane("qa4")
	tu := newTestTUI(p)

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		tu.handleIPCBead(serverConn, IPCRequest{Action: "bead", Target: "qa4", Text: "ini-svv7"})
		close(done)
	}()

	scanner := bufio.NewScanner(clientConn)
	if !scanner.Scan() {
		t.Fatalf("no response: %v", scanner.Err())
	}
	resp := scanner.Text()
	<-done

	if !strings.Contains(resp, `"ok":true`) {
		t.Errorf("response = %s, want ok:true for a live target pane", resp)
	}
	if got := p.BeadID(); got != "ini-svv7" {
		t.Errorf("BeadID() = %q, want ini-svv7", got)
	}
}
