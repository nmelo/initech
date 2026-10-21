package tui

import (
	"encoding/json"
	"math"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
)

// TestPaneResize_HugeColsClampedToCap is the ini-hup3 regression: a
// resize with an out-of-range column count (e.g. cols=MaxInt32 from a yamux
// resize control message) must be clamped, not passed straight to the emulator
// buffer where make(Line, width) panics with "makeslice: len out of range"
// (or, for a large finite width, allocates multiple GB and OOMs the daemon).
func TestPaneResize_HugeColsClampedToCap(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	p := &Pane{name: "eng1", emu: emu}

	// Before the fix this panics inside p.emu.Resize -> NewBuffer -> makeslice.
	p.Resize(24, math.MaxInt32)

	if w := emu.Width(); w != emuColsCap {
		t.Errorf("emulator width after huge-cols resize = %d, want clamped to %d", w, emuColsCap)
	}
}

// TestEffectiveEmuCols_Clamps pins the column clamp bounds (ini-hup3).
func TestEffectiveEmuCols_Clamps(t *testing.T) {
	cases := []struct{ in, want int }{
		{-5, 1},
		{0, 1},
		{1, 1},
		{80, 80},
		{emuColsCap, emuColsCap},
		{emuColsCap + 1, emuColsCap},
		{math.MaxInt32, emuColsCap},
	}
	for _, c := range cases {
		if got := effectiveEmuCols(c.in); got != c.want {
			t.Errorf("effectiveEmuCols(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// ini-41br: the row count of a resize is bounded like its width. Before the
// fix ResizeExact floored rows at 1 and passed them through, and Resize's
// effectiveEmuRows capped only its 3x growth, handing any larger request
// back unchanged; an alt-screen child got the raw rows. A resize control
// message with rows near MaxInt32 was a multi-gigabyte allocation, or a
// makeslice panic, in the process that owns every agent's PTY.

// resizeCatPane is a live /bin/cat pane, so a resize reaches a real PTY.
func resizeCatPane(t *testing.T) *Pane {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("windows: needs /bin/cat and a unix PTY to read the PTY size")
	}
	p, err := NewPane(PaneConfig{Name: "eng1", Command: []string{"/bin/cat"}}, 24, 80)
	if err != nil {
		t.Fatalf("NewPane: %v", err)
	}
	p.Start()
	t.Cleanup(p.Close)
	return p
}

// ptySize reads the size the PTY itself now has.
func ptySize(t *testing.T, p *Pane) (cols, rows int) {
	t.Helper()
	sz, ok := p.ptmx.(interface{ Size() (int, int, error) })
	if !ok {
		t.Fatal("the PTY does not report its size")
	}
	cols, rows, err := sz.Size()
	if err != nil {
		t.Fatalf("PTY size: %v", err)
	}
	return cols, rows
}

var resizePaths = map[string]func(p *Pane, rows, cols int){
	"Resize (window 1)":    (*Pane).Resize,
	"ResizeExact (daemon)": (*Pane).ResizeExact,
}

// hugeRowPaths adds window 1's alt-screen branch (vim, htop, Claude's
// fullscreen), which took the raw rows with no ceiling at all.
var hugeRowPaths = map[string]func(p *Pane, rows, cols int){
	"Resize (window 1)":    (*Pane).Resize,
	"ResizeExact (daemon)": (*Pane).ResizeExact,
	"Resize (alt-screen child)": func(p *Pane, rows, cols int) {
		if !p.emu.IsAltScreen() {
			p.emu.Write([]byte("\x1b[?1049h"))
		}
		p.Resize(rows, cols)
	},
}

// AC 1: a huge row count leaves the emulator and the PTY at the cap on both
// paths. A 5000-row probe goes first: if it is not clamped, the 1<<20
// request is never sent, since it would allocate gigabytes on this machine.
func TestPaneResize_HugeRowsClampedToCap(t *testing.T) {
	for name, resize := range hugeRowPaths {
		t.Run(name, func(t *testing.T) {
			p := resizeCatPane(t)
			resize(p, 5000, 80)
			if h := p.emu.Height(); h != emuRowsCap {
				t.Fatalf("a 5000-row request left the emulator %d rows high, want the cap %d (the 1<<20 request was not sent)", h, emuRowsCap)
			}
			if c, r := ptySize(t, p); r != emuRowsCap || c != 80 {
				t.Fatalf("a 5000-row request left the PTY at %dx%d, want 80x%d", c, r, emuRowsCap)
			}
			resize(p, 1<<20, 80)
			if h := p.emu.Height(); h != emuRowsCap {
				t.Fatalf("a 1<<20-row request left the emulator %d rows high, want %d", h, emuRowsCap)
			}
			if c, r := ptySize(t, p); r != emuRowsCap || c != 80 {
				t.Fatalf("a 1<<20-row request left the PTY at %dx%d, want 80x%d", c, r, emuRowsCap)
			}
		})
	}
}

// AC 2: a normal resize is unchanged on both paths: Resize grows the
// emulator 3x for a normal child, ResizeExact takes the rows as given, and
// the PTY matches the emulator.
func TestPaneResize_NormalSizesAreUnchanged(t *testing.T) {
	for _, rows := range []int{24, 40, 60} {
		for name, resize := range resizePaths {
			p := resizeCatPane(t)
			resize(p, rows, 100)
			want := rows
			if name == "Resize (window 1)" {
				want = 3 * rows
			}
			if h := p.emu.Height(); h != want || p.emu.Width() != 100 {
				t.Errorf("%s %d rows: emulator %dx%d, want 100x%d", name, rows, p.emu.Width(), h, want)
			}
			if c, r := ptySize(t, p); c != 100 || r != want {
				t.Errorf("%s %d rows: PTY %dx%d, want 100x%d", name, rows, c, r, want)
			}
		}
	}
}

// resizeViaHandler sends one resize control message through the daemon's
// real handler and waits for its reply.
func resizeViaHandler(t *testing.T, d *Daemon, rows, cols int) {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() { srv.Close(); cli.Close() })
	go d.handleControlStream(srv, NewIPCScanner(srv), "window2")
	if err := writeJSON(cli, ControlCmd{ID: "1", Action: "resize", Target: "eng1", Rows: rows, Cols: cols}); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	cli.SetReadDeadline(time.Now().Add(5 * time.Second))
	sc := NewIPCScanner(cli)
	for sc.Scan() {
		var resp ControlResp
		if json.Unmarshal(sc.Bytes(), &resp) == nil && resp.ID == "1" {
			return
		}
	}
	t.Fatalf("no reply to the resize: %v", sc.Err())
}

// AC 3, AC 4 and feed-it-nothing, through the daemon's resize handler for
// both a headless daemon (ResizeExact) and window 1 (Resize): a huge request
// is clamped on the pane, the PTY and the size a restart re-applies, and
// logged once; rows of zero or below change nothing.
func TestDaemonResize_ClampsOnBothPathsAndLogsOnce(t *testing.T) {
	for _, headless := range []bool{true, false} {
		name := map[bool]string{true: "headless daemon", false: "window 1"}[headless]
		t.Run(name, func(t *testing.T) {
			p := resizeCatPane(t)
			d := &Daemon{project: &config.Project{Name: "t"}, panes: []*Pane{p}, headless: headless}
			logs := captureLogs(t)

			resizeViaHandler(t, d, 1<<20, 1<<20)
			if h, w := p.emu.Height(), p.emu.Width(); h != emuRowsCap || w != emuColsCap {
				t.Fatalf("emulator %dx%d after a huge request, want %dx%d", w, h, emuColsCap, emuRowsCap)
			}
			if c, r := ptySize(t, p); c != emuColsCap || r != emuRowsCap {
				t.Fatalf("PTY %dx%d after a huge request, want %dx%d", c, r, emuColsCap, emuRowsCap)
			}
			if got := d.lastSizes["eng1"]; got != [2]int{emuRowsCap, emuColsCap} {
				t.Fatalf("the size a restart re-applies is %v, want the clamped %v", got, [2]int{emuRowsCap, emuColsCap})
			}
			if n := strings.Count(logs.String(), "resize request clamped"); n != 1 {
				t.Fatalf("a clamped request was logged %d times, want once:\n%s", n, logs.String())
			}
			for _, want := range []string{"requested_rows=1048576", "rows=300", "cols=1000"} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("the clamp log does not carry %q", want)
				}
			}

			// Feed it nothing: zero or negative rows are not a resize.
			before := [2]int{p.emu.Height(), p.emu.Width()}
			for _, rows := range []int{0, -5} {
				resizeViaHandler(t, d, rows, 80)
				if got := [2]int{p.emu.Height(), p.emu.Width()}; got != before {
					t.Errorf("a resize with rows %d changed the emulator to %v", rows, got)
				}
			}
		})
	}
}

// The clamp's bounds on both axes (the width table above, extended to rows).
func TestClampResizeRequest_Bounds(t *testing.T) {
	cases := []struct{ rows, cols, wantR, wantC int }{
		{24, 80, 24, 80},
		{emuRowsCap, emuColsCap, emuRowsCap, emuColsCap},
		{emuRowsCap + 1, 80, emuRowsCap, 80},
		{math.MaxInt32, 80, emuRowsCap, 80},
		{24, math.MaxInt32, 24, emuColsCap},
		{math.MaxInt32, math.MaxInt32, emuRowsCap, emuColsCap},
		{0, 80, 1, 80},
		{-5, -5, 1, 1},
	}
	for _, c := range cases {
		r, col, clamped := clampResizeRequest(c.rows, c.cols)
		if r != c.wantR || col != c.wantC {
			t.Errorf("clampResizeRequest(%d, %d) = %d, %d, want %d, %d", c.rows, c.cols, r, col, c.wantR, c.wantC)
		}
		if want := r != c.rows || col != c.cols; clamped != want {
			t.Errorf("clampResizeRequest(%d, %d) reported clamped=%v", c.rows, c.cols, clamped)
		}
	}
}

// A pane logs a clamped request once, not on every repeat of it (window 1's
// main loop retries a deferred resize each frame); a different clamped
// request is logged again.
func TestPaneResize_ARepeatedClampedRequestIsLoggedOnce(t *testing.T) {
	p := resizeCatPane(t)
	logs := captureLogs(t)
	p.Resize(5000, 80)
	p.Resize(5000, 80)
	if n := strings.Count(logs.String(), "resize request clamped"); n != 1 {
		t.Fatalf("the same clamped request was logged %d times, want once", n)
	}
	p.Resize(6000, 80)
	if n := strings.Count(logs.String(), "resize request clamped"); n != 2 {
		t.Fatalf("a different clamped request was not logged: %d lines", n)
	}
}
