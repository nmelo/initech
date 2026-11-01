# Operator Guide

You know Claude Code. You've never used initech. This guide gets you from zero to running a productive multi-agent session.

The README covers installation, configuration, command reference, and architecture. This guide covers the workflow: how to think about running a team of agents, what to do when things go wrong, and the patterns that make sessions productive.

## Your First Session

### Before You Launch

If you haven't set up a project yet: `mkdir myproject && cd myproject && initech init`. The init wizard walks you through project name, repo URL, and an interactive role selector. Start with the Standard preset (super, pm, eng1, eng2, qa1, qa2, shipper). You can add or remove agents later.

Re-running `initech init` in an existing project is safe for your files: it never deletes a role's existing `src/` directory. If a clone fails where `src/` already exists but is not a git checkout, init reports the failure, says the directory was left untouched, and leaves it as it is.

### The Files Your Agents Read

`initech init` writes one root `CLAUDE.md` at the project root and one `CLAUDE.md` per agent in its directory. Every agent reads both.

- **The root `CLAUDE.md` is the shared rulebook.** It holds what applies to every agent: the project documents, the roles, how work moves through beads, escalation, which changes are sensitive, the docs line every commit carries, evidence, QA pairs, who files and who closes beads, messaging, budget, the shared-machine rules and the learning protocol. A rule that applies to everyone is written here once.
- **`fleet/` and `skills/` hold what the rules point at.** `fleet/lessons.md` holds the incident behind each rule, `fleet/reference.md` the commands and the operator's inbox guide (`initech post`), and `skills/` one skill per repeated procedure, kept by super. `initech init` creates them once and never overwrites them, even with `--force`.
- **An agent's own `CLAUDE.md` carries only what that role does differently:** its identity, failure modes, decision authority and workflow. It opens by pointing at the root.

Each agent's `CLAUDE.md` starts with a two-line generated header naming the template it came from and a hash of its contents. `initech add-agent <role> --regen` rewrites that agent's `CLAUDE.md` from the current template, for example after upgrading initech. It refuses a file you edited after it was generated, or one with no header, and says why; add `--force` to overwrite it anyway.

Nothing changes in an existing project on its own. Its files change only when you rerun `initech init --force` (which rewrites the root and every agent's `CLAUDE.md`) or `initech add-agent <role> --regen`. Your `docs/`, `AGENTS.md` and `.gitignore` are never rewritten: init creates them when they are missing, with or without `--force`, and otherwise leaves them as they are.

### What Happens on Launch

Run `initech` (no subcommand) from your project root. The TUI starts all agents simultaneously. Each agent gets its own PTY running Claude Code with the flags from your `initech.yaml`.

Each agent pane gets `INITECH_AGENT` and `BEADS_ACTOR` set to its role, and `INITECH_SOCKET` pointing at this session, so `bd` records the agent as the actor of every bead change. The role wins over a `BEADS_ACTOR` already set in the shell that launched initech; that shell's own environment is never changed.

On a project's first launch, a welcome box lists the main shortcuts for 10 seconds, after the consent question if that is due. The key or paste that dismisses it still counts: a letter or a paste reaches the focused pane and a shortcut does its action. Escape only dismisses it.

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
| `attention.hooks` | Installs the notification hook that tells initech an agent is waiting on you. Written by the consent question (`true` for yes, `false` for no): at init, or for a project without an answer, on screen the first time window 1 starts, where any key other than `y` or `n` asks again next start. A `--window` viewer never asks. Once answered it is never asked again. | You answered the init question; change it by hand to switch. |
| `claude_command` | Replaces the `claude` command for every agent, e.g. `[env, SOME_VAR=1, claude]` to set an environment variable. | Every agent needs the same wrapper or environment. |
| `claude_args` | Flags for every Claude agent, e.g. `[--continue, --dangerously-skip-permissions]` to resume the last session and skip permission prompts. | Agents should pick up where they left off and work unattended. |
| `resource.auto_suspend`, `resource.pressure_threshold` | Parks idle agents when memory use passes the threshold (a percentage). Off when absent. | The machine runs more agents than its memory comfortably holds. |
| `running_pane_tint` | Background tint behind panes that are working. The default is `#0c120e`; trained fleets use `#1e2022`, a lighter grey. `none` turns it off. | The default tint is too faint on your display. |
| `typing_hold_quiet_s`, `typing_hold_max_s` | How long a message to a pane you are typing in waits for you to pause, and the longest it waits. See [Typing hold](#typing-hold). | The default 2 s pause or 15 s cap does not fit how you type. |
| `role_overrides.<role>.claude_args` | Flags for one role. They REPLACE `claude_args` for that role, so repeat the shared flags, then add e.g. `--model <model>` or `--settings <path>` for a per-agent settings file. | One role needs a different model or its own settings. |
| `telemetry` | `false` turns off the daily anonymous usage ping. On when absent. See [Usage data](#usage-data). | You do not want initech to send it. |

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

### Dispatching with assign

`initech assign <agent> <bead>... [-m "..."]` claims the beads in bd, shows them on the agent's ribbon and sends the agent one message. It looks at each bead first:

- **Already with that agent** (assigned to it, in the status a dispatch would write: in progress, or in QA for a QA agent): a fold-in. bd is left as it is, the message is delivered, and the ribbon gains the bead if it lacks it while keeping the beads it already shows.
- **In progress or in QA with another agent**: refused with one line naming who has it. `--move` reassigns it: the bead leaves the other agent's ribbon, and that agent is told in one line who moved it and to whom.
- **Closed**: refused; follow-on work gets its own bead.
- **Anything else**: dispatched, replacing the agent's ribbon with the beads of the call.

`initech deliver` fails the same way when its hand-off report does not reach the `--to` agent: the bead moves, but the command exits non-zero and prints the `initech send` line to tell the agent by hand (see Pane Ribbons for the summary line). With `--to`, it always reports to that agent or fails this way, on every step, and a `--to` that names no agent is refused before bd is touched. Without `--to`, the report goes to `super` and only when the acting agent changes: a hand-off, a verdict, a failure or the final close; `--report` forces one on any other step.

An agent that is stopped or whose process has exited gets nothing: no bead is claimed, and assign exits non-zero with the same reason `initech send` gives and the command that brings the agent back.

Several beads in one call each get their own rule; a refused bead is listed as failed, and the command fails only when every bead does. The event log records each dispatch, saying "fold-in" or "moved from" where it applies. Against an initech that does not report ribbons, a fold-in leaves the ribbon as it was and a move leaves the previous holder's ribbon alone, each with a warning; the message is still delivered.

**Work with no bead.** `initech assign <agent> -m "..."` with no bead id dispatches a small job that has no ticket, so it is still visible. Nothing is written to bd. The message is delivered exactly as `initech send` delivers it, the -m text as given. Once it is accepted, the agent's ribbon shows `no bead` and the message's first words (cut at 80 characters, like a bead title), `initech status` shows the same words in the Bead column ahead of any bead bd still has assigned to that agent, and the event log records the dispatch with no bead id. The marker is shown in every window, wakes with a suspended agent, and ends the way a bead pointer does: the next bead registered for the agent (`initech assign` or `initech bead`) replaces it, and `initech bead --clear` removes it; it does not survive a TUI restart. `initech assign <agent>` with neither a bead nor -m is a usage error. An initech too old to know work with no bead leaves the ribbon as it was and assign warns; the message is still delivered.

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

Keys always go to the highlighted pane, in every layout: whatever you type, paste or press Enter on lands in the pane drawn as focused, never in one you cannot see. A grid smaller than the fleet shows the first panes, and the focus moves to one of them; Option+arrows step through the panes on screen. When live mode moves the focused agent off screen, the focus moves to a pane that is drawn. Scrolling over a pane focuses it and highlights it at once.

### Activity Dots (Overlay Panel)

The overlay (toggle with Alt+s, or `panel` in the command bar) is your dashboard. It comes back after a relaunch shown or hidden as you left it; a second window always starts with it shown.

It lists this window's agents that are doing something or need you, in two columns: working on the left (running, background), needing attention on the right (waiting, pending input, a dialog or banner on screen, dead, and idle with a stuck bead, which reads `idle · stuck`, stuck by the pane header's rule), each column in the Agents panel's band order. Idle and suspended agents are not listed, with no row and no count; they are found in the Agents panel, which lists every agent. In a second window, which has no Agents panel, an agent hidden in that window stays listed whatever its state, with its hidden mark, because its dot is how it is unhidden there; the main window's overlay lists only active agents. A dead agent stays listed until it is restarted or removed. When none of the window's agents is listed, the panel shows the one line `all idle` where the columns would be. A row adds `· N held` for messages waiting and `· dnd <age>` while the agent is in do-not-disturb, after the state word. An agent changes column the moment its state changes. Clicking an agent's dot hides or shows it. A dot that has just changed agent, because a row moved, ignores clicks for one second, so a click never lands on an agent that slid under the cursor; click again. Under the columns, below a rule, the overlay has one row per effort in flight (an effort with a bead in progress, ready for QA or in QA, across the whole fleet), in effort id order, each reading as that effort's pane header does, such as `ini-wra 2/4 · 1/h · 0 stuck`, with ` (stale)` when bd has stopped answering, and blocked beads counted apart from stuck as in the header; with nothing in flight there are no rows and no rule. The rows come from the same once-a-minute refresh as the headers, a click on them does nothing, and a screen too short for them cuts them first.

| Dot | Meaning |
|-----|---------|
| Green (filled) | Agent is actively processing (PTY bytes flowing) |
| Gray (hollow) | Idle at prompt, no work assigned; or background: quiet, with a background shell, monitor or agent running (the row says `background` and the job's age) |
| Yellow (filled) | Idle but has work waiting (idle-with-backlog) |
| Red (filled) | Dead process (crashed or killed) |
| Blue (filled) | Suspended (auto-suspend feature) |

A gray dot with a bead ID showing means the agent finished work but didn't clear their bead display. Nudge them or check the bead status directly.

### Pane Ribbons

Each pane has a bottom ribbon showing:
- **Pane number and name**: "1 super", "2 eng1"
- **Status tag**: `[dead]` (process died), `[susp]` (suspended), `[+N]` (scrolled back N lines)
- **Bead ID**: Shows after the name when the agent has claimed a bead
- **No bead**: `no bead` and the first words of the message, for work dispatched with `initech assign <agent> -m "..."` and no bead

bd is the record of who holds a bead, and each agent's bead comes from it: every 10 seconds initech reads the in_progress beads from bd (one call, bounded at 5 seconds) and shows on each agent the bead assigned to its role. When a role holds two, the one last named with `initech bead` wins, else the most recently updated. `initech bead <id>` is a manual override. It takes only bead ids (a prefix, a dash and a key, with an optional dotted child number, such as `ini-abc` or `ini-abc.2`), checked against bd when bd answers. A pasted bd row (`id`, a tab, its title) is cut to the id, and stderr says what was kept. An id bd does not know, an empty argument and anything else are refused with one line and exit 1, and nothing changes; when bd does not answer, the id is set with a note. Only the id is stored and sent, to the ribbon, `initech status` and the session-bead record; bd's title shows on the ribbon for display only. The agent shows that bead until `initech bead --clear`, which returns it at once to the bead bd says, the last-named one when the role holds two, from bd's last answer with no wait for the next read. While bd is not answering, `--clear` returns to the last bead bd gave, still marked. When bd does not answer, each agent keeps the last bead bd gave and the ribbon, `initech status` and `patrol` mark it `bead? bd unreachable`; nothing is guessed. An agent that has been working (running, or quiet with a background job) for fifteen minutes with no bead, from bd or `initech bead`, reads `no bead` in its ribbon, in `initech status` and in `patrol`; idle and suspended time do not count, and the marker clears the moment a bead appears. It is display only: nothing is typed into the agent. A project without bd shows only what `initech bead` sets. A second window shows what window 1 reads. A claim (`bd update --assignee` plus `initech bead`, or `initech assign`) puts the bead on the agent's ribbon. `initech deliver` puts the ribbon where its bd write leaves the bead, never on the `--to` agent, who only gets the report: the bead comes off the deliverer's ribbon unless bd still has the deliverer holding it in progress, ready for QA or in QA (other beads on that ribbon stay); a QA FAIL that returns the bead to its implementer adds it to that agent's ribbon when the agent is a pane in this initech; a hand-off and a PASS set no other ribbon, so a QA agent's ribbon shows the bead once it claims it. deliver's last line ends with what happened to the ribbon: `ribbon cleared`, `ribbon set on <agent>`, `ribbon unchanged`, or `ribbon NOT updated: <reason>`; bd is written either way and the ribbon outcome does not change the exit code. The report to the `--to` agent is named on the same line, just before the ribbon: when it did not arrive (no such pane, the agent stopped or still starting, no TUI running), the line reads `report NOT sent to <agent>: <reason>` and deliver exits non-zero, printing the `initech send` command that tells the agent by hand. The bead has still moved. The TUI log records one line for every ribbon write it receives, with the agent, the bead ids or `clear`, and the result.

initech also keeps a record of which Claude session was on which agent and bead, and when, in `~/.claude-accounts/records/<rig>-YYYY-MM.jsonl`, where the cost ledger (cla) reads it, so the ledger charges each session's spend to the bead it worked. `<rig>` is the project name from initech.yaml, else the project folder's name. There is one file a month, kept 90 days after its month; initech removes only its own rig's files. The folder is created (mode 0700) when it is missing. When it cannot be written, initech says so once at start and in its log, keeps running, writes nothing, and looks again each minute; once it can write, the intervals it held arrive with their original times. Two rigs with the same name on one machine do not share a file: `<rig>.owner` beside the files names the project folder that owns the name, and a second folder writes `<rig>-<six hex>-YYYY-MM.jsonl` instead. Window 1 writes the record, or the daemon running the agents; read it, never edit it. A dev build (any version that is not a plain release number; `2.49.0` and `v2.49.0` are release numbers, while an empty or hand-set version and a prerelease such as `2.50.0-rc1` are not) never writes under `~/.claude-accounts`: it writes the same files under `<rig>/.initech/records/`, whatever `HOME` is, and says so once on stderr at start: `dev build: session records go to <path>, not the account ledger`. The same test decides whether a command that changes a live session (send, restart, stop and the like) is refused unless it carries `--allow-dev-delivery`; the refusal names the version it read. Files left under `.initech/session-beads-*` from earlier versions are no longer written or read. The format is in the spec under Session-bead record.

In `initech status`, a suspended agent shows Alive `no` and Status `suspended`; an agent that was stopped or whose process has exited shows Alive `no` and Status `stopped`. The header counts them the same way: `Session: demo (7 agents, 2 suspended, 1 stopped)`, where the first number is every agent the table lists and a count of zero is left out.

An agent whose pane is quiet but whose Claude Code footer shows a background shell, monitor or background agent is in the state `background`: it is not producing output, but it is not idle either. `initech status`, `initech patrol` and the Agents panel say so with the work and its age: `background, 1 shell 4m`, `background, 2 shells, 1 monitor 30s`. The count and the age are the running jobs': initech reads the pane's process tree, counts the background shells running under its Claude Code, and gives the oldest one's age from its process start. Claude Code keeps a finished shell listed in its footer until it is read; a listed shell that is no longer running does not count, and an agent whose listed shells have all finished reads idle. Where the process tree cannot be read (on Windows), and for background agents, which run inside Claude Code and have no process, the age is how long the footer has shown the work and says so: `background, 1 background agent, seen for 4m` (`seen 4m` in the Agents panel). background never overrides a stronger state: an agent producing output with a shell is `running, 1 shell 4m`, one with a dialog open is waiting, and a suspended or stopped agent reads as such. initech reads the footer once a second, so the state shows within a second of the work appearing and turns back to idle when the footer shows none; while a dialog covers the footer the last reading stands. Every window shows the same state. `initech patrol --active` keeps a background agent even when its screen is quiet. A background agent counts as working wherever initech decides working versus idle: it keeps its place in the live focus and live list splits, ranks in live mode as an agent producing output does, turns the top view's title green and marks its pane's ribbon with a dim `[bg 4m]` tag giving the job's age, which clears when the job ends. It does not get the running tint, which marks output, cannot be suspended by hand (suspending would stop its job) and is never auto-suspended. Its pane shows new output at most four times a second while the output keeps coming, and its last output as soon as it stops. Agents hosted by a cross-machine daemon do not report background work.

An agent whose pane is quiet but whose input box holds text that was not sent (a message whose Enter was not taken, a half-typed line, a paste waiting) is in the state `pending input`: the pane ribbon carries `[pending input]`, the overlay row a yellow dot, and `initech status` and the `initech patrol` header print `pending input`. Only the input box's own first row counts, so a dialog's or a picker's cursor row is not text, and nothing is pending while a dialog is open. It outranks background, and neither the memory policy nor `initech suspend` parks it, since the text would be lost. When `initech send` delivers a message and the box then holds other text (your own draft, put back after the send), it says so: `delivered to <agent>; the box still holds other text: "<line>"`. A pane another window or machine runs does not show pending.

An agent whose context is at or above 85% is flagged to compact soon: `initech status` prints `ctx 91%, compact soon` (the figure as read) after the state, the `initech patrol` header prints the same after the state, and the pane ribbon carries a yellow `[ctx 91%, compact soon]` tag after the state tags, before the bead. The figure is the one `status --json` reports as `context_percent`, read once a second: the status line's, or where the status line does not show it (a narrow pane, a dialog over it), the session transcript's. The threshold applies to that figure whichever source gave it. A figure read from the session transcript uses initech's table of context windows, 1,000,000 tokens for every model it has measured, so on an account whose window for a model is smaller the figure reads low, and the flag with it; where the status line shows its own figure, the status line's stands, and when the two differ by more than one point the TUI log says so. Below 85, or when no figure is known, there is no flag, and it goes at the next reading below 85, as after a compaction. The flag is not shown for an agent that is not running. A ribbon too narrow for the tag cuts it at the pane's edge and keeps the agent's name. A pane that another window runs carries no tag in this window; `initech status` and `initech patrol` still flag it. `status --json` has no flag of its own: `context_percent` carries the number.

An agent stopped on a Claude Code dialog reads `waiting` in `initech status`, the overlay and the needs-input list. A permission prompt or a question is announced by Claude Code itself and chimes. Claude Code's "Teach auto mode about your environment?" dialog is not announced, so initech recognises it on screen within a second and lists it without a chime, with that question as its preview; the row clears when the dialog closes. Its rm safety prompt ("Dangerous rm operation on statically-unresolvable target", which Claude Code blocks itself after a two-minute countdown) is announced only about ten seconds in; initech recognises it on screen within a second, so the agent reads waiting, not running, from the start, with "Dangerous rm operation: do you want to proceed?" as its preview; it chimes once Claude Code announces it, and the row clears when it is answered or blocks itself. Only dialogs initech knows by their words are recognised this way.

The top edge of every pane is a plain line in every state; nothing moves along it. A pane with a bead carries its effort's pace on that line, such as `ini-wra 2/4 · 1/h · 0 stuck`: the effort's beads closed of all of them, closed per hour over the last six hours, and the stuck ones (unchanged in bd an hour, owner idle, waiting or pending), then `· 1 blocked` when beads wait on something named: a bead in `blocked` or `waiting_on_nelson` counts as blocked and never as stuck. A `deferred` bead is left out of every count. It is recomputed once a minute from the bead poll, reads `(stale)` after it when bd stops answering, and is not there when the pane has no bead. `initech progress` gives the full read. A running pane is marked by its green square, its title and its tint, a background pane by its ribbon tag, a suspended or dead one by its ribbon tag.

`initech status` reads bd for its Bead column: each agent bd names as assignee of a bead in progress, ready for QA or in QA shows that bead and its status (a bead at QA passed has no holder to show). When bd names more than one bead for an agent, the one its ribbon shows comes first, the bead last named with `initech bead`, else the most recently updated, so the column and the ribbon lead with the same bead. When an agent's ribbon shows different beads from bd, the column shows bd's answer followed by `ribbon: <ids>` (`ribbon: -` for an empty ribbon), so a stale ribbon is visible; when they agree nothing is added. With `INITECH_SOCKET` pointing at another project's initech, the `Session:` header names the project behind that socket, not the project of the folder you ran it from; an initech too old to say which project it serves leaves the folder's name. The Bead column then shows the beads of the project behind the socket, read from that project's own folder, never yours; an initech too old to say where its project lives shows `(bd: unknown project)` there instead, as does a project whose folder is not on this machine. An `INITECH_SOCKET` that names your own project's socket, as every agent's does, counts as no override.


#### status --json

`initech status --json` prints a JSON array to stdout, one object per agent, and nothing else; errors and notes go to stderr. It exits 0 when the TUI answered. Every object has every key below. **A value initech did not read is `null`, never a guess.**

| Key | Value |
|-----|-------|
| `role` | The agent's name. |
| `host` | The peer it runs on; empty for a local agent. |
| `state` | `running`, `idle`, `pending` (text sits unsent in the input box), `background` (quiet, with background work under way; see `background_age_sec`), `waiting` (a dialog is waiting on the operator), `suspended`, or `stopped` (stopped or its process exited); `null` for any other state, such as an agent on another machine. |
| `alive`, `visible` | Whether its process is running, and whether its pane is shown. |
| `beads` | The bead ids bd names the agent as holding, read the way the table's Bead column reads them and in its order, the bead on the agent's ribbon first, so the two agree (`[]` when none). When the project has beads turned off, or bd gives no answer, the ids on the agent's ribbon stand in; `null` then from an initech too old to report the ribbon. |
| `bead_note` | The words of work dispatched with no bead, or empty. |
| `context_percent` | The context used: from the status line under the input box when it shows it, otherwise from the agent's Claude session transcript. |
| `background` | `shells`, `monitors`, `tasks`: the background shells and monitors Claude Code counts under the input box, and the background agents it lists there. `0` when Claude shows its mode line with none, including a mode line cut off in a narrow pane while it shows its `(shift+tab to cycle)` hint, which Claude draws only when there is no background work; `null` when no mode line is on screen, or a count is cut off at the pane's edge. |
| `banner` | The dialog open in the pane: `none`, `permission_prompt`, `trust_prompt`, `picker` (a question with options), `login`, or `unknown` (a dialog is open that initech does not recognise, which includes the usage-limit banner). |
| `banner_age_sec` | How long the dialog has held the pane's messages; `null` when none is holding them. |
| `queued_count` | Messages waiting for that dialog to close, or held for do-not-disturb. |
| `background_age_sec` | The age of the oldest running background job, from its process start, when `state` is `background`; where the process tree is not read, how long the footer has shown the work; `null` otherwise. |
| `account` | `user` and `model`: from the status line when it shows them, otherwise `user` from `cla`'s active account and `model` from the session transcript. |
| `dnd` | `true` while the agent is in do-not-disturb, `false` when it is not; `null` for an agent another machine runs, or from an initech too old to report it. |
| `dnd_age_sec` | How long the agent has been in do-not-disturb; `null` unless `dnd` is `true`. |

`context_percent` and `account` come from the status line the `cla statusline` command draws, read by the shape of each segment, not its place: the context is the first segment that is a whole percent, including when Claude cuts the line right after it (`10% │…`, `10%…`) but not inside it (`1…`), the account name the segment just before it, and the model the nearest segment before that which names a model (Haiku, Sonnet, Opus, Fable or Claude and a version). Any other segment, such as the `⎇ branch` the line starts with in a git folder, is skipped. What the status line does not show (Claude cuts it below about 40 columns, so a pane in a full grid shows little of it, and a dialog hides it) comes from sources that do not depend on the pane's width: the context from the last reply in the agent's Claude session transcript, the tokens that reply was sent (input, cache creation and cache read) as a whole percent of the model's context window, rounded down, which is how the status line computes it; the model from that reply's model, named as the status line names it but without a suffix such as `(1M context)`, so a narrow pane and a wide pane of one session report the same context and account name, and the same model up to that suffix; and the account name from `cla`'s record of the active account (`~/.claude-accounts/state.json`, its `active` field only). The status line stays first for each field it shows. The account name is the host's active `cla` profile, the name the status line prints, not the account a long-running agent's process signed in with: an agent started before a switch reads the new name from both sources. The transcript gives no context or model before the session's first reply, after a compaction until the next reply, after `/clear` until the new session's first reply, and for a model initech has not measured (it knows Opus 5.5, Sonnet 5.5, Fable 5.1 and Haiku 5.5, each with a 1,000,000-token window); a subagent's replies are not the agent's context. Where neither source answers, the field is `null`, never a guess. When the status line and the transcript both give a context and differ by more than one point, the status line's figure is reported and the TUI log says so once per agent and model, naming both figures. A pane with any other status line, or none, and no transcript answer reports them as `null`. When the running initech is too old to read its panes (started before status --json existed, as after an upgrade without a restart), every screen field reads `null` and `initech status --json` also prints `older initech running; restart it for status detail` on stderr; the JSON and the exit code are unchanged. Everything read from the screen is `null` for an agent that is not running, for a pane that is not Claude Code, while a dialog covers the rows under the input box, and from an initech too old to read them.

### Toast Notifications

Toasts appear in the bottom-right and auto-dismiss after 10 seconds.

- **Green**: Agent completed a bead (detected `ready_for_qa` transition)
- **Blue**: Agent claimed a new bead
- **Yellow**: Agent stalled (no output for 10+ minutes with bead assigned)
- **Red**: Agent stuck in error loop (3+ consecutive tool failures)
- **Gray**: Agent idle with ready beads in backlog (work is available but nobody's doing it)

### The Event Log

Press backtick, type `log` or `events`. This shows the last 60 minutes of detected events in a scrollable list. Useful when you step away and come back: "what happened while I was gone?"

### Live Focus Split

Option+Shift+F holds the focused agent on the left and shows the working or waiting agents on the right: running, quiet with a background shell, monitor or task, or waiting on you at a permission prompt. An agent whose prompt is answered leaves once its ten-second hold is over, like one that stops working. A pinned agent stays on the right whatever it is doing, except while it is suspended; it comes back when it wakes, still pinned.

Both splits survive a restart of initech. Restarted from the live split, initech opens in the live split with the same agent on the left and the agents working now on the right; restarted from the plain Focus split (Option+f), it opens in the Focus split with the same agent on the left and every other agent on the right. Either way the second press goes back to the layout you were in before the split. If that agent is no longer in `initech.yaml`, or is hidden, initech opens in the layout underneath instead and logs a warning naming the agent. Leaving a split before the restart opens the layout underneath, as before. A second window starts without a split. In both splits the mouse wheel over a pane on the right does nothing, so the sides never swap; over the agent on the left it scrolls that agent. A click on a right-side pane still moves it to the left.

### Live List Split

Option+Shift+L (or `list` in the command bar) holds the focused agent on the left and shows the working or waiting agents on the right in a grid whose shape no number of agents changes: four boxes across and three rows until you set another. The same agents qualify as in the live focus split: working agents, agents waiting at a permission prompt and pinned agents, a pinned agent leaving while it is suspended, with the same one-change-per-second rule and ten-second hold. They fill the boxes in the Agents panel's order, left to right and top to bottom, so the grid reads like a list. When an agent stops, the agents after it move up a box to close the gap; when one starts, it takes its place in that order. A cell with no agent stays empty.

When more agents are working than there are boxes, the agents already in a box keep it and the others wait; when a box frees, the first waiting agent in the panel's order takes it. A pinned agent always gets a box.

Inside the split, `grid CxR` sets the shape: C columns and R rows, each from 1 to 6. Any other value leaves the grid as it is and the command bar says why. Pressing Option+Shift+L again goes back to the layout you were in before. From the Focus split or the live focus split it keeps the agent on the left, and pressing it again goes back to the layout from before the first split. Option+Shift+F inside it switches to the live focus split on the same agent; Option+f inside it does nothing. Like the other splits it survives a restart with the same agent on the left and the same shape. The mouse wheel over a box on the right does nothing; over the agent on the left it scrolls that agent.

### Second Windows

Setting `window_listen` in `initech.yaml` (for example `window_listen: "127.0.0.1:7500"`) lets window 1 serve more windows; `initech --window 2` opens one. Windows authenticate to window 1 with a token. When `initech.yaml` sets `token`, every window uses it. Otherwise window 1 generates a new token each time it starts and writes it to `.initech/window-token`, readable only by you (mode 0600); a window on the same machine reads it from there, and window 1 removes it when it exits. Window 1 refuses any connection without the right token. Never share or copy that file: whoever holds the token can read every agent's screen and type into it. Start window 1 first; a second window started before it shows "window 1 is not running here" and connects on its own once window 1 is up, including after a restart of window 1.

Every window shows the same agents. An agent that window 1 restarts, starts, wakes or adds keeps working in every window: it appears in each one and streams there, and keys and sends from any window reach the new process, whichever way it was restarted (`initech restart`, `initech start`, the command bar or the `top` overlay). An agent added or restarted while a second window is attached appears there under its own name, and what you type into its pane reaches that agent and no other. If window 1's announcement of a new agent never arrives, the second window shows no pane for it rather than a pane wired to another agent. An agent window 1 removes leaves every window, and a send to it from any window fails with "not found". The agent screen a window or a connected machine can ask for is capped at 300 rows and 1000 columns; a larger request is clamped to that and logged once.

### Cross-Machine Daemon

`initech serve` runs a headless daemon that another machine's initech connects to. Its token is full access to every agent's terminal on that machine. The first run creates the token, stores it in `.initech/token` (mode 0600) and prints it once, in a snippet to paste into the other machine's `initech.yaml`. Later starts print where the token is stored, never its value, so a daemon run under a supervisor does not write the key into its log on every restart. `initech serve --show-token` prints the token again. `--token` still sets one for a run but is deprecated, and initech never echoes its value. Never use it in a shared shell, because other users can read command lines and shell history.

Once connected, address an agent on the other machine as `host:agent`. `initech send host:agent "..."` and `initech interrupt host:agent` (Escape, or Ctrl+C with `--hard`) act on that machine's agent, never on a local agent of the same name, and report what that machine answered: a send it rejects, or one that fails on the way, prints "not delivered" with the reason and exits non-zero; an interrupt it cannot carry out prints "not interrupted" with the reason, and is never typed as text instead. `initech suspend` and `initech resume` work only on this machine's agents: `host:agent` is refused with "not supported on a remote agent", so run them on that machine. A `host` that names this machine works as a plain agent name. Typing into a remote pane works for every agent on that machine, including agents the daemon started for a pushed configuration or a reload; before v2.30.1 those agents only showed output.

An agent the daemon hosts holds and reports messages the same way a local one does. A message that arrives while a dialog is open waits, and goes once the dialog closes: `initech send host:agent` answers `deferred to host:agent`, as a local send does, and exits 0. A send to a remote agent that is stopped, has exited or is still starting is refused with the same reason a local one gets. When a message is held, released, or cannot be delivered (the process exited, a dialog stayed open for 10 minutes, or a withheld Enter ran out of time), the connected initech shows the same toast and event-log line as for a local agent, named `host:agent`, and the daemon's log records it too. A report raised while no initech is connected is in the daemon's log only. A remote agent has no inbox entries, since it cannot post to the inbox. The agent's running or idle state follows its output: `initech status` on that machine reports it, and a stop gives a busy agent the same grace to finish as a local one gets.

### Upgrading with Rigs Running

`brew upgrade initech` removes `/opt/homebrew/bin/initech` before it builds the new version, so while it runs every agent's `initech send`, `peek` and `deliver` fails, and a hung brew leaves them failing. On a Mac with rigs running, the fleet upgrades through `scripts/release/brew_upgrade_bounded.sh`, not bare `brew upgrade`: brew only downloads and checks the tarball, the script builds the new version beside the old one and runs it to prove it, then renames the link over, so the link never goes away. A second run at the same time refuses. A brew call past its time bound is stopped, and the link stays on the old version. `--prune` also removes old versions from the Cellar, keeping the installed one, the previous one and any a running initech started from. On any other Mac, `brew upgrade initech` is the way to upgrade. A running rig stays on the version it started with until it restarts.

## Key Commands Quick Reference

These are the commands you'll use most. The full list is in the README.

| What you want | How |
|---------------|-----|
| Open command bar | Backtick (`` ` ``) |
| Switch between panes | Alt+Left / Alt+Right, or click |
| See all agents at once | Alt+s (overlay toggle) |
| Full-screen one pane | Alt+1 or `focus <name>` |
| Back to grid | Alt+2, Alt+3, or `grid` |
| Working agents in a fixed grid, panel order | Shift+Alt+L or `list` (then `grid CxR` for its shape) |
| Zoom focused pane | Alt+z |
| Ctrl+z in a pane | Ignored: nothing reaches the agent (zoom is Alt+z) |
| See what everyone is doing | `patrol` in command bar |
| Process table (memory, PIDs) | `top` or `ps` |
| Restart a stuck agent | `restart` or `r` |
| Check event history | `log` or `events` |
| Quit | Alt+q or `quit` (requires confirmation) |

### The Top Modal

`top` (or `ps`) opens a process table. Press `p` to pin/unpin agents (pinned agents are never auto-suspended). Press `r` to restart, `k` to kill, `h` to hide/show. Arrow keys to navigate.

## The iPhone and iPad App

### Blockers

Blockers is one list per running rig, in two kinds.

**Waiting on you** is each rig's beads in `waiting_on_nelson`, read with `bd list` once a minute. An item is the bead's title and drops off when the bead leaves that status. Agents' reply footers are not read for it. Tapping an item opens its assignee's pane.

**Stuck** lists each pane that cannot act, with the reason:
- a dialog open: a permission prompt, trust prompt, picker, login screen or an unknown dialog;
- waiting at a prompt, or text left unsent in its input box;
- idle while it owns a bead (in progress or later, not closed) that has not moved for an hour or more;
- a QA pane idle while its rig has a bead in `ready_for_qa`, or one in `in_qa` that is unassigned or assigned to it.

A rig with nothing in either kind shows as one quiet row.

The list reloads on every feed event that can change it: a pane state, a waiting item, a stuck pane, the service's `blockers` event, a reset, or a reply in a `super` pane. Other transcript lines do not reload it. The Blockers and Progress buttons on the Panes screen open each view as a sheet; tapping an item opens its pane and closes the sheet. With no rig running it reads "No rig is running."

### Progress

Progress shows the report of `initech progress --json` for each effort in flight in every running rig, as the service last ran it (every five minutes); the view reads it on open, every five minutes after, and on a pull to refresh. Each effort shows the counts the command prints: closed of total and the number closed in the window, beads an engineer worked and beads that were writing or review only, the engineers on it, each bead past open with its stage, owner, `AC n/m` and commits landed, the critical path, and each bead quiet an hour or more with what it waits on. It shows no time at all: no finish time, no estimate, no actual, and no clock time of a close. With no effort in flight it reads "No effort is in flight."

### Pairing and Unlocking

Install the app from TestFlight on the iPhone or iPad. On first open it asks for the pairing code: run `initech app-serve token` on the Mac and scan the QR code it shows, or paste the pairing link it prints (`initech://pair?host=<address>&port=<port>&token=<token>&fp=<fingerprint>`). The app keeps the pairing in the keychain of that device only. It talks to the service over HTTPS and accepts only the certificate whose SHA-256 matches `fp`; any other certificate is refused before a request, and so the token, is sent. A new token issued on the Mac makes the service refuse the old one; on a refused token or certificate the app says which and asks to pair again. A Mac that does not answer reads as that, and home shows no line about pins until the list has loaded.

The app locks behind Face ID (or the device passcode) on every open and every return from the background; a device with neither set stays locked.

Every error the app shows is also written to the device log under the subsystem `co.nmelo.initech`, one category per screen, with the error's domain and code; read it in Console with the device connected, or for a simulator with `xcrun simctl spawn <device> log show --predicate 'subsystem == "co.nmelo.initech"' --last 5m`. An entry never carries the error's words, a pane name, the token or a URL with it, a pane's text, a message body or a bead title. About, at the bottom of home, shows the version, the build number and the commit the build was made from, as `0.1.0 (1) 1a2b3c4`; a build whose commit could not be read shows `unknown` in its place.

### Home

Home lists the panes you pinned, in your order, each with the name you gave it, its state (working, idle, waiting at a prompt, pending input, or its banner), its context percentage, the bead it holds and the newest item it has waiting on you, until that item leaves the Blockers list. It updates live from the service's feed. A pinned pane whose rig stopped or that no longer exists stays in the list, greyed. With nothing pinned, home shows one line pointing to Rigs.

**Rigs** lists every running rig with the panes you have not pinned; the pin button pins one. Swipe a pinned row to rename or unpin it; Edit reorders. The pinned list is stored on the Mac, so every paired device shows the same list. On iPad, the pinned list is a sidebar beside the open pane.

### A Pane

A pane opens as its conversation, read from the pane's Claude Code transcript: the agent's replies as text with the footer lines (`▶`) at the bottom of each reply, each tool run as one line that expands on tap, your messages on the right. It opens on the newest entry and stays where you are reading when new entries arrive; Show older messages at the top reads the next older page, until the transcript's start. A pane with nothing in its transcript yet says so.

What you type in the box goes into the pane through the send path, as `initech send` would type it, and the message shows the verdict in the send path's own words: delivered, deferred because a dialog is open (it goes when the dialog closes), sent but not confirmed, or not delivered (with why).

Below the box, the key strip sends Enter, Esc, Ctrl-C, `y` or `n` into the pane as a single keystroke (`initech keystroke`), and shows the result under it. Delivered means the key reached the pane, not that it did anything: on Claude's numbered permission prompt, Enter says Yes and Esc says No, and `y`, `n` and Ctrl-C do nothing there; `y` and `n` are for Codex approvals and plain yes/no questions. At Claude's folder trust prompt Enter, Esc and Ctrl-C are refused, and the result says why: any of them could end the agent, so that prompt is answered in the pane itself. While the pane is at a dialog or banner, or waiting or pending, its last screen lines show above the input box, so you can read the prompt you are answering, and the strip says which keys act.

### The App Service

The app talks to `initech app-serve`, a service on the Mac. It listens on one address only: this Mac's Tailscale address by default (`--listen tailnet`, the release setting; the address on the interface that also holds Tailscale's `fd7a:115c:a1e0::/48` address, since a `100.64.0.0/10` address alone can be carrier NAT on the LAN), or, with `--listen local-network`, the Mac's private LAN address, for the build phase while Tailscale is stopped. It never listens on all interfaces or on loopback, and with no Tailscale address it does not start; its port is 7392 unless `--port` says otherwise. It finds rigs by scanning each `--projects` folder (by default the folder holding the rig it was started from) for `<folder>/*/initech.yaml`; a rig is running when its initech answers on its socket.

It serves HTTPS only (TLS 1.3) with its own self-signed certificate, `~/.config/initech/app-serve/cert.pem` (key mode 0600), made by the first `initech app-serve token` and kept across re-pairing; the app trusts the service only when its certificate's SHA-256 equals the pin in the pairing link. Every request carries the device token as `Authorization: Bearer <token>`; a request without it or with another one is refused with 401 and no body. `initech app-serve token` issues the token and prints it once, inside the pairing link `initech://pair?host=<address>&port=<port>&token=<token>&fp=<pin>`; only the token's SHA-256 is stored, in `~/.config/initech/app-serve/token.sha256` (mode 0600). The command also draws the link as a QR code for the phone. Issuing a new token takes effect at once, with no restart, and revokes the device paired before; before any token is issued the service runs and refuses every request.

macOS guards `~/Desktop` from programs launchd starts: run that way, the service cannot read rigs under it until it is allowed to. A folder's read has five seconds to answer; one that has not is skipped and reported once, and read again when its read returns, while rigs in the other folders are served. A folder that is refused or missing is reported once with its error. Until the first scan finishes, `/v1/rigs`, `/v1/status` and `/v1/conversation` answer 503 with the error `the rig scan has not finished`, and a poll still going after 30 seconds is logged once. Started from a terminal, the service has the terminal's access. Release binaries for macOS are signed with the operator's Developer ID, under the fixed identifier `com.neblihq.initech` of team K7Z8YKVA8G, and notarised, so macOS asks once whether initech may read the Desktop folder and keeps the answer across upgrades; a build from source is not signed that way, and macOS asks again for each one.

`GET /v1/rigs` lists every rig the scan found and whether it is running. `GET /v1/status` (and `GET /v1/status/<rig>`) gives, for every running rig, the objects `initech status --json` prints on it, each with `screen`: the pane's last 20 screen lines while its banner is set and not `none` or it is waiting or pending, else null. A rig's status is read at most once every five seconds; the service makes no bd or git call on a timer.

`GET /v1/feed` is the change feed, as server-sent events (`id:`, `event:`, `data:` with a JSON object). Every 30 seconds, and no faster, the service reads each running rig's pane list from its initech (no bd or git) and sends a `pane_state` event for each pane whose state or banner changed (`rig`, `pane`, `from`, `to`, `banner`) and a `stuck` event once when a pane comes to a dialog or banner or starts waiting or pending (`rig`, `pane`, `reason`); the first read of a rig only sets what later reads compare against. A subscriber that reconnects with the last id it saw (the `Last-Event-ID` header, or `?since=<id>`) gets every event after it from the last ten minutes; one whose id is older than that gets a `reset` event first and reloads; one with no id starts from now. An idle stream carries a comment line every 15 seconds. A stream ends when its token stops being the current one, within those 15 seconds, so a revoked device stops receiving. Twice a second the service looks at each pane's transcript (a file stat, no bd or git) and sends a `transcript` event (`rig`, `pane`, `message`, in the shape below) for each new message, so a new entry reaches the feed within a second; a pane first seen starts at the end of its transcript.

`GET /v1/conversation/<rig>/<pane>` is a pane's conversation, read from the newest Claude Code session transcript of that pane (the file its initech names in the pane list; for an initech too old to name one, the newest under that pane's folder in `~/.claude/projects`). It answers one page, `{"session", "messages", "start_reached", "before", "skipped_lines"}`, oldest first: the newest page, or with `?before=<n>` the page before that one, with `?limit=<n>` messages (50 by default, at most 200); paging back with each page's `before` reaches the transcript start, where `start_reached` is true. `before` is a JSON integer on every page, a cursor the app passes back as given; it is 0 on the page that reaches the start, where the app stops paging, since `?before=0` asks for the newest page again. A message is `{"id", "at", "kind", "text"}`: kind `agent` is a reply, with `footer` holding the `▶` lines of its last paragraph; `nelson` is what the operator typed or pasted into the pane, while the agent was mid-turn too; `message` is a message another agent sent, with `from`; `tool` is one tool run, whose `text` is its one line (the tool and what it ran on) and whose `tool.detail` is its input and result, cut to 4 KB. These four are every kind the service sends; the app shows an entry of a kind it does not know as plain text and skips one it cannot read, so the rest of the page still shows. Thinking, system reminders, background task notices, slash-command echoes, the compaction summary, subagents' lines and Claude Code's own records are left out; a malformed line is left out and counted in `skipped_lines`; a pane with no transcript is an empty page at the start.

`GET /v1/blockers` is the list the Blockers view shows, per running rig: `{"rig", "waiting": [{"bead", "pane", "text", "recommendation"}], "stuck": [{"pane", "reason"}], "quiet"}`. A waiting entry is a bead in `waiting_on_nelson`: `bead` is its id, `pane` its assignee, `text` its title, and `recommendation` is empty. The service reads `bd list --status waiting_on_nelson --json` in each running rig once a minute: one bd call per rig, and its only timed bd call besides the five-minute progress run. A stuck entry comes from the last pane read. Each read that changes the list sends a `blockers` event carrying it. A bead newly in the status sends a `waiting_item` event, `{"rig", "bead", "title", "pane", "text"}`; the first read after the service starts only sets what later reads compare against.

`GET /v1/progress` is the latest progress run, a list of the objects `initech progress --json` prints, one per running rig and effort with a bead in progress (`in_progress`, `ready_for_qa` or `in_qa`), `[]` before the first run; it never starts a read. The service reads it every five minutes, the first time when it starts, over six hours of commits and closes: these are its only bd and git calls on a timer. `GET /v1/progress/<rig>` is that rig's part of the run; with `?prefix=<p>` it is one report, read on request at most once every five seconds when the run did not include it.

`PUT /v1/devices` with `{"token": "<hex APNs device token>"}` registers a device for pushes and answers `{"devices": <count>}`; the app sends it on every launch, and a token already registered changes nothing. `DELETE /v1/devices/<token>` removes one; an unknown token is 404. The set is stored in `~/.config/initech/app-serve/devices.json` (mode 0600) as `{"devices": [{"token", "added"}]}`, at most 10 devices. A token that is not hex or is over 200 characters, an eleventh device, or a body with any other field is refused with 400 `{"verdict": "refused", "words"}`, and the stored set stands.

**Pushes.** The service sends Apple pushes to every registered device: one when a bead enters `waiting_on_nelson` in a running rig, at the next once-a-minute bd read, and one when a pane gets stuck (a dialog or banner, waiting, or pending). A bead pushes again only after it leaves the status and comes back; a pane only after it has recovered and gets stuck again; a recovery pushes nothing. A push carries the title `initech: <rig>` and, as its body, the bead id or the pane name, nothing else: never a bead's title, a message or any transcript text. A restart pushes nothing for what is already there: the first read of a rig after the service starts is its baseline, so a bead already waiting and a pane already stuck are not pushed until they leave and come back. Quiet beads are listed in the app and never pushed. The settings are the `apns` block of whichever rig's `initech.yaml` holds one (`key_path`, `key_id`, `team_id`, `topic`, and `environment`, which is `production` unless it says `sandbox`); the key at `key_path` must be mode 0600. With no block, two rigs holding different blocks, or a key that cannot be used, the service says so in one line and looks again after every poll, so a rig it could not read at first (a folder waiting on a macOS permission) turns pushes on as soon as a poll reads it; it then says once where pushes go. Nothing that happened while pushes were off is pushed, and once on, pushes keep their settings until the next start. Apple rejecting a device (410, or 400 `BadDeviceToken`) unregisters it, logged once. Any other failure is logged once an hour per device and never retried. With no device registered nothing is sent, and that is logged once an hour.

`GET /v1/pins` is the pinned list, `[{"rig", "pane", "name", "order"}]` in `order`, `[]` when nothing is pinned; `PUT /v1/pins` with the whole list replaces it. The list is stored on the Mac, in `~/.config/initech/app-serve/pins.json` (mode 0600), so every paired device shows the same one. A list with a pin missing its rig or pane, the same pane twice, a name over 80 characters, more than 200 pins or a field the service does not know is refused with 400, and the stored list stands.

`POST /v1/keys/<rig>/<pane>` with `{"key": "enter"}` (or `esc`, `ctrl-c`, `y`, `n`) presses that key in the pane exactly as `initech keystroke` does, as you, the operator: it goes into an open dialog (on Claude's numbered prompt `enter` is Yes and `esc` is No), and do-not-disturb does not hold it (you are the operator); a suspended pane refuses it, nothing typed. At Claude's folder trust prompt `enter`, `esc` and `ctrl-c` are refused as `initech keystroke` refuses them, nothing typed, because `enter` would choose the highlighted `No, exit`, `esc` would exit Claude and a second `ctrl-c` would too; the answer is 200 with the verdict `not_delivered` and the words `not delivered to <pane>: the folder trust prompt is up and Enter would choose No, exit; open the pane to answer it`. `y` and `n` are delivered and do nothing there. The answer is `{"verdict": ..., "words": ...}`: the verdict `initech keystroke` reached (`delivered` or `not_delivered`) and the line it prints for the same call, word for word. A request the service turns away before anything is typed answers `{"verdict": "refused", "words": ...}`: 400 for a key outside the five (with `initech keystroke`'s own refusal line) or a body that is not a JSON object, 404 for a rig not running or a pane it does not have.

`POST /v1/send/<rig>/<pane>` with `{"text": "..."}` types the message into the pane exactly as `initech send` does, as you, and presses Enter once (`"enter": false` leaves it in the box, as `--no-enter`). The answer carries the send's verdict and line, word for word: `delivered`; `deferred` when a dialog is open (the message waits and goes in when it closes); `unchecked` or `unconfirmed` when initech could not tell; `not_delivered`, which includes a slash command to a Claude Code pane that would paste rather than run (longer than 800 characters, or more than 3 lines), refused with the words `initech send` prints. Do-not-disturb does not hold it: you are the operator. An empty text is refused with 400 and nothing is sent.

`initech app-serve install` runs the service under launchd, which is its watchdog: it starts the service at login and ten seconds after it dies. It writes `~/Library/LaunchAgents/com.initech.app-serve.plist` and starts it with `launchctl bootstrap` in your login session; stdout and stderr go to `~/Library/Logs/initech/app-serve.log`, which is not rotated. The log is readable by its owner alone: install creates it 0600 in a 0700 folder before launchd starts the job, and the service sets it back to 0600 when it starts if it finds it wider, saying so in the log once. The job runs a copy of the binary at one fixed path, `~/Library/Application Support/initech/app-serve/initech` (0755, in a 0700 folder), kept current by the service itself. Install makes it from the file the path you ran resolves to (found through PATH when you ran it by name), checks it is byte-identical and, for a signed build, that it verifies and carries the same identifier and team, and keeps the path you ran in the job as `--source`. A source it cannot read or a copy that does not check out refuses the install with the reason, and nothing else is written. macOS keys the Desktop grant to a program by its real path, and a Homebrew build's real path names its version, so the copy is what lets the grant be given once: the release that first moves the job to the copy asks once more, and no upgrade after it asks again. When an upgrade moves `--source` to a new build, the service notices within a minute, copies and checks the new build over its copy (the path holds the old build or the new, never neither), says so in the log in one line naming the old and new build, lets requests in flight finish for up to five seconds and exits, and launchd starts the new build ten seconds later, and pushes nothing for a bead already waiting or a pane already stuck. A copy that fails is logged once, the service keeps running the build it has and tries again each minute, and `--notify` hears of it at most once an hour. A copy found behind `--source` at start (an upgrade while the service was not running) is replaced at the first minute the same way. Started by its own path rather than a link (a dev build), it says once at start that an upgrade does not restart it. It carries the `--listen`, `--port`, `--projects` and `--notify` you give, and your shell's `PATH`, since launchd's own has no `/opt/homebrew/bin` and the service runs `bd`; a tool that moves later needs `install` again. launchd starts jobs in `/`, so `--projects` defaults, at install time, to the folder holding the rig you are in, and is written out in full; with no rig found and no `--projects`, with `PATH` unset, or with no token issued yet (`run initech app-serve token first`), nothing is written. `--listen tailnet` is not refused while Tailscale is stopped: launchd keeps retrying and the log says why. Running it again with the same settings prints `already installed` and changes nothing; new settings replace the job. If `launchctl` fails, the job file is removed again and the error named. `initech app-serve uninstall` stops the service and removes the job and its copy, keeping the token, the certificate and the log; with nothing installed it says so.

`install` warns when `--projects`, the binary or a `PATH` entry is under `~/Desktop`, `~/Documents` or `~/Downloads`, the folders macOS guards for launchd jobs, and goes on. Install the unit from a signed release: the first time the service reads a rig under one of those folders, macOS shows its dialog once, and once it is answered Yes, later releases keep the access. A rig folder that does not answer, is refused or is missing is also sent to `--notify` as `initech app-serve cannot read <folder>: <why>`; the macOS reason is added only for a folder under one of those three.

`--notify <command>` runs after an unclean exit (a crash, or `kill -9`) with one argument, `initech app-serve restarted after an unclean exit`, bounded at ten seconds; if it fails, the log says so once and the service still starts. It runs at most once an hour for each kind of message, so a crash loop does not repeat the restart notice and that notice never silences a folder notice; the log records every restart. It runs with the `PATH` written at install. A plain `kill`, a logout and `uninstall` are clean exits and notify nothing, though launchd starts a killed service again. The marker of a run in progress is `~/.config/initech/app-serve/running`, written once the service is listening; neither it, the notify lines nor the log holds the token. A dev build under launchd has no `--allow-dev-delivery`, so its send and key routes refuse.

`initech app-serve token` prints the pairing link once as text and as a QR code of the same link in half-block characters, black on white whatever the terminal's colours, at most 57 columns wide: widen the pane if it wraps.

## Configuration

Settings live in `initech.yaml` at the project root and are read at startup.

### Typing hold

When an agent sends a message to a pane you are typing in, the message waits until you pause,
so your draft is not stashed and restored mid-word. While a message waits, the pane's ribbon
shows a ✉ mark. Messages to the same pane go one after another, in the order they arrived.
The sender's `initech send` returns once the message is actually delivered.

| Key | Default | Meaning |
|-----|---------|---------|
| `typing_hold_quiet_s` | `2` | Seconds without a keystroke in that pane before a waiting message goes. `0` turns the hold off: messages go at once. |
| `typing_hold_max_s` | `15` | The longest a message waits; then it goes even if you are still typing. Must be at least `typing_hold_quiet_s`. |

```yaml
typing_hold_quiet_s: 3
typing_hold_max_s: 20
```

A value that is negative, not a whole number, or a max below the quiet time is ignored: that
key uses its default (a max below the quiet time becomes the quiet time if the default is still
shorter) and the log names the key and the value. Typing in any window counts,
mouse input does not. Your own pastes, from any window, are never held.

For a send to an agent on another machine, the sending machine sizes its wait from its own
`typing_hold_max_s`, so give both machines the same value.

### Do-not-disturb

An agent in the middle of a step can run `initech dnd on` in its own pane. Until its turn
ends (the pane stops running), or it runs `initech dnd off`, messages other agents send it wait in the pane's message queue instead of
being typed into its input box, and each sender sees
`held: <agent> is in do-not-disturb (<age>, <N> messages ahead)` with exit 0: how long the
agent has been in it (45s, 12m, 1h) and how many messages wait ahead of this one. `initech
status` shows `dnd <age>` after the agent's state, and so does the `initech patrol` header;
`initech deliver` and `initech assign` print the held line and succeed. On screen, the pane's ribbon carries `dnd` after the agent's name, after the held-mail mark when there is one, and the Agents overlay row reads `· dnd <age>` after the state word and the held count (`idle · 2 held · dnd 5m`); the agent stays in the column its state puts it in. Both go at the next draw once do-not-disturb ends. `dnd off` delivers them in the order they arrived, one at a time, through the
normal path. `initech dnd on --for 30m` holds through the end of the turn until the timer runs
out instead; a timer is at most an hour. A message that cannot be delivered then is reported to you once, with how long
it waited. At 20 waiting messages a new one is refused with
`not delivered to <agent>: do-not-disturb queue is full` and exit 1; the oldest is kept.

Your own typing and pastes are never held, and neither is a message you send from outside any pane or an inbox reply: those go straight in. super can break through with `initech send --urgent`, which goes straight in and leaves `urgent from super broke do-not-disturb on <agent>` in the log and the event log; `--urgent` from any other agent is refused. Do-not-disturb trusts the sender's word, which is `INITECH_AGENT`: an agent that clears it reads as you. It is a courtesy between agents, not a security wall. To take a pane out yourself, run
`initech dnd --agent <agent> off`. Nothing is saved: restarting initech, restarting the agent or waking a suspended one clears it. An initech
started before this command existed answers `older initech running; restart it for
do-not-disturb` (exit 2).

### Usage data

initech sends one anonymous usage ping a day, so the maintainer knows how many installs there are and which versions they run. Window 1 sends it in the background after its first frame, at most once every 24 hours per install. A `--window` viewer, `initech serve` and the subcommands never send it. It carries exactly four things: a random install id, the initech version, the OS and the CPU architecture. It also asks PostHog not to derive a location from the request. It never carries names, paths, hostnames, roles, agent counts, project content or anything typed.

The install id is made once and kept in `~/.config/initech/install_id` (`%LOCALAPPDATA%\initech\install_id` on Windows), with the time of the last successful ping in `last_ping` next to it. A ping that fails is dropped without a message, never delays startup or exit, and is tried again at the next start.

To turn it off, set `telemetry: false` in `initech.yaml`, or set `DO_NOT_TRACK=1` in the environment; `DO_NOT_TRACK` wins over the file. Either one means no id file is made and nothing is sent.

## Troubleshooting

### Agent Not Responding

1. Check the overlay. Is the dot green (active) or gray (idle)?
2. If gray: the agent is at a prompt. Send a message: `initech send <agent> "status?"`.
3. If green but no visible progress: it might be in a long tool execution. Wait 2-3 minutes.
4. If still stuck: `initech peek <agent>` to see the last output. Look for error messages or permission prompts. `initech peek <agent> -n 50` returns the agent's last 50 lines whatever its pane's height: when the screen holds fewer, the lines above it come from the pane's scrollback, in order. When it returns fewer lines than asked, it says why on stderr, with stdout and the exit code unchanged: a pane showing a full-screen program (an editor, a pager) is read as its screen only (`pane is <rows> rows; peek reads the screen only`), and a pane whose whole output is shorter says so (`peek returned <k> of <n> lines: all the pane keeps (up to 10000 lines of history)`).
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

### Compacting an Agent

`initech compact eng1 "keep the bead id and the failing test"` types `/compact keep the bead id and the failing test` into eng1 through the same path as `initech send`, then watches eng1's context figure (the `context_percent` of `status --json`) for two minutes. When it drops you see `compacted: 82% -> 14%` and exit 0. With nothing after the name it types a plain `/compact`. A keep text that would make the command longer than 800 characters, or more than 3 lines, is refused before anything is typed with `not compacted: a slash command of <n> characters would paste, not run; Claude takes a command of at most 800` (or `... of <n> lines ..., at most 3 lines`), exit 1, and nothing is watched: Claude would take the command as pasted text and never compact. `sent; no drop seen in 2m` (exit 2) means the message went in but the figure did not fall, often because the agent was mid-turn: Claude compacts when the turn ends, so check it with `initech status` later. `sent; context figure unknown here` (exit 2) means neither the pane's status line nor its session transcript gave a figure. In a narrow pane the figure after a compaction comes from the agent's next reply, so the drop shows once the agent has replied; until then the watch keeps waiting. An agent can compact itself: run from its own pane it returns at once with `queued for your pane; compaction runs when this turn ends`. A send that is held or refused prints the send's own line, and nothing is watched. Codex, OpenCode and generic agents have no `/compact` and are refused with nothing typed (exit 1), and so is an agent on another machine, whose type this window does not know: run `initech compact` on that machine.

### Answering a Stuck Dialog with One Key

`initech keystroke eng1 y` presses one key in eng1's pane: `enter`, `esc`, `ctrl-c`, `y` or `n`, and nothing else is typed. It is how you free a pane stuck at a permission prompt or a yes/no question without opening it. On Claude's numbered permission prompt, `enter` says Yes (the highlighted option) and `esc` says No; `y` and `n` do nothing there, as when typed, and are for Codex approvals and plain y/n questions. `ctrl-c` stops a running command; at a permission prompt it is delivered and leaves the prompt up, since only `enter` and `esc` act there. An open dialog does not hold your key the way it holds a message; the key goes into whatever is on screen, so peek first. The exception is Claude's folder trust prompt (`Quick safety check: Is this a project you created or one you trust?`), which Claude shows first in a folder it does not know. Its highlighted row is `No, exit`, so `enter` would end the agent; `esc` ends it whichever row is highlighted; and a second `ctrl-c` ends it (the first only changes the footer to `Press Ctrl-C again to exit`). All three are refused there, nothing typed: `the folder trust prompt is up and Enter would choose No, exit; open the pane to answer it` (with the cursor moved to the other row, `Enter would choose Yes, I trust this folder`, since trusting a folder is decided in the pane, where its path is readable), `the folder trust prompt is up and Esc would exit Claude; open the pane to answer it`, and `the folder trust prompt is up and Ctrl-C twice would exit Claude; open the pane to answer it`. `y` and `n` are delivered and do nothing there. A key sent at the instant the pane's screen cannot be read is not checked for the prompt and goes in. Only your keys answer a dialog: an agent's key to a pane with a dialog open, super's included, is refused with `a dialog is open; only the operator may answer it`. `key y delivered to eng1` means the key reached the pane. `not delivered to eng1: ...` (exit 1) means nothing was typed: an unknown key name, an agent that is not running, a suspended agent (a key never wakes one; it stays parked), the folder trust prompt, or another agent's key to an agent in do-not-disturb (a key is never queued for later). Your own keys go through do-not-disturb, and so do super's with `--urgent`.

### Agent Lost Context (Post-Compaction)

Claude Code compacts conversation history when approaching context limits. Agents may lose working memory of what they were doing. Signs: agent asks "what should I work on?" when they have an active bead.

Recovery: `initech send <agent> "You were working on <bead-id>. Run bd show <bead-id> and continue from your last DONE comment."` The bead's PLAN and DONE comments provide recovery context.

### Messages Not Arriving

`initech send` waits until initech has seen what became of the message, about two seconds at most
for an agent at its prompt, then prints one line and exits with its status:

- `delivered to <agent>` (exit 0): the message was seen in the agent's input box and the box cleared.
  In a short pane, where Claude Code scrolls a long message inside the box and the box's bottom edge
  falls below the pane, the end of the message still in view counts as seen.
  A `--no-enter` send submits nothing and also reads delivered, as does a send that woke a suspended agent.
- `not delivered to <agent>: <reason>` (exit 1): the message is still in the box after the Enter and
  the one resend, or could not be typed at all.
- `not delivered to <agent>: a slash command of <n> characters would paste, not run; Claude takes a
  command of at most 800` or `not delivered to <agent>: a slash command of <n> lines would paste, not
  run; Claude takes a command of at most 3 lines` (exit 1): Claude Code collapses a message longer
  than 800 characters, or of more than 3 lines, into a `[Pasted text #N]` block, and a pasted block is
  sent as text, never run as a command. So a message to a Claude Code agent that begins with `/` and
  is over either limit is refused before anything is typed, before a dialog or do-not-disturb could
  hold it. A command of up to 3 lines and 800 characters is typed and runs. Plain text of any length
  is typed as before (Claude reads a pasted block), and Codex, OpenCode and generic agents are not
  checked.
- `could not confirm delivery to <agent>: <reason>; peek the pane` (exit 2): initech could not tell
  either way (the reasons are below). When initech held back the Enter, the reason is `submit withheld,
  awaiting composer repaint; the TUI delivers it when the box repaints and reports`, and the outcome
  arrives later as a toast. A running initech older than the command answers with `older initech
  running; restart it for delivery verdicts`; an agent on a machine running an older initech, with
  `older initech on <host>`.
- `sent to <agent>, unconfirmed: no delivery check for this agent type` (exit 0): Codex, OpenCode and
  generic agents have no input box initech can read, so nothing is checked.
- `deferred to <agent> — target has a modal open; will deliver when it closes` (exit 0): see the dialog
  bullet below.

An agent on another machine, named `host:agent` or by a bare name that resolves there, gets the same
answers from that machine. Other commands that type into an agent (`assign`, `deliver`'s report,
`add`, `clear`, `restart`, `start`) treat not delivered as a failure and every other answer as sent;
`assign <agent> -m` with no bead prints the send's line and exits as `initech send` does.

An agent whose process is not running gets
nothing typed: the send fails, exits non-zero and prints why, with the command that brings the agent
back. A stopped agent (after `initech stop`) reads `not delivered — agent not running: agent eng1 is
stopped; start it with initech start eng1`; one whose process ended on its own reads `...agent eng1
has exited; restart it with initech restart eng1`. A suspended agent is not refused: the send wakes it
and is delivered. If the agent doesn't react:
- The message was delivered but the agent is mid-work and hasn't processed it
- The message appeared as text input but the agent needs Enter pressed (add `--enter` if not default)
- The agent's Claude session is in a state where it can't read terminal input (rare, restart fixes it)
- The agent has a dialog open (a permission prompt, including the Write and Edit prompt "Do you want to create w.txt?" / "Do you want to make this edit to w.txt?", or a question). initech holds the message instead of typing into the dialog, says `deferred (modal open)`, and delivers it once the dialog closes. An agent on another machine behaves the same way: the send says `deferred to host:agent`, and its holds, releases and NOT delivered reports appear here as `host:agent`. A dialog counts as open only when it is drawn where Claude draws a live one: below the transcript, in place of the input box. Dialog wording quoted in the transcript above an idle input box does not hold mail. A dialog that has closed (answered, or cancelled with `initech interrupt`), or one announced by the agent but never drawn, stops holding mail once the input box has sat at rest for 5 seconds after the announcement. initech finds the input box at any pane width, a narrow pane's shorter rules included, and with a named session's label on its top rule. A background shell, monitor, task or subagent is not a dialog: a send to an agent running one is delivered as to any working pane. Each change is logged at Info as `dialog latch raised`, `dialog latch corroborated` or `dialog latch cleared`, with what caused it.
- A held message that cannot be delivered (the agent's process exited, the agent was removed, or a dialog stayed open for 10 minutes) is reported as `NOT delivered (...)` in a toast, the event log and the inbox, with how long it waited. Re-send it.
- Restarting an agent (`initech restart <agent>`, or restart from the command bar) carries the messages waiting for it to the new process, which receives them once it is ready; `initech restart` says how many it carried. If the new process cannot start, the messages stay where they were and are reported as stranded.

When initech cannot see the message in the agent's input box, it holds back the Enter instead of
guessing, keeps checking, and logs exactly one outcome for that message: `withheld submit DELIVERED`
(the text was found and submitted), `withheld submit SUPERSEDED` (a later send reached the agent
first and this one was reported not delivered), or `withheld submit NOT DELIVERED`. It never presses
Enter while a dialog or option picker is open. A NOT DELIVERED or SUPERSEDED message needs to be resent.

After pressing Enter, initech counts a message as delivered only when it has seen the message in the
agent's input box, before or after the Enter (its text, or Claude's `[Pasted text #N +M lines]`
placeholder where M is the number of line breaks in the message, a trailing one included, or the bare
`[Pasted text #N]` Claude shows for a message with no line break longer than 800 characters), and then
seen it leave. When Claude scrolls a message taller than its input box inside the box, so only the end
shows, that end counts as the message's text if everything in the box is part of the message and it
holds at least 12 characters besides spaces. The box is read across all its rows, the same way at any
pane width and whether or not its top border carries the session's name. A box that only changed is not a sighting. The log line is `submitted: composer cleared`,
with how many milliseconds after the Enter the box was seen without the message, and what it showed;
because the message is usually seen during the wait before the Enter, that can read under 50, including
0. A box showing Claude's queued-message line or the operator's own restored draft counts as cleared; a
dialog on screen does not. If the message is still there after one second, initech
presses Enter once more; if it is still there after that, it is reported as `NOT delivered (...)`. That
second Enter is never pressed while a dialog is open: initech checks at the moment it would press, and
under a dialog it presses nothing and reports `could not confirm delivery (...)` with the reason
`dialog open at the retry`. If
initech could not recognise the message in the box at all (no input box on screen, a placeholder with a
different count, or other text only), it presses nothing and reports
`could not confirm delivery (...)` in a toast, the event log and the inbox, with the reason. When the
reason is that the message was never seen in the box, it names how many readings initech took and the
first line of the box in the last reading before the Enter and the last after it (`no box`, `an empty
box`, or the line, cut to 60 characters). Check the agent's pane and re-send if the message is not there.

A reading of the box counts only when the box's own rows, from the `❯` row through its bottom border,
read the same and in the same place before and after it; an answer streaming above the box, the spinner
and the hint rows below it never spoil a reading. The log line for a message initech could not confirm,
`submit outcome UNKNOWN: could not confirm`, counts what those readings saw:

- `samples_before` is the number of readings taken during the wait before the Enter.
- `samples_after` is the number taken after the Enter, a resend's included.
- `torn` is how many changed while being read and were skipped.
- `stable` is how many held still.
- `sighted` is how many of the stable readings showed the message in the box.

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
- **closed**: Done; super checked the evidence and closed it with `bd close`

`initech deliver` moves a bead one step along this chain and stops at `qa_passed`; it never closes a bead.

Three side states sit beside the chain and are not steps: `blocked` (waiting on something named, such as another bead, a person, a release or a machine, with the wait in a comment), `deferred` (parked on purpose) and `waiting_on_nelson` (waiting on a decision, click or answer only you give, with the question in the latest comment). Any status added to bd's `status.custom` after `qa_passed` is a side state too. Set one with `bd update <id> --status blocked` (or `deferred`, `waiting_on_nelson`) and a comment; when the wait ends, move it back with `bd update <id> --status open` or `in_progress`. `initech deliver` on a bead in a side state writes nothing, sends no report and exits 1: `not delivered: <id> is blocked (<the latest comment's first line>); move it back to open or in_progress first`, or for `waiting_on_nelson`, `not delivered: <id> waits on Nelson (<first line>); super moves it when he answers`.

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

### Reading an Effort's Progress

`initech progress [<prefix>] [--hours N] [--json]` shows whether a multi-bead effort is moving and where it is held. `<prefix>` picks the effort by bead id (an epic's children, such as `ini-wra.`); without one it reads the whole rig. `--hours` is the window for commits and closes, six by default. `--json` prints the same report as one JSON object instead of the table: `beads` (the rows, each with `ac_proven` and `ac_items`, null without a criteria section, and `commits`, null when no repo was read), `closed`, `total`, `stuck`, `blocked`, `worked_by_engineers`, `writing_review_only`, `stages`, `closed_recent` (every close in the window; the table lists the last eight), `critical_path`, `engineers`, `quiet` (each with its `wait`) and `notes`; an effort with no bead is a report with `total` 0 and empty lists. A `deferred` bead is in no row, count or list. It is read-only.

It prints, in this order:

- **A table of the beads past open and not closed**, sorted by stage (`no bead in flight` when there is none): the owner; `AC n/m`, the acceptance items proven out of the bullets under the bead's Acceptance Criteria heading (an engineer posts one comment per item, starting `AC <n> proven`; `-` when the bead has no such heading); the commits on main in the window whose subject names the bead as a word (`-` when initech.yaml lists no repos); and the last move, the newest of a status change, a comment or a commit, with who made it. A bead whose title starts with `Step ` is left out of the table.
- **The effort lines:** closed of total, split into beads an engineer worked and beads that were writing or review only, then the stuck and the blocked beads, counted as in the pane header (`; stuck 1, blocked 2`); a bead waiting on Nelson's answer reads `blocked (Nelson)` in the table; beads per stage; the beads closed in the window (the count and the last eight by title); the critical path, up to five open beads with the most beads waiting on them; and the engineers on it.
- **What each quiet bead waits on.** A bead with no move for an hour or more reads `working, unrecorded` when its owner's pane is running or has background work (an engineer working with no comment or commit), `waiting on "<thing>"` when its last comment names what holds it (waiting on, blocked on, held for), and `stalled` otherwise.
- **Notes on anything it could not read.** One note covers agent panes it could not read because no initech is answering; then no quiet bead reads working. Another covers a fetch that ran out of time.

Commits come from the asking agent's own src clone after a fetch of origin/main bounded at ten seconds; a fetch that runs out of time reads the clone as it stands and says so. Outside an agent, or for a repo that clone is not, they come from GitHub (`gh api`). Times are Eastern. When bd does not answer, it prints one line and exits 1; a prefix that matches no bead prints one line. It never prints a finish time, an estimate or an actual.

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

`initech down` stops the session and prints `Session '<name>' stopped.`, naming the project the stopped initech serves, which it asks that initech before stopping it. With `INITECH_SOCKET` pointing at another project's initech, that is the other project, not the folder you ran it from; an initech too old to say which project it serves is named by its socket path instead.

### After a Crash

If initech crashes mid-session, agents' Claude sessions survive (they're independent processes, though they'll lose their terminal). Relaunch `initech` and agents reconnect via `--continue`.
