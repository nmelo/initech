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
| `beads.stuck_after` | How long a bead may sit before it reads stuck, as a duration such as `20m`. One hour when absent; a value that is not a duration above zero fails the load. The pane header, the overlay, `initech progress` and the app all count by it; `initech progress --stuck-after <duration>` asks for another value for one run. | The rig's beads move faster or slower than one an hour. |
| `beads.idle_after` | How long the owner's pane of an `in_progress` bead may sit idle before the bead reads idle-owner, as a duration such as `30m`. Twenty minutes when absent; a value that is not a duration above zero fails the load. The pane header, the overlay, `initech progress` (whose JSON the app service carries) and the line to super all read by it. | The rig's agents think longer or shorter than twenty minutes between steps. |
| `product_repo` | A top-level path to a local clone of the rig's product repo, absolute or relative to the rig root: the rig numbers then read that clone's `origin/main` after a fetch bounded at 10 seconds, and the push runs of the GitHub repo its `origin` names, instead of the first repo's. A path that is not a git clone warns once at load and the repo numbers print `?`; under `numbers:` it fails the load with `"product_repo" is not a number's _min or _max`. | The rig's first repo is not its product. |
| `numbers` | Thresholds for the rig's numbers, such as `gate_red_max: 1` or `idle_engineers_min: 2`; each crossing sends super one line. None when absent; an unknown key or a value below zero fails the load naming the key. See [Rig numbers](#rig-numbers). | You want super told when a number leaves its range. |
| `telemetry` | `false` turns off the daily anonymous usage ping. On when absent. See [Usage data](#usage-data). | You do not want initech to send it. |
| `ceiling` | Not a rig key. The machine ceiling is the whole Mac's and lives in its own file; a `ceiling:` key in `initech.yaml` fails the load and names that file. See [The machine ceiling](#the-machine-ceiling). | Never: no rig can set or raise it. |

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

The overlay has two forms with the same content. Floating is a box in the top right corner over the panes. Pinned is a section across the top of the page, with the pane grid shrunk to fit below it: a header line reading `Agents`, with the visible and hidden counts and the other-window line when there are any, then eight rows split into two columns, the listed agents on the left (working first, then the rest, each remote machine under its divider) and the effort rows on the right, with the rig numbers as the section's last line. A column longer than its eight rows scrolls under the mouse wheel; a click in the section reaches no pane, and a click on an agent's dot hides or shows it as in the box. On a terminal under 30 rows each column folds to one line: the count, the first entries that fit and `+N` for the rest (`12 agents: eng1 running, eng2 running +10`), with the rig numbers on a line of their own once they exist; at 30 rows or more the columns come back. Shift+Alt+s flips between the forms in any window, and `agents pin` and `agents float` in the command bar set one in the main window (the only window with a command bar); pinning a hidden overlay shows it, and Alt+s hides and shows either form. The main window keeps the form in `.initech/layout.yaml` as `agents_form: floating` or `agents_form: pinned`, written each time it changes and read at start: a file without the key, or with any other value, starts floating (another value also logs one warning naming it). A second window always starts floating and keeps its choice for its own session only.

It lists this window's agents that are doing something or need you, in two columns: working on the left (running, background), needing attention on the right (waiting, pending input, a dialog or banner on screen, dead, idle with a stuck bead, which reads `idle · stuck <id>`, stuck by the pane header's rule, and idle on an `in_progress` bead for the rig's `beads.idle_after`, which reads `idle 23m · ▶ <id>`), each column in the Agents panel's band order. Idle and suspended agents are not listed, with no row and no count; they are found in the Agents panel, which lists every agent. In a second window, which has no Agents panel, an agent hidden in that window stays listed whatever its state, with its hidden mark, because its dot is how it is unhidden there; the main window's overlay lists only active agents. A dead agent stays listed until it is restarted or removed. When none of the window's agents is listed, the panel shows the one line `all idle` where the columns would be. A row adds `· N held` for messages waiting and `· dnd <age>` while the agent is in do-not-disturb, after the state word. An agent changes column the moment its state changes. Clicking an agent's dot hides or shows it. A dot that has just changed agent, because a row moved, ignores clicks for one second, so a click never lands on an agent that slid under the cursor; click again. Under the columns, below a rule, the overlay has one row per effort in flight (an effort with a bead in progress, ready for QA or in QA, across the whole fleet), in effort id order, each reading as that effort's pane header does after the state icon of the effort's own bead (the bead whose id the row names; none when no such bead exists), with that bead's bd title after its id and a middle dot, such as `▶ ini-wra · Agents overlay rows 2/4 · 1/h · 0 stuck`; the title is cut with `…` to fit the column, never wrapped and never widening the box, and is left out when it has no room or no such bead exists; with ` (stale)` when bd has stopped answering, and blocked and waiting beads counted apart from stuck as in the header. A row follows its waiting count with the first three ids and `+N` for the rest, and an effort with only waiting beads past the value has a row too, because a bead with no holder has no agent row. An agent's own row adds `· waiting <id>` when it is listed for another reason; an idle agent whose only bead is waiting stays unlisted, and its bead is on the effort row. With nothing in flight or waiting there are no rows and no rule. The rows come from the same once-a-minute refresh as the headers, a click on them does nothing, and a screen too short for them cuts them first. In the main window the last of these rows is the rig's numbers (see [Rig numbers](#rig-numbers)), once the first minute's read has finished.

| Dot | Meaning |
|-----|---------|
| Green (filled) | Agent is actively processing (PTY bytes flowing) |
| Gray (hollow) | Idle at prompt, no work assigned; or background: quiet, with a background shell, monitor or agent running (the row says `background` and the job's age) |
| Yellow (filled) | Idle but has work waiting (idle-with-backlog) |
| Red (filled) | Dead process (crashed or killed) |
| Blue (filled) | Suspended (auto-suspend feature) |

A gray dot with a bead ID showing means the agent finished work but didn't clear their bead display. Nudge them or check the bead status directly.

### Rig numbers

The main window reads the rig's numbers once a minute and keeps them: `rig: merges/h 3 · gate 2g/1r med 4m · landing 1 · filed +2/-1 · no move 20m 2 · idle 3`. In order: commits on the first repo's `main` in the last hour; that hour's push CI runs on `main`, green and red, with their median minutes; the landing line, beads at `qa_passed` that none of `main`'s latest 100 commits names; beads filed and closed in the hour; `in_progress` beads with no move for 20 minutes (a status change, a comment or a commit); and engineers (`eng<N>` panes) that are idle. The repo numbers come from `gh`, at most once a minute with each read bounded at 10 seconds; the bead numbers come from the bead poll the window already makes, with no bd call of their own. A number initech could not read prints `?` (when `gh` cannot reach GitHub, logged once); a rig whose first repo is not on GitHub, or that has none, prints its repo numbers as `-`. The overlay carries the line as its last row, and `initech status` prints it after the agent table. `initech status` reads the main window's numbers and never runs `gh` itself, except when the main window has none yet, when it reads them once and says so after the line (`computed now: window 1 has no numbers yet`). `initech status --json` stays one object per agent; `initech status --json --numbers` prints the numbers as one object with `source` (`window 1` or `computed`), `numbers` (each field, null when unknown) and `line`.

A `numbers:` block in `initech.yaml` sets thresholds: one key per number and direction, `<number>_max` or `<number>_min`, for `merges_per_hour`, `gate_green`, `gate_red`, `gate_median_minutes`, `landing_depth`, `filed`, `closed`, `no_move_20m` and `idle_engineers`. A number above its max or below its min sends super one line through the send path, under its usual holds, such as `[from initech] rig numbers: gate red is 2, over its max of 1`. It sends once per crossing, nothing while the number stays outside, and again only after it comes back inside and crosses again; an unknown number never sends. With no block nothing is sent and the line still prints.

### Pane Ribbons

Each pane has a bottom ribbon showing:
- **Pane number and name**: "1 super", "2 eng1"
- **Status tag**: `[dead]` (process died), `[susp]` (suspended), `[+N]` (scrolled back N lines)
- **Bead ID**: Shows after the name when the agent has claimed a bead, after one character for the bead's state: `○` open, `▶` in_progress, `◎` ready_for_qa or in_qa, `◆` qa_passed or ready_to_ship, `☾` waiting, `■` blocked, `?` waiting_on_nelson, `❄` deferred, `✓` closed, such as `2 eng1 ▶ ini-abc`. The state is the one bd gave at the last 10-second read below, so a bead that moves shows its new icon at the next read. A status not in that list, a bead whose status has not been read yet (one just named with `initech bead`), and a pane another window runs show `|` instead. If a terminal font cannot draw one of the icons, its fallback is `z` for `☾`, `⊙` for `◎` and `+` for `◆`.
- **No bead**: `no bead` and the first words of the message, for work dispatched with `initech assign <agent> -m "..."` and no bead

bd is the record of who holds a bead, and each agent's bead comes from it: every 10 seconds initech reads the in_progress beads from bd (one call, bounded at 5 seconds) and shows on each agent the bead assigned to its role. When a role holds two, the one last named with `initech bead` wins, else the most recently updated. `initech bead <id>` is a manual override. It takes only bead ids (a prefix, a dash and a key, with an optional dotted child number, such as `ini-abc` or `ini-abc.2`), checked against bd when bd answers. A pasted bd row (`id`, a tab, its title) is cut to the id, and stderr says what was kept. An id bd does not know, an empty argument and anything else are refused with one line and exit 1, and nothing changes; when bd does not answer, the id is set with a note. Only the id is stored and sent, to the ribbon, `initech status` and the session-bead record; bd's title shows on the ribbon for display only. The agent shows that bead until `initech bead --clear`, which returns it at once to the bead bd says, the last-named one when the role holds two, from bd's last answer with no wait for the next read. While bd is not answering, `--clear` returns to the last bead bd gave, still marked. When bd does not answer, each agent keeps the last bead bd gave and the ribbon, `initech status` and `patrol` mark it `bead? bd unreachable`; nothing is guessed. An agent that has been working (running, or quiet with a background job) for fifteen minutes with no bead, from bd or `initech bead`, reads `no bead` in its ribbon, in `initech status` and in `patrol`; idle and suspended time do not count, and the marker clears the moment a bead appears. It is display only: nothing is typed into the agent. A project without bd shows only what `initech bead` sets. A second window shows what window 1 reads. A claim (`bd update --assignee` plus `initech bead`, or `initech assign`) puts the bead on the agent's ribbon. `initech deliver` puts the ribbon where its bd write leaves the bead, never on the `--to` agent, who only gets the report: the bead comes off the deliverer's ribbon unless bd still has the deliverer holding it in progress, ready for QA or in QA (other beads on that ribbon stay); a QA FAIL that returns the bead to its implementer adds it to that agent's ribbon when the agent is a pane in this initech; a hand-off and a PASS set no other ribbon, so a QA agent's ribbon shows the bead once it claims it. deliver's last line ends with what happened to the ribbon: `ribbon cleared`, `ribbon set on <agent>`, `ribbon unchanged`, or `ribbon NOT updated: <reason>`; bd is written either way and the ribbon outcome does not change the exit code. The report to the `--to` agent is named on the same line, just before the ribbon: when it did not arrive (no such pane, the agent stopped or still starting, no TUI running), the line reads `report NOT sent to <agent>: <reason>` and deliver exits non-zero, printing the `initech send` command that tells the agent by hand. The bead has still moved. The TUI log records one line for every ribbon write it receives, with the agent, the bead ids or `clear`, and the result.

initech also keeps a record of which Claude session was on which agent and bead, and when, in `~/.claude-accounts/records/<rig>-YYYY-MM.jsonl`, where the cost ledger (cla) reads it, so the ledger charges each session's spend to the bead it worked. `<rig>` is the project name from initech.yaml, else the project folder's name. Claude Code names its transcript folder after the working directory's real path, so a rig root reached through a symlink is resolved: initech looks under both spellings and reads the newest transcript, and a rig on a real path has one folder as before. A pane folder that cannot be resolved (a path that does not exist, a symlink loop) logs one line and is read as spelled. There is one file a month, kept 90 days after its month; initech removes only its own rig's files. The folder is created (mode 0700) when it is missing. When it cannot be written, initech says so once at start and in its log, keeps running, writes nothing, and looks again each minute; once it can write, the intervals it held arrive with their original times. Two rigs with the same name on one machine do not share a file: `<rig>.owner` beside the files names the project folder that owns the name, and a second folder writes `<rig>-<six hex>-YYYY-MM.jsonl` instead. Window 1 writes the record, or the daemon running the agents; read it, never edit it. A dev build (any version that is not a plain release number; `2.49.0` and `v2.49.0` are release numbers, while an empty or hand-set version and a prerelease such as `2.50.0-rc1` are not) never writes under `~/.claude-accounts`: it writes the same files under `<rig>/.initech/records/`, whatever `HOME` is, and says so once on stderr at start: `dev build: session records go to <path>, not the account ledger`. The same test decides whether a command that changes a live session (send, restart, stop and the like) is refused unless it carries `--allow-dev-delivery`; the refusal names the version it read. Files left under `.initech/session-beads-*` from earlier versions are no longer written or read. The format is in the spec under Session-bead record.

In `initech status`, a suspended agent shows Alive `no` and Status `suspended`; an agent that was stopped or whose process has exited shows Alive `no` and Status `stopped`. The header counts them the same way: `Session: demo (7 agents, 2 suspended, 1 stopped)`, where the first number is every agent the table lists and a count of zero is left out.

An agent whose pane is quiet but whose Claude Code footer shows a background shell, monitor or background agent is in the state `background`: it is not producing output, but it is not idle either. `initech status`, `initech patrol` and the Agents panel say so with the work and its age: `background, 1 shell 4m`, `background, 2 shells, 1 monitor 30s`. The count and the age are the running jobs': initech reads the pane's process tree, counts the background shells running under its Claude Code, and gives the oldest one's age from its process start. Claude Code keeps a finished shell listed in its footer until it is read; a listed shell that is no longer running does not count, and an agent whose listed shells have all finished reads idle. Where the process tree cannot be read (on Windows), and for background agents, which run inside Claude Code and have no process, the age is how long the footer has shown the work and says so: `background, 1 background agent, seen for 4m` (`seen 4m` in the Agents panel). background never overrides a stronger state: an agent producing output with a shell is `running, 1 shell 4m`, one with a dialog open is waiting, and a suspended or stopped agent reads as such. initech reads the footer once a second, so the state shows within a second of the work appearing and turns back to idle when the footer shows none; while a dialog covers the footer the last reading stands. Every window shows the same state. `initech patrol --active` keeps a background agent even when its screen is quiet. A background agent counts as working wherever initech decides working versus idle: it keeps its place in the live focus and live list splits, ranks in live mode as an agent producing output does, turns the top view's title green and marks its pane's ribbon with a dim `[bg 4m]` tag giving the job's age, which clears when the job ends. It does not get the running tint, which marks output, cannot be suspended by hand (suspending would stop its job) and is never auto-suspended. Its pane shows new output at most four times a second while the output keeps coming, and its last output as soon as it stops. Agents hosted by a cross-machine daemon do not report background work.

An agent whose pane is quiet but whose input box holds text that was not sent (a message whose Enter was not taken, a half-typed line, a paste waiting) is in the state `pending input`: the pane ribbon carries `[pending input]`, the overlay row a yellow dot, and `initech status` and the `initech patrol` header print `pending input`. Only the input box's own first row counts, so a dialog's or a picker's cursor row is not text, and nothing is pending while a dialog is open. It outranks background, and neither the memory policy nor `initech suspend` parks it, since the text would be lost. When `initech send` delivers a message and the box then holds other text (your own draft, put back after the send), it says so: `delivered to <agent>; the box still holds other text: "<line>"`. A pane another window or machine runs does not show pending.

An agent whose context is at or above 85% is flagged to compact soon: `initech status` prints `ctx 91%, compact soon` (the figure as read) after the state, the `initech patrol` header prints the same after the state, and the pane ribbon carries a yellow `[ctx 91%, compact soon]` tag after the state tags, before the bead. The figure is the one `status --json` reports as `context_percent`, read once a second: the status line's, or where the status line does not show it (a narrow pane, a dialog over it), the session transcript's. The threshold applies to that figure whichever source gave it. A figure read from the session transcript uses initech's table of context windows, 1,000,000 tokens for every model it has measured, so on an account whose window for a model is smaller the figure reads low, and the flag with it; where the status line shows its own figure, the status line's stands, and when the two differ by more than one point the TUI log says so. Below 85, or when no figure is known, there is no flag, and it goes at the next reading below 85, as after a compaction. The flag is not shown for an agent that is not running. A ribbon too narrow for the tag cuts it at the pane's edge and keeps the agent's name. A pane that another window runs carries no tag in this window; `initech status` and `initech patrol` still flag it. `status --json` has no flag of its own: `context_percent` carries the number.

An agent stopped on a Claude Code dialog reads `waiting` in `initech status`, the overlay and the needs-input list. A permission prompt or a question is announced by Claude Code itself and chimes. Claude Code's "Teach auto mode about your environment?" dialog is not announced, so initech recognises it on screen within a second and lists it without a chime, with that question as its preview; the row clears when the dialog closes. Its rm safety prompt ("Dangerous rm operation on statically-unresolvable target", which Claude Code blocks itself after a two-minute countdown) is announced only about ten seconds in; initech recognises it on screen within a second, so the agent reads waiting, not running, from the start, with "Dangerous rm operation: do you want to proceed?" as its preview; it chimes once Claude Code announces it, and the row clears when it is answered or blocks itself. Once such a prompt, with its countdown, has stayed on screen for three seconds, initech sends super one message, `[from initech] <agent> is at a prompt it cannot pass alone: Do you want to proceed? Its screen gives the reason: "<reason>". Claude Code blocks it in <M:SS>. Answer it: initech keystroke <agent> y (or n), or tell <agent> to rewrite the command.`, with the time left as the screen shows it and the reason quoted from the agent's screen as printable text, at most 160 characters. Only a prompt with its two answers under the question counts. It sends once per prompt (a new prompt sends again; one answered within the three seconds sends nothing), only to the one pane named super in this initech, and never for super's own prompt or for a prompt without a countdown. initech never answers the prompt and never types into the agent's pane for it. Only dialogs initech knows by their words are recognised this way.

The top edge of every pane is a plain line in every state; nothing moves along it. A pane with a bead carries its effort's pace on that line, such as `ini-wra 2/4 · 1/h · 0 stuck`: the effort's beads closed of all of them, closed per hour over the last six hours, and the stuck ones (in the rig's working chain, `in_progress` and `status.custom` up to `qa_passed`, with no move for the rig's `beads.stuck_after`, one hour by default, and the owner idle, waiting or pending, where a move is a status change, a comment or a commit; the header and the overlay learn of a comment at the next bead poll, and when a bead crosses the value they read its comments and the commits that name it once, so a move made before initech started or a commit alone counts too; that read asks the first role's clone as it stands, without fetching), then `· 1 blocked` when beads are blocked: a bead in `blocked` or `waiting_on_nelson` counts as blocked and never as stuck, then `· 1 waiting` when beads sit in `waiting` past the same value with an idle owner or no holder, then `· 1 idle` when an `in_progress` bead's owner pane has been idle for the rig's `beads.idle_after`, twenty minutes by default, whatever the bead's moves, with each such owner and how long after it on the overlay's effort row (`· idle eng3 23m`). A bead has three readings, each its own count: stuck (the bead has not moved and its owner is quiet), over (a waiting bead whose wait has ended) and idle owner (the pane is quiet, not the bead). A bead can be stuck and idle-owner at once and is listed under both; a waiting bead is never idle-owner. The idle time is the pane's own, from its last activity change, so only panes this initech runs give it; an owner on another machine reads nothing here. An idle owner sends super one line, `[from initech] <agent> idle <N>m on ▶ <id>; check on it`, through the send path with its usual holds, once per bead per idle spell: nothing more while the pane stays idle, and a new line only after it was active and went idle again. initech takes no action on the pane. Waiting is a calm count, never stuck and never blocked. Where a waiting bead names what it waits on (another bead, a tag, a run or a date; see `initech progress`), initech judges it in the bead poll: the other bead from the poll itself, a date from the clock, a tag with one `git ls-remote` on the rig's first repo and a run with one `gh run view`, each at most once a minute per target and never again once over. A wait that is over counts in the waiting count whatever the bead's age, adds ` · 1 over` in red to the line, and sends super one line, `[from initech] ☾ <id> waits on <target>: <event>; move it on`, through the send path with its usual holds, once per bead and target; it is sent again only when the bead waits on a new target. Each line is recorded on the bead itself, in its metadata as `wait_over_sent: <target>` (one `bd update` when the line goes out, cleared when the bead waits on a new target), so a window 1 that restarts sends no line a second time. initech never moves the bead; whoever reads the line does. A `deferred` bead is left out of every count. It is recomputed once a minute from the bead poll, reads `(stale)` after it when bd stops answering, and is not there when the pane has no bead. `initech progress` gives the full read. A running pane is marked by its green square, its title and its tint, a background pane by its ribbon tag, a suspended or dead one by its ribbon tag.

`initech status` reads bd for its Bead column: each agent bd names as assignee of a bead in progress, ready for QA or in QA shows that bead and its status (a bead at QA passed has no holder to show). When bd names more than one bead for an agent, the one its ribbon shows comes first, the bead last named with `initech bead`, else the most recently updated, so the column and the ribbon lead with the same bead. When an agent's ribbon shows a bead bd does not give that agent, or nothing while bd gives it one, the column shows bd's answer followed by `ribbon: <ids>` (`ribbon: -` for an empty ribbon), so a stale ribbon is visible. A ribbon on one of several beads the agent holds agrees with bd and adds nothing, and so does any ribbon while bd has not answered, when the stale mark shows instead. With `INITECH_SOCKET` pointing at another project's initech, the `Session:` header names the project behind that socket, not the project of the folder you ran it from; an initech too old to say which project it serves leaves the folder's name. The Bead column then shows the beads of the project behind the socket, read from that project's own folder, never yours; an initech too old to say where its project lives shows `(bd: unknown project)` there instead, as does a project whose folder is not on this machine. An `INITECH_SOCKET` that names your own project's socket, as every agent's does, counts as no override.


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

An agent running on the other machine sends with `initech send` like any agent. Every pane `initech serve` starts carries the serve's socket and the agent's own name, including agents your initech pushes to it. A name that machine does not run, such as `super`, goes through the serve to your initech, the one that pushed the sending agent, and the sender hears your initech's answer in the same words as a local send (`delivered to super`, `deferred to super`, `not delivered to super: <why>`). If your initech is not connected, the send is not delivered and says so, and it is not kept to send later. Only an agent the serve started can send this way: any other process on that machine is refused with `only an agent this serve started can send to another machine through it`. `initech send host:agent` there works as before.

A remote agent's pane takes the size of its tile here and resizes with it: a layout change or a terminal resize reaches the agent on the other machine, so its text wraps at the tile's width. A size the other machine refuses or does not answer logs one line, `viewer resize not applied`, in this machine's log. An agent has one terminal size, so two initech windows or machines viewing the same remote agent at different sizes cannot both be right: the last one to resize sets the size, and the other wraps wrong until it resizes or the first leaves. When a viewer leaves, the other machine gives the agent back the size of the viewer that remains. The other machine must run a release that does this; an older one keeps the departed viewer's size until the remaining tile next changes.

A remote can be added or removed while initech runs, with no restart. Window 1 re-reads `initech.yaml` whenever it changes, once the file has held still for half a second, and applies only its `remotes:` block. A remote added there connects, gets its `roles:` pushed as at start, and its agents appear in the Agents panel as they arrive; one that does not answer shows as connecting and keeps retrying, as at start. A remote removed detaches: its agents leave the panel and the layout, and the event log says `<name> left the remotes (N agents detached; their mail stays on the peer)`. Nothing on that machine is stopped or suspended, and any mail it holds for its agents stays there. A remote whose `addr` or `token` changes is detached and connected again under the same name, the old connection closed before the new one opens; a change to its `roles:` or `root:` takes effect at the next start. A file that does not parse or validate changes nothing: the running remotes stay as they are, and the error, never a value from the file, is logged once. Only window 1 reloads; a `--window` viewer follows window 1.

`initech remote add <name> --addr <host:port>` writes the remote into `initech.yaml`, keeping every other key and comment, and tells window 1 to re-read it at once; `initech remote remove <name>` takes it out the same way. `--roles a,b` names the roles to push; without it, the remote shows the agents it already runs. The token is never taken on the command line: `--token` is refused, because a typed token stays in your shell history and the process list. Pass `--token-file <file>`, a file only you can read (mode 0600, checked on macOS and Linux; read once), or leave it off to be asked for the token at the terminal with nothing echoed; Enter alone, or no terminal to ask on, leaves the remote using the project's `token`. initech never prints the token. With no initech running, the commands still edit the file, and the next start reads it.

### Upgrading with Rigs Running

`brew upgrade initech` removes `/opt/homebrew/bin/initech` before it builds the new version, so while it runs every agent's `initech send`, `peek` and `deliver` fails, and a hung brew leaves them failing. On a Mac with rigs running, the fleet upgrades through `scripts/release/brew_upgrade_bounded.sh`, not bare `brew upgrade`: brew only downloads and checks the tarball, the script builds the new version beside the old one and runs it to prove it, then renames the link over, so the link never goes away. A second run at the same time refuses. A brew call past its time bound is stopped, and the link stays on the old version. `--prune` also removes old versions from the Cellar, keeping the installed one, the previous one and any a running initech started from. On any other Mac, `brew upgrade initech` is the way to upgrade. A running rig stays on the version it started with until it restarts.

## Key Commands Quick Reference

These are the commands you'll use most. The full list is in the README.

| What you want | How |
|---------------|-----|
| Open command bar | Backtick (`` ` ``) |
| Switch between panes | Alt+Left / Alt+Right, or click |
| See all agents at once | Alt+s (overlay toggle) |
| Overlay floating or pinned across the top | Shift+Alt+s, or `agents pin` / `agents float` |
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
| Read what an agent said and ran, from its session | `initech last <agent>` in a shell |
| Add or remove another machine, no restart | `initech remote add <name> --addr <host:port>`, `initech remote remove <name>` (Cross-Machine Daemon) |

### initech last

`initech last <agent> [-n N]` prints the agent's newest N entries (20 by default, at most 200) from its newest Claude Code session transcript, oldest first, as plain text: each entry is its time, its kind (`agent`, `nelson`, `message from <agent>`, `tool`) and its text; a reply keeps its footer lines, and a tool run is its one line and its input and result, cut to 4 KB. It reads the transcript through the reader the app's conversation route uses, so it prints exactly what the app shows: the same entries left out, and a malformed line skipped and counted in a last line `skipped <n> malformed line`. The transcript is the one the pane list names for the agent, also under a rig reached through a symlink. An agent with no transcript prints `no transcript for <agent>` and exits 1; an agent on another machine (`host:agent`, or a pane the list shows as remote) is refused, since only this machine's agents are read; `-n` outside 1 to 200 is refused. A tool result can carry whatever the agent saw, secrets included, so treat the output as you would the agent's pane.

### The Top Modal

`top` (or `ps`) opens a process table. Press `p` to pin/unpin agents (pinned agents are never auto-suspended). Press `r` to restart, `k` to kill, `h` to hide/show. Arrow keys to navigate.

## The iPhone and iPad App

### Blockers

Blockers is one list per running rig, in two kinds.

**Waiting on you** is each rig's beads in `waiting_on_nelson`, read with `bd list` once a minute. An item is the bead's title and drops off when the bead leaves that status. Agents' reply footers are not read for it. Tapping an item opens its assignee's pane.

**Stuck** lists each pane that cannot act, with the reason:
- a dialog open: a permission prompt, trust prompt, picker, login screen or an unknown dialog;
- waiting at a prompt, or text left unsent in its input box;
- idle while it owns a bead in the rig's working chain that has not moved for the rig's `beads.stuck_after`, one hour by default;
- a QA pane idle while its rig has a bead in `ready_for_qa`, or one in `in_qa` that is unassigned or assigned to it.

A rig with nothing in either kind shows as one quiet row.

The list reloads on every feed event that can change it: a pane state, a waiting item, a stuck pane, the service's `blockers` event, a reset, or a reply in a `super` pane. Other transcript lines do not reload it. The Blockers and Progress buttons on the Panes screen open each view as a sheet; tapping an item opens its pane and closes the sheet. With no rig running it reads "No rig is running."

### Progress

Progress shows the report of `initech progress --json` for each effort in flight in every running rig, as the service last ran it (every five minutes); the view reads it on open, every five minutes after, and on a pull to refresh. Each effort shows the counts the command prints: closed of total and the number closed in the window, beads an engineer worked and beads that were writing or review only, the engineers on it, each bead past open with its stage, owner, `AC n/m` and commits landed, the critical path, the stuck beads, each bead quiet for the rig's `beads.stuck_after` or more with what it waits on, and the beads in `waiting` past that value with the first line of the latest comment. It shows no time at all: no finish time, no estimate, no actual, and no clock time of a close. With no effort in flight it reads "No effort is in flight."

### Pairing and Unlocking

Install the app from TestFlight on the iPhone or iPad. On first open it asks for the pairing code: run `initech app-serve token` on the Mac and scan the QR code it shows, or paste the pairing link it prints (`initech://pair?host=<address>&port=<port>&token=<token>&fp=<fingerprint>`). The app keeps the pairing in the keychain of that device only. It talks to the service over HTTPS and accepts only the certificate whose SHA-256 matches `fp`; any other certificate is refused before a request, and so the token, is sent. A new token issued on the Mac makes the service refuse the old one; on a refused token or certificate the app says which and asks to pair again. A Mac that does not answer reads as that, and home shows no line about pins until the list has loaded. Each request checks the pin itself; a TLS challenge other than the server's certificate gets the system's default handling. When the app ends a connection itself it says why: a certificate that is not the paired one, or a handshake that brought none. A read cancelled because its screen went away or reloaded shows nothing; a pane's conversation is then read once more. Each pin decision and each failed request (its route and the raw error) go to the same device log as shown errors, in the category `service`; never the token.

The app locks behind Face ID (or the device passcode) on every open and every return from the background; a device with neither set stays locked.

Every error the app shows is also written to the device log under the subsystem `co.nmelo.initech`, one category per screen, with the error's domain and code; read it in Console with the device connected, or for a simulator with `xcrun simctl spawn <device> log show --predicate 'subsystem == "co.nmelo.initech"' --last 5m`. An entry never carries the error's words, a pane name, the token or a URL with it, a pane's text, a message body or a bead title. About, at the bottom of home, shows the version, the build number and the commit the build was made from, as `0.1.0 (1) 1a2b3c4`; a build whose commit could not be read shows `unknown` in its place.

### Pushes

On the first open after pairing the app asks once whether it may send notifications; it never asks again, and Settings changes the answer. With notifications allowed, every launch registers the device with the Mac (`PUT /v1/devices`), once per launch, and again if iOS gives the device a new token; unlocking after the app was in the background is the same launch. With notifications off, home shows "Pushes are off" with an Open Settings button, and the app registers nothing. If the Mac refuses the registration, home shows one line with the Mac's words, and the app tries again at the next launch. Pairing again first removes the device from the Mac's list (`DELETE /v1/devices/<token>`, one attempt). An app installed from TestFlight or the App Store gets pushes from a service on Apple's production server, the default; a build installed from Xcode gets them only from a service whose `apns` block says `sandbox`.

A push shows its title, `initech: <rig>`, and one more word: the pane's name for a stuck pane, or the bead's id for a bead that waits on you. The app shows nothing else from a push; a bead's title comes from the Waiting on you list the Mac serves. Tapping a pane's push opens that pane, or home with one line saying the pane is no longer running. Tapping a bead's push opens Blockers at that rig, scrolled to the bead's row while it still waits on you; once it has left that state, the list shows as it stands. Tapping a push with neither opens home. While the app is open, a push for the pane on screen shows no banner and the pane refreshes, as does a bead's push while Blockers is open; any other push shows its banner, and home refreshes.

### Home

Home lists the panes you pinned, in your order, each with the name you gave it, its state (working, idle, waiting at a prompt, pending input, or its banner), its context percentage, the bead it holds and the newest item it has waiting on you, until that item leaves the Blockers list. It updates live from the service's feed. A pinned pane whose rig stopped or that no longer exists stays in the list, greyed. With nothing pinned, home shows one line pointing to Rigs.

**Rigs** lists every running rig with the panes you have not pinned; the pin button pins one. Swipe a pinned row to rename or unpin it; Edit reorders. The pinned list is stored on the Mac, so every paired device shows the same list. On iPad, the pinned list is a sidebar beside the open pane.

### A Pane

A pane opens as its conversation, read from the pane's Claude Code transcript: the agent's replies as text with the footer lines (`▶`) at the bottom of each reply, each tool run as one line that expands on tap, your messages on the right. It opens on the newest entry, and Show older messages at the top reads the next older page, until the transcript's start. At the bottom, the conversation follows new entries; scrolled back, it keeps your place, and a line at the bottom edge says what arrived below ("new below", or "3 new below"). Tapping the line, or scrolling to the bottom, goes to the newest entry and clears it. A message you send always takes you to the bottom. A pane with nothing in its transcript yet says so.

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

`GET /v1/progress` is the latest progress run, a list of the objects `initech progress --json` prints, one per running rig and effort with a bead in progress (`in_progress`, `ready_for_qa` or `in_qa`) or in `waiting`, `[]` before the first run; it never starts a read. The service reads it every five minutes, the first time when it starts, over six hours of commits and closes: these are its only bd and git calls on a timer. `GET /v1/progress/<rig>` is that rig's part of the run; with `?prefix=<p>` it is one report, read on request at most once every five seconds when the run did not include it.

`PUT /v1/devices` with `{"token": "<hex APNs device token>"}` registers a device for pushes and answers `{"devices": <count>}`; the app sends it on every launch, and a token already registered changes nothing. `DELETE /v1/devices/<token>` removes one; an unknown token is 404. The set is stored in `~/.config/initech/app-serve/devices.json` (mode 0600) as `{"devices": [{"token", "added"}]}`, at most 10 devices. A token that is not hex or is over 200 characters, an eleventh device, or a body with any other field is refused with 400 `{"verdict": "refused", "words"}`, and the stored set stands.

**Pushes.** The service sends Apple pushes to every registered device: one when a bead enters `waiting_on_nelson` in a running rig, at the next once-a-minute bd read, and one when a pane gets stuck (a dialog or banner, waiting, or pending). A bead pushes again only after it leaves the status and comes back; a pane only after it has recovered and gets stuck again; a recovery pushes nothing. A push carries the title `initech: <rig>` and, as its body, the bead id or the pane name, nothing else: never a bead's title, a message or any transcript text. A restart pushes nothing for what is already there: the first read of a rig after the service starts is its baseline, so a bead already waiting and a pane already stuck are not pushed until they leave and come back. Quiet beads are listed in the app and never pushed. The settings are the `apns` block of whichever rig's `initech.yaml` holds one (`key_path`, `key_id`, `team_id`, `topic`, and `environment`, which is `production` unless it says `sandbox`); the key at `key_path` must be mode 0600. With no block, two rigs holding different blocks, or a key that cannot be used, the service says so in one line and looks again after every poll, so a rig it could not read at first (a folder waiting on a macOS permission) turns pushes on as soon as a poll reads it; it then says once where pushes go. Nothing that happened while pushes were off is pushed, and once on, pushes keep their settings until the next start. Apple rejecting a device (410, or 400 `BadDeviceToken`) unregisters it, logged once. Any other failure is logged once an hour per device and never retried. With no device registered nothing is sent, and that is logged once an hour.

`GET /v1/pins` is the pinned list, `[{"rig", "pane", "name", "order"}]` in `order`, `[]` when nothing is pinned; `PUT /v1/pins` with the whole list replaces it. The list is stored on the Mac, in `~/.config/initech/app-serve/pins.json` (mode 0600), so every paired device shows the same one. A list with a pin missing its rig or pane, the same pane twice, a name over 80 characters, more than 200 pins or a field the service does not know is refused with 400, and the stored list stands.

`POST /v1/keys/<rig>/<pane>` with `{"key": "enter"}` (or `esc`, `ctrl-c`, `y`, `n`) presses that key in the pane exactly as `initech keystroke` does, as you, the operator: it goes into an open dialog (on Claude's numbered prompt `enter` is Yes and `esc` is No), and do-not-disturb does not hold it (you are the operator); a suspended pane refuses it, nothing typed. At Claude's folder trust prompt `enter`, `esc` and `ctrl-c` are refused as `initech keystroke` refuses them, nothing typed, because `enter` would choose the highlighted `No, exit`, `esc` would exit Claude and a second `ctrl-c` would too; the answer is 200 with the verdict `not_delivered` and the words `not delivered to <pane>: the folder trust prompt is up and Enter would choose No, exit; open the pane to answer it`. `y` and `n` are delivered and do nothing there. The answer is `{"verdict": ..., "words": ...}`: the verdict `initech keystroke` reached (`delivered` or `not_delivered`) and the line it prints for the same call, word for word. A request the service turns away before anything is typed answers `{"verdict": "refused", "words": ...}`: 400 for a key outside the five (with `initech keystroke`'s own refusal line) or a body that is not a JSON object, 404 for a rig not running or a pane it does not have.

`POST /v1/send/<rig>/<pane>` with `{"text": "..."}` types the message into the pane exactly as `initech send` does, as you, and presses Enter once (`"enter": false` leaves it in the box, as `--no-enter`). The answer carries the send's verdict and line, word for word: `delivered`; `deferred` when a dialog is open (the message waits and goes in when it closes); `unchecked` or `unconfirmed` when initech could not tell; `not_delivered`, which includes a slash command to a Claude Code pane that would paste rather than run (longer than 800 characters, or more than 3 lines), refused with the words `initech send` prints. Do-not-disturb does not hold it: you are the operator. An empty text is refused with 400 and nothing is sent.

`initech app-serve install` runs the service under launchd, which is its watchdog: it starts the service at login and ten seconds after it dies. It writes `~/Library/LaunchAgents/com.initech.app-serve.plist` and starts it with `launchctl bootstrap` in your login session; stdout and stderr go to `~/Library/Logs/initech/app-serve.log`, which is not rotated. The log is readable by its owner alone: install creates it 0600 in a 0700 folder before launchd starts the job, and the service sets it back to 0600 when it starts if it finds it wider, saying so in the log once. The job runs a copy of the binary at one fixed path, `~/Library/Application Support/initech/app-serve/initech` (0755, in a 0700 folder), kept current by the service itself and by initech's first run of each new release; it is also the stable path for your own launchd jobs (see A Stable initech Path for launchd Jobs). Install makes it from the file the path you ran resolves to (found through PATH when you ran it by name), checks it is byte-identical and, for a signed build, that it verifies and carries the same identifier and team, and keeps the path you ran in the job as `--source`. A source it cannot read or a copy that does not check out refuses the install with the reason, and nothing else is written. macOS keys the Desktop grant to a program by its real path, and a Homebrew build's real path names its version, so the copy is what lets the grant be given once: the release that first moves the job to the copy asks once more, and no upgrade after it asks again. When an upgrade moves `--source` to a new build, the service notices within a minute, copies and checks the new build over its copy (the path holds the old build or the new, never neither), says so in the log in one line naming the old and new build, lets requests in flight finish for up to five seconds and exits, and launchd starts the new build ten seconds later, and pushes nothing for a bead already waiting or a pane already stuck. A copy that fails is logged once, the service keeps running the build it has and tries again each minute, and `--notify` hears of it at most once an hour. A copy found behind `--source` at start (an upgrade while the service was not running) is replaced at the first minute the same way. Started by its own path rather than a link (a dev build), it says once at start that an upgrade does not restart it. It carries the `--listen`, `--port`, `--projects` and `--notify` you give, and your shell's `PATH`, since launchd's own has no `/opt/homebrew/bin` and the service runs `bd`; a tool that moves later needs `install` again. launchd starts jobs in `/`, so `--projects` defaults, at install time, to the folder holding the rig you are in, and is written out in full; with no rig found and no `--projects`, with `PATH` unset, or with no token issued yet (`run initech app-serve token first`), nothing is written. `--listen tailnet` is not refused while Tailscale is stopped: launchd keeps retrying and the log says why. Running it again with the same settings looks once whether launchd has the job: loaded, it prints `already installed` and changes nothing; not loaded, it starts the job it finds. New settings stop the old job and wait, up to ten seconds, for launchd to let it go before starting the new one; a job still loaded after that is named with the command to stop it, and the new one is not started. If starting the job fails, the job file and the copy stay, the message says the service is not running and gives the `launchctl bootstrap` command to run by hand, and `initech app-serve install` again starts it too. `initech app-serve uninstall` stops the service and removes the job and its copy, keeping the token, the certificate and the log; with nothing installed it says so.

`install` warns when `--projects`, the binary or a `PATH` entry is under `~/Desktop`, `~/Documents` or `~/Downloads`, the folders macOS guards for launchd jobs, and goes on. Install the unit from a signed release: the first time the service reads a rig under one of those folders, macOS shows its dialog once, and once it is answered Yes, later releases keep the access. A rig folder that does not answer, is refused or is missing is also sent to `--notify` as `initech app-serve cannot read <folder>: <why>`; the macOS reason is added only for a folder under one of those three.

`--notify <command>` runs after an unclean exit (a crash, or `kill -9`) with one argument, `initech app-serve restarted after an unclean exit`, bounded at ten seconds; if it fails, the log says so once and the service still starts. It runs at most once an hour for each kind of message, so a crash loop does not repeat the restart notice and that notice never silences a folder notice; the log records every restart. It runs with the `PATH` written at install. A plain `kill`, a logout and `uninstall` are clean exits and notify nothing, though launchd starts a killed service again. The marker of a run in progress is `~/.config/initech/app-serve/running`, written once the service is listening; neither it, the notify lines nor the log holds the token. A dev build under launchd has no `--allow-dev-delivery`, so its send and key routes refuse.

`initech app-serve token` prints the pairing link once as text and as a QR code of the same link in half-block characters, black on white whatever the terminal's colours, at most 57 columns wide: widen the pane if it wraps.

### A Stable initech Path for launchd Jobs

A launchd job of yours that runs initech (a watcher that runs `initech status`, `initech send` or `initech peek` on a timer) runs it from `~/Library/Application Support/initech/app-serve/initech`, not from `/opt/homebrew/bin/initech`. macOS keys a privacy grant such as the Desktop folder to a program that is not an app bundle by its real path, with a requirement on its signing identity. The Homebrew link resolves to a path that names the version, so a job run through it is a new program to macOS at every upgrade and asks again. The path above never changes, so the grant is given once and holds across upgrades.

initech keeps that file current itself. On the first run of a new release build through the Homebrew link, any initech command copies the build there, checks the copy (the same bytes and, for a signed build, a signature that verifies with the same identifier and team as the copy already there), and renames it into place, so the path always holds a whole build. A stamp beside it, `version-<X.Y.Z>`, records the version, so every later run costs one look at that stamp and nothing else. The app service, when installed, runs from the same file and refreshes it too when it sees a new build; the two never write it at the same moment. A dev build, a build run by its own path, and the copy itself never write it. A refresh that fails keeps the build already there and writes one line to `refresh.log` in the same folder, never to the command's own output; the next run tries again.

To point a job at it, make the first entry of its `ProgramArguments` that path, or call that path from the job's script instead of `initech` on `PATH`. The first run of the job after the change asks for the grant once; after that, no upgrade asks again. `initech app-serve uninstall` removes the file and its folder; the next initech command run through the link puts it back at the same path, and its grant still holds. A copy deleted by hand, with its stamp left beside it, comes back at the next release or with `initech app-serve install`.

## Configuration

Settings live in `initech.yaml` at the project root and are read at startup.

### Typing hold

When an agent sends a message to a pane you are typing in, the message waits until you pause,
so your draft is not stashed and restored mid-word. A message that submits first types the stash
key (Ctrl+S) to keep your draft, and the stash key is typed only into a terminal in raw mode, as
Claude Code's is: a pane in cooked mode (a shell, or an agent still starting) gets the message
without it, because there Ctrl+S stops the pane's output. While a message waits, the pane's ribbon
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

Your own typing and pastes are never held, and neither is a message you send from outside any pane or an inbox reply: those go straight in. super can break through with `initech send --urgent`, which goes straight in and leaves `urgent from super broke do-not-disturb on <agent>` in the log and the event log; `--urgent` from any other agent is refused. A message initech sends itself, `[from initech] ...`, goes to super through the same path and is held like any agent's message. Do-not-disturb trusts the sender's word, which is `INITECH_AGENT`: an agent that clears it reads as you. It is a courtesy between agents, not a security wall. To take a pane out yourself, run
`initech dnd --agent <agent> off`. Nothing is saved: restarting initech, restarting the agent or waking a suspended one clears it. An initech
started before this command existed answers `older initech running; restart it for
do-not-disturb` (exit 2).

### Schedules

A rig keeps its own schedule, and window 1 runs it whenever the rig is up: through restarts, and after the agent session that added a job has ended. A job delivers a message, or the output of a one-line command, to one pane, at a time of day or on an interval.

| Command | What it does |
|---------|--------------|
| `initech cron add --to <pane> --at 09:30 --message "<text>"` | A job at a time of day. After the time, add `weekdays`, `weekends` or days like `mon,thu` (`--at "09:30 weekdays"`); without one it runs daily. |
| `initech cron add --to <pane> --every 15m --command "<line>"` | A job on an interval of a minute or more. |
| `initech cron list` | Every job: its owner, id, pane, when, next run, last run and what it does. |
| `initech cron remove <id>` | Removes a job. An agent removes its own; super removes any. |

`add` and `remove` run inside an agent's pane: initech works out the agent from the pane the command runs in, as it does for `initech post`, so a job's owner is the agent that added it and only that agent, or super, can remove it. Outside an agent's pane, and on Windows, they are refused. A job of an agent that has since left stays listed and runs until super removes it.

A message arrives as `[from <owner>, schedule <id>] <text>`. A command runs in the rig root and is stopped at 30 seconds; its output, cut at 2 KB, arrives the same way with the command line and its exit code. Both go through the same path as `initech send`: it removes control characters from them as from any message (see Messages Not Arriving), so neither can type keys or repaint the pane; a dialog or do-not-disturb holds them like any other message; and a suspended agent wakes for them. A job whose time passed while initech was down runs once at the next start and says when it was due; an interval job carries on from the start, without catching up. A job never runs twice for one due time, even across a restart in the same minute.

Super hears one message, from initech, when a job cannot be delivered (no such pane, the pane not running, or the message refused), naming the job, its owner and why, and when a command fails or is stopped. Nothing is dropped silently. A job that keeps failing to deliver for the same reason is said once, not every run, until it delivers again or fails for another reason; this is kept in `schedule.yaml`, so a restart does not say it again.

The schedule is `.initech/schedule.yaml`, readable by the rig's user only (0600), and the commands are the way to change it. A hand edit that does not parse, or names an owner that is not an agent of the rig, is refused when it is read, with the line named in a message to super, and the last good schedule keeps running. Whoever can write that file can schedule a command, as with the operating system's crontab, which is why it is readable by the rig's user only. A job holds no secrets: a command that needs one reads it from where it is kept. Not built: retries, chaining, more than one line of command, a log per job, and editing a job (remove it and add it again).

### Usage data

initech sends one anonymous usage ping a day, so the maintainer knows how many installs there are and which versions they run. Window 1 sends it in the background after its first frame, at most once every 24 hours per install. A `--window` viewer, `initech serve` and the subcommands never send it. It carries exactly four things: a random install id, the initech version, the OS and the CPU architecture. It also asks PostHog not to derive a location from the request. It never carries names, paths, hostnames, roles, agent counts, project content or anything typed.

The install id is made once and kept in `~/.config/initech/install_id` (`%LOCALAPPDATA%\initech\install_id` on Windows), with the time of the last successful ping in `last_ping` next to it. A ping that fails is dropped without a message, never delays startup or exit, and is tried again at the next start.

To turn it off, set `telemetry: false` in `initech.yaml`, or set `DO_NOT_TRACK=1` in the environment; `DO_NOT_TRACK` wins over the file. Either one means no id file is made and nothing is sent.

### The machine ceiling

One Mac runs every rig on it, so initech reads the Mac as a whole and refuses to start or wake one more agent when it is full. The ceiling is three numbers in one file, `~/Library/Application Support/initech/machine.yaml` (the `initech` folder in your user config folder on other systems; `INITECH_MACHINE_DIR` names another folder outright, which is for test rigs, so a rig's initech reads a scratch ceiling while its agents keep your real home and login):

```yaml
ceiling:
  sessions: 30        # awake Claude sessions, across every rig
  free_memory_gb: 8   # reclaimable memory, in GB
  load: 30            # the 5-minute load average
```

When the file is absent the ceiling is 30 sessions, load 30 and free memory of the lower of 8 GB and 20% of total memory; a key left out keeps its default, and a key set is used as written. A value that is not a number (or, for `sessions`, not a whole number above 0), or a key that is not one of these three, fails the read naming the key, such as `ceiling.sessions`; initech then keeps the last good ceiling (the defaults before one) and logs the error once. The file is the machine owner's, written by hand; no rig can set or raise it, and a `ceiling:` key in a rig's `initech.yaml` fails that rig's load.

An awake session is an agent pane whose Claude process is alive and not suspended: running, background, idle, waiting on a dialog and pending input all count. A suspended agent, a dead one, a pane with no process and a pane running a plain shell do not, and neither does a Claude session initech did not start. Agents an `initech serve` daemon starts are not counted, and the ceiling does not refuse them. Window 1 of every rig writes its awake count once a minute to `~/Library/Application Support/initech/fleet/<rig>.json` (the folder 0700, the file 0600, with window 1's pid and the time); every window reads every rig's file at the same cadence and adds them, skipping a file whose pid is gone or that is three minutes old or more, so a rig that quit or crashed drops out by itself. It reads free memory and the 5-minute load from the system at the same time. Free memory means reclaimable memory, what the system can hand out now: free, inactive and speculative pages on macOS (`vm_stat`), `MemAvailable` on Linux. A number initech cannot read never engages the ceiling, and is logged once.

The ceiling is engaged while any number is at its value or past it: that many awake sessions or more, that much reclaimable memory or less, that load or more. It clears only when all three have been under for one full minute, so a dip does not let a start through only to engage again. Between two readings initech counts the starts it let through itself, so a burst of starts inside one minute cannot carry the Mac past the ceiling.

At the ceiling, initech refuses every start and wake of an agent: `initech start`, `initech resume`, `initech restart` and the restart command, `initech add`, the wake a message, a keystroke or the agents grid would cause, and the rig's own launch. The window still opens at the ceiling; an agent refused at launch stays parked, and wakes later through any wake. A refused agent's pane shows the reason across its bottom rows and `[ceiling]` on its ribbon, and `initech status` shows it on the agent's row: `not started: the Mac is at its ceiling: 31 awake sessions, 6.1 GB free, load 34; ceiling 30 / 8 GB / 30`. A refused command exits non-zero with the same words. initech never retries a refused start; run it again once the Mac is under. The ceiling never stops, suspends, interrupts or signals anything that is running: a restart is refused before the running agent is touched.

A message to a suspended agent at the ceiling is held, not lost. It waits on the agent's queue and the sender hears `deferred to <agent> — the Mac is at its ceiling; will wake and deliver when it is under`, exit 0. When the ceiling clears, initech wakes the agents holding such messages one at a time, each through the ceiling again, and delivers each message once.

`initech status` prints the fleet line after the agent table, `fleet 31 awake · 6.1 GB free · swap 2.3 GB · load 34 · ceiling 30 / 8 GB (default) / 30`, red and ending `· at the ceiling` while it is engaged; it reads window 1's last reading, or the machine itself when window 1 has none. The ceiling names the free memory value in effect and where it came from: `default` for 8 GB, `20% of 16 GB` for the share of a smaller machine, `machine.yaml` for a value set in the file. Swap in use is information only and never engages the ceiling. `initech status --json --numbers` carries the same in `numbers.fleet`: `awake`, `free_gb`, `swap_gb` and `load` (null when unreadable), `ceiling` (with `free_memory_source`), `at_ceiling` and `line`. The overlay's rig row ends with the fleet count, `· fleet 31 awake`.

There is no override from a rig or a flag, no per-rig share of the ceiling, and no stopping or suspending agents to get under it.

## Troubleshooting

### Agent Not Responding

1. Check the overlay. Is the dot green (active) or gray (idle)?
2. If gray: the agent is at a prompt. Send a message: `initech send <agent> "status?"`.
3. If green but no visible progress: it might be in a long tool execution. Wait 2-3 minutes.
4. If still stuck: `initech peek <agent>` to see the last output. Look for error messages or permission prompts. `initech peek <agent> -n 50` returns the agent's last 50 lines whatever its pane's height: when the screen holds fewer, the lines above it come from the pane's scrollback, in order. When it returns fewer lines than asked, it says why on stderr, with stdout and the exit code unchanged: a pane showing a full-screen program (an editor, a pager) is read as its screen only (`pane is <rows> rows; peek reads the screen only`), and a pane whose whole output is shorter says so (`peek returned <k> of <n> lines: all the pane keeps (up to 10000 lines of history)`).
5. Last resort: restart via backtick -> `restart` (when focused on that pane) or `initech restart <agent>` from another terminal.

### An Agent Stopped Reading Its Input

An agent whose process stops reading its input cannot take keys or messages, and it wedges only its own pane: the rest of the window keeps moving. The first key, paste or message that cannot go in brings one notice, `<agent> stopped reading its input; keys and messages to it are not going in`, and from then on what you type at that pane is dropped, with no further notice. When the agent reads again the pane takes input again, with no notice.

A message sent to it waits at most two seconds. If none of the message had gone in, `initech send` answers `deferred to <agent> — the pane is not reading its input; will deliver when it does` (exit 0), and the message goes in once the agent reads again. If part of it had, `initech send` answers `could not confirm delivery to <agent>: the pane stopped reading its input while this message was being written; it will not be sent again, because a resend could duplicate it if the agent resumes; peek the pane` (exit 2): the rest may still arrive if the agent resumes, so look at the pane before sending it again. Suspending or restarting the pane works as usual.

### Agent Crashed (Red Dot / [dead])

The process exited. Common causes:
- Claude hit a context limit and exited
- A permission was denied and Claude stopped
- Out of memory (check `top` for RSS values)

To recover: restart from the `top` modal (select agent, press `r`) or via command bar (`restart`). Claude's `--continue` flag will resume from the last session. Either restart runs in the background, so the window keeps moving while it happens, and a restart that fails reaches you as a notice, not as an error in the command bar.

### TUI Crash

If the TUI itself crashes:
1. Check `.initech/crash.log` for panic stack traces and render watchdog dumps. While the screen is frozen the watchdog writes one dump past ten seconds, then at most one every five minutes, each saying how long the freeze has lasted. Past 20 MB the file moves to `crash.log.1`, replacing the previous one, and a new `crash.log` starts
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

Before a message is typed, initech removes from it everything that could act as keys: ESC and every
control character other than tab and newline (a carriage return inside the message among them), DEL,
the C1 controls, invalid UTF-8, and whole escape sequences (CSI, OSC, DCS, SOS, PM and APC, so the
paste end `ESC[201~` too). An escape sequence with no end loses only its ESC. Tab, newline and every
printable character stay. This holds for every message, whoever sends it: a command, a schedule,
another window or machine, initech's own lines, and a paste from another window. A message that lost
characters is still typed, and its line says so: `delivered to <agent>; <n> control characters
removed` (a whole sequence counts as one), and every other answer above gains the same `; <n> control
characters removed`. A message that held nothing else is refused and nothing is typed: `not delivered
to <agent>: the message held only control characters (<n> removed)` (exit 1).

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
- Restarting an agent (`initech restart <agent>`, or restart from the command bar or the `top` modal) carries the messages waiting for it to the new process, which receives them once it is ready; `initech restart` says how many it carried. If the new process cannot start, the messages stay where they were and are reported as stranded.

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
box`, or the line, cut to 60 characters). initech then keeps watching that agent's box for up to 5 minutes: if the box holds that message's own text and nothing else (not a collapsed `[Pasted text #N]` line, which another paste could also show, and not with anything typed before or after it, such as your own draft) and no dialog is open, it presses Enter once and reports `delivered after the composer repainted (...)`; if the box shows something else it stops watching and reports nothing more; a message waiting in the box this way is also sent before the agent's next message arrives. Check the agent's pane before re-sending: a message that reads `could not confirm` may already be there, or be sent this way.

A reading of the box counts only when the box's own rows, from the `❯` row through its bottom border,
read the same and in the same place before and after it; an answer streaming above the box, the spinner
and the hint rows below it never spoil a reading. The log line for a message initech could not confirm,
`submit outcome UNKNOWN: could not confirm`, counts what those readings saw:

- `samples_before` is the number of readings taken during the wait before the Enter.
- `samples_after` is the number taken after the Enter, a resend's included.
- `torn` is how many changed while being read and were skipped.
- `stable` is how many held still.
- `sighted` is how many of the stable readings showed the message in the box.
- `cols` and `rows` are the pane's terminal size as initech read it; the `submitted: composer cleared` and `SUBMIT NOT TAKEN` lines carry them too.
- `claude_code` is the Claude Code version the agent's own session transcript last named, or `unknown` before it names one; the same two lines carry it too.
- `capture` is the file holding the screen initech read at that moment, in `.initech/inject-captures/` beside the log (the folder readable only by you, like the log), one file per such message, the newest 20 kept. Empty when there is no log file.

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

`initech progress [<prefix>] [--hours N] [--stuck-after <duration>] [--json]` shows whether a multi-bead effort is moving and where it is held. `<prefix>` picks the effort by bead id (an epic's children, such as `ini-wra.`); without one it reads the whole rig. `--hours` is the window for commits and closes, six by default. `--stuck-after` is how long a bead may sit before it reads stuck, for this run: it wins over `beads.stuck_after` in `initech.yaml`, which wins over one hour, and the header and the JSON (`stuck_after`) name the value in effect. `--json` prints the same report as one JSON object instead of the table: `beads` (the rows, each with `ac_proven` and `ac_items`, null without a criteria section, and `commits`, null when no repo was read), `closed`, `total`, `stuck`, `blocked`, `worked_by_engineers`, `writing_review_only`, `stages`, `closed_recent` (every close in the window; the table lists the last eight), `critical_path`, `engineers`, `quiet` (each with its `wait`), `waiting`, `waiting_over`, `idle_after` and `idle_owner` (each with its `owner` and `idle_min`) and `notes`; an effort with no bead is a report with `total` 0 and empty lists. A `deferred` bead is in no row, count or list. It is read-only.

It prints, in this order:

- **A table of the beads past open and not closed**, sorted by stage (`no bead in flight` when there is none): the owner; `AC n/m`, the acceptance items proven out of the bullets under the bead's Acceptance Criteria heading (an engineer posts one comment per item, starting `AC <n> proven`; `-` when the bead has no such heading); the commits on main in the window whose subject names the bead as a word (`-` when initech.yaml lists no repos); and the last move, the newest of a status change, a comment or a commit, with who made it. A bead whose title starts with `Step ` is left out of the table.
- **The effort lines:** closed of total, split into beads an engineer worked and beads that were writing or review only, then the stuck and the blocked beads, counted as in the pane header (`; stuck 1, blocked 2`, then `, waiting 3` when there are any), with a `stuck:` line naming the stuck beads (`stuck_ids` in the JSON); a bead waiting on Nelson's answer reads `blocked (Nelson)` in the table; beads per stage; the beads closed in the window (the count and the last eight by title); the critical path, up to five open beads with the most beads waiting on them; and the engineers on it.
- **What each quiet bead waits on.** A bead in the rig's working chain (no side state: `waiting`, `blocked`, `waiting_on_nelson` and `deferred` are never here) with no move for the stuck-after value or more reads `working, unrecorded` when its owner's pane is running or has background work (an engineer working with no comment or commit), `waiting on "<thing>"` when its last comment names what holds it (waiting on, blocked on, held for), and `stalled` otherwise.
- **The waiting beads.** Each bead the waiting count counts (`waiting` in the JSON), with its owner, `-` for none, the first line of its latest comment, and what it waits on. A bead waits on another bead through a bd dependency (`bd dep add <id> --blocked-by <other>`), and on a tag, a GitHub run or a date through one metadata key: `bd update <id> --set-metadata wait_on=tag:<name>`, `run:<id>` or `date:<YYYY-MM-DD>`. initech judges the wait: over when the other bead is closed, the tag is in the rig's first repo, the run has completed, or the day after the date has begun. An over wait reads `[OVER: <event>]`, such as `[OVER: tag v2.58.0 exists]`, counts in the waiting count whatever the bead's age, never reads stuck, and the counts line adds `(N over)` (`waiting_over` in the JSON). A waiting bead with no typed target, or a run GitHub does not know, reads `waits on unknown`. Each tag and run is asked once per report; one it cannot answer stays open with a note.
- **The idle owners.** Under `owner idle <beads.idle_after> or more:`, each `in_progress` bead whose owner's pane has been idle that long, read as in the pane header, as `<id> <owner>: idle 23m`; the counts line adds `, owner idle N`. The idle time comes from the running initech's pane list, so with no initech answering, from an older initech that does not send it, or for an owner on another machine, nothing reads idle-owner.
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
