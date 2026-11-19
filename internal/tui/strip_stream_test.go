package tui

import (
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

func stripBookAt(t *testing.T) *stripBook {
	return loadStripBook(filepath.Join(t.TempDir(), "strip.yaml"), time.Now())
}

// D8: the owner takes the strip's rows off a viewer's request at full tile
// height, on a window server (Resize) and a serve daemon (ResizeExact), so
// the agent never draws under the strip whichever window asked last.
func TestStripStream_TheOwnerTakesTheRowsOffAViewersRequest(t *testing.T) {
	for _, headless := range []bool{false, true} {
		p := newEmuPane("eng1", 120, 38)
		p.setStripForTest(pictureItems)
		d := &Daemon{headless: headless}
		d.resizeForViewer(p, "window2", 38, 120)
		// A window server grows a normal-screen terminal (effectiveEmuRows);
		// either way it is sized from 34 rows, the request less the strip.
		want := 34
		if !headless {
			want = effectiveEmuRows(34)
		}
		if _, rows := p.emuSize(); rows != want {
			t.Errorf("headless %v: terminal %d rows after a 38-row request, want %d (from 34)", headless, rows, want)
		}
	}
}

// The viewer side: a strip message rides the lossless announcement queue, in
// order with stream_added, never the drop-oldest events buffer.
func TestStripStream_TheViewerQueuesItLosslessly(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	m := NewControlMux(b)
	defer m.Close()
	go writeJSON(a, stripMsg{Action: stripAction, Name: "eng1", Items: []string{"one"}})
	select {
	case <-m.Announcements():
	case ev := <-m.Events():
		t.Fatalf("strip went to the events buffer: %+v", ev)
	case <-time.After(5 * time.Second):
		t.Fatal("no announcement")
	}
	got := m.TakeAnnouncements()
	if len(got) != 1 || got[0].Action != stripAction || got[0].Name != "eng1" || !reflect.DeepEqual(got[0].Items, []string{"one"}) {
		t.Fatalf("announcements %+v", got)
	}
}

// The viewer side: the list reaches a pane already shown and one that
// attaches later.
func TestStripStream_AViewersPanesTakeTheList(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	shown := NewRemotePane("eng1", "window1", a, drainedMux(t), 80, 24)
	pc := &peerConn{panes: []PaneView{shown}}
	pc.applyStrip("eng1", []string{"one"})
	pc.applyStrip("eng2", []string{"two"})
	if got := shown.StripItems(); !reflect.DeepEqual(got, []string{"one"}) {
		t.Fatalf("shown pane: %q", got)
	}
	if got := pc.stripFor("eng2"); !reflect.DeepEqual(got, []string{"two"}) {
		t.Fatalf("for a pane attaching later: %q", got)
	}
	pc.applyStrip("eng1", nil)
	if shown.StripItems() != nil {
		t.Fatal("clear did not reach the shown pane")
	}
}

// AC3b: a second window draws the same strip as window 1, cell for cell, and
// its emulator gives up the same rows; the content stops above the strip.
func TestStripStream_ASecondWindowDrawsTheSameStrip(t *testing.T) {
	_, local := stripScene(120, 40, pictureItems)

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	rp := NewRemotePane("super", "window1", a, drainedMux(t), 120, 38)
	rp.region = Region{X: 0, Y: 0, W: 120, H: 40}
	rp.SetStrip(pictureItems)
	rp.Resize(38, 120)
	if got := rp.emu.Height(); got != 34 {
		t.Fatalf("viewer emulator %d rows, want 34", got)
	}
	s := newSceneScreen(120, 40)
	rp.Render(s, false, false, 1, Selection{})
	s.Show()
	for y := 35; y <= 38; y++ {
		if got, want := stripRowDump(s, y), stripRowDump(local, y); got != want {
			t.Errorf("row %d differs from window 1:\n got %s\nwant %s", y, got, want)
		}
	}
}

// stripRowDump is one row of screenDump: its runes, then its style runs.
func stripRowDump(s tcell.SimulationScreen, y int) string {
	lines := strings.Split(screenDump(s), "\n")
	return lines[2*y] + " " + lines[2*y+1]
}

// drainedMux is a control mux whose far end reads and drops everything, for a
// viewer pane whose resizes go to an owner the test does not run.
func drainedMux(t *testing.T) *ControlMux {
	t.Helper()
	far, near := net.Pipe()
	go io.Copy(io.Discard, far)
	m := NewControlMux(near)
	t.Cleanup(func() { m.Close(); far.Close() })
	return m
}
