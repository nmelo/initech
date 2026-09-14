package tui

import (
	"fmt"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/gdamore/tcell/v2"
)

// ini-di8h: every window gets window 1's two wheel rules. A fullscreen child
// is sent the event and scrolls itself; any other child ignores the wheel, so
// the pane's own history moves.

// wheelSpy is a RemotePane that records which rule the wheel took. Embedding
// the real type keeps the emulator (and so the fullscreen decision) real.
type wheelSpy struct {
	*RemotePane
	ups, downs int
	forwarded  []ControlCmd
}

func (w *wheelSpy) ScrollUp(n int)   { w.ups += n }
func (w *wheelSpy) ScrollDown(n int) { w.downs += n }
func (w *wheelSpy) ForwardWheel(lx, ly int, up bool, mods tcell.ModMask) {
	w.forwarded = append(w.forwarded, w.wheelCmd(lx, ly, up, mods))
}

func di8hViewerPane(t *testing.T, lines int) *RemotePane {
	t.Helper()
	rp := NewRemotePane("eng1", WindowOnePeerName, nil, nil, 80, 10)
	rp.region = Region{X: 0, Y: 0, W: 80, H: 11} // InnerSize rows = 10
	for i := 1; i <= lines; i++ {
		rp.pushChunk([]byte(fmt.Sprintf("line %d\r\n", i)))
		rp.DrainData()
	}
	return rp
}

func di8hRender(t *testing.T, rp *RemotePane) string {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()
	s.SetSize(80, 11)
	rp.Render(s, false, false, 1, Selection{})
	return readScreenRect(s, 0, 0, 80, 11)
}

func TestDi8hWheel_RoutesByWhetherTheChildIsFullscreen(t *testing.T) {
	tui := newTestTUI()

	plain := &wheelSpy{RemotePane: di8hViewerPane(t, 50)}
	tui.wheelPane(plain, 4, 2, true, 0)
	tui.wheelPane(plain, 4, 2, false, 0)
	if plain.ups != wheelScrollRows || plain.downs != wheelScrollRows {
		t.Fatalf("non-fullscreen child: ups=%d downs=%d, want %d each", plain.ups, plain.downs, wheelScrollRows)
	}
	if len(plain.forwarded) != 0 {
		t.Fatalf("non-fullscreen child received forwarded wheel: %+v", plain.forwarded)
	}

	full := &wheelSpy{RemotePane: di8hViewerPane(t, 50)}
	full.emu.Write([]byte("\x1b[?1049h")) // child goes fullscreen (alt screen)
	tui.wheelPane(full, 4, 2, true, 0)
	tui.wheelPane(full, 4, 2, false, 0)
	if full.ups != 0 || full.downs != 0 {
		t.Fatalf("fullscreen child scrolled initech's history: ups=%d downs=%d", full.ups, full.downs)
	}
	if len(full.forwarded) != 2 {
		t.Fatalf("fullscreen child got %d forwarded notches, want 2", len(full.forwarded))
	}
	if full.forwarded[0].Wheel != "up" || full.forwarded[1].Wheel != "down" {
		t.Fatalf("forwarded directions = %q/%q", full.forwarded[0].Wheel, full.forwarded[1].Wheel)
	}
	if full.forwarded[0].Action != "mouse" || full.forwarded[0].Target != "eng1" {
		t.Fatalf("forwarded command = %+v", full.forwarded[0])
	}
}

func TestDi8hViewerScroll_ShowsEarlierOutputAndWheelDownReturnsToLive(t *testing.T) {
	rp := di8hViewerPane(t, 300)
	if live := di8hRender(t, rp); !strings.Contains(live, "line 300") {
		t.Fatalf("viewer did not start at the live tail:\n%s", live)
	}
	rp.ScrollUp(wheelScrollRows)
	scrolled := di8hRender(t, rp)
	if !strings.Contains(scrolled, "line 297") || strings.Contains(scrolled, "line 300") {
		t.Fatalf("wheel up did not move into history:\n%s", scrolled)
	}
	if !rp.InScrollback() {
		t.Fatal("InScrollback false while showing history")
	}
	rp.ScrollDown(wheelScrollRows)
	back := di8hRender(t, rp)
	if !strings.Contains(back, "line 300") {
		t.Fatalf("wheel down did not return to live:\n%s", back)
	}
	if rp.InScrollback() {
		t.Fatal("InScrollback true at the live edge")
	}
}

func TestDi8hViewerScroll_NewOutputDoesNotYankAScrolledView(t *testing.T) {
	rp := di8hViewerPane(t, 300)
	rp.ScrollUp(wheelScrollRows)
	before := di8hRender(t, rp)
	for i := 301; i <= 320; i++ {
		rp.pushChunk([]byte(fmt.Sprintf("line %d\r\n", i)))
		rp.DrainData()
	}
	after := di8hRender(t, rp)
	if !strings.Contains(after, "line 297") {
		t.Fatalf("new output yanked the scrolled view:\n%s", after)
	}
	if before != after {
		t.Fatalf("scrolled view changed while output arrived:\nBEFORE\n%s\nAFTER\n%s", before, after)
	}
}

// The two pane kinds must land on the same row for the same gesture, or a
// viewer "scrolls" by a different amount than window 1 does.
func TestDi8hScrollRule_ViewerAndWindowOneAgreeOnTheViewTop(t *testing.T) {
	local := &Pane{name: "eng1", emu: vt.NewSafeEmulator(80, 10), alive: true, visible: true,
		region: Region{X: 0, Y: 0, W: 80, H: 12}} // TerminalSize rows = 10
	remote := di8hViewerPane(t, 300)
	for i := 1; i <= 300; i++ {
		local.emu.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	for notches := 1; notches <= 4; notches++ {
		local.ScrollUp(wheelScrollRows)
		remote.ScrollUp(wheelScrollRows)
		if local.scrollOffset != remote.scroll.offset {
			t.Fatalf("notch %d: window 1 offset %d, viewer offset %d", notches, local.scrollOffset, remote.scroll.offset)
		}
		localTop, _ := local.contentOffset()
		var remoteTop int
		withEmulator(remote.emu, screenTryBudget, func(e *vt.Emulator) {
			_, innerRows := remote.region.InnerSize()
			remoteTop = scrollbackViewTop(e, innerRows, remote.scroll.offset)
		})
		if localTop != remoteTop {
			t.Fatalf("notch %d: window 1 view top %d, viewer view top %d", notches, localTop, remoteTop)
		}
	}
}

func TestDi8hWheelCommand_CarriesEmulatorCoordinatesAndDirection(t *testing.T) {
	rp := di8hViewerPane(t, 50)
	cmd := rp.wheelCmd(7, 2, true, tcell.ModShift)
	// Emulator is 10 rows and the pane shows 10, so content row 2 is row 2.
	if cmd.X != 7 || cmd.Y != 2 {
		t.Fatalf("coordinates = (%d,%d), want (7,2)", cmd.X, cmd.Y)
	}
	if cmd.Mods != int(uv.ModShift) {
		t.Fatalf("mods = %d, want %d", cmd.Mods, int(uv.ModShift))
	}
	if got := rp.wheelCmd(0, 0, false, 0).Wheel; got != "down" {
		t.Fatalf("wheel = %q, want down", got)
	}
}

func TestDi8hWheelEventFromCmd_MapsDirectionAndPosition(t *testing.T) {
	up := wheelEventFromCmd(ControlCmd{X: 3, Y: 9, Wheel: "up", Mods: int(uv.ModCtrl)})
	if up.Button != uv.MouseWheelUp || up.X != 3 || up.Y != 9 || up.Mod != uv.ModCtrl {
		t.Fatalf("up event = %+v", up)
	}
	if down := wheelEventFromCmd(ControlCmd{Wheel: "down"}); down.Button != uv.MouseWheelDown {
		t.Fatalf("down event button = %v", down.Button)
	}
	// A garbled or older peer scrolls back rather than toward live.
	if odd := wheelEventFromCmd(ControlCmd{Wheel: ""}); odd.Button != uv.MouseWheelUp {
		t.Fatalf("empty direction button = %v", odd.Button)
	}
}

// The clamp only decides anything at the top of the buffer, so it needs a
// cell that goes there: a wrong row count in the viewer's clamp is invisible
// three notches into a 300-line history and stops the operator short of the
// oldest line here.
func TestDi8hViewerScroll_StopsAtTheOldestLineLikeWindowOne(t *testing.T) {
	local := &Pane{name: "eng1", emu: vt.NewSafeEmulator(80, 10), alive: true, visible: true,
		region: Region{X: 0, Y: 0, W: 80, H: 12}} // TerminalSize rows = 10
	remote := di8hViewerPane(t, 300)
	for i := 1; i <= 300; i++ {
		local.emu.Write([]byte(fmt.Sprintf("line %d\r\n", i)))
	}
	for i := 0; i < 200; i++ { // far past the oldest line
		local.ScrollUp(wheelScrollRows)
		remote.ScrollUp(wheelScrollRows)
	}
	if local.scrollOffset != remote.scroll.offset {
		t.Fatalf("at the top: window 1 offset %d, viewer offset %d", local.scrollOffset, remote.scroll.offset)
	}
	if top, _ := local.contentOffset(); top != 0 {
		t.Fatalf("window 1 view top at the oldest line = %d, want 0", top)
	}
	screen := di8hRender(t, remote)
	if !strings.Contains(screen, "line 1 ") && !strings.Contains(screen, "line 1\n") {
		t.Fatalf("viewer did not reach the oldest line:\n%s", screen)
	}
}
