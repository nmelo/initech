package appserve

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

var now = time.Date(2026, 11, 1, 4, 0, 0, 0, time.UTC)

func reply(text string, footer ...string) Reply { return Reply{Text: text, Footer: footer} }

func superPane(replies ...Reply) PaneSnapshot {
	return PaneSnapshot{Name: "super", State: "idle", Banner: "none", Replies: replies}
}

// testRule is the default value over this rig's working chain.
var testRule = progress.NewRule(time.Hour, []string{"in_progress", "ready_for_qa", "in_qa", "qa_passed"})

func oneRig(panes []PaneSnapshot, beads ...BeadSnapshot) Snapshot {
	return Snapshot{Now: now, Rigs: []RigSnapshot{{Rig: "initech", Panes: panes, Beads: beads, Rule: testRule}}}
}

func only(t *testing.T, s Snapshot) RigBlockers {
	t.Helper()
	got := Blockers(s)
	if len(got) != 1 {
		t.Fatalf("Blockers gave %d rigs, want 1", len(got))
	}
	return got[0]
}

// AC1: the latest reply that carries a footer sets the waiting items, each
// the ▶ line's text, the first with the reply's recommendation.
func TestBlockers_LatestFooterSetsTheWaitingItems(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{superPane(
		reply("old", "- ▶ Old item"),
		reply("Context.\n\n**Recommendation:** ship the patch, it is small.\n\nShip it?",
			"- ▶ Your go for the patch release", "- ▶ Reconnect the Chrome extension"),
	)}))
	want := []WaitingItem{
		{Pane: "super", Text: "Your go for the patch release", Recommendation: "ship the patch, it is small."},
		{Pane: "super", Text: "Reconnect the Chrome extension"},
	}
	if !reflect.DeepEqual(r.Waiting, want) {
		t.Errorf("waiting = %+v\nwant %+v", r.Waiting, want)
	}
	if r.Quiet {
		t.Error("a rig with waiting items reads quiet")
	}
}

// AC1: a reply with no footer clears nothing.
func TestBlockers_ReplyWithNoFooterClearsNothing(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{superPane(
		reply("ask", "- ▶ Your go for the patch release"),
		reply("a short answer with no footer"),
	)}))
	if len(r.Waiting) != 1 || r.Waiting[0].Text != "Your go for the patch release" {
		t.Errorf("waiting = %+v, want the item from the earlier footer", r.Waiting)
	}
}

// AC1: an item disappears when a later footer lacks its text.
func TestBlockers_LaterFooterWithoutTheLineDropsIt(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{superPane(
		reply("ask", "- ▶ Your go for the patch release", "- ▶ Tailscale: start it"),
		reply("done", "- ▶ Tailscale: start it"),
	)}))
	if len(r.Waiting) != 1 || r.Waiting[0].Text != "Tailscale: start it" {
		t.Errorf("waiting = %+v, want only the line still in the latest footer", r.Waiting)
	}
}

// "- ▶ nothing" is a footer with no items: it clears.
func TestBlockers_NothingFooterClears(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{superPane(
		reply("ask", "- ▶ Your go for the patch release"),
		reply("all done", "- ▶ nothing"),
	)}))
	if len(r.Waiting) != 0 || !r.Quiet {
		t.Errorf("waiting = %+v quiet = %v, want none and quiet", r.Waiting, r.Quiet)
	}
}

// Only the super pane's footer counts: an engineer's ▶ line is not Nelson's.
func TestBlockers_OnlyTheSuperPaneIsRead(t *testing.T) {
	eng := PaneSnapshot{Name: "eng1", State: "running", Banner: "none", Replies: []Reply{reply("x", "- ▶ not his")}}
	r := only(t, oneRig([]PaneSnapshot{eng}))
	if len(r.Waiting) != 0 {
		t.Errorf("an engineer footer became a waiting item: %+v", r.Waiting)
	}
	// Positive twin: the same reply on the super pane is read.
	eng.Name = "super"
	if r := only(t, oneRig([]PaneSnapshot{eng})); len(r.Waiting) != 1 {
		t.Errorf("the super pane footer was not read: %+v", r.Waiting)
	}
}

// AC6: a footer with no recommendation line gives items with none.
func TestBlockers_FooterWithNoRecommendationLine(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{superPane(reply("Context only.", "- ▶ Your go"))}))
	if len(r.Waiting) != 1 || r.Waiting[0].Recommendation != "" {
		t.Errorf("waiting = %+v, want one item with no recommendation", r.Waiting)
	}
}

func TestRecommendationOf_ReadsTheCommonShapes(t *testing.T) {
	for text, want := range map[string]string{
		"Recommendation: A, because it is cheaper.":     "A, because it is cheaper.",
		"**Recommendation:** A.":                        "A.",
		"3. **Recommendation.** A, and why.":            "A, and why.",
		"- Recommendation: B":                           "B",
		"My recommendation: B":                          "B",
		"context\nrecommendation — go with C\nquestion": "go with C",
		"No recommendations were made here.":            "",
		"Recommendations follow below.":                 "",
		"":                                              "",
	} {
		if got := recommendationOf(text); got != want {
			t.Errorf("recommendationOf(%q) = %q, want %q", text, got, want)
		}
	}
}

func pane(name, state, banner string) PaneSnapshot {
	return PaneSnapshot{Name: name, State: state, Banner: banner}
}

// AC2: a pane that cannot act appears with its reason.
func TestBlockers_StuckPanesCarryTheirReason(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{
		pane("eng1", "waiting", "permission_prompt"),
		pane("eng2", "waiting", "none"),
		pane("eng3", "pending", "none"),
		pane("eng4", "idle", "login"),
		pane("eng5", "running", "unknown"),
		pane("eng6", "running", "none"),
		pane("eng7", "idle", ""),
	}))
	want := []StuckPane{
		{Pane: "eng1", Reason: "permission prompt open"},
		{Pane: "eng2", Reason: "waiting at a prompt"},
		{Pane: "eng3", Reason: "text left unsent in the input box"},
		{Pane: "eng4", Reason: "login screen open"},
		{Pane: "eng5", Reason: "a dialog is open"},
	}
	if !reflect.DeepEqual(r.Stuck, want) {
		t.Errorf("stuck = %+v\nwant %+v", r.Stuck, want)
	}
	if r.Quiet {
		t.Error("a rig with stuck panes reads quiet")
	}
}

// AC2: a bead quiet an hour with its owner idle is stuck on its owner.
func TestBlockers_QuietBeadWithIdleOwner(t *testing.T) {
	quiet := BeadSnapshot{ID: "ini-a1", Status: "in_progress", Assignee: "eng1", LastMove: now.Add(-90 * time.Minute)}
	panes := []PaneSnapshot{pane("eng1", "idle", "none")}
	r := only(t, oneRig(panes, quiet))
	want := []StuckPane{{Pane: "eng1", Reason: "ini-a1 quiet 1h30m, its owner idle"}}
	if !reflect.DeepEqual(r.Stuck, want) {
		t.Errorf("stuck = %+v, want %+v", r.Stuck, want)
	}
	for name, b := range map[string]BeadSnapshot{
		"moved 59m ago": {ID: "ini-a1", Status: "in_progress", Assignee: "eng1", LastMove: now.Add(-59 * time.Minute)},
		"open":          {ID: "ini-a1", Status: "open", Assignee: "eng1", LastMove: now.Add(-5 * time.Hour)},
		"closed":        {ID: "ini-a1", Status: "closed", Assignee: "eng1", LastMove: now.Add(-5 * time.Hour)},
		"never moved":   {ID: "ini-a1", Status: "in_progress", Assignee: "eng1"},
		"owner absent":  {ID: "ini-a1", Status: "in_progress", Assignee: "eng9", LastMove: now.Add(-5 * time.Hour)},
	} {
		if r := only(t, oneRig(panes, b)); len(r.Stuck) != 0 {
			t.Errorf("%s: stuck = %+v, want none", name, r.Stuck)
		}
	}
	// "An hour or more": exactly an hour counts.
	hour := quiet
	hour.LastMove = now.Add(-time.Hour)
	if r := only(t, oneRig(panes, hour)); len(r.Stuck) != 1 || r.Stuck[0].Reason != "ini-a1 quiet 1h00m, its owner idle" {
		t.Errorf("quiet exactly an hour: stuck = %+v, want one", r.Stuck)
	}
	if r := only(t, oneRig([]PaneSnapshot{pane("eng1", "running", "none")}, quiet)); len(r.Stuck) != 0 {
		t.Errorf("a running owner: stuck = %+v, want none", r.Stuck)
	}
}

// AC2: a QA pane idle while work waits for QA.
func TestBlockers_IdleValidatorWhileWorkWaits(t *testing.T) {
	qa := pane("qa1", "idle", "none")
	moved := now.Add(-time.Minute)
	for name, b := range map[string]BeadSnapshot{
		"ready_for_qa":         {ID: "ini-b1", Status: "ready_for_qa", Assignee: "eng1", LastMove: moved},
		"in_qa unassigned":     {ID: "ini-b1", Status: "in_qa", LastMove: moved},
		"in_qa assigned to it": {ID: "ini-b1", Status: "in_qa", Assignee: "qa1", LastMove: moved},
	} {
		r := only(t, oneRig([]PaneSnapshot{qa}, b))
		want := []StuckPane{{Pane: "qa1", Reason: "idle while ini-b1 waits for QA"}}
		if !reflect.DeepEqual(r.Stuck, want) {
			t.Errorf("%s: stuck = %+v, want %+v", name, r.Stuck, want)
		}
	}
	for name, c := range map[string]struct {
		p PaneSnapshot
		b BeadSnapshot
	}{
		"in_qa with another QA": {qa, BeadSnapshot{ID: "ini-b1", Status: "in_qa", Assignee: "qa2", LastMove: moved}},
		"QA working":            {pane("qa1", "running", "none"), BeadSnapshot{ID: "ini-b1", Status: "ready_for_qa", LastMove: moved}},
		"no QA work":            {qa, BeadSnapshot{ID: "ini-b1", Status: "in_progress", Assignee: "eng1", LastMove: moved}},
		"not a QA pane":         {pane("eng1", "idle", "none"), BeadSnapshot{ID: "ini-b1", Status: "ready_for_qa", LastMove: moved}},
	} {
		if r := only(t, oneRig([]PaneSnapshot{c.p}, c.b)); len(r.Stuck) != 0 {
			t.Errorf("%s: stuck = %+v, want none", name, r.Stuck)
		}
	}
}

// AC2 and AC6: a rig with nothing shows as a quiet row; no rig gives none.
func TestBlockers_QuietRigAndNoRig(t *testing.T) {
	r := only(t, oneRig([]PaneSnapshot{pane("super", "idle", "none"), pane("eng1", "running", "none")}))
	if !r.Quiet || len(r.Waiting) != 0 || len(r.Stuck) != 0 {
		t.Errorf("a rig with nothing: %+v, want quiet", r)
	}
	if got := Blockers(Snapshot{Now: now}); len(got) != 0 {
		t.Errorf("no running rig: %+v, want none", got)
	}
	if r := only(t, oneRig(nil)); !r.Quiet {
		t.Errorf("a rig with no panes: %+v, want quiet", r)
	}
}

// The JSON the endpoint serves: empty lists are [], never null, and every
// rig of the snapshot has a row in order.
func TestBlockers_JSONShape(t *testing.T) {
	s := Snapshot{Now: now, Rigs: []RigSnapshot{{Rig: "initech"}, {Rig: "theguy", Panes: []PaneSnapshot{superPane(reply("x", "- ▶ Your go"))}}}}
	b, err := json.Marshal(Blockers(s))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"rig":"initech","waiting":[],"stuck":[],"quiet":true},{"rig":"theguy","waiting":[{"pane":"super","text":"Your go","recommendation":""}],"stuck":[],"quiet":false}]`
	if string(b) != want {
		t.Errorf("json =\n%s\nwant\n%s", b, want)
	}
}

func TestFooterItem_StripsTheMarker(t *testing.T) {
	for line, want := range map[string]string{
		"- ▶ Your go":              "Your go",
		"▶ Your go":                "Your go",
		"  -  ▶  Go  ":             "Go",
		"- ▶ nothing":              "",
		"- ▶ Nothing.":             "",
		"- ▶ ":                     "",
		"- not a item":             "",
		"- ▶ nothing yet from qa3": "nothing yet from qa3",
	} {
		if got := footerItem(line); got != want {
			t.Errorf("footerItem(%q) = %q, want %q", line, got, want)
		}
	}
	if strings.Contains(footerItem("- ▶ a"), "▶") {
		t.Error("marker kept")
	}
}

// ini-e4c AC3: the app's blockers count through the same Stuck with the rig's
// rule: the value decides, and only the rig's working chain can be stuck.
func TestBlockers_CountByTheRigsRule(t *testing.T) {
	bead := func(id, status string, age time.Duration) BeadSnapshot {
		return BeadSnapshot{ID: id, Status: status, Assignee: "eng1", LastMove: now.Add(-age)}
	}
	panes := []PaneSnapshot{pane("eng1", "idle", "none")}
	beads := []BeadSnapshot{
		bead("ini-a.1", "in_progress", 20*time.Minute),
		bead("ini-a.2", "review", 30*time.Minute),
		bead("ini-a.3", "in_progress", 5*time.Minute),
		bead("ini-a.4", "in_qa", 40*time.Minute),
		bead("ini-a.5", "waiting", 40*time.Minute),
		bead("ini-a.6", "blocked", 40*time.Minute),
	}
	stuckIDs := func(rule progress.Rule) string {
		s := Snapshot{Now: now, Rigs: []RigSnapshot{{Rig: "r", Panes: panes, Beads: beads, Rule: rule}}}
		var ids []string
		for _, st := range Blockers(s)[0].Stuck {
			ids = append(ids, strings.Fields(st.Reason)[0])
		}
		return strings.Join(ids, " ")
	}
	chain := []string{"in_progress", "review"}
	if got := stuckIDs(progress.NewRule(15*time.Minute, chain)); got != "ini-a.1 ini-a.2" {
		t.Errorf("at 15m stuck = %q, want ini-a.1 ini-a.2", got)
	}
	if got := stuckIDs(progress.NewRule(time.Hour, chain)); got != "" {
		t.Errorf("at 1h stuck = %q, want none", got)
	}
	if got := stuckIDs(progress.Rule{}); got != "" {
		t.Errorf("no rule read yet: stuck = %q, want none", got)
	}
}
