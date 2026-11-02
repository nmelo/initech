package cmd

import (
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-0kr: the ribbon shows one bead, so a role holding two read as a mismatch
// on every line. The note now fires only when the ribbon really disagrees with
// bd: a ribbon bead bd does not give the role, or no ribbon while bd holds one.

// twoBeadsBd is a fake bd giving eng1 two beads in progress and eng2 one.
const twoBeadsBd = `#!/bin/sh
[ "$1" = list ] || exit 1
want="$3"
[ "$2" = --status ] || want="in_progress,ready_for_qa,in_qa,qa_passed"
emit() { case ",$want," in *",$1,"*) echo "$2";; esac; }
emit in_progress '{"id":"ini-a","title":"First","status":"in_progress","assignee":"eng1"}'
emit in_progress '{"id":"ini-b","title":"Second","status":"in_progress","assignee":"eng1"}'
emit in_progress '{"id":"ini-c","title":"Theirs","status":"in_progress","assignee":"eng2"}'
`

// failingBd is bd unreachable.
const failingBd = "#!/bin/sh\nexit 1\n"

func statusRow(t *testing.T, bd, agent string, panes ...tui.PaneInfo) string {
	t.Helper()
	for _, line := range strings.Split(jvrsStatusWith(t, bd, false, panes...), "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == agent {
			return line
		}
	}
	t.Fatalf("no row for %s", agent)
	return ""
}

// TestStatus_ARibbonOnEitherOfTwoHeldBeadsIsNoMismatch is AC1.
func TestStatus_ARibbonOnEitherOfTwoHeldBeadsIsNoMismatch(t *testing.T) {
	for _, shown := range []string{"ini-a", "ini-b"} {
		row := statusRow(t, twoBeadsBd, "eng1", jvrsPane("eng1", shown), jvrsPane("eng2", "ini-c"))
		if strings.Contains(row, "ribbon:") {
			t.Errorf("ribbon on %s, one of the two bd gives eng1: row %q has a note", shown, row)
		}
	}
}

// TestStatus_ARibbonOutsideTheHeldBeadsStillSaysSo is AC2: the note as today,
// for a bead bd gives another role, and with an unheld bead beside a held one.
func TestStatus_ARibbonOutsideTheHeldBeadsStillSaysSo(t *testing.T) {
	row := statusRow(t, twoBeadsBd, "eng1", jvrsPane("eng1", "ini-c"), jvrsPane("eng2", "ini-c"))
	if !strings.Contains(row, "ribbon: ini-c") {
		t.Errorf("a ribbon on another role's bead: row %q, want the note", row)
	}
	row = statusRow(t, twoBeadsBd, "eng1", jvrsPane("eng1", "ini-a", "ini-c"), jvrsPane("eng2", "ini-c"))
	if !strings.Contains(row, "ribbon: ini-a,ini-c") {
		t.Errorf("a ribbon with one unheld bead: row %q, want the note", row)
	}
}

// TestStatus_AnEmptyRibbonWhileBdHoldsABeadSaysSo is the other half of the rule.
func TestStatus_AnEmptyRibbonWhileBdHoldsABeadSaysSo(t *testing.T) {
	row := statusRow(t, twoBeadsBd, "eng2", jvrsPane("eng1", "ini-a"), jvrsPane("eng2"))
	if !strings.Contains(row, "ribbon: -") {
		t.Errorf("an empty ribbon with bd holding ini-c: row %q, want \"ribbon: -\"", row)
	}
}

// TestStatus_NoNoteWithNothingToCompare is AC3's feed-it-nothing: no ribbon and
// no beads, bd unreachable, and a TUI whose bd poll has gone stale.
func TestStatus_NoNoteWithNothingToCompare(t *testing.T) {
	if row := statusRow(t, twoBeadsBd, "qa1", jvrsPane("qa1"), jvrsPane("eng1", "ini-a")); strings.Contains(row, "ribbon:") {
		t.Errorf("no ribbon and no beads: row %q has a note", row)
	}
	if row := statusRow(t, failingBd, "eng1", jvrsPane("eng1", "ini-a")); strings.Contains(row, "ribbon:") {
		t.Errorf("bd unreachable: row %q has a note: bd said nothing to disagree with", row)
	}
	stale := jvrsPane("eng1", "ini-gone")
	stale.BeadStale = true
	row := statusRow(t, twoBeadsBd, "eng1", stale)
	if strings.Contains(row, "ribbon:") {
		t.Errorf("a stale poll: row %q has a note", row)
	}
	if !strings.Contains(row, tui.BeadStaleNote) {
		t.Errorf("a stale poll: row %q lacks the stale mark", row)
	}
}

func TestRibbonDisagrees(t *testing.T) {
	held := []beadInfo{{ID: "ini-a"}, {ID: "ini-b"}}
	for _, c := range []struct {
		name   string
		ribbon []string
		held   []beadInfo
		want   bool
	}{
		{"one of two", []string{"ini-a"}, held, false},
		{"the other of two", []string{"ini-b"}, held, false},
		{"both", []string{"ini-b", "ini-a"}, held, false},
		{"outside the set", []string{"ini-c"}, held, true},
		{"one in, one out", []string{"ini-a", "ini-c"}, held, true},
		{"ribbon empty, bd holds", nil, held, true},
		{"ribbon set, bd holds none", []string{"ini-a"}, nil, true},
		{"both empty", nil, nil, false},
	} {
		if got := ribbonDisagrees(c.ribbon, c.held); got != c.want {
			t.Errorf("%s: ribbonDisagrees = %v, want %v", c.name, got, c.want)
		}
	}
}
