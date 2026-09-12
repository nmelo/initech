package lifecycle

import (
	"testing"

	"github.com/nmelo/initech/internal/roles"
)

// testChain is this project's real chain: [open, in_progress] + bd's
// status.custom + [closed].
var testChain = []string{"open", "in_progress", "ready_for_qa", "in_qa", "qa_passed", "ready_to_ship", "closed"}

func TestDispatch_TargetStateAndAssigneeForEveryRole(t *testing.T) {
	for _, tc := range []struct {
		name    string
		family  roles.RoleFamily
		current string
		want    Transition
	}{
		// An implementer dispatch claims the bead for work.
		{"eng from open", roles.FamilyEng, "open",
			Transition{Status: "in_progress", Assignee: AssigneeTarget, RecordImplementer: true}},
		{"eng re-dispatch after a QA fail", roles.FamilyEng, "in_progress",
			Transition{Status: "in_progress", Assignee: AssigneeTarget, RecordImplementer: true}},
		{"other role (shipper, pm) from open", roles.FamilyOther, "open",
			Transition{Status: "in_progress", Assignee: AssigneeTarget, RecordImplementer: true}},

		// GITHUB #34, verbatim: assign qa1 on a ready_for_qa bead must write
		// in_qa, not in_progress. The bead never misreports its state between
		// dispatch and the validator's first action.
		{"#34: qa dispatch on a delivered bead", roles.FamilyQA, "ready_for_qa",
			Transition{Status: "in_qa", Assignee: AssigneeTarget}},
		{"qa dispatch on a fresh bead", roles.FamilyQA, "open",
			Transition{Status: "in_qa", Assignee: AssigneeTarget}},
		// Already in the target state: a no-op write, never an error (AC edge).
		{"qa dispatch on a bead already in_qa", roles.FamilyQA, "in_qa",
			Transition{Status: "in_qa", Assignee: AssigneeTarget}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Dispatch(tc.family, tc.current)
			if got.Status != tc.want.Status || got.Assignee != tc.want.Assignee || got.RecordImplementer != tc.want.RecordImplementer {
				t.Errorf("Dispatch(%s, %s) = {%s %v record=%v}, want {%s %v record=%v}",
					tc.family, tc.current, got.Status, got.Assignee, got.RecordImplementer,
					tc.want.Status, tc.want.Assignee, tc.want.RecordImplementer)
			}
			if got.Why == "" {
				t.Error("every transition names the rule that produced it")
			}
		})
	}
}

func TestDeliver_TargetStateAndAssigneeForEveryCell(t *testing.T) {
	for _, tc := range []struct {
		name    string
		family  roles.RoleFamily
		current string
		verdict Verdict
		want    Transition
		wantOK  bool
	}{
		// GITHUB #32, verbatim: the implementer handoff clears the assignee.
		// Before this table it wrote ready_for_qa and left the bead claimed.
		{"#32: eng handoff clears the assignee", roles.FamilyEng, "in_progress", VerdictNone,
			Transition{Status: "ready_for_qa", Assignee: AssigneeClear, RecordImplementer: true}, true},
		{"#32: shipper/pm handoff clears it too", roles.FamilyOther, "in_progress", VerdictNone,
			Transition{Status: "ready_for_qa", Assignee: AssigneeClear, RecordImplementer: true}, true},

		// PASS was already correct and keeps its assignee.
		{"qa PASS advances to qa_passed", roles.FamilyQA, "in_qa", VerdictPass,
			Transition{Status: "qa_passed", Assignee: AssigneeKeep}, true},
		// A validator who never got the in_qa flip (the #34 shape) still
		// means qa_passed when they say PASS — the verdict is about the
		// work, not about where the bead happens to sit.
		{"qa PASS from the handoff state", roles.FamilyQA, "ready_for_qa", VerdictPass,
			Transition{Status: "qa_passed", Assignee: AssigneeKeep}, true},
		// Past the review, PASS is just an advance: a bead already at
		// qa_passed walks onward instead of being rewritten to itself.
		{"qa PASS on an already-passed bead advances it", roles.FamilyQA, "qa_passed", VerdictPass,
			Transition{Status: "ready_to_ship", Assignee: AssigneeKeep}, true},

		// GITHUB #33, verbatim: a FAIL went to ready_for_qa (the implementer's
		// handoff state — the wrong direction) and kept the QA assignee.
		{"#33: qa FAIL goes back to the implementer", roles.FamilyQA, "in_qa", VerdictFail,
			Transition{Status: "in_progress", Assignee: AssigneeImplementer}, true},

		// The fallback: the chain walk, assignee untouched. This is how a
		// qa_passed bead is walked to closed one deliver at a time.
		{"walk qa_passed onward", roles.FamilyOther, "qa_passed", VerdictNone,
			Transition{Status: "ready_to_ship", Assignee: AssigneeKeep}, true},
		{"walk ready_to_ship to closed", roles.FamilyEng, "ready_to_ship", VerdictNone,
			Transition{Status: "closed", Assignee: AssigneeKeep}, true},
		{"eng --fail walks back one step", roles.FamilyEng, "in_progress", VerdictFail,
			Transition{Status: "open", Assignee: AssigneeKeep}, true},

		// No-ops: the ends of the chain.
		{"terminal state does not advance", roles.FamilyEng, "closed", VerdictNone, Transition{}, false},
		{"initial state does not regress", roles.FamilyEng, "open", VerdictFail, Transition{}, false},

		// A QA role holding an in_progress bead (the #34 session-reset shape)
		// must not be advanced into the implementer's handoff state by the
		// fallback. Refused, not silently written.
		{"qa never writes ready_for_qa, even via the fallback", roles.FamilyQA, "in_progress", VerdictNone,
			Transition{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Deliver(tc.family, tc.current, tc.verdict, testChain)
			if ok != tc.wantOK {
				t.Fatalf("Deliver(%s, %s, %q) ok = %v, want %v (transition %+v)", tc.family, tc.current, tc.verdict, ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Status != tc.want.Status || got.Assignee != tc.want.Assignee || got.RecordImplementer != tc.want.RecordImplementer {
				t.Errorf("Deliver(%s, %s, %q) = {%s %v record=%v}, want {%s %v record=%v}",
					tc.family, tc.current, tc.verdict, got.Status, got.Assignee, got.RecordImplementer,
					tc.want.Status, tc.want.Assignee, tc.want.RecordImplementer)
			}
			if got.Why == "" {
				t.Error("every transition names the rule that produced it")
			}
		})
	}
}

// The invariant #33 exists to enforce, asserted over the WHOLE table rather
// than at the one cell that reported it: no QA delivery, in any state, with
// any verdict, ever writes the implementer's handoff state.
func TestDeliver_AQARoleNeverWritesReadyForQA(t *testing.T) {
	for _, current := range testChain {
		for _, v := range []Verdict{VerdictNone, VerdictPass, VerdictFail} {
			if got, ok := Deliver(roles.FamilyQA, current, v, testChain); ok && got.Status == "ready_for_qa" {
				t.Errorf("Deliver(qa, %s, %q) wrote ready_for_qa — the implementer's handoff state, from a QA role", current, v)
			}
		}
	}
}

// A FAIL sends the bead back to whoever built it, so the implementer op must
// be reachable only from a QA FAIL: any other cell handing out
// AssigneeImplementer would reassign a bead to a stale name.
func TestDeliver_OnlyAQAFailAsksForTheImplementer(t *testing.T) {
	for _, f := range []roles.RoleFamily{roles.FamilyEng, roles.FamilyQA, roles.FamilyOther} {
		for _, current := range testChain {
			for _, v := range []Verdict{VerdictNone, VerdictPass, VerdictFail} {
				got, ok := Deliver(f, current, v, testChain)
				if !ok || got.Assignee != AssigneeImplementer {
					continue
				}
				if f != roles.FamilyQA || v != VerdictFail {
					t.Errorf("Deliver(%s, %s, %q) asks for the implementer; only a QA FAIL may", f, current, v)
				}
			}
		}
	}
}
