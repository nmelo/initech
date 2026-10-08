package tui

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
)

// ini-77ys: Option+Shift+F did nothing useful in the operator's Ghostty. The
// v2.15.0 tests built tcell events by hand ('F' with ModAlt) and so tested an
// encoding the operator's terminal never sends. These tests start one step
// earlier: the BYTES each terminal family writes for the chord go through
// tcell's own input parser via a fake TTY, and the event tcell produces is
// what reaches handleKey. The instrument is the decoder the product runs.

// byteTTY is a tcell.Tty fed from a pipe the test writes terminal bytes into.
// Writes from tcell (setup sequences, frames) are captured so the test can
// confirm which keyboard protocols tcell asked the terminal for.
type byteTTY struct {
	r       *io.PipeReader
	w       *io.PipeWriter
	mu      sync.Mutex
	written bytes.Buffer
	once    sync.Once
}

func newByteTTY() *byteTTY {
	r, w := io.Pipe()
	return &byteTTY{r: r, w: w}
}

func (b *byteTTY) Start() error { return nil }
func (b *byteTTY) Stop() error  { return nil }

// Drain closes the write end so tcell's blocked input reader returns. A real
// TTY's Drain exists for exactly this: Fini waits on that reader, and a
// no-op here hangs every test at teardown.
func (b *byteTTY) Drain() error               { b.once.Do(func() { b.w.Close() }); return nil }
func (b *byteTTY) NotifyResize(func())        {}
func (b *byteTTY) Read(p []byte) (int, error) { return b.r.Read(p) }
func (b *byteTTY) Close() error               { b.w.Close(); return b.r.Close() }
func (b *byteTTY) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.Write(p)
}
func (b *byteTTY) WindowSize() (tcell.WindowSize, error) {
	return tcell.WindowSize{Width: 120, Height: 40}, nil
}
func (b *byteTTY) setup() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.written.String()
}

// decodeTerminalBytes returns the key event tcell's real parser produces for
// raw terminal input. xterm-256color stands in for xterm-ghostty, which is
// not in tcell's built-in terminfo set: what matters is that it is XTermLike,
// the condition under which tcell enables CSI-u and modifyOtherKeys — and the
// test asserts tcell actually requested them, rather than assuming it.
func decodeTerminalBytes(t *testing.T, raw string) *tcell.EventKey {
	t.Helper()
	ti, err := tcell.LookupTerminfo("xterm-256color")
	if err != nil {
		t.Fatalf("terminfo: %v", err)
	}
	tty := newByteTTY()
	s, err := tcell.NewTerminfoScreenFromTtyTerminfo(tty, ti)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	defer s.Fini()

	go func() { _, _ = tty.w.Write([]byte(raw)) }()

	deadline := time.After(2 * time.Second)
	events := make(chan tcell.Event, 8)
	go func() {
		for {
			ev := s.PollEvent()
			if ev == nil {
				return
			}
			events <- ev
		}
	}()
	for {
		select {
		case ev := <-events:
			if k, ok := ev.(*tcell.EventKey); ok {
				if !bytes.Contains([]byte(tty.setup()), []byte("\x1b[>1u")) {
					t.Errorf("tcell did not request the kitty keyboard protocol; this fixture would not match a real Ghostty")
				}
				return k
			}
		case <-deadline:
			t.Fatalf("tcell produced no key event for %q", raw)
		}
	}
}

// Every encoding a real terminal sends for Option+Shift+F enters the live
// focus split, and none of them toggles the plain Focus split.
func TestOptionShiftF_EveryTerminalEncodingEntersLiveFocus(t *testing.T) {
	for _, tc := range []struct{ terminal, raw string }{
		// Ghostty / kitty / WezTerm, kitty protocol flag 1: the UNSHIFTED key
		// code with modifiers 1+shift+alt = 4. This is the operator's case.
		{"kitty CSI-u (Ghostty)", "\x1b[102;4u"},
		// xterm modifyOtherKeys mode 2: CSI 27 ; mods ; shifted char ~.
		{"xterm modifyOtherKeys", "\x1b[27;4;70~"},
		// Legacy Meta-sends-Escape (Terminal.app, iTerm2 default): ESC F.
		{"legacy ESC prefix", "\x1bF"},
	} {
		t.Run(tc.terminal, func(t *testing.T) {
			ev := decodeTerminalBytes(t, tc.raw)
			tui := liveFocusTUI(t)
			tui.handleKey(ev)
			if tui.liveFocus == nil {
				t.Fatalf("%s: tcell decoded %q as key=%v rune=%q mod=%v and the live focus split did not engage",
					tc.terminal, tc.raw, ev.Key(), ev.Rune(), ev.Modifiers())
			}
			if tui.focusSplitPrev != nil {
				t.Errorf("%s: the plain Focus split engaged as well", tc.terminal)
			}
		})
	}
}

// Option+f with no Shift is still the static Focus split in the same three
// encodings — the fix must not swallow the unshifted chord.
func TestOptionF_EveryTerminalEncodingStaysTheStaticFocusSplit(t *testing.T) {
	for _, tc := range []struct{ terminal, raw string }{
		{"kitty CSI-u (Ghostty)", "\x1b[102;3u"},
		{"xterm modifyOtherKeys", "\x1b[27;3;102~"},
		{"legacy ESC prefix", "\x1bf"},
	} {
		t.Run(tc.terminal, func(t *testing.T) {
			ev := decodeTerminalBytes(t, tc.raw)
			tui := liveFocusTUI(t)
			tui.handleKey(ev)
			if tui.liveFocus != nil {
				t.Errorf("%s: Option+f entered the LIVE focus split", tc.terminal)
			}
			if tui.layoutState.Mode != Layout2Col || tui.focusSplitPrev == nil {
				t.Errorf("%s: Option+f did not enter the static Focus split (mode=%v)", tc.terminal, tui.layoutState.Mode)
			}
		})
	}
}

// The Shift+Alt+digit live presets keep working through the shared
// normalisation, in the kitty encoding Ghostty uses for them.
func TestShiftOptionDigit_KittyEncodingStillAppliesTheLivePreset(t *testing.T) {
	ev := decodeTerminalBytes(t, "\x1b[50;4u") // Shift+Alt+2
	tui := liveFocusTUI(t)
	tui.layoutPresets = defaultLayoutPresets()
	tui.handleKey(ev)
	if tui.layoutState.Mode != LayoutLive {
		t.Errorf("Shift+Option+2 via kitty CSI-u: mode = %v, want LayoutLive", tui.layoutState.Mode)
	}
}

// altShiftedRune's table, directly: the three shapes fold to one answer,
// and an unshifted letter or a non-Alt key is not a shifted chord.
func TestAltShiftedRune_FoldsTheThreeEncodings(t *testing.T) {
	for _, tc := range []struct {
		name    string
		ev      *tcell.EventKey
		base    rune
		shifted bool
	}{
		{"kitty", tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt|tcell.ModShift), 'f', true},
		{"modifyOtherKeys", tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModAlt|tcell.ModShift), 'f', true},
		{"legacy", tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModAlt), 'f', true},
		{"plain Option+f", tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModAlt), 'f', false},
		{"no Alt", tcell.NewEventKey(tcell.KeyRune, 'F', tcell.ModShift), 0, false},
		{"digit with Shift", tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModAlt|tcell.ModShift), '3', true},
		{"digit without Shift", tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModAlt), '3', false},
	} {
		base, shifted := altShiftedRune(tc.ev)
		if shifted != tc.shifted || (shifted && base != tc.base) {
			t.Errorf("%s: altShiftedRune = (%q, %v), want (%q, %v)", tc.name, base, shifted, tc.base, tc.shifted)
		}
	}
}
