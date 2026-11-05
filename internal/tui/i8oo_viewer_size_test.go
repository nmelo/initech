package tui

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// viewerRig is a headless daemon with one pane, sized by named viewers.
func viewerRig(t *testing.T, headless bool) (*Daemon, *Pane) {
	t.Helper()
	d := newTestDaemon(t)
	d.headless = headless
	p := newEmuPane("eng1", 80, 24)
	d.panes = []*Pane{p}
	return d, p
}

func sizeOf(p *Pane) [2]int { c, r := p.emuSize(); return [2]int{r, c} }

// ini-i8oo: the second viewer leaves, and the pane goes back to the first.
func TestViewerLeft_TheLastToSetLeavingGivesThePaneBack(t *testing.T) {
	d, p := viewerRig(t, true)
	d.resizeForViewer(p, "laptop", 36, 55)
	d.resizeForViewer(p, "probe", 46, 100)
	d.viewerLeft("probe")
	if got := sizeOf(p); got != [2]int{36, 55} {
		t.Errorf("pane %v after the second viewer left, want the first's 36x55", got)
	}
	if got := d.lastSizes["eng1"]; got != [2]int{36, 55} {
		t.Errorf("lastSizes %v: a restart would spawn at the departed size", got)
	}
}

// With three viewers the most recent remaining size wins, not any older one.
func TestViewerLeft_TheMostRecentRemainingSizeWins(t *testing.T) {
	d, p := viewerRig(t, true)
	d.resizeForViewer(p, "a", 20, 40)
	d.resizeForViewer(p, "b", 30, 60)
	d.resizeForViewer(p, "a", 21, 41)
	d.resizeForViewer(p, "c", 40, 90)
	d.viewerLeft("c")
	if got := sizeOf(p); got != [2]int{21, 41} {
		t.Errorf("pane %v, want a's latest 21x41", got)
	}
}

// A viewer leaving whose size was not the one in effect changes nothing: the
// viewer that set it is still there, and its agent is not repainted.
func TestViewerLeft_AnOlderViewerLeavingChangesNothing(t *testing.T) {
	d, p := viewerRig(t, true)
	d.resizeForViewer(p, "a", 20, 40)
	d.resizeForViewer(p, "b", 30, 60)
	p.resizeSettleFrames = 0
	d.viewerLeft("a")
	if got := sizeOf(p); got != [2]int{30, 60} {
		t.Errorf("pane %v, want b's 30x60 still", got)
	}
	if p.resizeSettleFrames != 0 {
		t.Error("the pane was resized though its size did not change")
	}
}

// Feed it nothing: the only viewer leaves and the pane keeps its last agreed
// size; an unknown viewer leaving, and no viewer ever, do nothing.
func TestViewerLeft_NobodyLeftKeepsTheLastSize(t *testing.T) {
	d, p := viewerRig(t, true)
	d.viewerLeft("never-seen")
	d.resizeForViewer(p, "laptop", 36, 55)
	d.viewerLeft("never-seen")
	d.viewerLeft("laptop")
	if got := sizeOf(p); got != [2]int{36, 55} {
		t.Errorf("pane %v, want its last agreed 36x55", got)
	}
}

// AC4: a remaining size equal to the one in effect does not resize, so an
// agent at an agreeing size is not repainted.
func TestViewerLeft_AnAgreeingSizeIsNotResizedAgain(t *testing.T) {
	d, p := viewerRig(t, true)
	d.resizeForViewer(p, "a", 36, 55)
	d.resizeForViewer(p, "b", 36, 55)
	p.resizeSettleFrames = 0
	d.viewerLeft("b")
	if p.resizeSettleFrames != 0 {
		t.Error("the pane was resized to the size it already had")
	}
}

// AC4: a window server runs this code inside the TUI over local panes: it
// remembers nothing and gives nothing back, as before. Positive twin: the
// headless tests above.
func TestViewerLeft_AWindowServerIsUnchanged(t *testing.T) {
	d, p := viewerRig(t, false)
	d.resizeForViewer(p, "window-2", 30, 60)
	d.resizeForViewer(p, "window-3", 40, 90)
	if len(d.viewerSizes) != 0 {
		t.Errorf("a window server recorded viewers: %v", d.viewerSizes)
	}
	want := sizeOf(p)
	d.viewerLeft("window-3")
	if got := sizeOf(p); got != want {
		t.Errorf("a window leaving resized a local pane: %v, want %v", got, want)
	}
	if got := d.lastSizes["eng1"]; got != [2]int{40, 90} {
		t.Errorf("lastSizes %v, still kept as before", got)
	}
}

// A viewer with no name cannot be seen to leave, so it is not remembered.
func TestViewerLeft_AnUnnamedViewerIsNotRemembered(t *testing.T) {
	d, p := viewerRig(t, true)
	d.resizeForViewer(p, "", 30, 60)
	if len(d.viewerSizes) != 0 {
		t.Errorf("recorded %v", d.viewerSizes)
	}
}

// resizeVia sends a resize over the control stream as a viewer does.
func resizeVia(t *testing.T, d *Daemon, peer string, rows, cols int) ControlResp {
	t.Helper()
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go d.handleControlStream(server, bufio.NewScanner(server), peer)
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(client).Encode(ControlCmd{ID: "r1", Action: "resize", Target: "eng1", Rows: rows, Cols: cols}); err != nil {
		t.Fatal(err)
	}
	var resp ControlResp
	if err := json.NewDecoder(client).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// AC5, feed it nothing: a zero size changes no pane and is never given back
// later. Positive twin: a real size through the same handler is applied.
func TestResizeHandler_AZeroSizeIsNeitherAppliedNorRemembered(t *testing.T) {
	d, p := viewerRig(t, true)
	resizeVia(t, d, "laptop", 36, 55)
	if got := sizeOf(p); got != [2]int{36, 55} {
		t.Fatalf("a real size through the handler: pane %v", got)
	}
	for _, s := range [][2]int{{0, 0}, {0, 100}, {46, 0}, {-1, -1}} {
		resizeVia(t, d, "probe", s[0], s[1])
		if got := sizeOf(p); got != [2]int{36, 55} {
			t.Fatalf("size %v was applied: pane %v", s, got)
		}
	}
	if _, ok := d.viewerSizes["eng1"]["probe"]; ok {
		t.Error("a zero size was remembered")
	}
	d.viewerLeft("probe")
	if got := sizeOf(p); got != [2]int{36, 55} {
		t.Errorf("pane %v after the viewer that sent only zeros left", got)
	}
}

// AC3: a request the peer clamps is remembered as applied, not as asked, so a
// re-apply cannot give back a size the peer refused to hold.
func TestResizeHandler_AClampedSizeIsRememberedClamped(t *testing.T) {
	d, p := viewerRig(t, true)
	resizeVia(t, d, "a", 1_000_000, 80)
	want := sizeOf(p)
	if want[0] >= 1_000_000 {
		t.Fatalf("the request was not clamped: %v", want)
	}
	if v := d.viewerSizes["eng1"]["a"]; [2]int{v.rows, v.cols} != want {
		t.Errorf("remembered %dx%d, the pane holds %v", v.rows, v.cols, want)
	}
}

// AC3: a size the peer refuses logs one warning, not one per layout change; a
// size it answers clears that, so a later refusal is said again.
func TestSendResize_ARefusedSizeWarnsOnce(t *testing.T) {
	logs := captureLogs(t)
	rp := &RemotePane{name: "eng1", host: "wb", emu: vt.NewSafeEmulator(10, 5), mux: rejectingDaemon(t, `agent "eng1" not found`)}
	const line = "viewer resize not applied"
	for i := 0; i < 3; i++ {
		rp.sendResize(36, 55)
	}
	if n := countLines(logs, line); n != 1 {
		t.Errorf("the refusal logged %d lines, want 1:\n%s", n, logs)
	}
	rp.sendResize(40, 60)
	if n := countLines(logs, line); n != 2 {
		t.Errorf("a different refused size logged %d lines in all, want 2", n)
	}
	rp.noteResizeAnswer(40, 60, "")
	rp.sendResize(40, 60)
	if n := countLines(logs, line); n != 3 {
		t.Errorf("after an answered size the refusal logged %d lines in all, want 3", n)
	}
}

// AC5, feed it nothing: a peer that never answers logs one warning when the
// request times out, and the caller is not stuck.
func TestSendResize_APeerThatNeverAnswersWarnsOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 10-second request timeout; runs in the full suite, skipped under -short")
	}
	logs := captureLogs(t)
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	go func() { _, _ = io.Copy(io.Discard, server) }() // reads, never answers
	rp := &RemotePane{name: "eng1", host: "wb", emu: vt.NewSafeEmulator(10, 5), mux: NewControlMux(client)}
	rp.sendResize(36, 55)
	if n := countLines(logs, "viewer resize not applied"); n != 1 {
		t.Errorf("logged %d lines, want 1:\n%s", n, logs)
	}
}
