package tui

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	xpty "github.com/charmbracelet/x/xpty"
	"github.com/gdamore/tcell/v2"
)

// ini-sf5.2: a pane at or above 85% context is flagged "ctx 91%, compact
// soon" in status, patrol and its ribbon, from the figure the maintenance
// tick reads; below 85, or unknown, nothing.

// sf52Screen is the 120-column Haiku capture with its context set to pct.
func sf52Screen(t *testing.T, pct string) ([]string, int) {
	t.Helper()
	rows, width := corpusFooter(t, "haiku316_branch_w120.txt")
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.Replace(r, "│ 7% │", "│ "+pct+" │", 1)
	}
	return out, width
}

// sf52Tick paints a live pane with the capture at pct and runs one
// maintenance tick over it.
func sf52Tick(t *testing.T, p *Pane, pct string, now time.Time) {
	t.Helper()
	rows, width := sf52Screen(t, pct)
	p.mu.Lock()
	p.alive, p.lastEmuCols = true, width
	p.mu.Unlock()
	nma5Paint(p, "\x1b[2J\x1b[H"+strings.Join(rows, "\r\n"))
	var last time.Time
	maintainModal([]PaneView{p}, now, &last)
}

// AC1: the tick's figure at 91 and at exactly 85 flags; at 84 it does not;
// a dialog over the footer with no transcript leaves no figure (AC3).
func TestContextFlag_TheTickReadsTheFigure(t *testing.T) {
	now := time.Now()
	for _, c := range []struct{ pct, want string }{
		{"91%", "ctx 91%, compact soon"},
		{"85%", "ctx 85%, compact soon"},
		{"84%", ""},
		{"0%", ""},
	} {
		_, width := sf52Screen(t, c.pct)
		p := newEmuPane("eng1", width, 8)
		sf52Tick(t, p, c.pct, now)
		if got := ContextFlagText(p.ContextFigure()); got != c.want {
			t.Errorf("footer %s: flag %q, want %q", c.pct, got, c.want)
		}
	}
	if got := ContextFlagText(nil); got != "" {
		t.Errorf("unknown figure: %q, want no flag", got)
	}
}

// AC2: the flag clears on the next tick that reads below 85, and on one that
// reads no figure at all (a dialog over the footer, no transcript).
func TestContextFlag_ClearsOnTheNextReadBelow(t *testing.T) {
	now := time.Now()
	_, width := sf52Screen(t, "91%")
	p := newEmuPane("eng1", width, 8)
	sf52Tick(t, p, "91%", now)
	if ContextFlagText(p.ContextFigure()) == "" {
		t.Fatal("fixture: no flag at 91")
	}
	sf52Tick(t, p, "14%", now.Add(2*time.Second))
	if got := ContextFlagText(p.ContextFigure()); got != "" {
		t.Errorf("after a compaction to 14%%: %q, want none", got)
	}
	sf52Tick(t, p, "91%", now.Add(4*time.Second))
	p.mu.Lock()
	p.alive = true
	p.mu.Unlock()
	nma5Paint(p, "\x1b[2J\x1b[H Do you want to proceed?\r\n ❯ 1. Yes\r\n   2. No")
	var last time.Time
	maintainModal([]PaneView{p}, now.Add(6*time.Second), &last)
	if got := p.ContextFigure(); got != nil {
		t.Errorf("no footer and no transcript: figure %d, want unknown", *got)
	}
}

// A narrow pane has no footer figure; its transcript's gives the flag
// (ini-484's source).
func TestContextFlag_ANarrowPaneFlagsFromItsTranscript(t *testing.T) {
	dir := t.TempDir()
	s484Transcript(t, dir, "a.jsonl", s484Reply("claude-opus-5-5", 2, 5000, 905_000, false))
	p := s484Pane(t, "eng7", "haiku316_branch_cut_w29.txt", dir)
	s484Read(p)
	p.mu.Lock()
	p.alive, p.lastEmuCols = true, 29
	p.mu.Unlock()
	var last time.Time
	maintainModal([]PaneView{p}, time.Now(), &last)
	if got := ContextFlagText(p.ContextFigure()); got != "ctx 91%, compact soon" {
		t.Errorf("29-column pane at 910005 tokens: %q, want the flag at 91", got)
	}
}

// The list and patrol replies carry the figure while flagged, from the TUI
// and the daemon; nothing below the line.
func TestContextFlag_TheRepliesCarryIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix domain socket directly")
	}
	now := time.Now()
	panes := map[string]string{"eng1": "91%", "eng2": "85%", "eng3": "84%"}
	var ps []*Pane
	for _, name := range []string{"eng1", "eng2", "eng3"} {
		_, width := sf52Screen(t, panes[name])
		p := newEmuPane(name, width, 8)
		sf52Tick(t, p, panes[name], now)
		ps = append(ps, p)
	}
	ps = append(ps, newEmuPane("eng4", 40, 8)) // never ticked: unknown
	want := map[string]string{"eng1": "91", "eng2": "85", "eng3": "", "eng4": ""}

	got := l6omList(t, &TUI{panes: toPaneViews(ps)}, false)
	for name, w := range want {
		if g := string(got[name]["context_soon"]); g != w {
			t.Errorf("TUI list %s context_soon = %q, want %q", name, g, w)
		}
	}

	d := &Daemon{panes: ps}
	infos, ok := d.AllPanes()
	if !ok {
		t.Fatal("daemon list failed")
	}
	for _, in := range infos {
		g := ""
		if in.ContextSoon != nil {
			g = strings.TrimSpace(string(mustJSON(t, *in.ContextSoon)))
		}
		if g != want[in.Name] {
			t.Errorf("daemon list %s context_soon = %q, want %q", in.Name, g, want[in.Name])
		}
	}

	dir, err := os.MkdirTemp("", "sf5")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	tu := &TUI{panes: toPaneViews(ps)}
	cleanup, err := tu.startIPC(filepath.Join(dir, "p.sock"))
	if err != nil {
		t.Fatalf("startIPC: %v", err)
	}
	defer cleanup()
	resp := ipcAsk(t, filepath.Join(dir, "p.sock"), IPCRequest{Action: "patrol", Lines: 2})
	if !resp.OK {
		t.Fatalf("patrol: %s", resp.Error)
	}
	for name, w := range want {
		if g := string(l6omByName(t, resp.Data)[name]["context_soon"]); g != w {
			t.Errorf("patrol %s context_soon = %q, want %q", name, g, w)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// sf52Ribbon is a pane's ribbon row and the style of the cell at col.
func sf52Ribbon(s tcell.SimulationScreen, p *Pane) string {
	return ysr3Row(s, p.region, p.region.Y+p.region.H-1)
}

// AC1 for the header: the flagged pane's ribbon shows the tag in yellow
// beside the bead; one at 84 shows none; the tag comes and goes with the
// figure on a partial frame (the chrome is redrawn when the look changes).
func TestContextFlag_TheRibbonShowsTheTag(t *testing.T) {
	tui, s := ysr3Parked(t)
	p, q := ysr2Pane(tui, "p01"), ysr2Pane(tui, "p02")
	p.SetBead("ini-sf5.2", "")
	set := func(pane *Pane, pct *int) {
		pane.mu.Lock()
		pane.ctxFigure = pct
		pane.mu.Unlock()
	}
	set(p, l6omInt(91))
	set(q, l6omInt(84))
	tui.render()
	row := sf52Ribbon(s, p)
	i := strings.Index(row, "[ctx 91%, compact soon]")
	if i < 0 || !strings.Contains(row[i:], "] | ini-") {
		t.Fatalf("p01's ribbon %q, want the tag before the bead", row)
	}
	_, _, style, _ := s.GetContent(p.region.X+len([]rune(row[:i])), p.region.Y+p.region.H-1)
	if fg, _, _ := style.Decompose(); fg != tcell.ColorYellow {
		t.Errorf("the tag is drawn in %v, want yellow", fg)
	}
	if strings.Contains(sf52Ribbon(s, q), "ctx") {
		t.Errorf("p02 at 84%% shows %q, want no tag", sf52Ribbon(s, q))
	}
	ysr2SameAsFull(t, tui, s, "the tag on")

	set(p, l6omInt(12))
	tui.render()
	if strings.Contains(sf52Ribbon(s, p), "ctx") {
		t.Errorf("after the compaction p01 still reads %q", sf52Ribbon(s, p))
	}
	ysr2SameAsFull(t, tui, s, "the tag gone")
}

// endedPty is a PTY whose child has gone: the next read ends readLoop.
type endedPty struct{ xpty.Pty }

func (endedPty) Read([]byte) (int, error) { return 0, io.EOF }

// A pane keeps its last screen when its process stops, footer and all, and
// the tick keeps reading it; the ribbon carries the state marker alone, and
// the tag means a running agent. Running, the same pane shows the tag.
func TestContextFlag_AStoppedPaneShowsNoTag(t *testing.T) {
	for _, c := range []struct {
		name, marker string
		stop         func(*Pane)
	}{
		{"dead", "[dead]", func(p *Pane) {
			p.ptmx = endedPty{}
			p.readLoop()
		}},
		{"suspended", "[susp", parkPaneSuspended},
	} {
		t.Run(c.name, func(t *testing.T) {
			tui, s := ysr3Parked(t)
			p := ysr2Pane(tui, "p01")
			_, width := sf52Screen(t, "91%")
			p.emu.Resize(width, 8)
			now := time.Now()
			sf52Tick(t, p, "91%", now)
			p.markFrameChanged() // the paint went round readLoop, which marks it
			tui.render()
			if row := sf52Ribbon(s, p); !strings.Contains(row, "[ctx 91%") {
				t.Fatalf("running at 91%%: the ribbon reads %q, want the tag", row)
			}
			ysr2SameAsFull(t, tui, s, "running, the tag on")

			c.stop(p)
			var last time.Time
			maintainModal([]PaneView{p}, now.Add(2*time.Second), &last)
			if p.ContextFigure() == nil || *p.ContextFigure() != 91 {
				t.Fatalf("fixture: the stopped pane no longer reads 91 from its screen: %v", p.ContextFigure())
			}
			tui.render()
			row := sf52Ribbon(s, p)
			if !strings.Contains(row, c.marker) {
				t.Fatalf("the ribbon reads %q, want %s", row, c.marker)
			}
			if strings.Contains(row, "ctx") {
				t.Errorf("a stopped pane's ribbon reads %q, want no tag", row)
			}
			ysr2SameAsFull(t, tui, s, "the tag gone with the process")
		})
	}
}

// AC3: a ribbon too narrow for the tag keeps the name and cuts the tag at
// the pane's edge, as the other tags are cut.
func TestContextFlag_ANarrowRibbonKeepsTheName(t *testing.T) {
	sim := tcell.NewSimulationScreen("")
	sim.Init()
	sim.SetSize(40, 5)
	r := Region{X: 0, Y: 0, W: 14, H: 5}
	look := ribbonLook{title: " 3 eng7 ", warn: "[ctx 91%, compact soon] ", bead: "ini-sf5.2"}
	renderRibbon(&clampedScreen{Screen: sim, r: r}, r, look)
	got := ysr3Row(sim, Region{X: 0, Y: 0, W: 16}, 4)
	if got != "  3 eng7 [ctx   " {
		t.Errorf("a 14-wide ribbon reads %q, want the name whole and the tag cut at the edge", got)
	}
}
