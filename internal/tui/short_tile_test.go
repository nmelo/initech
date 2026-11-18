package tui

import (
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func TestShortTilePTYAtSpawnAndResize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix PTY")
	}
	p, err := NewPane(PaneConfig{Name: "seat", Command: []string{"/bin/cat"}}, 1, 53)
	if err != nil {
		t.Fatal(err)
	}
	p.Start()
	t.Cleanup(p.Close)
	check := func(stage string, cols, rows int) {
		t.Helper()
		c, r := ptySize(t, p)
		ec, er := p.emuSize()
		if c != cols || r != rows || ec != cols || er != rows {
			t.Errorf("%s: PTY %dx%d, emulator %dx%d, want %dx%d", stage, c, r, ec, er, cols, rows)
		}
	}
	check("spawn", 53, 40)
	p.Resize(5, 54)
	check("resize", 54, 40)
	p.Resize(6, 55)
	check("six rows, normal buffer", 55, 18)
	p.emu.Write([]byte("\x1b[?1049h"))
	p.Resize(1, 56)
	check("fullscreen", 56, 40)
	p.Resize(6, 57)
	check("six rows, fullscreen", 57, 6)
	p.ResizeExact(1, 58)
	check("headless owner", 58, 40)
}

func TestShortTileHiddenWakeKeepsTerminalWidthAndHeight(t *testing.T) {
	for _, kind := range []string{"codex", "agy"} {
		t.Run(kind, func(t *testing.T) {
			p := NewParkedPane(PaneConfig{Name: "seat", AgentType: kind}, 1, 47)
			if c, r := p.emuSize(); c != 47 || r != 40 {
				t.Errorf("parked size to inherit on wake: %dx%d, want 47x40", c, r)
			}
			// No drawn region. Fullscreen entry used to turn this into 1x1
			// even when the child had started with a usable terminal.
			p.emu.Write([]byte("\x1b[?1049h"))
			p.checkAltScreenTransition()
			if c, r := p.emuSize(); c != 47 || r != 40 {
				t.Errorf("hidden fullscreen size: %dx%d, want 47x40", c, r)
			}
		})
	}
}

func TestShortTileShowsAndCopiesTheLastDrawnRow(t *testing.T) {
	for _, outer := range []int{1, 2, 3} {
		for _, fullscreen := range []bool{false, true} {
			p := newAltScreenTestPane(47, 40)
			p.visible = true
			p.region = Region{W: 47, H: outer}
			if fullscreen {
				p.emu.Write([]byte("\x1b[?1049h"))
			}
			p.emu.Write([]byte("history\x1b[39;1Hprompt\x1b[40;1Hstatus"))
			ui, screen := simTUI(t, 47, 8, p)
			p.Render(screen, false, false, 1, Selection{})
			y := 0
			if outer == 3 {
				y++
			}
			screen.Show()
			if got := screenRows(t, screen)[y]; got != "status" {
				t.Errorf("outer %d fullscreen %v: drawn %q, want status", outer, fullscreen, got)
			}
			ui.layoutState.Focused = agentKey(p)
			ui.plan.Panes = []PaneRender{{Pane: p, Region: p.region}}
			ui.handleMouse(tcell.NewEventMouse(0, y, tcell.Button1, 0))
			ui.handleMouse(tcell.NewEventMouse(5, y, tcell.Button1, 0))
			if got := ui.extractSelectionText(); got != "status" {
				t.Errorf("outer %d fullscreen %v: copied %q", outer, fullscreen, got)
			}
			if got := peekReadBlocking(p, 50); got.rows != 40 || !strings.Contains(got.text, "history") || !strings.Contains(got.text, "status") {
				t.Errorf("peek missed terminal rows: %+v", got)
			}
		}
	}
}

func TestShortTileLongSendToAwakeSeatWithBeadIsConfirmed(t *testing.T) {
	sendBubble(t, func(t *testing.T) {
		nma5Window(t, 300*time.Millisecond)
		body := strings.Repeat("one line of work ", 8)
		f := &pkinFake{body: body, after: "❯ ", takes: func(int) bool { return true }}
		ui, p := v69Rig(t, f)
		p.SetBead("task-17", "Work")
		p.region = Region{W: 120, H: 3}
		p.Resize(1, 120)
		f.paint("❯ ")
		if resp := v69Send(t, ui, p.Name(), body, true); !resp.OK || resp.Delivery != DeliveryDelivered || f.enters.Load() != 1 {
			t.Fatalf("reply %+v, enters %d", resp, f.enters.Load())
		}
	})
}

func TestShortTileAddReportsPlacedAndUnplacedSeats(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a Unix PTY")
	}
	for _, mode := range []LayoutMode{LayoutGrid, LayoutFocus} {
		ui, _ := simTUI(t, 100, 5, testPane("owner"))
		ui.layoutState.Mode = mode
		ui.layoutState.Focused = "owner"
		dir := t.TempDir()
		ui.paneConfigBuilder = func(name string) (PaneConfig, error) {
			return PaneConfig{Name: name, Dir: dir, Command: []string{"/bin/cat"}}, nil
		}
		resp := i35wIPC(t, func(c net.Conn) { ui.handleIPCAdd(c, IPCRequest{Target: "eng2"}) })
		p := ui.findPaneByName("eng2")
		if p == nil {
			t.Fatalf("add: %+v", resp)
		}
		t.Cleanup(p.Close)
		tile := "1-row tile"
		if mode == LayoutFocus {
			tile = "no visible tile"
		}
		if !resp.OK || !strings.Contains(resp.Data, tile) || !strings.Contains(resp.Data, "40 rows") {
			t.Errorf("%v add: %+v", mode, resp)
		}
		c, r := ptySize(t, p.(*Pane))
		if r != 40 || c <= 1 {
			t.Errorf("%v added PTY: %dx%d", mode, c, r)
		}
	}
}

func TestShortTileSizingPreservesSavedLayout(t *testing.T) {
	p := newAltScreenTestPane(47, 40)
	p.visible = true
	ui, screen := simTUI(t, 47, 12, p)
	ui.projectRoot = t.TempDir()
	ui.applyLayout()
	ui.saveLayoutIfConfigured()
	path := layoutPathFor(t, ui)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(123456, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(47, 5)
	ui.applyLayout()
	if _, r := p.emuSize(); r != 40 {
		t.Errorf("short terminal has %d rows", r)
	}
	screen.SetSize(47, 10)
	ui.applyLayout()
	info, err := os.Stat(path)
	if err != nil || !info.ModTime().Equal(stamp) {
		t.Fatalf("sizing rewrote the saved layout: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatalf("sizing changed layout: %v, %q -> %q", err, before, after)
	}
}

func TestShortTileWakeInheritsTheChildsActualSize(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses stty on a Unix PTY")
	}
	for _, kind := range []string{"codex", "agy"} {
		t.Run(kind, func(t *testing.T) {
			cfg := PaneConfig{Name: "seat", AgentType: kind, Command: []string{"/bin/sh", "-c", `printf '\033[?1049h'; stty size; exec cat`}}
			p := NewParkedPane(cfg, 1, 7)
			// The last resize came from a viewer. The owner's old rectangle
			// no longer governs the child once the owner hides this seat.
			p.region = Region{W: 30, H: 12}
			p.Resize(1, 7)
			p.SetBead("work-17", "Work")
			ui := newTestTUI(p)
			ui.layoutState.Hidden[p.Name()] = true
			if err := ui.resumePane(p, "test"); err != nil {
				t.Fatal(err)
			}
			child := ui.panes[0].(*Pane)
			t.Cleanup(child.Close)
			if child == p || !child.IsAlive() || child.BeadID() != "work-17" {
				t.Fatalf("wake lost the working seat")
			}
			if c, r := ptySize(t, child); c != 7 || r != 40 {
				t.Fatalf("child PTY is %dx%d", c, r)
			}
			if got := peekReadBlocking(child, 40); !got.alt || !strings.Contains(got.text, "40 7") {
				t.Fatalf("fullscreen child's stty output: %+v", got)
			}
		})
	}
}

func TestShortTileInlineScrollAndCopyFollowThePromptInBothWindows(t *testing.T) {
	for _, viewer := range []bool{false, true} {
		p := newAltScreenTestPane(47, 40)
		p.visible = true
		ui, screen := simTUI(t, 47, 5, p)
		var pane PaneView = p
		r := Region{W: 47, H: 2}
		p.region = r
		if viewer {
			rp := NewRemotePane("seat", "peer", nil, nil, 47, 1)
			rp.region = r
			pane = rp
		}
		ui.panes = []PaneView{pane}
		ui.layoutState.Focused = agentKey(pane)
		ui.plan.Panes = []PaneRender{{Pane: pane, Region: r}}
		pane.Emulator().Write([]byte("older\r\nhistory\r\nprompt\r\nstatus"))
		for i, want := range []string{"status", "prompt", "status"} {
			switch i {
			case 1:
				pane.ScrollUp(1)
			case 2:
				pane.ScrollDown(1)
			}
			pane.Render(screen, false, false, 1, Selection{})
			screen.Show()
			if got := screenRows(t, screen)[0]; got != want {
				t.Errorf("viewer %v offset step %d drew %q, want %q", viewer, i, got, want)
			}
			ui.sel.active = false
			ui.handleMouse(tcell.NewEventMouse(0, 0, tcell.Button1, 0))
			ui.handleMouse(tcell.NewEventMouse(len(want)-1, 0, tcell.Button1, 0))
			if got := ui.extractSelectionText(); got != want {
				t.Errorf("viewer %v offset step %d copied %q, want %q", viewer, i, got, want)
			}
		}
	}
}

// fixedSizePTY accepts resize requests but keeps reporting its actual size.
// This reproduces the case in which an emulator alone would claim success.
type fixedSizePTY struct {
	*spawnPty
	cols, rows int
	err        error
}

func (p fixedSizePTY) Size() (int, int, error) { return p.cols, p.rows, p.err }

func TestShortTileAddRefusesAnUnusableTerminalBeforeSaving(t *testing.T) {
	for _, test := range []struct {
		name          string
		width, height int
		readErr       error
	}{
		{"height", 50, 1, nil},
		{"width", 1, 40, nil},
		{"unreadable", 50, 40, os.ErrClosed},
	} {
		t.Run(test.name, func(t *testing.T) {
			ui, _ := simTUI(t, 100, 5, testPane("owner"))
			ui.projectRoot = t.TempDir()
			ui.applyLayout()
			ui.saveLayoutIfConfigured()
			path := layoutPathFor(t, ui)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			ui.paneConfigBuilder = func(name string) (PaneConfig, error) {
				return PaneConfig{Name: name, Dir: ui.projectRoot}, nil
			}
			var child *Pane
			ui.paneSpawner = func(cfg PaneConfig, rows, cols int) (*Pane, error) {
				child = newEmuPane(cfg.Name, cols, rows)
				child.ptmx = fixedSizePTY{newSpawnPty(t), test.width, test.height, test.readErr}
				return child, nil
			}
			resp := i35wIPC(t, func(c net.Conn) { ui.handleIPCAdd(c, IPCRequest{Target: "eng2"}) })
			if resp.OK || !strings.Contains(resp.Error, "terminal") || len(ui.panes) != 1 || child == nil || child.IsAlive() {
				t.Fatalf("add %+v, %d seats left, child %v", resp, len(ui.panes), child != nil)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("refused add changed saved layout: %v", err)
			}
		})
	}
}

func TestShortTileViewerReassertsItsSizeOnWake(t *testing.T) {
	rp := NewRemotePane("seat", "peer", nil, nil, 80, 24)
	ui := newTestTUI()
	ui.panes = []PaneView{rp}
	wake := func() {
		ui.applyAgentStatus("seat", nil, "", "", WaitingState{}, true)
		ui.applyAgentStatus("seat", nil, "", "", WaitingState{}, false)
	}
	wake()
	if rp.emu.Width() != 80 || rp.emu.Height() != 24 {
		t.Fatal("a viewer without a tile claimed the owner's size")
	}
	rp.region = Region{W: 53, H: 3}
	wake()
	if rp.resizeTimer != nil {
		defer rp.resizeTimer.Stop()
	}
	if rp.emu.Width() != 53 || rp.emu.Height() != 40 || rp.pendingRows != 1 || rp.pendingCols != 53 {
		t.Fatalf("viewer draws %dx%d and requests %dx%d", rp.emu.Width(), rp.emu.Height(), rp.pendingCols, rp.pendingRows)
	}
}
