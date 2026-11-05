package tui

import (
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/config"
)

// i8ooPainter stands in for Claude Code's repaint, the one thing the garble
// needs: on every SIGWINCH it clears and paints one line exactly as wide as
// the PTY says it is. The scratch rig on the bead shows the real agent doing
// the same, with real Haiku; a test cannot spend a real agent on every run.
const i8ooPainter = `paint() { set -- $(stty size); n=$(($2 - 8)); printf '\033[2J\033[H'; printf 'START%*sEND' "$n" '' | tr ' ' x; }
trap paint WINCH
paint
while :; do sleep 0.1; done`

// i8ooPeer is a headless serve on loopback with one real agent, as RunDaemon
// wires it, taking real connections.
func i8ooPeer(t *testing.T) (*Daemon, *Pane, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the painter is a bash script reading stty: not on Windows")
	}
	d := newTestDaemon(t)
	d.headless = true
	d.project.PeerName = "wb"
	d.project.Token = "t"
	p, err := NewPane(PaneConfig{Name: "eng1", Command: []string{"bash", "-c", i8ooPainter}}, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	rb := NewRingBuf(DefaultRingBufSize)
	ms := NewMultiSink()
	ms.Add(rb)
	d.ringBufs["eng1"], d.multiSinks["eng1"] = rb, ms
	p.SetNetworkSink(ms)
	p.Start()
	d.panes = []*Pane{p}
	t.Cleanup(p.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go d.handleConnection(c)
		}
	}()
	return d, p, ln.Addr().String()
}

// i8ooViewer connects as the viewer named name and gives its eng1 pane.
func i8ooViewer(t *testing.T, addr, name string) (*peerConn, *RemotePane) {
	t.Helper()
	pc, err := connectPeer("wb", config.Remote{Addr: addr, Token: "t"}, &config.Project{PeerName: name, Token: "t"})
	if err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}
	t.Cleanup(func() { pc.session.Close() })
	for _, pv := range pc.panes {
		if rp, ok := pv.(*RemotePane); ok && rp.Name() == "eng1" {
			rp.Start()
			return pc, rp
		}
	}
	t.Fatalf("%s has no eng1 pane", name)
	return nil, nil
}

// emuRows is the emulator's screen, one string per row, spaces trimmed.
func emuRows(e *vt.SafeEmulator) []string {
	var rows []string
	for y := 0; y < e.Height(); y++ {
		var b strings.Builder
		for x := 0; x < e.Width(); x++ {
			if c := e.CellAt(x, y); c != nil && c.Content != "" {
				b.WriteString(c.Content)
			} else {
				b.WriteByte(' ')
			}
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	return rows
}

// paintedAt reports whether the viewer's frame holds the agent's line whole on
// one row, exactly as wide as the viewer's own tile. Stranded at another
// width, the line wraps across rows and no row holds both ends.
func paintedAt(rp *RemotePane, cols int) bool {
	rp.DrainData()
	for _, r := range emuRows(rp.emu) {
		if strings.HasPrefix(r, "START") && strings.HasSuffix(r, "END") && len(r) == cols {
			return true
		}
	}
	return false
}

func waitTrue(d time.Duration, f func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return f()
}

func peerCols(p *Pane) int { c, _ := p.emuSize(); return c }

// ini-i8oo AC2, by effect, with a real PTY and two real viewers: a second
// viewer connects at another size and leaves, and the remaining viewer's tile
// renders the line wrapped at its own width. Red at base: stranded at the
// departed viewer's size, the line broken across rows, for as long as nobody
// resizes (Nelson's screenshot; the scratch rig on the bead).
func TestViewerLeaving_TheRemainingTileGetsItsOwnSizeBack(t *testing.T) {
	if testing.Short() {
		t.Skip("real TCP peer sessions and a real PTY; runs in the full suite, skipped under -short")
	}
	_, p, addr := i8ooPeer(t)
	_, laptop := i8ooViewer(t, addr, "laptop")
	laptop.Resize(22, 55)
	if !waitTrue(5*time.Second, func() bool { return paintedAt(laptop, 55) }) {
		t.Fatalf("precondition: one viewer, sizes agreeing, the frame is not whole:\n%s", strings.Join(emuRows(laptop.emu), "\n"))
	}

	second, probe := i8ooViewer(t, addr, "probe-pre")
	probe.Resize(30, 100)
	if !waitTrue(5*time.Second, func() bool { return peerCols(p) == 100 }) {
		t.Fatalf("the second viewer's size did not reach the PTY: %d", peerCols(p))
	}
	// The instrument can see the defect: while the sizes disagree the frame
	// holds no whole line (last to resize wins, the guide's stated limit).
	if !waitTrue(5*time.Second, func() bool { return !paintedAt(laptop, 55) }) {
		t.Fatalf("sizes disagree yet the frame is whole, so this test cannot see the garble:\n%s", strings.Join(emuRows(laptop.emu), "\n"))
	}

	second.session.Close()
	if !waitTrue(6*time.Second, func() bool { return peerCols(p) == 55 }) {
		t.Fatalf("after the second viewer left the PTY stays %d wide under a 55-column tile", peerCols(p))
	}
	if !waitTrue(5*time.Second, func() bool { return paintedAt(laptop, 55) }) {
		t.Errorf("the PTY is back but the frame is not whole:\n%s", strings.Join(emuRows(laptop.emu), "\n"))
	}
	pc, pr := p.emuSize()
	if lc, lr := laptop.emu.Width(), laptop.emu.Height(); pc != 55 || pr != 22 || lc != 55 || lr != 22 {
		t.Errorf("sizes disagree: peer %dx%d, viewer emulator %dx%d, tile 22x55", pr, pc, lr, lc)
	}
}

// AC3: a tile change reaches the peer, all three sizes agreeing, and a viewer
// reconnecting under its own name has not left: the old connection's end does
// not give its replacement's size away.
func TestViewerSizes_ATileChangeAgreesAndAReconnectIsNotALeave(t *testing.T) {
	if testing.Short() {
		t.Skip("real TCP peer sessions and a real PTY; runs in the full suite, skipped under -short")
	}
	_, p, addr := i8ooPeer(t)
	_, laptop := i8ooViewer(t, addr, "laptop")
	laptop.Resize(20, 70)
	if !waitTrue(5*time.Second, func() bool { return paintedAt(laptop, 70) }) {
		t.Fatalf("a tile change did not reach the agent: peer %d, frame:\n%s", peerCols(p), strings.Join(emuRows(laptop.emu), "\n"))
	}
	pc, pr := p.emuSize()
	if laptop.emu.Width() != 70 || laptop.emu.Height() != 20 || pc != 70 || pr != 20 {
		t.Errorf("tile 20x70, viewer emulator %dx%d, peer %dx%d", laptop.emu.Height(), laptop.emu.Width(), pr, pc)
	}

	_, first := i8ooViewer(t, addr, "second")
	first.Resize(30, 100)
	if !waitTrue(5*time.Second, func() bool { return peerCols(p) == 100 }) {
		t.Fatalf("second viewer's size did not arrive: %d", peerCols(p))
	}
	_, again := i8ooViewer(t, addr, "second") // takes over the name; the old connection ends
	again.Resize(30, 90)
	if !waitTrue(5*time.Second, func() bool { return peerCols(p) == 90 }) {
		t.Fatalf("the replacement's size did not arrive: %d", peerCols(p))
	}
	time.Sleep(1500 * time.Millisecond) // the old connection's end has run by now
	if got := peerCols(p); got != 90 {
		t.Errorf("the replaced connection's end took the size away: peer %d, want 90", got)
	}
}
