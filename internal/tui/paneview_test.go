// Tests for PaneView interface compliance and getter methods on Pane.
package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"github.com/gdamore/tcell/v2"
	"golang.org/x/term"
)

// TestPaneImplementsPaneView is a runtime check that supplements the
// compile-time assertion (var _ PaneView = (*Pane)(nil)) in pane.go.
func TestPaneImplementsPaneView(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{name: "eng1", emu: emu, alive: true}
	var pv PaneView = p
	if pv.Name() != "eng1" {
		t.Errorf("Name() = %q, want 'eng1'", pv.Name())
	}
}

// TestPaneView_Name returns the pane's display name.
func TestPaneView_Name(t *testing.T) {
	p := &Pane{name: "super", emu: vt.NewSafeEmulator(10, 5)}
	if p.Name() != "super" {
		t.Errorf("Name() = %q, want 'super'", p.Name())
	}
}

// TestPaneView_Host returns "" for local panes.
func TestPaneView_Host(t *testing.T) {
	p := &Pane{name: "eng1", emu: vt.NewSafeEmulator(10, 5)}
	if p.Host() != "" {
		t.Errorf("Host() = %q, want empty string for local pane", p.Host())
	}
}

// TestPaneView_IsAlive reflects the alive field.
func TestPaneView_IsAlive(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), alive: true}
	if !p.IsAlive() {
		t.Error("IsAlive() should be true when alive=true")
	}
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	if p.IsAlive() {
		t.Error("IsAlive() should be false when alive=false")
	}
}

// TestPaneView_IsSuspended reflects the suspended field.
func TestPaneView_IsSuspended(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5)}
	if p.IsSuspended() {
		t.Error("IsSuspended() should be false by default")
	}
	p.SetSuspended(true)
	if !p.IsSuspended() {
		t.Error("IsSuspended() should be true after SetSuspended(true)")
	}
}

// TestPaneView_IsPinned reflects the pinned field.
func TestPaneView_IsPinned(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5)}
	if p.IsPinned() {
		t.Error("IsPinned() should be false by default")
	}
	p.SetPinned(true)
	if !p.IsPinned() {
		t.Error("IsPinned() should be true after SetPinned(true)")
	}
}

// TestPaneView_Activity returns the current activity state.
func TestPaneView_Activity(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), activity: StateRunning}
	if p.Activity() != StateRunning {
		t.Errorf("Activity() = %v, want StateRunning", p.Activity())
	}
}

// TestPaneView_LastOutputTime returns the last output timestamp.
func TestPaneView_LastOutputTime(t *testing.T) {
	now := time.Now()
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), lastOutputTime: now}
	if !p.LastOutputTime().Equal(now) {
		t.Error("LastOutputTime() should return the stored time")
	}
}

// TestPaneView_BeadID returns the current bead assignment.
func TestPaneView_BeadID(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), beadID: "ini-abc"}
	if p.BeadID() != "ini-abc" {
		t.Errorf("BeadID() = %q, want 'ini-abc'", p.BeadID())
	}
}

// TestPaneView_SessionDesc returns the session description.
func TestPaneView_SessionDesc(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), sessionDesc: "my session"}
	if p.SessionDesc() != "my session" {
		t.Errorf("SessionDesc() = %q, want 'my session'", p.SessionDesc())
	}
}

// TestPaneView_Emulator returns the SafeEmulator.
func TestPaneView_Emulator(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{emu: emu}
	if p.Emulator() != emu {
		t.Error("Emulator() should return the pane's SafeEmulator")
	}
}

// TestPaneView_AgentType returns the pane's semantic agent type.
func TestPaneView_AgentType(t *testing.T) {
	p := &Pane{emu: vt.NewSafeEmulator(10, 5), agentType: "codex"}
	if p.AgentType() != "codex" {
		t.Errorf("AgentType() = %q, want codex", p.AgentType())
	}
}

// TestPaneView_GetRegion returns the pane's screen region.
func TestPaneView_GetRegion(t *testing.T) {
	r := Region{X: 10, Y: 20, W: 80, H: 24}
	p := &Pane{emu: vt.NewSafeEmulator(80, 24), region: r}
	if p.GetRegion() != r {
		t.Errorf("GetRegion() = %v, want %v", p.GetRegion(), r)
	}
}

// TestPaneView_SendKey forwards a key event through the emulator.
func TestPaneView_SendKey(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	p := &Pane{name: "eng1", emu: emu, alive: true}
	// Must not panic.
	p.SendKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
}

// TestPaneView_SendText injects text through the local pane send path.
func TestPaneView_SendText(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	oldState, err := term.MakeRaw(int(tty.Fd()))
	if err != nil {
		t.Fatalf("MakeRaw: %v", err)
	}
	defer term.Restore(int(tty.Fd()), oldState)

	emu := vt.NewSafeEmulator(80, 24)
	readDone := make(chan []byte, 1)
	go func() {
		var got []byte
		buf := make([]byte, 256)
		for len(got) < 1 {
			n, err := emu.Read(buf)
			got = append(got, buf[:n]...)
			if err != nil {
				break
			}
		}
		readDone <- got
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true, ptmx: ptmx}
	p.SendText("hi", false)

	buf := make([]byte, 256)
	tty.SetReadDeadline(time.Now().Add(time.Second))
	n, err := tty.Read(buf)
	if err != nil {
		t.Fatalf("SendText PTY read: %v", err)
	}
	if got := string(buf[:n]); got != "\x1b[200~hi\x1b[201~" {
		t.Fatalf("SendText PTY write = %q, want %q", got, "\x1b[200~hi\x1b[201~")
	}

	select {
	case got := <-readDone:
		if len(got) < 1 {
			t.Fatalf("SendText: got %d bytes, want >= 1 (Ctrl+S)", len(got))
		}
		if got[0] != 0x13 {
			t.Errorf("SendText: first byte = 0x%02x, want 0x13 (Ctrl+S stash)", got[0])
		}
		if strings.Contains(string(got), "hi") {
			t.Errorf("SendText emulator output = %q, want no pasted body bytes", string(got))
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("SendText: timed out waiting for emulator output")
	}
}

// TestPaneView_InterfaceMethodSet ensures all PaneView methods are callable
// through the interface (not just directly on *Pane).
func TestPaneView_InterfaceMethodSet(t *testing.T) {
	emu := vt.NewSafeEmulator(10, 5)
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	p := &Pane{
		name:      "test",
		emu:       emu,
		alive:     true,
		activity:  StateIdle,
		region:    Region{X: 0, Y: 0, W: 10, H: 5},
		kittEpoch: time.Now(),
	}

	var pv PaneView = p

	// Call every interface method to verify they don't panic.
	_ = pv.Name()
	_ = pv.Host()
	_ = pv.IsAlive()
	_ = pv.IsSuspended()
	_ = pv.IsPinned()
	_ = pv.Activity()
	_ = pv.LastOutputTime()
	_ = pv.BeadID()
	_ = pv.SessionDesc()
	_ = pv.Emulator()
	_ = pv.GetRegion()
	pv.SendKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))

	s := tcell.NewSimulationScreen("")
	s.Init()
	s.SetSize(10, 6)
	pv.Render(s, false, false, 1, Selection{})
}
