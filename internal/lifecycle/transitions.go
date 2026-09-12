package lifecycle

import "github.com/nmelo/initech/internal/roles"

// transitions.go is THE lifecycle transition table (ini-1fb9). Every bead
// state and assignee write initech makes comes from here: `initech assign`
// reads Dispatch, `initech deliver` reads Deliver, and neither decides a
// status or an assignee on its own.
//
// WHY A TABLE AND NOT THREE FIXES. GitHub #32/#33/#34 are one family — a
// command writing a state it chose locally:
//
//	#32  deliver flipped to ready_for_qa and never touched the assignee, so
//	     every correct handoff left the bead claimed by the implementer.
//	#33  deliver --verdict FAIL wrote ready_for_qa — the implementer's
//	     handoff state, the wrong DIRECTION — and kept the QA assignee.
//	#34  assign wrote in_progress whatever the target's role, so a validator
//	     dispatch misreported the bead until the validator self-corrected.
//
// #33 is a sibling of #21, closed in May for the same class: that fix
// replaced deliver's role-aware decision with a pure chain walk, which was
// correct for the cell it touched and wrong for the FAIL branch added later.
// A table one caller can bypass is how that happens, so the table is the only
// place a target state is named, and the tests assert the invariant over
// every cell rather than at the cell that reported the bug.
//
// The chain walk is still here as the FALLBACK row, deliberately: a bead is
// walked from qa_passed to closed by repeated `initech deliver`, and only the
// named rows below override that.

// Verdict is a delivery's outcome. VerdictNone is a plain success;
// VerdictFail covers both `--verdict FAIL` (a QA rejection) and `--fail` (an
// implementer reporting they could not finish) — the family in the table
// says which meaning applies.
type Verdict string

const (
	VerdictNone Verdict = ""
	VerdictPass Verdict = "PASS"
	VerdictFail Verdict = "FAIL"
)

// AssigneeOp is what a transition does to the bead's assignee. It is part of
// the transition because "which state" and "who holds it" are one decision:
// #32 shipped because they were made in different places, and one of them
// was made nowhere.
type AssigneeOp int

const (
	// AssigneeKeep leaves the assignee untouched.
	AssigneeKeep AssigneeOp = iota
	// AssigneeTarget sets the assignee to the dispatch target.
	AssigneeTarget
	// AssigneeClear empties the assignee: the bead is handed off and nobody
	// holds it until it is dispatched again.
	AssigneeClear
	// AssigneeImplementer sets the assignee to the bead's recorded
	// implementer. Callers that cannot determine one must clear it and say
	// so, never silently keep the current holder.
	AssigneeImplementer
)

func (op AssigneeOp) String() string {
	switch op {
	case AssigneeTarget:
		return "set-to-target"
	case AssigneeClear:
		return "cleared"
	case AssigneeImplementer:
		return "set-to-implementer"
	default:
		return "kept"
	}
}

// Transition is one row's answer: the status to write, what happens to the
// assignee, whether the caller should record who the implementer is, and the
// rule that produced it (named in the operator-facing summary, so a surprising
// write can be traced to a row rather than guessed at).
type Transition struct {
	Status            string
	Assignee          AssigneeOp
	RecordImplementer bool
	Why               string
}

// implementerHandoffState is the state an implementer hands a bead off into,
// and the one a QA role must never write (#33).
const implementerHandoffState = "ready_for_qa"

// qaVerdictState is the last state at which a bead is still under review, and
// qaPassedState is where a passing verdict puts it.
const (
	qaVerdictState = "in_qa"
	qaPassedState  = "qa_passed"
)

// atOrBefore reports whether state a sits at or before state b in the chain.
// An unknown state answers false: a bead in a state the chain does not
// contain gets the fallback, never a verdict row derived from a position
// nobody can place.
func atOrBefore(chain []string, a, b string) bool {
	ia, ib := -1, -1
	for i, s := range chain {
		if s == a {
			ia = i
		}
		if s == b {
			ib = i
		}
	}
	return ia >= 0 && ib >= 0 && ia <= ib
}

// Dispatch returns the transition for `initech assign <agent> <bead>`, keyed
// by the TARGET's role family. A validator dispatch puts the bead in the
// validation state directly; anything else claims it for work.
//
// Independent of the current state on purpose: a dispatch is an instruction
// about who works on the bead next, not a step along the chain, and the bead
// may legitimately arrive from any state (a fresh bead, a delivered one, a
// re-dispatch after a failed review). Assigning a bead already in the target
// state is a no-op write, not an error.
func Dispatch(family roles.RoleFamily, current string) Transition {
	if family == roles.FamilyQA {
		return Transition{
			Status:   "in_qa",
			Assignee: AssigneeTarget,
			Why:      "validator dispatch",
		}
	}
	return Transition{
		Status:            "in_progress",
		Assignee:          AssigneeTarget,
		RecordImplementer: true,
		Why:               "implementer dispatch",
	}
}

// Deliver returns the transition for `initech deliver`, keyed by the CALLER's
// role family, the bead's current state and the verdict. ok is false when
// there is nothing to write — the ends of the chain, or a QA delivery that
// would otherwise land in the implementer's handoff state; the caller reports
// the no-op with Why and leaves the bead alone.
func Deliver(family roles.RoleFamily, current string, verdict Verdict, chain []string) (Transition, bool) {
	switch {
	// A QA rejection sends the bead back to whoever built it: the review is
	// the implementer's problem again, so the state and the holder both move
	// (#33). Never the handoff state, which would read to super and to the
	// sweep ledger as a fresh engineer handoff.
	case family == roles.FamilyQA && verdict == VerdictFail:
		return Transition{
			Status:   "in_progress",
			Assignee: AssigneeImplementer,
			Why:      "QA verdict FAIL: back to the implementer",
		}, true

	// A QA pass advances the bead and leaves it where it is: the reviewer
	// stays the last actor until the bead is dispatched onward.
	//
	// Applies while the bead is still UNDER REVIEW — at or before the
	// validation state — so a validator who never got the in_qa flip (the
	// #34 shape) still means qa_passed when they say PASS. Past that point
	// the review is over and PASS is just an advance, which the fallback
	// handles: a bead already at qa_passed is walked onward toward closed,
	// not rewritten to the state it is in.
	//
	// FAIL is deliberately not gated the same way: a rejection is meaningful
	// at any point (a re-review of a passed bead sends it back), while a
	// second PASS on a passed bead has nothing to say.
	case family == roles.FamilyQA && verdict == VerdictPass && atOrBefore(chain, current, qaVerdictState):
		return Transition{
			Status:   qaPassedState,
			Assignee: AssigneeKeep,
			Why:      "QA verdict PASS",
		}, true

	// The implementer handoff: the work is done and the bead is nobody's
	// until QA is dispatched. Clearing here is #32's fix, and recording the
	// implementer here is what makes a later FAIL able to find them — this
	// write is the last moment that fact exists on the bead.
	case family != roles.FamilyQA && verdict == VerdictNone && current == "in_progress":
		return Transition{
			Status:            implementerHandoffState,
			Assignee:          AssigneeClear,
			RecordImplementer: true,
			Why:               "implementer handoff",
		}, true
	}

	// Fallback: the plain chain walk, assignee untouched. Advancing a
	// qa_passed bead toward closed, an implementer's --fail walking back one
	// step, and any state this table does not name all land here.
	var (
		status string
		canDo  bool
	)
	if verdict == VerdictFail {
		status, canDo = PrevState(chain, current)
	} else {
		status, canDo = NextState(chain, current)
	}
	if !canDo {
		return Transition{}, false
	}
	// The invariant #33 exists to enforce, applied to the fallback too: a QA
	// role never writes the implementer's handoff state, whatever the chain
	// would say. Reached when a validator holds a bead in an implementer
	// state (the session-reset shape in #34) — refused rather than written.
	if family == roles.FamilyQA && status == implementerHandoffState {
		return Transition{}, false
	}
	return Transition{
		Status:   status,
		Assignee: AssigneeKeep,
		Why:      "lifecycle walk",
	}, true
}
