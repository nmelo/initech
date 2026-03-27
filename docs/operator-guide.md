# Operator Guide

You know Claude Code. You've never used initech. This guide gets you from zero to running a productive multi-agent session.

The README covers installation, configuration, command reference, and architecture. This guide covers the workflow: how to think about running a team of agents, what to do when things go wrong, and the patterns that make sessions productive.

## Your First Session

### Before You Launch

Run `initech doctor` first. It checks prerequisites (claude, git, bd), validates your project config, and reports terminal environment. Fix anything red before proceeding.

If you haven't set up a project yet: `mkdir myproject && cd myproject && initech init`. The init wizard walks you through project name, repo URL, and an interactive role selector. Start with the Standard preset (super, pm, eng1, eng2, qa1, qa2, shipper). You can add or remove agents later.

### What Happens on Launch

Run `initech` (no subcommand) from your project root. The TUI starts all agents simultaneously. Each agent gets its own PTY running Claude Code with the flags from your `initech.yaml`.

You'll see a grid of panes. Each pane is a live Claude Code session. The floating overlay panel in the top-right shows all agents with activity indicators. Within 10-30 seconds, agents initialize and their prompts appear.

### Your First Five Minutes

1. **Find the super pane.** Click it or use Alt+Left/Right to navigate. Super is your coordinator. Every other agent communicates through super.

2. **Tell super what to do.** Type naturally in super's pane. "We're building a REST API for user management. Start with the PRD." Super will dispatch work to PM, who will produce a PRD, which you review, and the cycle begins.

3. **Watch the overlay.** Green dots mean an agent is actively processing. Gray dots mean idle. Yellow dots (idle with work waiting) mean something needs your attention. Toasts appear in the bottom-right when agents complete beads, stall, or hit errors.

4. **Don't micromanage.** Let super coordinate. Your job is to set direction, make decisions when asked, and approve results. Super handles dispatch, monitoring, and QA routing.

## The Dispatch Cycle

This is the core loop that drives all work in initech.

### The Full Cycle

```
You decide what to build
  -> PM grooms it into bead(s) with acceptance criteria
    -> Super dispatches to an engineer
      -> Engineer comments PLAN, implements, writes tests, comments DONE
        -> Super routes to QA (or skips for low-risk changes)
          -> QA verifies, passes or fails
            -> You close the bead (or it bounces back for rework)
```

Every step has a handoff. Messages between agents flow through `initech send`. Every transition is tracked in beads (`bd`). Nothing is invisible.

### What the Operator Does at Each Step

**Setting direction.** Tell super what you want. Be specific about the problem, not the solution. "Users can't paste images into agent panes" is better than "add EventPaste handling to tui.go". Let PM and arch figure out the how.

**Reviewing.** When PM produces a PRD or spec, read it. When an engineer proposes a design, review it. Your approval unblocks implementation. Delays here stall the whole pipeline.

**Deciding.** Super will present decisions as structured options: "Option A does X, option B does Y, I recommend A because Z." Pick one and move on. If you need more info, ask. Don't let decisions sit.

**Closing beads.** Only you close beads. When QA passes and the work looks right, `bd close <id>`. This signals the team that the work is accepted.

### Tiered QA

Not everything needs full QA. Super applies these tiers automatically, but understanding them helps you intervene when needed:

- **Full QA** (visual verification): P1 bugs, UI changes, new features.
- **Light QA** (tests + code review): P2/P3 bugs, internal changes, refactors.
- **Skip QA** (engineer tests are enough): Doc fixes, template changes, dead code removal, one-line constant changes.

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
4. Run `initech doctor` to check for stale socket/PID files

If there's a stale socket: `rm .initech/initech.sock` and relaunch. `initech doctor` will tell you if this is needed.

### Agent Lost Context (Post-Compaction)

Claude Code compacts conversation history when approaching context limits. Agents may lose working memory of what they were doing. Signs: agent asks "what should I work on?" when they have an active bead.

Recovery: `initech send <agent> "You were working on <bead-id>. Run bd show <bead-id> and continue from your last DONE comment."` The bead's PLAN and DONE comments provide recovery context.

### Messages Not Arriving

`initech send` returns immediately with OK or an error. If the agent doesn't react:
- The message was delivered but the agent is mid-work and hasn't processed it
- The message appeared as text input but the agent needs Enter pressed (add `--enter` if not default)
- The agent's Claude session is in a state where it can't read terminal input (rare, restart fixes it)

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
- **qa_passed**: QA verified, waiting for operator to close
- **closed**: Done, accepted by the operator

### What You Should See on Each Bead

Engineers are required to:
1. **Comment PLAN** before writing code (numbered steps, files, test approach)
2. **Write tests** alongside implementation
3. **Push to git** before marking ready_for_qa
4. **Comment DONE** with what changed, what tests were added, and the commit hash

If you see a bead marked `ready_for_qa` without a DONE comment or without a pushed commit, send it back. QA can't verify work that isn't documented or isn't in the repo.

### Common bd Commands

```bash
bd ready            # Beads ready for work (no blockers)
bd list --status in_progress   # What's actively being worked on
bd show <id>        # Full bead details with comments
bd close <id>       # Accept and close (operator only)
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

If initech crashes mid-session, agents' Claude sessions survive (they're independent processes, though they'll lose their terminal). Relaunch `initech` and agents reconnect via `--continue`. Check `initech doctor` first to clear any stale socket files.
