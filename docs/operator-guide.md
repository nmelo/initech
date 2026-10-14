# Operator Guide

You know Claude Code. You've never used initech. This guide gets you from zero to running a productive multi-agent session.

The README covers installation, configuration, command reference, and architecture. This guide covers the workflow: how to think about running a team of agents, what to do when things go wrong, and the patterns that make sessions productive.

## Your First Session

### Before You Launch

If you haven't set up a project yet: `mkdir myproject && cd myproject && initech init`. The init wizard walks you through project name, repo URL, and an interactive role selector. Start with the Standard preset (super, pm, eng1, eng2, qa1, qa2, shipper). You can add or remove agents later.

### The Files Your Agents Read

`initech init` writes one root `CLAUDE.md` at the project root and one `CLAUDE.md` per agent in its directory. Every agent reads both.

- **The root `CLAUDE.md` is the shared rulebook.** It holds what applies to every agent: the project documents, the roles, how work moves through beads, escalation, which changes are sensitive, the docs line every commit carries, evidence, QA pairs, who files and who closes beads, messaging, budget, the shared-machine rules and the learning protocol. A rule that applies to everyone is written here once.
- **`fleet/` and `skills/` hold what the rules point at.** `fleet/lessons.md` holds the incident behind each rule, `fleet/reference.md` the commands and the operator's inbox guide (`initech post`), and `skills/` one skill per repeated procedure, kept by super. `initech init` creates them once and never overwrites them, even with `--force`.
- **An agent's own `CLAUDE.md` carries only what that role does differently:** its identity, failure modes, decision authority and workflow. It opens by pointing at the root.

Each agent's `CLAUDE.md` starts with a two-line generated header naming the template it came from and a hash of its contents. `initech add-agent <role> --regen` rewrites that agent's `CLAUDE.md` from the current template, for example after upgrading initech. It refuses a file you edited after it was generated, or one with no header, and says why; add `--force` to overwrite it anyway.

Nothing changes in an existing project on its own. Its files change only when you rerun `initech init --force` (which rewrites the root and every agent's `CLAUDE.md`) or `initech add-agent <role> --regen`.

### What Happens on Launch

Run `initech` (no subcommand) from your project root. The TUI starts all agents simultaneously. Each agent gets its own PTY running Claude Code with the flags from your `initech.yaml`.

You'll see a grid of panes. Each pane is a live Claude Code session. The floating overlay panel in the top-right shows all agents with activity indicators. Within 10-30 seconds, agents initialize and their prompts appear.

### Your First Five Minutes

1. **Find the super pane.** Click it or use Alt+Left/Right to navigate. Super is your coordinator. Every other agent communicates through super.

2. **Tell super what to do.** Type naturally in super's pane. "We're building a REST API for user management. Start with the PRD." Super will dispatch work to PM, who will produce a PRD, which you review, and the cycle begins.

3. **Watch the overlay.** Green dots mean an agent is actively processing. Gray dots mean idle. Yellow dots (idle with work waiting) mean something needs your attention. Toasts appear in the bottom-right when agents complete beads, stall, or hit errors.

4. **Don't micromanage.** Let super coordinate. Your job is to set direction, make decisions when asked, and approve results. Super handles dispatch, monitoring, and QA routing.

## What a Trained Fleet Sets

`initech init` ends a new `initech.yaml` with commented-out examples of the settings experienced fleets add by hand. None of them takes effect until you remove the `#`. `initech init --force` on an existing project adds only the examples the file does not already have, live or commented, and changes no line you wrote; a plain `initech init` on an existing project adds nothing. One exception to both: if the file has no `attention.hooks` answer yet, init asks the consent question and adds that one key.

When initech changes `initech.yaml` (the hooks answer, `initech add-agent`, `initech delete-agent`), it re-reads the file and changes only the lines of the keys it means to change. Your comments, blank lines, key order and keys initech does not know stay exactly as they are; a new key goes at the end of its section, and a removed role takes only its own lines. A list written on one line (`roles: [a, b]`) is rewritten as one item per line when it changes, and a comment on that line goes with it. If the file does not parse, or a change cannot be made safely (for example a value that comes from a YAML merge key), initech leaves the file untouched and names the key and line to change by hand. Two initech commands writing the file at once both land.

| Key | What it does | Set it when |
|-----|--------------|-------------|
| `attention.hooks` | Installs the notification hook that tells initech an agent is waiting on you. Written by the consent question at init (`true` for yes, `false` for no) and never asked again. | You answered the init question; change it by hand to switch. |
| `claude_command` | Replaces the `claude` command for every agent, e.g. `[env, SOME_VAR=1, claude]` to set an environment variable. | Every agent needs the same wrapper or environment. |
| `claude_args` | Flags for every Claude agent, e.g. `[--continue, --dangerously-skip-permissions]` to resume the last session and skip permission prompts. | Agents should pick up where they left off and work unattended. |
| `resource.auto_suspend`, `resource.pressure_threshold` | Parks idle agents when memory use passes the threshold (a percentage). Off when absent. | The machine runs more agents than its memory comfortably holds. |
| `running_pane_tint` | Background tint behind panes that are working. The default is `#0c120e`; trained fleets use `#1e2022`, a lighter grey. `none` turns it off. | The default tint is too faint on your display. |
| `role_overrides.<role>.claude_args` | Flags for one role. They REPLACE `claude_args` for that role, so repeat the shared flags, then add e.g. `--model <model>` or `--settings <path>` for a per-agent settings file. | One role needs a different model or its own settings. |

## The Dispatch Cycle

This is the core loop that drives all work in initech.

### The Full Cycle

```
You decide what to build
  -> PM grooms it into bead(s) with acceptance criteria
    -> Super dispatches to an engineer
      -> Engineer comments PLAN, implements, writes tests, comments DONE
        -> Engineer hands it straight to its QA pair when the root says QA tests it (a sensitive change, a new user flow, a P1 fix)
          -> QA verifies, sends PASS or FAIL to the engineer and super
            -> Super closes the bead on evidence it checked itself (or it bounces back for rework)
```

Every step has a handoff. Messages between agents flow through `initech send`. Every transition is tracked in beads (`bd`). Nothing is invisible.

Agents on the same bead talk to each other directly and post everything on the bead; super reads the bead and steps in when a ready bead sits unclaimed. The root `CLAUDE.md` holds a **QA pairs** table that `initech init` generates from the roles list: engineers are split into blocks, one per QA, with any extra engineers going to the first QAs. If your pair is busy, the first free QA takes the work. With no QA role, the table says the engineer's DONE evidence is the gate and super closes on it after checking it; super never grades. Edit the table to change pairs. `initech add-agent` of an eng or qa role re-renders the table and says so, unless you edited it, in which case it leaves the table alone and tells you to add the new agent's pair yourself.

### What the Operator Does at Each Step

**Setting direction.** Tell super what you want. Be specific about the problem, not the solution. "Users can't paste images into agent panes" is better than "add EventPaste handling to tui.go". Let PM and arch figure out the how.

**Reviewing.** When PM produces a PRD or spec, read it. When an engineer proposes a design, review it. Your approval unblocks implementation. Delays here stall the whole pipeline.

**Deciding.** Super will present decisions as structured options: "Option A does X, option B does Y, I recommend A because Z." Pick one and move on. If you need more info, ask. Don't let decisions sit.

**New beads.** Every bead exists because you said yes: pm drafts the proposal, super asks you one question, pm files on your yes. Super closes beads once the engineer and QA are done and the change is merged, after checking the evidence itself.

### Sensitive Changes

One question sets how much review a change gets: is it sensitive? The root `CLAUDE.md` defines it (money; sign-in, access or credentials; a database or stored-data migration; a data format between components) and has one line you edit to add your project's own areas. A sensitive change gets a ruling before code, a short-lived `review/<bead-id>` branch, a mutation table, and QA's verdict (and sec's, when you have a sec agent) on its branch head before it merges. Everything else goes straight to main; QA tests it on main only when it is a new user flow or a P1 fix. An engineer who finds the spec silent or the bead bigger than written says so in one line before the PLAN, and the bead stops until someone answers.

The root and every agent's `CLAUDE.md` name only the roles your fleet has: without a `sec` agent, pm rules on a sensitive change alone; without an `arch` agent, a `bigger:` line goes to super. In the root, each sentence that depends on the roles sits alone between two `<!-- initech:roles <name> ... -->` markers. Hiring or firing an agent (`initech add-agent`, `initech delete-agent`) rewrites the text between those markers and nothing else, so your own edits to the root stay, including the time box and review rules. A root missing a block keeps it missing and the command says so. An agent's own `CLAUDE.md` is rendered for the roles the fleet had at the time; after hiring `sec` or `arch`, `initech add-agent <role> --regen` brings another agent's file up to date.

### Evidence Tiers

PM sets an evidence tier on each bead when grooming it. It decides how much of the engineer's evidence QA re-runs:

- **FULL** (the default): QA reproduces the engineer's red-then-green test run, checks the change runs from the real entry point, and runs it by hand.
- **LIGHT**: QA checks the runs and artifacts the engineer cited instead of re-running them. A bead that touches security, a public artifact or data leaving the system is never LIGHT.
- **NONE**: every criterion is a readback of deployed state or a CI run; super opens the cited runs and closes the bead.

## Reading the TUI

### Activity Dots (Overlay Panel)

The overlay (toggle with Alt+s) is your dashboard.

| Dot | Meaning |
|-----|---------|
| Green (filled) | Agent is actively processing (PTY bytes flowing) |
| Gray (hollow) | Idle at prompt, no work assigned |
| Yellow (filled) | Idle but has work waiting (idle-with-backlog) |
| Red (filled) | Dead process (crashed or killed) |
| Blue (filled) | Suspended (auto-suspend feature) |

A gray dot with a bead ID showing means the agent finished work but didn't clear their bead display. Nudge them or check the bead status directly.

### Pane Ribbons

Each pane has a bottom ribbon showing:
- **Pane number and name**: "1 super", "2 eng1"
- **Status tag**: `[dead]` (process died), `[susp]` (suspended), `[+N]` (scrolled back N lines)
- **Bead ID**: Shows after the name when the agent has claimed a bead

### Toast Notifications

Toasts appear in the bottom-right and auto-dismiss after 10 seconds.

- **Green**: Agent completed a bead (detected `ready_for_qa` transition)
- **Blue**: Agent claimed a new bead
- **Yellow**: Agent stalled (no output for 10+ minutes with bead assigned)
- **Red**: Agent stuck in error loop (3+ consecutive tool failures)
- **Gray**: Agent idle with ready beads in backlog (work is available but nobody's doing it)

### The Event Log

Press backtick, type `log` or `events`. This shows the last 60 minutes of detected events in a scrollable list. Useful when you step away and come back: "what happened while I was gone?"

## Key Commands Quick Reference

These are the commands you'll use most. The full list is in the README.

| What you want | How |
|---------------|-----|
| Open command bar | Backtick (`` ` ``) |
| Switch between panes | Alt+Left / Alt+Right, or click |
| See all agents at once | Alt+s (overlay toggle) |
| Full-screen one pane | Alt+1 or `focus <name>` |
| Back to grid | Alt+2, Alt+3, or `grid` |
| Zoom focused pane | Alt+z |
| See what everyone is doing | `patrol` in command bar |
| Process table (memory, PIDs) | `top` or `ps` |
| Restart a stuck agent | `restart` or `r` |
| Check event history | `log` or `events` |
| Quit | Alt+q or `quit` (requires confirmation) |

### The Top Modal

`top` (or `ps`) opens a process table. Press `p` to pin/unpin agents (pinned agents are never auto-suspended). Press `r` to restart, `k` to kill, `h` to hide/show. Arrow keys to navigate.

## Troubleshooting

### Agent Not Responding

1. Check the overlay. Is the dot green (active) or gray (idle)?
2. If gray: the agent is at a prompt. Send a message: `initech send <agent> "status?"`.
3. If green but no visible progress: it might be in a long tool execution. Wait 2-3 minutes.
4. If still stuck: `initech peek <agent>` to see the last output. Look for error messages or permission prompts.
5. Last resort: restart via backtick -> `restart` (when focused on that pane) or `initech restart <agent>` from another terminal.

### Agent Crashed (Red Dot / [dead])

The process exited. Common causes:
- Claude hit a context limit and exited
- A permission was denied and Claude stopped
- Out of memory (check `top` for RSS values)

To recover: restart from the `top` modal (select agent, press `r`) or via command bar (`restart`). Claude's `--continue` flag will resume from the last session.

### TUI Crash

If the TUI itself crashes:
1. Check `.initech/crash.log` for panic stack traces
2. Check `.initech/stderr.log` for native crash output
3. Check `.initech/initech.log` for application-level errors

If there's a stale socket: `rm .initech/initech.sock` and relaunch.

### A Secondary Window Looks Frozen (Multi-Monitor)

If a secondary window (`initech --window N`) hangs — the terminal stops
repainting but the process is still alive — its monitor keeps showing agent
output frozen at the moment it wedged, while still *looking* live. This is the
one display state that can briefly misrepresent liveness, so it is worth
knowing the bound.

**A wedged window is detected and folded back within ~12 seconds** (worst
case; the same fold-back notice as a window that crashed). Its agents
reappear in window 1 automatically — they never stop running, and they do not
stay invisible.

A window that hangs only briefly — a heavy render, a slow moment — is *not*
folded back: transient stalls shorter than about 7 seconds are tolerated, so
a busy-but-healthy window keeps its agents.

If the wedged window later recovers, just rerun `initech --window N`. It
reattaches through the ordinary path and its previous split is restored; there
is no wedge-specific recovery step.

A window that is closed or killed outright is detected immediately (single
render cycle) — the ~12s bound applies only to the alive-but-unresponsive
case, where nothing about the connection signals a problem and the only
evidence is a missed keepalive.

### Agent Lost Context (Post-Compaction)

Claude Code compacts conversation history when approaching context limits. Agents may lose working memory of what they were doing. Signs: agent asks "what should I work on?" when they have an active bead.

Recovery: `initech send <agent> "You were working on <bead-id>. Run bd show <bead-id> and continue from your last DONE comment."` The bead's PLAN and DONE comments provide recovery context.

### Messages Not Arriving

`initech send` returns immediately with OK or an error. If the agent doesn't react:
- The message was delivered but the agent is mid-work and hasn't processed it
- The message appeared as text input but the agent needs Enter pressed (add `--enter` if not default)
- The agent's Claude session is in a state where it can't read terminal input (rare, restart fixes it)

When initech cannot see the message in the agent's input box, it holds back the Enter instead of
guessing, keeps checking, and logs exactly one outcome for that message: `withheld submit DELIVERED`
(the text was found and submitted), `withheld submit SUPERSEDED` (a later send reached the agent
first and this one was reported not delivered), or `withheld submit NOT DELIVERED`. It never presses
Enter while a dialog or option picker is open. A NOT DELIVERED or SUPERSEDED message needs to be resent.

## The Bead Workflow

Beads (`bd`) are the work tracking system. Every unit of work is a bead.

### Lifecycle

```
open -> in_progress -> ready_for_qa -> in_qa -> qa_passed -> closed
```

- **open**: Work is defined but nobody's started
- **in_progress**: An engineer claimed it and is working
- **ready_for_qa**: Engineer finished, pushed code, ready for QA verification
- **in_qa**: QA is actively testing
- **qa_passed**: QA verified, waiting for super to close
- **closed**: Done; super checked the evidence and closed it

### What You Should See on Each Bead

Engineers are required to:
1. **Comment PLAN** before writing code (numbered steps, files, test approach)
2. **Write tests** alongside implementation
3. **Push to git** before marking ready_for_qa
4. **Comment DONE** with its evidence: each new test shown failing before the fix and passing after, proof the change runs from the real entry point, the skip count of every test suite cited, the code-review findings and what happened to each, and exact steps QA can run

If you see a bead marked `ready_for_qa` without a DONE comment, with a DONE that says only "tests pass", or without a pushed commit, send it back. QA can't verify work that isn't documented or isn't in the repo.

### Common bd Commands

```bash
bd ready            # Beads ready for work (no blockers)
bd list --status in_progress   # What's actively being worked on
bd show <id>        # Full bead details with comments
bd close <id>       # Close (super, on evidence it checked)
```

## Tips

### Start Small

Your first session should have 3-4 agents (super, pm, eng1, qa1). Learn the rhythm before scaling to 8+. More agents means more coordination overhead.

### Let Super Drive

Resist the urge to send messages directly to engineers. Work through super. Super tracks who's doing what, prevents dispatch collisions, and applies QA routing. Direct messages to engineers bypass these safeguards.

The exception: when you're debugging a specific agent issue, direct `initech peek` and `initech send` are fine.

### Use Patrol, Not Individual Peeks

`patrol` in the command bar shows all agents' recent output in one scrollable view. It's much faster than peeking agents one by one, especially with 6+ agents.

### Watch for Idle-with-Backlog

A yellow dot in the overlay means an agent is idle but there are ready beads in the backlog. This is a dispatch opportunity. Tell super to check `bd ready` and assign work.

### Pin Critical Agents

If you're using auto-suspend (`--auto-suspend`), pin agents that should never be suspended: `pin super` in the command bar. Super and any agent mid-work on a P1 should be pinned.

### The 15-Minute Rule

If an agent hasn't produced output in 15 minutes with a bead assigned, something is wrong. The stall detection fires a yellow toast, but don't wait for it. A quick `initech peek <agent>` tells you if they're stuck, waiting for input, or just in a long operation.

### Landing the Plane

At the end of a session, tell super: "landing the plane." Super will instruct all agents to commit, push, and update their bead status. Wait for confirmations before shutting down. This prevents lost work from agents that were mid-task.

### After a Crash

If initech crashes mid-session, agents' Claude sessions survive (they're independent processes, though they'll lose their terminal). Relaunch `initech` and agents reconnect via `--continue`.
