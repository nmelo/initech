package tui

import (
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// newTitleEmu returns an emulator whose title changes are recorded.
func newTitleEmu(t *testing.T, cols, rows int) (*vt.SafeEmulator, func() []string) {
	t.Helper()
	emu := vt.NewSafeEmulator(cols, rows)
	var mu sync.Mutex
	var titles []string
	emu.SetCallbacks(vt.Callbacks{Title: func(s string) {
		mu.Lock()
		titles = append(titles, s)
		mu.Unlock()
	}})
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { emu.Close() })
	return emu, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), titles...)
	}
}

func emuRow(e *vt.SafeEmulator, y, cols int) string {
	return strings.TrimRight(e.RowText(y, cols), " ")
}

// ini-xe43: a title whose UTF-8 holds the byte 0x9C (✳ is e2 9c b3, the
// glyph Claude Code puts in front of a finished session's title) is a title,
// all of it; none of it lands on the screen.
func TestOSCTitle_UTF8WithC1STByteStaysATitle(t *testing.T) {
	emu, titles := newTitleEmu(t, 40, 4)
	emu.Write([]byte("\x1b[1;3H\x1b]0;✳ Open-sourcing epic\x07"))
	if row := emuRow(emu, 0, 40); row != "" {
		t.Errorf("title text leaked onto the screen: %q", row)
	}
	if got := titles(); len(got) != 1 || got[0] != "✳ Open-sourcing epic" {
		t.Errorf("titles = %q, want the whole title once", got)
	}
	// What the operator did next: type over it. Only the typed text shows.
	emu.Write([]byte("a\x1b[K"))
	if row := emuRow(emu, 0, 40); row != "  a" {
		t.Errorf("after typing: row = %q, want just the typed a", row)
	}
}

// Other glyphs with 0x9C in their UTF-8 behave the same, inside OSC 8
// hyperlinks too, and the string still ends where it should: on BEL, and on
// the 7-bit ST (ESC \).
func TestOSC_StringsStillEndOnBELAndSevenBitST(t *testing.T) {
	emu, titles := newTitleEmu(t, 40, 4)
	// ✻ e2 9c bb, ✜ e2 9c 9c (two 0x9C bytes).
	emu.Write([]byte("\x1b]2;✻ spinner ✜\x1b\\after"))
	if got := titles(); len(got) != 1 || got[0] != "✻ spinner ✜" {
		t.Errorf("titles = %q", got)
	}
	if row := emuRow(emu, 0, 40); row != "after" {
		t.Errorf("row = %q, want only the text after ESC \\", row)
	}
	emu.Write([]byte("\r\n\x1b]8;;http://x/✳\x07link\x1b]8;;\x07 done"))
	if row := emuRow(emu, 1, 40); row != "link done" {
		t.Errorf("hyperlink row = %q, want the link text and nothing from the URL", row)
	}
}
