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

// RootHowWeWorkSection is the bead lifecycle every role follows. Which path a change takes, and when QA tests it, is RootSensitiveChangesSection.
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
`

// RootEscalationSection is when an agent messages, escalates or decides on its own.
const RootEscalationSection = `## Escalation

Message your coordinator only at a state change that needs its action: done,
blocked with the question, a hold request, a finding the operator must see.
Keep it under about 600 characters; the detail goes on the bead.

- **Escalate to super:** a step only someone else can unblock (a ruling, a hold on a shared resource, an operator decision, another agent's code); a finding outside your bead; a red main whose owner you cannot tell; done or delivered.
- **Decide it yourself and note it on the bead:** anything reversible inside your own bead (the approach, the order of steps, a rerun, fixing your own red, test and fixture choices); a nit after QA passed; who owns a red when the log names it (tell that owner directly).
- **Do not message at all:** acks, progress with no state change, "still waiting", a repeat of what is on the bead.
- **When unsure whether something is yours:** choose what you judge right on the merits, not the easiest; reversibility decides whether you may act without asking, never which option wins. Say on the bead what you chose, the alternative and why, and keep working. A choice that hides a problem (a retry that masks a failure, a skipped or loosened test, a widened timeout, a quieted alert) is never yours to take alone.

No agent asks the operator directly; every question goes to super.
`

// RootDocsGateSection is the docs line every commit carries.
const RootDocsGateSection = `## Docs gate
`

// RootEvidenceSection is how a claim is verified, what a DONE carries, and the evidence tiers pm sets at grooming.
const RootEvidenceSection = `## Evidence

### Verification rules

- Never make a decision, finding or claim from code you have not just fetched: ` + "`" + `git fetch origin` + "`" + `, then read origin/main (` + "`" + `git show origin/main:<path>` + "`" + `), never a working tree.
- Verify by effect, never by a ledger, a name or a comment: read the live object, run the probe, grep the served bytes.
- Before trusting a search that found nothing, run the same key against a case where the thing is known to exist.
- A negative assertion needs a positive twin: an element that renders nothing must not pass.
- "Green" means CI-green on the exact sha.
- Report a commit as landed only after ` + "`" + `git merge-base --is-ancestor <sha> origin/main` + "`" + ` says so.
- A fix on disk does not reach a running process: restart it, then verify from the restarted context.
- When you change what a value means, audit every place that draws a conclusion from its absence.

### Delivering

A DONE carries its own evidence, on the bead:
1. **Fires-check.** For every new or modified test or guard, the red that proves it can fail, then the green. A change with no test names the evidence that stands in for one.
2. **Reachability.** The change runs from the production entry point on the live path; a new field arrives at its consumer.
3. **Skip count.** For every suite cited, how many tests skipped and what they are, cited to an artifact that contains the counts.
4. **Code review.** A ` + "`" + `CODE REVIEW:` + "`" + ` line listing each finding as fixed, or dismissed with the reason.
5. **QA steps.** Executable steps: the exact command, the expected result, and which criterion each covers.

Vague evidence ("suite green", "tested on device") goes back like missing evidence. A DONE whose own artifact contradicts its own sentence is named by the grader as evidence-contradicts-claim; a contradiction the author reports first is never charged. A claim that is true but narrow is a bound, not a contradiction. The full DONE goes on the bead; ` + "`" + `initech deliver -m` + "`" + ` carries only its headline, five lines at most.

### Evidence tiers

pm sets one tier per bead at grooming:
- **FULL** (default): QA reproduces the fires-check, the reachability and the by-hand run.
- **LIGHT**: QA verifies the cited runs and artifacts instead of re-running them: opens every run id, confirms red and green at the pinned sha, checks reachability against the diff, confirms the skip counts.
- **NONE**: every criterion is a readback of deployed state or a CI run; super opens the cited runs and closes.

A LIGHT bead that touches a security surface, a public artifact or data leaving the system is mis-tiered: send it back to pm.
`

// RootQAPairsSection is how ready work reaches QA: straight from the
// engineer to its QA pair, never through super; who messages whom while a
// bead is in flight; and the pairs table, which RenderRootCLAUDE generates
// from the roles list in place of {{qa_pairs_table}}.
const RootQAPairsSection = `## QA pairs

### Speed

When a change is ready (pushed; for a sensitive change, its review branch with CI green on that sha), send it to your QA pair, and to sec if it is sensitive, at the same moment, directly, not through super: ` + "`" + `initech deliver <id> --to <pair>` + "`" + `. If your pair is busy, the first free QA takes it. Merge the moment the needed verdicts are on the bead. Only a P0 interrupts work in progress.

### Messaging between agents on a bead

Agents on the same bead talk directly and post everything on the bead. The implementer sends the sha to its QA pair (and to sec and shipper where the rules say so); pm, sec and arch send a ruling straight to the implementer and to each other; super reads it on the bead. Message super for: a DONE or a verdict (super closes), a blocker that needs a decision outside the bead, anything for the operator, a hold, a finding outside your bead, an idle or stuck agent, a new dispatch. When unsure, include super.

Each engineer hands ready work to the QA named in this table, generated from the roles list; the project may edit it.

{{qa_pairs_table}}`

// RootFilingClosingSection is who files beads and who closes them.
const RootFilingClosingSection = `## Filing and closing
`

// RootMessagingSection is initech send/peek/status/patrol, how to address super, and the announcement rule; the operator inbox guide follows it in the rendered root.
const RootMessagingSection = `## Messaging

Use ` + "`" + `initech send` + "`" + ` and ` + "`" + `initech peek` + "`" + ` for all agent communication. Work
arrives from super unless your CLAUDE.md says otherwise.

` + "`" + `` + "`" + `` + "`" + `bash
initech send <role> "[from <you>] <message>"   # message an agent
initech peek <role>                           # read an agent's terminal
initech status                                # every agent, its activity and bead
initech patrol                                # peek at all agents at once
` + "`" + `` + "`" + `` + "`" + `

- **Write to super** with ` + "`" + `[from <you>]` + "`" + ` first; when to write is the Escalation rule. A blocker reads ` + "`" + `initech send super "[from <you>] BLOCKED on <id>: <the question>"` + "`" + `.
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

// RootSections returns the shared sections in render order for a fleet of
// roleNames. The operator inbox guide is part of Messaging: it renders
// straight after that section's text, where an agent reading about messages
// meets it. Sensitive changes names sec and arch only when the fleet has them.
func RootSections(roleNames []string) []string {
	return []string{
		RootHowWeWorkSection,
		RootEscalationSection,
		RootSensitiveChangesSection + "\n" + RootRoleBlock(hasRole(roleNames, "sec"), hasRole(roleNames, "arch")),
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
