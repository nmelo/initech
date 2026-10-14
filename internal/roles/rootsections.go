package roles

// Root CLAUDE.md sections (ini-vby5.1). The project-root CLAUDE.md is the
// fleet's shared rulebook: every agent reads it through Claude Code's
// CLAUDE.md hierarchy, so a rule that applies to every role lives here ONCE
// and the role templates carry only what each role does differently.
//
// One constant per shared rule, in render order: RootSections lists them and
// scaffold.RenderRootCLAUDE writes them after Project Documents and Roles. A
// section whose rule has not been written yet holds only its heading; whoever
// writes the rule fills that one constant, so parallel work on different
// rules never touches the same lines. They are constants in this package, not
// in scaffold, so scripts/templatecensus checks every initech verb they teach.

// RootHowWeWorkSection is the bead lifecycle and QA tiers every role follows.
const RootHowWeWorkSection = `## How we work

Every unit of work is a bead. These rules apply to every role; your own
CLAUDE.md adds what your role does differently.

### Bead lifecycle

` + "`" + `open -> in_progress -> ready_for_qa -> in_qa -> qa_passed -> closed` + "`" + `

- Claim a bead and show it in the TUI: ` + "`" + `bd update <id> --status in_progress --assignee <you>` + "`" + ` then ` + "`" + `initech bead <id>` + "`" + `.
- Comment a PLAN on the bead before writing code: numbered steps, files, how you will test.
- Write tests with the code. A bug fix gets a regression test that fails before the fix and passes after.
- Push before marking ready_for_qa. QA pulls from the remote; an unpushed commit does not exist for them.
- Finish with ` + "`" + `initech deliver <id>` + "`" + `: it moves the bead one step, clears the TUI and reports to super. Only QA moves a bead to qa_passed; only the operator closes beads.

### QA tiers

- **Full QA:** P1 bugs, rendering and UI changes, new user-facing features.
- **Light QA (tests plus a read of the change):** P2/P3 fixes, internal changes, refactors with test coverage.
- **Skip QA:** template text, doc fixes, mechanical and constant changes.
`

// RootEscalationSection is when an agent messages, escalates or decides on its own.
const RootEscalationSection = `## Escalation
`

// RootSensitiveChangesSection is which changes are sensitive and the path they take.
const RootSensitiveChangesSection = `## Sensitive changes
`

// RootDocsGateSection is the docs line every commit carries.
const RootDocsGateSection = `## Docs gate
`

// RootEvidenceSection is what a DONE and a verdict must carry.
const RootEvidenceSection = `## Evidence

No completion claim without fresh verification evidence. Before reporting DONE:
1. Run the tests and include the output showing they pass.
2. Run the build and confirm it exits 0.
3. If the bead has behavioural acceptance criteria, run the software and check the behaviour.
4. Put that evidence in the DONE comment.

"Should pass" and "passed earlier" are not evidence.
`

// RootQAPairsSection is how ready work reaches QA.
const RootQAPairsSection = `## QA pairs
`

// RootFilingClosingSection is who files beads and who closes them.
const RootFilingClosingSection = `## Filing and closing
`

// RootMessagingSection is initech send/peek/status/patrol, reporting, escalating a blocker, and the announcement rule; the operator inbox guide follows it in the rendered root.
const RootMessagingSection = `## Messaging

Use ` + "`" + `initech send` + "`" + ` and ` + "`" + `initech peek` + "`" + ` for all agent communication. Work
arrives from super unless your CLAUDE.md says otherwise.

` + "`" + `` + "`" + `` + "`" + `bash
initech send <role> "[from <you>] <message>"   # message an agent
initech peek <role>                           # read an agent's terminal
initech status                                # every agent, its activity and bead
initech patrol                                # peek at all agents at once
` + "`" + `` + "`" + `` + "`" + `

- **Report to super** with ` + "`" + `[from <you>]` + "`" + ` first. Super cannot see your work unless you say so; report every finished task.
- **Escalate a blocker** within 15 minutes: ` + "`" + `initech send super "[from <you>] BLOCKED on <id>: <reason>"` + "`" + `.
- **Say what happened, not which bead.** The operator does not memorize bead IDs. Bad: "ini-y71 ready for QA". Good: "Duplicate agent fill fix ready for QA". Bead IDs belong in metadata (` + "`" + `--bead` + "`" + `); ` + "`" + `initech deliver` + "`" + ` and ` + "`" + `initech assign` + "`" + ` handle this for you.
`

// RootBudgetSection is who may spend more time and tokens, and when.
const RootBudgetSection = `## Budget
`

// RootSharedMachineSection is the rules for a machine other agents and projects share.
const RootSharedMachineSection = `## Shared machine

Other agents, and often other projects, run on this machine.

- **Never leave a process running when your turn ends.** Servers, load generators and rigs run in the foreground or under a timeout; stop what you started, by PID.
- **Never ` + "`" + `pkill -f` + "`" + `.** It matches other agents' processes, and over ssh it can match its own shell. Kill by PID.
- **Never print other processes' full command lines** (` + "`" + `ps aux` + "`" + `, ` + "`" + `pgrep -fl` + "`" + `): other agents' shells carry secrets. Use PIDs and process names.
`

// RootFleetFilesSection is the fleet files beside the charters and what goes in each.
const RootFleetFilesSection = `## Fleet files
`

// RootBeadsSection is the bd commands every role uses.
const RootBeadsSection = `## Beads

Uses beads (` + "`" + `bd` + "`" + ` CLI). All work is tracked as beads; no markdown TODO lists.

` + "`" + `` + "`" + `` + "`" + `bash
bd ready                                 # unblocked work
bd list                                  # all beads
bd show <id>                             # bead details and comments
bd update <id> --status <status>         # transition a bead
bd comments add <id> --author <you> "…"  # PLAN, DONE, verdicts
` + "`" + `` + "`" + `` + "`" + `
`

// RootLearningSection is how a correction becomes a durable edit.
const RootLearningSection = `## Learning protocol

When the operator or super corrects you, or the work reveals a process gap:
1. Apply the correction immediately.
2. Decide where the gap is: your CLAUDE.md, this root file, or a spec.
3. Edit that file so the lesson persists. A correction edits the sentence it contradicts.
`

// RootSections returns the shared sections in render order. The operator
// inbox guide is part of Messaging: it renders straight after that section's
// text, where an agent reading about messages meets it.
func RootSections() []string {
	return []string{
		RootHowWeWorkSection,
		RootEscalationSection,
		RootSensitiveChangesSection,
		RootDocsGateSection,
		RootEvidenceSection,
		RootQAPairsSection,
		RootFilingClosingSection,
		RootMessagingSection + OperatorInboxGuide,
		RootBudgetSection,
		RootSharedMachineSection,
		RootFleetFilesSection,
		RootBeadsSection,
		RootLearningSection,
	}
}
