package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestRemotePaneImplementsPaneView(t *testing.T) {
	// Compile-time assertion is in remote_pane.go; this test verifies at
	// runtime that all PaneView methods are callable without panic.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rp := NewRemotePane("eng1", "workbench", server, NewControlMux(client), 80, 24)
	if rp.Name() != "eng1" {
		t.Errorf("Name() = %q, want eng1", rp.Name())
	}
	if rp.Host() != "workbench" {
		t.Errorf("Host() = %q, want workbench", rp.Host())
	}
	if !rp.IsAlive() {
		t.Error("new RemotePane should be alive")
	}
	if rp.IsSuspended() {
		t.Error("RemotePane should never be suspended")
	}
	if rp.IsPinned() {
		t.Error("RemotePane should never be pinned")
	}
	if rp.IdleWithBacklog() {
		t.Error("RemotePane should not have backlog")
	}
	if rp.BacklogCount() != 0 {
		t.Error("RemotePane backlog should be 0")
	}
	if rp.Emulator() == nil {
		t.Error("Emulator() should not be nil")
	}
}

func TestRemotePaneSetBead(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	rp := NewRemotePane("eng1", "wb", server, NewControlMux(client), 80, 24)
	rp.SetBead("ini-abc", "Test bead")
	if rp.BeadID() != "ini-abc" {
		t.Errorf("BeadID() = %q, want ini-abc", rp.BeadID())
	}
}

func TestRemotePaneReadLoopFeedsEmulator(t *testing.T) {
	// Create a pipe: write PTY bytes on one end, RemotePane reads on the other.
	server, client := net.Pipe()
	defer server.Close()

	// Use a separate pipe for control.
	ctrlS, ctrlC := net.Pipe()
	defer ctrlS.Close()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24)
	rp.Start()

	// Write some terminal content.
	server.Write([]byte("Hello from remote\r\n"))
	time.Sleep(100 * time.Millisecond)

	// The emulator should have received the content.
	cols := rp.Emulator().Width()
	var line string
	for col := 0; col < cols; col++ {
		cell := rp.Emulator().CellAt(col, 0)
		if cell != nil && cell.Content != "" {
			line += cell.Content
		} else {
			line += " "
		}
	}
	if len(line) < 17 || line[:17] != "Hello from remote" {
		t.Errorf("emulator content = %q, want starts with 'Hello from remote'", line[:min(20, len(line))])
	}

	// Activity should be running (recent output).
	if rp.Activity() != StateRunning {
		t.Errorf("activity = %v, want StateRunning", rp.Activity())
	}

	rp.Close()
}

func TestRemotePaneSendKeyWritesToStream(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	ctrlS, ctrlC := net.Pipe()
	defer ctrlS.Close()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24)

	// Read what SendKey produces on the other end.
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := server.Read(buf)
		done <- buf[:n]
	}()

	// Send a regular character.
	ev := newKeyEvent('a')
	rp.SendKey(ev)

	select {
	case data := <-done:
		if string(data) != "a" {
			t.Errorf("SendKey('a') produced %q, want 'a'", string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SendKey data")
	}
}

func TestRemotePaneSendTextUsesControlChannel(t *testing.T) {
	// SendText sends a control command, not raw bytes.
	streamS, streamC := net.Pipe()
	defer streamS.Close()
	defer streamC.Close()

	ctrlS, ctrlC := net.Pipe()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", streamC, NewControlMux(ctrlC), 80, 24)

	// Read the control command from the server end and echo back with ID.
	done := make(chan ControlCmd, 1)
	go func() {
		scanner := bufio.NewScanner(ctrlS)
		if scanner.Scan() {
			var cmd ControlCmd
			json.Unmarshal(scanner.Bytes(), &cmd)
			done <- cmd
			// Echo response with the request ID so ControlMux routes it.
			resp, _ := json.Marshal(ControlResp{ID: cmd.ID, OK: true})
			ctrlS.Write(resp)
			ctrlS.Write([]byte("\n"))
		}
	}()

	go rp.SendText("hello world", true)

	select {
	case cmd := <-done:
		if cmd.Action != "send" {
			t.Errorf("action = %q, want send", cmd.Action)
		}
		if cmd.Target != "eng1" {
			t.Errorf("target = %q, want eng1", cmd.Target)
		}
		if cmd.Text != "hello world" {
			t.Errorf("text = %q, want 'hello world'", cmd.Text)
		}
		if !cmd.Enter {
			t.Error("enter should be true")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for SendText control command")
	}
}

func TestRemotePaneCloseMarksNotAlive(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	ctrlS, ctrlC := net.Pipe()
	defer ctrlS.Close()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", client, NewControlMux(ctrlC), 80, 24)
	if !rp.IsAlive() {
		t.Fatal("should be alive before close")
	}
	rp.Close()
	if rp.IsAlive() {
		t.Error("should not be alive after close")
	}
}

func TestRemotePaneResizeDebounce(t *testing.T) {
	// Verify that rapid resizes are debounced: only the final dimensions
	// reach the control channel after the debounce window.
	streamS, streamC := net.Pipe()
	defer streamS.Close()
	defer streamC.Close()

	ctrlS, ctrlC := net.Pipe()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", streamC, NewControlMux(ctrlC), 80, 24)

	// Fire 20 rapid resizes.
	for i := 0; i < 20; i++ {
		rp.Resize(20+i, 60+i)
	}

	// Wait for debounce to fire (50ms + margin).
	time.Sleep(150 * time.Millisecond)

	// Read from the control channel. Should have exactly 1 message with
	// the final dimensions (rows=39, cols=79).
	ctrlS.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	scanner := bufio.NewScanner(ctrlS)
	var commands []ControlCmd
	for scanner.Scan() {
		var cmd ControlCmd
		if err := json.Unmarshal(scanner.Bytes(), &cmd); err == nil {
			commands = append(commands, cmd)
		}
	}

	if len(commands) != 1 {
		t.Fatalf("debounce: got %d resize commands, want 1", len(commands))
	}
	if commands[0].Rows != 39 || commands[0].Cols != 79 {
		t.Errorf("final resize = %dx%d, want 39x79", commands[0].Rows, commands[0].Cols)
	}
}

func TestRemotePaneResizeUpdatesEmulatorImmediately(t *testing.T) {
	streamS, streamC := net.Pipe()
	defer streamS.Close()
	defer streamC.Close()

	ctrlS, ctrlC := net.Pipe()
	defer ctrlS.Close()
	defer ctrlC.Close()

	rp := NewRemotePane("eng1", "wb", streamC, NewControlMux(ctrlC), 80, 24)
	rp.Resize(40, 120)

	// Emulator resize is now async (goroutine). Poll for the expected dimensions.
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if rp.Emulator().Width() == 120 && rp.Emulator().Height() == 40 {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("emu dimensions = %dx%d, want 120x40 (after 100ms poll)",
		rp.Emulator().Width(), rp.Emulator().Height())
}

func TestTcellKeyToANSI(t *testing.T) {
	tests := []struct {
		name string
		key  tcell.Key
		want string
	}{
		{"enter", tcell.KeyEnter, "\r"},
		{"backspace", tcell.KeyBackspace2, "\x7f"},
		{"escape", tcell.KeyEscape, "\x1b"},
		{"up", tcell.KeyUp, "\x1b[A"},
		{"down", tcell.KeyDown, "\x1b[B"},
		{"right", tcell.KeyRight, "\x1b[C"},
		{"left", tcell.KeyLeft, "\x1b[D"},
		{"ctrl-c", tcell.KeyCtrlC, "\x03"},
		{"ctrl-d", tcell.KeyCtrlD, "\x04"},
		{"tab", tcell.KeyTab, "\t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := tcell.NewEventKey(tt.key, 0, tcell.ModNone)
			got := string(tcellKeyToANSI(ev))
			if got != tt.want {
				t.Errorf("tcellKeyToANSI(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// helper: create a tcell key event for a rune.
func newKeyEvent(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone)
}
