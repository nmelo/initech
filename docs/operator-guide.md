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

`initech deliver` fails the same way when its hand-off report does not reach the `--to` agent: the bead moves, but the command exits non-zero and prints the `initech send` line to tell the agent by hand (see Pane Ribbons for the summary line).

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
- **No bead**: `no bead` and the first words of the message, for work dispatched with `initech assign <agent> -m "..."` and no bead

bd is the record of who holds a bead; the ribbon displays it and is moved by the commands that move bd. A claim (`bd update --assignee` plus `initech bead`, or `initech assign`) puts the bead on the agent's ribbon. `initech deliver` puts the ribbon where its bd write leaves the bead, never on the `--to` agent, who only gets the report: the bead comes off the deliverer's ribbon unless bd still has the deliverer holding it in progress, ready for QA or in QA (other beads on that ribbon stay); a QA FAIL that returns the bead to its implementer adds it to that agent's ribbon when the agent is a pane in this initech; a hand-off and a PASS set no other ribbon, so a QA agent's ribbon shows the bead once it claims it. deliver's last line ends with what happened to the ribbon: `ribbon cleared`, `ribbon set on <agent>`, `ribbon unchanged`, or `ribbon NOT updated: <reason>`; bd is written either way and the ribbon outcome does not change the exit code. The report to the `--to` agent is named on the same line, just before the ribbon: when it did not arrive (no such pane, the agent stopped or still starting, no TUI running), the line reads `report NOT sent to <agent>: <reason>` and deliver exits non-zero, printing the `initech send` command that tells the agent by hand. The bead has still moved. The TUI log records one line for every ribbon write it receives, with the agent, the bead ids or `clear`, and the result.

`initech status` reads bd for its Bead column: each agent bd names as assignee of a bead in progress, ready for QA or in QA shows that bead and its status (a bead at QA passed has no holder to show). When an agent's ribbon shows different beads from bd, the column shows bd's answer followed by `ribbon: <ids>` (`ribbon: -` for an empty ribbon), so a stale ribbon is visible; when they agree nothing is added.

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

Option+Shift+F holds the focused agent on the left and shows the working agents on the right. A pinned agent stays on the right whatever it is doing, except while it is suspended; it comes back when it wakes, still pinned.

Both splits survive a restart of initech. Restarted from the live split, initech opens in the live split with the same agent on the left and the agents working now on the right; restarted from the plain Focus split (Option+f), it opens in the Focus split with the same agent on the left and every other agent on the right. Either way the second press goes back to the layout you were in before the split. If that agent is no longer in `initech.yaml`, or is hidden, initech opens in the layout underneath instead and logs a warning naming the agent. Leaving a split before the restart opens the layout underneath, as before. A second window starts without a split.

### Live List Split

Option+Shift+L (or `list` in the command bar) holds the focused agent on the left and shows the working agents on the right in a grid that never changes shape: four boxes across and three rows by default. The same agents qualify as in the live focus split: working agents and pinned agents, a pinned agent leaving while it is suspended, with the same one-change-per-second rule and ten-second hold. They fill the boxes in the Agents panel's order, left to right and top to bottom, so the grid reads like a list. When an agent stops, the agents after it move up a box to close the gap; when one starts, it takes its place in that order. A box with no agent shows a dim border and the word `waiting`.

When more agents are working than there are boxes, the agents already in a box keep it and the others wait; when a box frees, the first waiting agent in the panel's order takes it. A pinned agent always gets a box.

Inside the split, `grid 4xR` sets the number of rows, R from 1 to 6. Any other value leaves the grid as it is and the command bar says why. Pressing Option+Shift+L again goes back to the layout you were in before. From the Focus split or the live focus split it keeps the agent on the left, and pressing it again goes back to the layout from before the first split. Option+Shift+F inside it switches to the live focus split on the same agent; Option+f inside it does nothing. Like the other splits it survives a restart with the same agent on the left and the same number of rows.

### Second Windows

Setting `window_listen` in `initech.yaml` (for example `window_listen: "127.0.0.1:7500"`) lets window 1 serve more windows; `initech --window 2` opens one. Windows authenticate to window 1 with a token. When `initech.yaml` sets `token`, every window uses it. Otherwise window 1 generates a new token each time it starts and writes it to `.initech/window-token`, readable only by you (mode 0600); a window on the same machine reads it from there, and window 1 removes it when it exits. Window 1 refuses any connection without the right token. Never share or copy that file: whoever holds the token can read every agent's screen and type into it. Start window 1 first; a second window started before it shows "window 1 is not running here" and connects on its own once window 1 is up, including after a restart of window 1.

Every window shows the same agents. An agent that window 1 restarts, starts, wakes or adds keeps working in every window: it appears in each one and streams there, and keys and sends from any window reach the new process, whichever way it was restarted (`initech restart`, `initech start`, the command bar or the `top` overlay). An agent added or restarted while a second window is attached appears there under its own name, and what you type into its pane reaches that agent and no other. If window 1's announcement of a new agent never arrives, the second window shows no pane for it rather than a pane wired to another agent. An agent window 1 removes leaves every window, and a send to it from any window fails with "not found".

### Cross-Machine Daemon

`initech serve` runs a headless daemon that another machine's initech connects to. Its token is full access to every agent's terminal on that machine. The first run creates the token, stores it in `.initech/token` (mode 0600) and prints it once, in a snippet to paste into the other machine's `initech.yaml`. Later starts print where the token is stored, never its value, so a daemon run under a supervisor does not write the key into its log on every restart. `initech serve --show-token` prints the token again. `--token` still sets one for a run but is deprecated, and initech never echoes its value. Never use it in a shared shell, because other users can read command lines and shell history.

Once connected, address an agent on the other machine as `host:agent`. `initech send host:agent "..."` and `initech interrupt host:agent` (Escape, or Ctrl+C with `--hard`) act on that machine's agent, never on a local agent of the same name, and report what that machine answered: a send it rejects, or one that fails on the way, prints "not delivered" with the reason and exits non-zero; an interrupt it cannot carry out prints "not interrupted" with the reason, and is never typed as text instead. `initech suspend` and `initech resume` work only on this machine's agents: `host:agent` is refused with "not supported on a remote agent", so run them on that machine. A `host` that names this machine works as a plain agent name. Typing into a remote pane works for every agent on that machine, including agents the daemon started for a pushed configuration or a reload; before v2.30.1 those agents only showed output.

An agent the daemon hosts holds and reports messages the same way a local one does. A message that arrives while a dialog is open waits, and goes once the dialog closes: `initech send host:agent` answers `deferred to host:agent`, as a local send does, and exits 0. A send to a remote agent that is stopped, has exited or is still starting is refused with the same reason a local one gets. When a message is held, released, or cannot be delivered (the process exited, a dialog stayed open for 10 minutes, or a withheld Enter ran out of time), the connected initech shows the same toast and event-log line as for a local agent, named `host:agent`, and the daemon's log records it too. A report raised while no initech is connected is in the daemon's log only. A remote agent has no inbox entries, since it cannot post to the inbox. The agent's running or idle state follows its output: `initech status` on that machine reports it, and a stop gives a busy agent the same grace to finish as a local one gets.

## Key Commands Quick Reference

These are the commands you'll use most. The full list is in the README.

| What you want | How |
|---------------|-----|
| Open command bar | Backtick (`` ` ``) |
| Switch between panes | Alt+Left / Alt+Right, or click |
| See all agents at once | Alt+s (overlay toggle) |
| Full-screen one pane | Alt+1 or `focus <name>` |
| Back to grid | Alt+2, Alt+3, or `grid` |
| Working agents in a fixed four-wide grid, panel order | Shift+Alt+L or `list` (then `grid 4xR` for rows) |
| Zoom focused pane | Alt+z |
| See what everyone is doing | `patrol` in command bar |
| Process table (memory, PIDs) | `top` or `ps` |
| Restart a stuck agent | `restart` or `r` |
| Check event history | `log` or `events` |
| Quit | Alt+q or `quit` (requires confirmation) |

### The Top Modal

`top` (or `ps`) opens a process table. Press `p` to pin/unpin agents (pinned agents are never auto-suspended). Press `r` to restart, `k` to kill, `h` to hide/show. Arrow keys to navigate.

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

### Usage data

initech sends one anonymous usage ping a day, so the maintainer knows how many installs there are and which versions they run. Window 1 sends it in the background after its first frame, at most once every 24 hours per install. A `--window` viewer, `initech serve` and the subcommands never send it. It carries exactly four things: a random install id, the initech version, the OS and the CPU architecture. It also asks PostHog not to derive a location from the request. It never carries names, paths, hostnames, roles, agent counts, project content or anything typed.

The install id is made once and kept in `~/.config/initech/install_id` (`%LOCALAPPDATA%\initech\install_id` on Windows), with the time of the last successful ping in `last_ping` next to it. A ping that fails is dropped without a message, never delays startup or exit, and is tried again at the next start.

To turn it off, set `telemetry: false` in `initech.yaml`, or set `DO_NOT_TRACK=1` in the environment; `DO_NOT_TRACK` wins over the file. Either one means no id file is made and nothing is sent.

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

`initech send` returns immediately with OK or an error. An agent whose process is not running gets
nothing typed: the send fails, exits non-zero and prints why, with the command that brings the agent
back. A stopped agent (after `initech stop`) reads `not delivered — agent not running: agent eng1 is
stopped; start it with initech start eng1`; one whose process ended on its own reads `...agent eng1
has exited; restart it with initech restart eng1`. A suspended agent is not refused: the send wakes it
and is delivered. If the agent doesn't react:
- The message was delivered but the agent is mid-work and hasn't processed it
- The message appeared as text input but the agent needs Enter pressed (add `--enter` if not default)
- The agent's Claude session is in a state where it can't read terminal input (rare, restart fixes it)
- The agent has a dialog open (a permission prompt, including the Write and Edit prompt "Do you want to create w.txt?" / "Do you want to make this edit to w.txt?", or a question). initech holds the message instead of typing into the dialog, says `deferred (modal open)`, and delivers it once the dialog closes. An agent on another machine behaves the same way: the send says `deferred to host:agent`, and its holds, releases and NOT delivered reports appear here as `host:agent`. A dialog counts as open only when it is drawn where Claude draws a live one: below the transcript, in place of the input box. Dialog wording quoted in the transcript above an idle input box does not hold mail. A dialog announced by the agent but never drawn stops holding mail once the input box has sat idle for 5 seconds after the announcement. Each change is logged at Info as `dialog latch raised`, `dialog latch corroborated` or `dialog latch cleared`, with what caused it.
- A held message that cannot be delivered (the agent's process exited, the agent was removed, or a dialog stayed open for 10 minutes) is reported as `NOT delivered (...)` in a toast, the event log and the inbox, with how long it waited. Re-send it.
- Restarting an agent (`initech restart <agent>`, or restart from the command bar) carries the messages waiting for it to the new process, which receives them once it is ready; `initech restart` says how many it carried. If the new process cannot start, the messages stay where they were and are reported as stranded.

When initech cannot see the message in the agent's input box, it holds back the Enter instead of
guessing, keeps checking, and logs exactly one outcome for that message: `withheld submit DELIVERED`
(the text was found and submitted), `withheld submit SUPERSEDED` (a later send reached the agent
first and this one was reported not delivered), or `withheld submit NOT DELIVERED`. It never presses
Enter while a dialog or option picker is open. A NOT DELIVERED or SUPERSEDED message needs to be resent.

After pressing Enter, initech counts a message as delivered only when it has seen the message in the
agent's input box, before or after the Enter (its text, or Claude's `[Pasted text #N +M lines]`
placeholder where M is the number of line breaks in the message, a trailing one included), and then
seen it leave. A box that only changed is not a sighting. The log line is `submitted: composer cleared`,
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
`could not confirm delivery (...)` in a toast, the event log and the inbox, with the reason. Check the
agent's pane and re-send if the message is not there.

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
