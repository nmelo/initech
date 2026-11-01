package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-wra.3: a pane that has worked fifteen minutes with no bead reads
// "no bead" in initech status's Bead column; bd's answer still wins.
func TestStatus_UnattributedWorkReadsNoBead(t *testing.T) {
	plain := jvrsPane("eng2")
	plain.Activity = "running"
	marked := jvrsPane("eng3")
	marked.Activity = "running"
	marked.Unattributed = true
	held := jvrsPane("eng1", "ini-p") // bd holds ini-p for eng1
	held.Unattributed = true
	rows := jvrsStatus(t, plain, marked, held)

	if !strings.Contains(rows["eng3"], "no bead") {
		t.Errorf("eng3, 15 minutes of work with no bead: row %q, want \"no bead\"", rows["eng3"])
	}
	if strings.Contains(rows["eng2"], "no bead") {
		t.Errorf("eng2, not marked: row %q, want no \"no bead\"", rows["eng2"])
	}
	if strings.Contains(rows["eng1"], "no bead") || !strings.Contains(rows["eng1"], "ini-p") {
		t.Errorf("eng1, bd holds ini-p: row %q, want ini-p and no \"no bead\"", rows["eng1"])
	}
}

// The list reply's unattributed field is read.
func TestStatus_ReadsTheUnattributedField(t *testing.T) {
	var pi paneInfo
	if err := json.Unmarshal([]byte(`{"name":"eng1","unattributed":true}`), &pi); err != nil || !pi.Unattributed {
		t.Fatalf("unattributed not read: %+v %v", pi, err)
	}
	_ = tui.PaneInfo{Unattributed: true}
}

// wra3OrderBd gives eng1 two in_progress beads, bd's newest first, the way
// bd list orders them.
const wra3OrderBd = `#!/bin/sh
[ "$1" = list ] || exit 1
echo '{"id":"ini-new","title":"Newer","status":"in_progress","assignee":"eng1"}'
echo '{"id":"ini-old","title":"Named last","status":"in_progress","assignee":"eng1"}'
`

// AC5: with two in_progress beads for a role, status's Bead column and
// status --json put the pane's bead first, the one its ribbon shows, with the
// same set. Red at base: both lead with bd's newest.
func TestStatus_TwoBeadsLeadWithTheRibbonsBead(t *testing.T) {
	pane := jvrsPane("eng1", "ini-old")
	table := jvrsStatusWith(t, wra3OrderBd, false, pane)
	var row string
	for _, line := range strings.Split(table, "\n") {
		if f := strings.Fields(line); len(f) > 0 && f[0] == "eng1" {
			row = line
		}
	}
	first := ""
	for _, f := range strings.Fields(row) {
		if strings.HasPrefix(f, "ini-") {
			first = f
			break
		}
	}
	if first != "ini-old" {
		t.Errorf("table row %q: the Bead column leads with %q, want the ribbon's ini-old, not bd's newest ini-new", row, first)
	}

	var agents []map[string]any
	if err := json.Unmarshal([]byte(jvrsStatusWith(t, wra3OrderBd, true, pane)), &agents); err != nil || len(agents) != 1 {
		t.Fatalf("status --json: %v %v", agents, err)
	}
	got, _ := json.Marshal(agents[0]["beads"])
	if string(got) != `["ini-old","ini-new"]` {
		t.Errorf("status --json beads %s, want [\"ini-old\",\"ini-new\"]: the ribbon's bead first, the same set", got)
	}
}

// AC5 feed-it-nothing: no ribbon, an empty ribbon, and a ribbon bead bd does
// not hold keep bd's order; the order never changes the set.
func TestStatus_BeadOrderKeepsBdsOrderWithoutARibbonBead(t *testing.T) {
	held := []beadInfo{{ID: "ini-new"}, {ID: "ini-old"}}
	empty := []string{}
	other := []string{"ini-x"}
	named := []string{"ini-old"}
	ids := func(bs []beadInfo) string {
		var s []string
		for _, b := range bs {
			s = append(s, b.ID)
		}
		return strings.Join(s, ",")
	}
	for name, ribbon := range map[string]*[]string{"nil": nil, "empty": &empty, "not held": &other} {
		if got := ids(heldInRibbonOrder(paneInfo{Beads: ribbon}, held)); got != "ini-new,ini-old" {
			t.Errorf("%s ribbon: order %s, want bd's ini-new,ini-old", name, got)
		}
	}
	if got := ids(heldInRibbonOrder(paneInfo{Beads: &named}, held)); got != "ini-old,ini-new" {
		t.Errorf("ribbon ini-old: order %s, want ini-old,ini-new", got)
	}
	if ids(held) != "ini-new,ini-old" {
		t.Errorf("bd's slice was changed in place: %s", ids(held))
	}
}
