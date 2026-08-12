package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Byte-for-byte single-window regression for ini-9ka.5.
//
// The AC requires that a single-window fleet's modal is UNCHANGED by the
// monitor-tier work -- proven against today's modal, not "looks right". The
// golden file this compares against was captured from CURRENT MAIN
// (8796e44, before any rendering change in this bead) by running:
//
//	go test ./internal/tui/ -run TestAgentsGrid_SingleWindowGolden -update-golden
//
// Capturing it from a branch that already contained the change would only
// prove the code agrees with itself, which is not what "unchanged" means.
// Regenerating it is therefore a deliberate act that must be justified: if
// this test fails, the single-window modal changed, and that is the finding.

// agentsGoldenFleet is a fleet spanning all three seeded bands (core/eng/qa)
// with more than one row in a band, so the golden covers band leads, label
// rows, multi-row wrapping, and the footer -- not just a trivial one-band case.
var agentsGoldenFleet = []string{
	"super", "pm", "shipper",
	"eng1", "eng2", "eng3", "eng4", "eng5", "eng6", "eng7",
	"qa1", "qa2", "qa3",
}

// renderAgentsGridToString renders the modal on a fresh simulation screen and
// returns the full screen contents as text, one line per row, trailing
// whitespace trimmed per line so incidental padding cannot mask a diff.
func renderAgentsGridToString(t *testing.T, names ...string) string {
	t.Helper()
	tui, s := newTestTUIWithScreen(names...)
	tui.agents.selected = 0
	tui.renderAgentsGrid()

	w, h := s.Size()
	var b strings.Builder
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			ch, _, _, _ := s.GetContent(x, y)
			if ch == 0 {
				ch = ' '
			}
			line.WriteRune(ch)
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

func agentsGoldenPath() string {
	return filepath.Join("testdata", "agents_grid_single_window.golden")
}

// TestAgentsGrid_SingleWindowGolden is the byte-for-byte regression. With one
// window configured, the rendered modal must equal the golden captured from
// main before the tier work.
func TestAgentsGrid_SingleWindowGolden(t *testing.T) {
	got := renderAgentsGridToString(t, agentsGoldenFleet...)

	if updateGolden() {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(agentsGoldenPath(), []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden updated: %s", agentsGoldenPath())
		return
	}

	want, err := os.ReadFile(agentsGoldenPath())
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-golden, but only deliberately): %v", err)
	}
	if got != string(want) {
		t.Errorf("single-window modal changed.\n--- want (golden, captured from main) ---\n%s\n--- got ---\n%s\n%s",
			string(want), got, firstDiffLine(string(want), got))
	}
}

// firstDiffLine reports the first differing line, so a failure names the row
// that changed instead of leaving a reader to eyeball two full screens.
func firstDiffLine(want, got string) string {
	wl := strings.Split(want, "\n")
	gl := strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}
		if i < len(gl) {
			b = gl[i]
		}
		if a != b {
			return fmt.Sprintf("first difference at line %d:\n  want: %q\n   got: %q", i, a, b)
		}
	}
	return ""
}
