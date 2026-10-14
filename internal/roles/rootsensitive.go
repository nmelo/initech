package roles

import "strings"

// The Sensitive changes section of the root CLAUDE.md: "one question decides
// the path of every change: is it sensitive?" (ini-vby5.3), with Building and
// Reviewing as its subsections.
//
// The root names only roles the fleet has: sec and arch are not in the
// default fleet, so their lines render from the roles list. Those lines sit
// between RootRoleBlockBegin and RootRoleBlockEnd, which hiring or firing an
// agent re-renders (scaffold.RefreshRootRoleBlock). Everything outside the
// markers is the project's to edit, including the line that extends the
// sensitive list, so it sits above the block.

// RootRoleBlockBegin opens the part of the root CLAUDE.md that is rendered
// from the roles list and rewritten when an agent is hired or fired.
const RootRoleBlockBegin = "<!-- initech:roles begin. Rewritten when an agent is hired or fired; edits inside this block are lost. -->"

// RootRoleBlockEnd closes the block RootRoleBlockBegin opens.
const RootRoleBlockEnd = "<!-- initech:roles end -->"

// RootSensitiveChangesSection is the Sensitive changes heading, the generic sensitive
// list and the line a project edits to extend it. Outside the role block.
const RootSensitiveChangesSection = `## Sensitive changes

One question sets the path of every change. Here, sensitive means it touches money; sign-in, access or credentials (auth, tenancy, sessions, keys, tokens); a database or stored-data migration; or a data format between components. Everything not sensitive goes straight to main.
This project also treats nothing else as sensitive. (To extend the list, edit this line: name what else in place of "nothing else".)
`

// RootRoleBlock renders the Sensitive-changes path, Building and Reviewing
// for a fleet with or without sec and arch, wrapped in the role-block
// markers.
func RootRoleBlock(hasSec, hasArch bool) string {
	ruling := "(a) Before code, pm rules on the approach alone and names the security conditions on the bead; the engineer quotes them in the PLAN."
	sameSHA := "(d) When the branch is ready, QA gets the branch head SHA."
	merge := "(e) Merge to main only when QA's verdict is on the bead for that SHA."
	backTo := "QA"
	if hasSec {
		ruling = "(a) Before code, one joint ruling from pm and sec, with sec's conditions quoted verbatim on the bead."
		sameSHA = "(d) When the branch is ready, sec and QA get the same SHA at once."
		merge = "(e) Merge to main only when both verdicts, sec's and QA's, are on the bead for that SHA."
		backTo = "sec and QA"
	}
	bigger := "goes to super, who rules or asks the operator"
	if hasArch {
		bigger = "goes to arch through super"
	}

	var b strings.Builder
	b.WriteString(RootRoleBlockBegin + "\n")
	b.WriteString("A sensitive change takes this path:\n")
	b.WriteString(ruling + "\n")
	b.WriteString("(b) Work on a short-lived branch `review/<bead-id>`, never on main.\n")
	b.WriteString("(c) Before review, a mutation table: for every boundary the change adds (each allow check, cap, refusal), flip or delete it and show the test that goes red with its failure line, plus one test per check fed nothing (empty, missing, error, unknown) proving it refuses. A surviving mutant is a missing test.\n")
	b.WriteString(sameSHA + "\n")
	b.WriteString(merge + " After a rebase, an equal `git patch-id --stable` per commit keeps the verdicts. A change to tests or comments only keeps every verdict, with the proof posted. A product-code change goes back to " + backTo + ".\n")
	b.WriteString("(f) Delete the branch and say 0 refs remain.\n\n")

	b.WriteString("### Building\n\n")
	b.WriteString("Before the PLAN, read the code the bead touches and post exactly one line on the bead: `as specified`, `doc gap: <what the spec does not say>` (goes to pm through super) or `bigger: because <X>` (" + bigger + "). The last two stop the bead: it is not built as written until answered. Otherwise PLAN and start in the same turn. Time box each step to 10-15 minutes, then report.\n\n")

	b.WriteString("### Reviewing\n\n")
	b.WriteString("- A sensitive change gets one full review on its final head before merge, done by the engineer in their own session.\n")
	b.WriteString("- An ordinary change under about 100 lines in one part of the product gets no review beyond its red-first test and CI. A larger one merges first and gets one review afterwards, its findings fixed in a follow-up. Only a blocking defect holds a merge.\n")
	if hasArch {
		b.WriteString("- super or pm may ask arch for one structural review when a change touches two components, a migration, a stream or a cache, or when the engineer asks.\n")
	}
	b.WriteString("- QA: a sensitive change is tested on its branch head before merge; everything else is tested on main only when it is a new user flow or a P1 fix.\n")
	b.WriteString(RootRoleBlockEnd + "\n")
	return b.String()
}

// hasRole reports whether roleNames has an agent named role exactly.
func hasRole(roleNames []string, role string) bool {
	for _, r := range roleNames {
		if r == role {
			return true
		}
	}
	return false
}
