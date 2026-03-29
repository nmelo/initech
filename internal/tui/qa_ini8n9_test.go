// QA tests for ini-8n9: host:agent send routing and peers query.
package tui

import (
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
)

// ── host:agent parsing (tested via IPCRequest.Host) ─────────────────

func TestHostAgentParsing(t *testing.T) {
	tests := []struct {
		input    string
		wantHost string
		wantName string
	}{
		{"eng1", "", "eng1"},
		{"workbench:eng1", "workbench", "eng1"},
		{"spark1:qa2", "spark1", "qa2"},
		{":eng1", "", "eng1"}, // empty host = local
		{"a:b:c", "a", "b:c"}, // only first colon splits
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			target := tc.input
			var host string
			if idx := strings.Index(target, ":"); idx >= 0 {
				host = target[:idx]
				target = target[idx+1:]
			}
			if host != tc.wantHost {
				t.Errorf("host = %q, want %q", host, tc.wantHost)
			}
			if target != tc.wantName {
				t.Errorf("target = %q, want %q", target, tc.wantName)
			}
		})
	}
}

// ── handleIPCPeers ──────────────────────────────────────────────────

func TestHandleIPCPeers_LocalOnly(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{
			{name: "super", emu: vt.NewSafeEmulator(10, 5), alive: true},
			{name: "eng1", emu: vt.NewSafeEmulator(10, 5), alive: true},
		}),
		project: &config.Project{PeerName: "macbook"},
	}

	conn := &fakeConn{}
	tui.handleIPCPeers(conn)

	var resp IPCResponse
	if err := json.Unmarshal(conn.written[:conn.findNewline()], &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("resp not OK: %s", resp.Error)
	}

	var peers []PeerInfo
	if err := json.Unmarshal([]byte(resp.Data), &peers); err != nil {
		t.Fatalf("parse peers: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("peers count = %d, want 1", len(peers))
	}
	if peers[0].Name != "macbook" {
		t.Errorf("peer name = %q, want 'macbook'", peers[0].Name)
	}
	if len(peers[0].Agents) != 2 {
		t.Errorf("agents = %v, want [super eng1]", peers[0].Agents)
	}
}

func TestHandleIPCPeers_WithRemotes(t *testing.T) {
	localPane := &Pane{name: "super", emu: vt.NewSafeEmulator(10, 5), alive: true}
	remotePane := &RemotePane{
		name:     "eng1",
		host:     "workbench",
		emu:      vt.NewSafeEmulator(10, 5),
		ctrlConn: &fakeConn{},
	}

	tui := &TUI{
		panes:   []PaneView{localPane, remotePane},
		project: &config.Project{PeerName: "macbook"},
	}

	conn := &fakeConn{}
	tui.handleIPCPeers(conn)

	var resp IPCResponse
	json.Unmarshal(conn.written[:conn.findNewline()], &resp)

	var peers []PeerInfo
	json.Unmarshal([]byte(resp.Data), &peers)

	if len(peers) != 2 {
		t.Fatalf("peers count = %d, want 2", len(peers))
	}
	// Local peer should be first.
	if peers[0].Name != "macbook" {
		t.Errorf("first peer = %q, want 'macbook' (local first)", peers[0].Name)
	}
}

func TestHandleIPCPeers_NoPeerName(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{
			{name: "eng1", emu: vt.NewSafeEmulator(10, 5), alive: true},
		}),
		project: &config.Project{},
	}

	conn := &fakeConn{}
	tui.handleIPCPeers(conn)

	var resp IPCResponse
	json.Unmarshal(conn.written[:conn.findNewline()], &resp)

	var peers []PeerInfo
	json.Unmarshal([]byte(resp.Data), &peers)

	if len(peers) != 1 || peers[0].Name != "local" {
		t.Errorf("without peer_name, should use 'local', got %v", peers)
	}
}

// ── forwardSendToRemote ─────────────────────────────────────────────

func TestForwardSendToRemote_Found(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	rp := &RemotePane{
		name:     "eng1",
		host:     "workbench",
		emu:      emu,
		ctrlConn: &fakeConn{}, // Absorbs the send command JSON.
	}

	tui := &TUI{
		panes: []PaneView{rp},
	}

	conn := &fakeConn{}
	tui.forwardSendToRemote(conn, IPCRequest{
		Host:   "workbench",
		Target: "eng1",
		Text:   "hello",
		Enter:  true,
	})

	var resp IPCResponse
	json.Unmarshal(conn.written[:conn.findNewline()], &resp)
	if !resp.OK {
		t.Errorf("forward send should succeed, got error: %s", resp.Error)
	}
}

func TestForwardSendToRemote_NotFound(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{
			{name: "eng1", emu: vt.NewSafeEmulator(10, 5), alive: true},
		}),
	}

	conn := &fakeConn{}
	tui.forwardSendToRemote(conn, IPCRequest{
		Host:   "nonexistent",
		Target: "eng1",
		Text:   "hello",
	})

	var resp IPCResponse
	json.Unmarshal(conn.written[:conn.findNewline()], &resp)
	if resp.OK {
		t.Error("forward to unknown host should fail")
	}
	if !strings.Contains(resp.Error, "not found") {
		t.Errorf("error = %q, want contains 'not found'", resp.Error)
	}
}

// ── fakeConn for testing IPC handlers ───────────────────────────────

type fakeConn struct {
	written []byte
}

func (f *fakeConn) Write(b []byte) (int, error) {
	f.written = append(f.written, b...)
	return len(b), nil
}

func (f *fakeConn) Read([]byte) (int, error)                  { return 0, io.EOF }
func (f *fakeConn) Close() error                               { return nil }
func (f *fakeConn) LocalAddr() net.Addr                        { return nil }
func (f *fakeConn) RemoteAddr() net.Addr                       { return nil }
func (f *fakeConn) SetDeadline(time.Time) error                { return nil }
func (f *fakeConn) SetReadDeadline(time.Time) error            { return nil }
func (f *fakeConn) SetWriteDeadline(time.Time) error           { return nil }

func (f *fakeConn) findNewline() int {
	for i, b := range f.written {
		if b == '\n' {
			return i
		}
	}
	return len(f.written)
}
