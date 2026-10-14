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
- Finish with ` + "`" + `initech deliver <id>` + "`" + `: it moves the bead one step, clears the TUI and reports to super. Only QA moves a bead to qa_passed; only super closes beads (Filing and closing).
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

The project documents live in ` + "`" + `docs/` + "`" + ` at the workspace root. Every commit to the product repo carries a docs line: ` + "`" + `docs: <doc> §<section>` + "`" + ` naming the section it edited (` + "`" + `docs: spec §3.2` + "`" + `), or ` + "`" + `docs: no change, <reason>` + "`" + `.
- "No change" is true only when nothing a reader relies on changed. A new or changed limit, timeout, cap, name, message, field or error state goes in the doc that describes it.
- The engineer who builds the change edits that section in the same change, never "pm later".
- Docs are present tense and target state: no dates, bead ids, rulings or change history; a decision changes the sentence it affects, the same day; each line lives in exactly one document.
- pm owns product rulings and arch the system shape; they review the doc edits at close.
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

// RootFilingClosingSection is who files beads and who closes them, and how
// a bead is titled and referred to.
const RootFilingClosingSection = `## Filing and closing

- Every bead exists because the operator said yes: pm drafts the proposal (the bead, why, a recommendation, the alternatives), super asks the operator one question, pm files on the yes. pm may file a P0/P1 defect at once and say so. Only pm runs ` + "`" + `bd create` + "`" + `.
- A finding inside in-flight work stays on that bead; a finding outside your bead is one line to super, who reports it to pm.
- Super closes a bead once the engineer and QA are done and the change is merged on main, on evidence super checked itself; closing never waits for a release. Engineers deliver; they do not close. A second ` + "`" + `initech deliver` + "`" + ` on a delivered bead walks it further.
- Priority is the true severity, never inflated.
- **Titles are for the operator:** ` + "`" + `<area>: <plain-English outcome or symptom>` + "`" + `, ten words or fewer after the area, no acronyms, tool, class or spec names. Everywhere else, refer to work by title with the id in parentheses, "Hot-add keeps the layout (ini-2p91)", never the bare id.
`

// RootMessagingSection is sending safely, the shape of a message, peeking before and after, and the announcement rule; the operator inbox guide follows it in the rendered root.
const RootMessagingSection = `## Messaging

Use ` + "`" + `initech send` + "`" + ` and ` + "`" + `initech peek` + "`" + ` for all agent communication. Work
arrives from super unless your CLAUDE.md says otherwise.

` + "`" + `` + "`" + `` + "`" + `bash
initech send <role> '[from <you>] <message>'   # message an agent
initech send <role> -f <file>                 # a long body, or one with a single quote (or --stdin)
initech peek <role> -n 5                      # read an agent's terminal
initech status                                # every agent, its activity and bead
initech patrol                                # peek at all agents at once
` + "`" + `` + "`" + `` + "`" + `

### Sending

- **Single quotes around the body.** Double quotes let the shell expand backticks and ` + "`" + `$VAR` + "`" + `, and the message arrives mangled with no error. A body that must contain a single quote, or is long, goes through ` + "`" + `--stdin` + "`" + ` or ` + "`" + `-f <file>` + "`" + `.
- **Start with ` + "`" + `[from <you>]` + "`" + `.** Under about 600 characters; the detail goes on the bead. A ` + "`" + `-m` + "`" + ` on ` + "`" + `initech deliver` + "`" + ` over about 600 characters can wedge the receiving pane.
- **Every send is checked for submission:** if the Enter was not taken, it is pressed once more. A send reported as not taken, you resend.
- **Write to super** when the Escalation rule says to. **When you stop,** tell super in one line: what is done, what is left, what you wait for. A blocker reads ` + "`" + `initech send super '[from <you>] BLOCKED on <id>: <the question>'` + "`" + `.

### Shape

**Summary** (one or two sentences: what you did) + **Outcome** (one or two sentences: complete / passed / failed / blocked / needs-input / partial, and what the reader must do), then at most three bullets. Five lines at most, or start with "Long report because: <reason>". No recaps, file lists, narration, pleasantries or "on it".

**Say what happened, not which bead.** Refer to work by its title with the id in parentheses (Filing and closing), never a bare id. Bad: "ini-y71 ready for QA". Good: "Duplicate agent fill fix (ini-y71) ready for QA". Bead IDs also go in metadata (` + "`" + `--bead` + "`" + `); ` + "`" + `initech deliver` + "`" + ` and ` + "`" + `initech assign` + "`" + ` handle this for you.

### Before and after

- Before messaging an agent you have not heard from: ` + "`" + `initech peek <agent> -n 5` + "`" + `.
- After a dispatch, peek within two minutes to confirm the agent picked it up.
- The operator's inbox, ` + "`" + `initech post` + "`" + `, and its five rules are in ` + "`" + `fleet/reference.md` + "`" + `.
`

// RootBudgetSection is who may spend more time and tokens, and when.
const RootBudgetSection = `## Budget

Spending time and tokens for better software. We want robust, correct and secure software; our currency is time and tokens. Who may spend more:

- **Always spend, no asking:** security and access (fail closed, the feed-it-nothing tests, the mutation table), anything that could lose or corrupt data or money, a defect you found in your own change, and the tests that prove your change by effect.
- **Your call, inside the bead:** making the code you touch more robust (error paths, timeouts, limits, an extra edge-case test), up to about a quarter more than the step's time box. Say on the bead what you added and why.
- **Not your call; tell super:** past that budget, anything outside the code the bead touches (a refactor, a neighbour's bug, a performance push beyond the bead's target), new tooling for a one-off, or another polish pass after QA passed. That is a new bead or the "bigger" line; super routes it and decides the time and tokens up to its line, and large trades go to the operator. Money is never an agent's or super's call.
- **Proof costs too:** size a proof to the failure it guards against, and take it the cheapest way that would still catch it. A heavier check, or one on production load, states its reason on the bead first.
- **Always save:** do not re-measure what is already known, do not rerun without a hypothesis, prefer a reviewed one-off fix to new tooling, skip review on small ordinary changes, and do not reopen a bead for post-pass nits. Idle is fine; do not invent work when your bead is done. Push back on work that does not pay for itself, in one sentence with the cost, to whoever gave it to you; silence is the failure.
- Do the tier you were asked for, not more. Reports are five lines or fewer unless blocked. A finding outside your bead is one line on the bead. Do not re-ask a closed question.
`

// RootSharedMachineSection is the rules for a machine other agents and projects share.
const RootSharedMachineSection = `## Shared machine

Other agents, and often other projects, run on this machine.
- Never ` + "`" + `pkill -f` + "`" + ` or ` + "`" + `killall` + "`" + `: pattern kills hit other agents' processes; stop only PIDs you started. Never print full command lines (` + "`" + `ps aux` + "`" + `, ` + "`" + `pgrep -fl` + "`" + `): another agent's shell can carry secrets; use ` + "`" + `ps -o pid=,comm=` + "`" + `.
- Before a heavy step (build, full suite, rig) read ` + "`" + `sysctl -n vm.loadavg` + "`" + ` (Linux: ` + "`" + `cat /proc/loadavg` + "`" + `) and wait while the 5-minute average is above 30. Locally run the tests for what you changed; full suites run in CI.
- Never leave a process running when your turn ends; stop what you started, by PID.
- Never send synthetic input to the operator's screen (keystroke automation, screen control, activating his windows). Drive a TUI in a detached session or a PTY you own; browser checks are headless scripts that exit.
- **Waiting:** never block your session on a wait: no foreground command that sleeps, polls or runs over about two minutes. A wait for one outcome is a background command whose loop exits on every terminal state; a watch that reports each event is a Monitor that also matches the failures. Long jobs run in the background with output in a file. Never wait in series for what can run in parallel.
- **Secrets:** never read a secret value; check presence or a digest and print only the verdict. A leak into a transcript cannot be scrubbed: say so in the same message; super and sec decide on rotation. Never paste keys, licences or tokens into beads, messages or sessions. A key never travels on a command line or through a pipe into an interpreter: use a file with mode 600 or an environment variable, deleted after.
- **Destructive commands:** ` + "`" + `rm -rf` + "`" + ` on a variable path is guarded: ` + "`" + `"${VAR:?}"` + "`" + `. Never put a glob, a ` + "`" + `$(...)` + "`" + ` or a path you have not just listed into rm, mv or ` + "`" + `find -delete` + "`" + `. A safety prompt that stops a delete is right: never answer yes; re-issue it in the safe form or leave the files and note them on the bead. Read exit codes unpiped (` + "`" + `cmd; echo $?` + "`" + `). Never force-push; undo with a revert commit.
<!-- Opt-in, for a fleet that has found background agents unreliable; move it out of this comment to adopt it: No subagents. Do the work yourself in your own session; never start subagents, teammates or workflows: they get stuck and nobody sees it. A required review is done by you in your own session. -->
`

// RootFleetFilesSection is the fleet files beside the charters and what goes in each.
const RootFleetFilesSection = `## Fleet files

- ` + "`" + `fleet/lessons.md` + "`" + `: the incident behind each rule. Charters carry no history; a correction edits the sentence it contradicts and the story goes here.
- ` + "`" + `fleet/reference.md` + "`" + `: commands, the QA pairs and who owns which area.
- ` + "`" + `skills/` + "`" + `: one skill per repeated multi-step procedure, written and maintained by super. Tell super when a step is wrong or missing.
`

// RootBeadsSection is the bd commands every role uses.
const RootBeadsSection = `## Beads

Uses beads (` + "`" + `bd` + "`" + ` CLI). All work is tracked as beads; no markdown TODO lists. The commands are in ` + "`" + `fleet/reference.md` + "`" + `.
`

// RootLearningSection is how a correction becomes a durable edit.
const RootLearningSection = `## Learning protocol

When the operator or super corrects you, or the work reveals a process gap, apply the correction now and tell super if a charter or this file should change; super edits the template. A correction edits the sentence it contradicts; the story behind it goes to ` + "`" + `fleet/lessons.md` + "`" + `.
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
		RootMessagingSection,
		RootBudgetSection,
		RootSharedMachineSection,
		RootFleetFilesSection,
		RootBeadsSection,
		RootLearningSection,
	}
}
