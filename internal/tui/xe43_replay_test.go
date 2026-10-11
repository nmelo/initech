package tui

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
	"github.com/nmelo/initech/internal/recording"
)

// ini-xe43 AC1, on the real bytes. testdata/xe43_suggestion_overtype.irec is
// a Claude Code 2.1.296 session captured in a PTY (prompt suggestions on),
// made shape-only with `initech recording shape`: printed text is x, escape
// sequences (and so the window titles) are as Claude sent them. Its output
// records are split at the keys typed: [0] start, [1] the prompt, [2] the
// reply up to the suggestion, [3] after 'a', [4] space, [5] 'b', [6..8]
// backspaces, [9] exit.
//
// Record [2] ends with Claude's "✳ <title>" OSC 0 (✳ carries the byte 0x9C)
// and the dim suggestion. Before the fix the parser cut the title at 0x9C and
// printed " Folder file listing" into the prompt row, where it stayed when
// the operator typed.
func TestXe43_TypingOverASuggestionLeavesOnlyTheTypedText(t *testing.T) {
	rd, err := recording.Open("testdata/xe43_suggestion_overtype.irec")
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	h := rd.Header()
	if !h.ShapeOnly {
		t.Fatal("fixture must be a shape-only copy")
	}
	var outs [][]byte
	for {
		r, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if r.Kind == recording.KindOutput {
			outs = append(outs, r.Data)
		}
	}
	if len(outs) < 4 {
		t.Fatalf("fixture has %d output records, want the split described above", len(outs))
	}

	emu := vt.NewSafeEmulator(h.Cols, h.Rows)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()
	defer emu.Close()
	for _, o := range outs[:3] {
		emu.Write(o)
	}
	const promptRow = 35 // Claude's composer row on a 40-row screen (CUP 36).
	const prompt = "❯ "

	// Before typing: the prompt, then the dim suggestion, then nothing.
	row := emuRow(emu, promptRow, h.Cols)
	sugg := strings.TrimPrefix(row, prompt)
	if !strings.HasPrefix(row, prompt) || sugg == "" || strings.Trim(sugg, "x") != "" {
		t.Fatalf("before typing, prompt row = %q; want the prompt and the (shaped) suggestion only", row)
	}
	for x := 2; x < 2+len(sugg); x++ {
		c := emu.CellAt(x, promptRow)
		if c == nil || !strings.Contains(c.Style.String(), "\x1b[2m") {
			t.Fatalf("suggestion cell %d is not dim: %+v", x, c)
		}
	}

	// The first key: only the typed character remains.
	emu.Write(outs[3])
	if row := emuRow(emu, promptRow, h.Cols); row != prompt+"x" {
		t.Errorf("after the first key, prompt row = %q, want %q", row, prompt+"x")
	}
	if c := emu.CellAt(2, promptRow); c == nil || strings.Contains(c.Style.String(), "\x1b[2m") {
		t.Errorf("the typed character is dim: %+v", c)
	}
}
