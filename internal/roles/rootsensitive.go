package roles

import (
	"regexp"
	"strings"
)

// The Sensitive changes section of the root CLAUDE.md: "one question decides
// the path of every change: is it sensitive?" (ini-vby5.3), with Building and
// Reviewing as its subsections.
//
// The root names only roles the fleet has (sec and arch are not in the
// default fleet). Every sentence that depends on the roles list is a named
// role block: a section holds {{roles:<name>}}, RenderRoleBlocks puts the
// block's text there between two markers, and hiring or firing an agent
// re-renders the text between the markers (scaffold.RefreshRootRoleBlocks).
// Only those sentences sit inside markers; everything else in the root is the
// project's to edit and survives a hire.

// RoleBlockNames lists the named role blocks, in the order they appear in the
// rendered root.
var RoleBlockNames = []string{"roles-list", "sensitive-ruling", "sensitive-merge", "bigger", "structural-review", "qa-tests", "docs-owners", "handoff"}

// RoleBlockBegin opens the named role block.
func RoleBlockBegin(name string) string {
	return "<!-- initech:roles " + name + " begin. Rewritten when an agent is hired or fired; edits inside are lost. -->"
}

// RoleBlockEnd closes the named role block.
func RoleBlockEnd(name string) string {
	return "<!-- initech:roles " + name + " end -->"
}

// RoleBlockBody is the text of the named role block for a fleet of
// roleNames, ending in a newline, or "" for a block with nothing to say in
// this fleet. Unknown names render "".
func RoleBlockBody(name string, roleNames []string) string {
	sec, arch, qa := hasRole(roleNames, "sec"), hasRole(roleNames, "arch"), hasFamily(roleNames, FamilyQA)
	switch name {
	case "sensitive-ruling":
		if sec {
			return "(a) Before code, one joint ruling from pm and sec, with sec's conditions quoted verbatim on the bead.\n"
		}
		return "(a) Before code, pm rules on the approach alone and names the security conditions on the bead; the engineer quotes them in the PLAN.\n"
	case "sensitive-merge":
		if !qa && sec {
			return "(d) When the branch is ready, sec gets the head SHA, and the engineer's DONE carries the mutation table for it.\n" +
				"(e) Merge to main only when sec's verdict is on the bead for that SHA and super has checked the DONE evidence. After a rebase, an equal `git patch-id --stable` per commit keeps both. A product-code change goes back to sec.\n"
		}
		if !qa {
			return "(d) When the branch is ready, the engineer's DONE carries the head SHA and the mutation table.\n" +
				"(e) Merge to main only when super has checked that evidence for that SHA. After a rebase, an equal `git patch-id --stable` per commit keeps the check. A product-code change goes back to super.\n"
		}
		if sec {
			return "(d) When the branch is ready, sec and QA get the same SHA at once.\n" +
				"(e) Merge to main only when both verdicts, sec's and QA's, are on the bead for that SHA. After a rebase, an equal `git patch-id --stable` per commit keeps the verdicts. A change to tests or comments only keeps every verdict, with the proof posted. A product-code change goes back to sec and QA.\n"
		}
		return "(d) When the branch is ready, QA gets the branch head SHA.\n" +
			"(e) Merge to main only when QA's verdict is on the bead for that SHA. After a rebase, an equal `git patch-id --stable` per commit keeps the verdict. A change to tests or comments only keeps the verdict, with the proof posted. A product-code change goes back to QA.\n"
	case "bigger":
		if arch {
			return "A `bigger:` line goes to arch through super.\n"
		}
		return "A `bigger:` line goes to super, who rules or asks the operator.\n"
	case "structural-review":
		if arch {
			return "- super or pm may ask arch for one structural review when a change touches two components, a migration, a stream or a cache, or when the engineer asks.\n"
		}
		return ""
	case "roles-list":
		var b strings.Builder
		for _, r := range roleNames {
			b.WriteString("- `" + r + "/` - " + RoleSummary(r) + "\n")
		}
		return b.String()
	case "qa-tests":
		if qa {
			return "- QA: a sensitive change is tested on its branch head before merge; everything else is tested on main only when it is a new user flow or a P1 fix.\n"
		}
		return "- There is no QA role: the engineer's DONE evidence is the gate, and super closes on it after checking it.\n"
	case "handoff":
		ready := "When a change is ready (pushed; for a sensitive change, its review branch with CI green on that sha), "
		if qa {
			return ready + "send it to your QA pair, and for a sensitive change to every reviewer Sensitive changes names, at the same moment, directly, not through super: `initech deliver <id> --to <pair>`. If your pair is busy, the first free QA takes it.\n"
		}
		return ready + "deliver it with its DONE evidence: `initech deliver <id>`, and for a sensitive change send it to every reviewer Sensitive changes names. Super checks the evidence and closes.\n"
	case "docs-owners":
		if arch {
			return "- pm owns product rulings and arch the system shape; they review the doc edits at close.\n"
		}
		return "- pm owns product rulings and the system shape, and reviews the doc edits at close.\n"
	}
	return ""
}

// RenderRoleBlock is the named block for roleNames between its markers,
// without a trailing newline.
func RenderRoleBlock(name string, roleNames []string) string {
	return RoleBlockBegin(name) + "\n" + RoleBlockBody(name, roleNames) + RoleBlockEnd(name)
}

// RenderRoleBlocks replaces every {{roles:<name>}} in text with that block
// for roleNames.
func RenderRoleBlocks(text string, roleNames []string) string {
	for _, name := range RoleBlockNames {
		text = strings.ReplaceAll(text, "{{roles:"+name+"}}", RenderRoleBlock(name, roleNames))
	}
	return text
}

// RootSensitiveChangesSection is the Sensitive changes section with its
// Building and Reviewing subsections; the role-dependent sentences are
// {{roles:...}} blocks.
const RootSensitiveChangesSection = `## Sensitive changes

One question sets the path of every change. Here, sensitive means it touches money; sign-in, access or credentials (auth, tenancy, sessions, keys, tokens); a database or stored-data migration; or a data format between components. Everything not sensitive goes straight to main.
Add your project's own sensitive areas here: (none yet).

A sensitive change takes this path:
{{roles:sensitive-ruling}}
(b) Work on a short-lived branch ` + "`" + `review/<bead-id>` + "`" + `, never on main.
(c) Before review, a mutation table: for every boundary the change adds (each allow check, cap, refusal), flip or delete it and show the test that goes red with its failure line, plus one test per check fed nothing (empty, missing, error, unknown) proving it refuses. A surviving mutant is a missing test.
{{roles:sensitive-merge}}
(f) Delete the branch and say 0 refs remain.

### Building

Before the PLAN, read the code the bead touches and post exactly one line on the bead: ` + "`" + `as specified` + "`" + `, ` + "`" + `doc gap: <what the spec does not say>` + "`" + ` (goes to pm through super) or ` + "`" + `bigger: because <X>` + "`" + `. The last two stop the bead: it is not built as written until answered. Otherwise PLAN and start in the same turn. Time box each step to 10-15 minutes, then report.
{{roles:bigger}}

### Reviewing

- A sensitive change gets one full review on its final head before merge, done by the engineer in their own session.
- An ordinary change under about 100 lines in one part of the product gets no review beyond its red-first test and CI. A larger one merges first and gets one review afterwards, its findings fixed in a follow-up. Only a blocking defect holds a merge.
{{roles:structural-review}}
{{roles:qa-tests}}
`

// hasRole reports whether roleNames has an agent named role exactly.
func hasRole(roleNames []string, role string) bool {
	for _, r := range roleNames {
		if r == role {
			return true
		}
	}
	return false
}

// roleCondition matches {{if <role>}}...{{end}} and {{if !<role>}}...{{end}}
// spans (no nesting). The role "qa" means any QA role (qa1, qa2, ...).
var roleCondition = regexp.MustCompile(`(?s)\{\{if (!?)([a-z][a-z0-9-]*)\}\}(.*?)\{\{end\}\}`)

// ApplyRoleConditions keeps the text of each {{if <role>}}...{{end}} span
// when the fleet has that role, and of each {{if !<role>}}...{{end}} span
// when it does not; the other spans are removed with their markers. A span
// that should drop a whole line carries the line's newline inside it.
// Charters use it so they name only roles the fleet has.
func ApplyRoleConditions(text string, roleNames []string) string {
	return roleCondition.ReplaceAllStringFunc(text, func(m string) string {
		g := roleCondition.FindStringSubmatch(m)
		has := hasRole(roleNames, g[2])
		if g[2] == "qa" {
			has = hasFamily(roleNames, FamilyQA) // any QA role: qa1, qa2, ...
		}
		if has != (g[1] == "!") {
			return g[3]
		}
		return ""
	})
}
