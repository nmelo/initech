<p align="center">
  <img src="stapler.png" alt="I believe you have my stapler" width="300">
</p>

# initech

[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Tests](https://img.shields.io/badge/tests-10%2C500_lines-brightgreen)](https://github.com/nmelo/initech)
[![Homebrew](https://img.shields.io/badge/brew-nmelo%2Ftap-orange?logo=homebrew)](https://github.com/nmelo/homebrew-tap)

A terminal multiplexer purpose-built for running multiple Claude Code agents simultaneously. Each agent gets its own PTY-backed pane with terminal emulation, activity detection, bead tracking, and reliable IPC messaging. Replaces tmux with a runtime that understands agent state, work assignments, and session lifecycle. 9,300 lines of Go, 10,500 lines of tests, 15 CLI commands, 11 role templates.

## Why It Exists

Running multiple Claude Code agents in tmux works for small teams but degrades in three specific ways that initech solves.

**Messages fail silently.** tmux send-keys has no delivery guarantee. When a completion report from eng to super drops, the entire dispatch chain stalls. Nobody knows eng finished. Nobody dispatches QA. initech's IPC socket confirms delivery or returns an explicit error within seconds.

**Agent state is invisible.** In tmux, a hung agent and a productive one look identical. The only way to know what's happening is to peek each pane manually, which scales linearly with agent count. initech renders all agents simultaneously with activity indicators (derived from PTY output byte flow), bead assignments in ribbon badges, and toast notifications when agents complete, stall, or get stuck in error loops.

**Work is invisible to the runtime.** tmux doesn't know what beads exist, who's assigned to what, or that an agent just ran `bd update --status ready_for_qa`. initech's event system parses Claude's JSONL session logs for bd commands, detects status transitions, and surfaces them as typed events. When an agent finishes, a green toast appears. When an agent stalls for 10+ minutes, a yellow warning fires. When idle agents have ready beads in the backlog, the mismatch is flagged.

## Quick Start

```bash
# Install
brew tap nmelo/tap && brew install initech

# Check prerequisites
initech doctor

# Bootstrap a new project
mkdir myproject && cd myproject
initech init

# Launch the TUI
initech
```

`initech init` prompts for a project name, presents an interactive role selector (arrow keys, space to toggle, presets for small/standard/full teams), and scaffolds the full project: `initech.yaml`, agent directories with CLAUDE.md files, git submodules, beads database, and project documents (PRD, spec, system design, roadmap).

`initech` (no subcommand) launches the TUI. All agent panes start simultaneously. Each pane runs Claude with the appropriate permission level.

## What You See

The TUI renders all agent panes in a configurable grid. The bottom ribbon of each pane shows the agent's name and current bead assignment. A floating overlay panel in the top-right shows every agent's activity state (green dot = active, gray = idle, yellow = idle with work waiting) and bead ID.

### Layouts

Three layout modes, switchable via keyboard or the command modal:

- **Grid** (default): NxM grid auto-calculated from pane count. Manual override with `grid CxR`.
- **Focus**: Single pane, full screen. Switch with `focus [name]` or Alt+1.
- **Main + stacked**: 60/40 split, large pane left, stacked panes right. Switch with `main` or Alt+4.

Zoom (Alt+z) expands the focused pane to full screen regardless of layout mode. Layout persists across sessions in `.initech/layout.yaml`.

### Command Modal

Press backtick to open the command bar. Commands:

| Command | Effect |
|---------|--------|
| `grid [CxR]` | Set grid layout (e.g., `grid 3x2`). No arg = auto-calculate. |
| `focus [name]` | Full-screen on a pane. No arg = current pane. |
| `zoom` | Toggle zoom on focused pane. |
| `panel` | Toggle agent status overlay. |
| `main` | Main + stacked layout. |
| `show <name\|all>` | Show a hidden pane. |
| `hide <name>` | Hide a pane from the grid. |
| `view <n1> [n2] ...` | Show only listed panes, hide rest. |
| `layout reset` | Reset to auto-calculated defaults. |
| `restart` / `r` | Kill and relaunch the focused pane. |
| `patrol` | Scrollable view of all agents' recent output. |
| `top` / `ps` | Activity monitor: PID, memory, command, bead per agent. |
| `add <name>` | Add a new agent pane (workspace must exist). |
| `remove <name>` / `rm` | Remove an agent pane. |
| `log` / `events` | Scrollable event history (last 60 minutes). |
| `help` / `?` | Reference card with all commands and keybindings. |
| `quit` / `q` | Exit (with confirmation). |

### Keybindings

| Key | Action |
|-----|--------|
| `` ` `` (backtick) | Open/close command modal |
| Alt+Left/Right | Navigate between panes |
| Alt+1 | Focus mode (single pane) |
| Alt+2 | 2x2 grid |
| Alt+3 | 3x3 grid |
| Alt+4 | Main + stacked layout |
| Alt+z | Zoom/unzoom focused pane |
| Alt+s | Toggle agent status overlay |
| Alt+q | Quit |
| Mouse click | Focus pane |
| Mouse drag | Select text (copies to clipboard on release) |
| Scroll wheel | Scroll pane history |

### Toast Notifications

The event system watches Claude's JSONL session logs and fires toast notifications for work state changes:

- **Green toast**: Agent completed a bead (detected from `bd update --status ready_for_qa`)
- **Yellow toast**: Agent stalled (no output for 10+ minutes with a bead assigned)
- **Red toast**: Agent stuck in error loop (3+ consecutive tool failures)
- **Blue toast**: Agent claimed a new bead
- **Gray toast**: Agent idle with ready beads in the backlog

Detection is automatic. No agent cooperation required beyond normal bd usage.

### Activity Monitor

The `top` command (or `ps`) opens a full-screen process table showing each agent's PID, process name, launch command, RSS memory usage, bead assignment, and status. Supports actions: `r` to restart, `k` to kill, `h` to hide/show.

## Configuration

### initech.yaml

```yaml
project: myproject
root: /Users/you/Desktop/Projects/myproject

repos:
  - url: git@github.com:you/myproject.git
    name: myproject

beads:
  prefix: mp

claude_args: ["--continue", "--dangerously-skip-permissions"]

roles:
  - super
  - pm
  - eng1
  - eng2
  - qa1
  - qa2
  - shipper

role_overrides:
  super:
    claude_args: []   # Supervised: no skip-permissions
  eng1:
    tech_stack: "Go 1.25, cobra, tcell"
    build_cmd: "make build"
    test_cmd: "make test"
```

### Role Catalog

13 well-known roles with production CLAUDE.md templates:

| Role | Permission | Needs src/ | What they own |
|------|-----------|-----------|---------------|
| super | Supervised | No | Dispatch, monitoring, session lifecycle |
| pm | Autonomous | No | Product truth, requirements, acceptance criteria |
| arch | Autonomous | No | System design, API contracts, ADRs |
| eng1, eng2, eng3 | Autonomous | Yes | Implementation, tests, code quality |
| qa1, qa2 | Autonomous | Yes | Behavioral verification, test evidence |
| shipper | Supervised | Yes | Builds, packaging, distribution |
| sec | Autonomous | No | Security posture, threat modeling |
| pmm | Autonomous | No | External messaging, competitive intel |
| writer | Autonomous | No | User-facing documentation |
| ops | Autonomous | No | End-user workflow testing |
| growth | Autonomous | Yes | Metrics, analytics, experiments |

Unknown role names are valid. `LookupRole("designer")` returns a default (Autonomous, no src). Custom roles get a generic CLAUDE.md template.

### CLAUDE.md Hierarchy

initech uses Claude Code's CLAUDE.md file hierarchy for agent instructions:

```
myproject/
  CLAUDE.md            # Project-wide protocols (all agents inherit)
  super/CLAUDE.md      # Supervisor-specific instructions
  eng1/CLAUDE.md       # Engineer-specific instructions
  eng1/src/.claude/    # Claude Code session state
```

Each role's CLAUDE.md encodes identity, decision authority, constraints, workflow, and communication protocols. The templates are the core asset: they encode institutional knowledge about how each role should behave in a multi-agent development team.

## CLI Reference

All commands communicate with the running TUI via a Unix domain socket at `/tmp/initech-<project>.sock`.

| Command | Description |
|---------|-------------|
| `initech` | Launch the TUI (reads initech.yaml from cwd or parent) |
| `initech init` | Bootstrap project with interactive role selection |
| `initech send <role> <text>` | Deliver text to an agent's terminal (with Enter) |
| `initech send <role> <text> --no-enter` | Deliver text without pressing Enter |
| `initech peek <role> [-n lines]` | Read agent's terminal output (default: all) |
| `initech patrol [-n lines] [--active]` | Bulk peek: all agents' output in one call |
| `initech bead [id]` | Report current bead assignment to the TUI |
| `initech bead --clear` | Clear bead assignment |
| `initech status` | Agent table: activity, bead, alive status |
| `initech stop <role...>` | Kill agent processes (panes stay in roster) |
| `initech start <role...> [--bead id]` | Respawn agents with --continue |
| `initech restart <role> [--bead id]` | Kill + respawn agent |
| `initech add <name>` | Add agent to running session (workspace must exist) |
| `initech remove <name>` | Remove agent from running session |
| `initech down` | Shut down TUI and all agents |
| `initech standup` | Morning standup from beads (shipped, active, next) |
| `initech doctor` | Check prerequisites with versions and fix instructions |
| `initech version` | Print version |

## Architecture

```
cmd/                    # Cobra CLI commands (15 commands)
internal/
  color/                # CLI color helpers (charmbracelet/x/ansi)
  config/               # initech.yaml types, Load, Validate, Discover
  exec/                 # Runner interface for shelling out (testing seam)
  git/                  # git init, submodule add, commit
  roles/                # Role catalog (13 roles), template rendering, selector
  scaffold/             # Directory tree creation, CLAUDE.md generation
  tui/                  # The TUI runtime:
    tui.go              #   Event loop, lifecycle, layout application
    input.go            #   Key handling, command modal, tab completion
    mouse.go            #   Mouse events, text selection, clipboard
    render.go           #   Screen drawing, overlay, toasts, command bar
    top.go              #   Activity monitor modal
    layout.go           #   LayoutState, RenderPlan, persistence
    pane.go             #   PTY management, emulator, activity detection
    ipc.go              #   Unix socket server, all IPC handlers
    events.go           #   Event types, detection (completion, stall, stuck)
```

### How It Works

The TUI is a single Go process that owns a PTY per agent. Each PTY runs Claude via a login shell (`$SHELL -l -c "claude --continue [flags]"`). Terminal output flows through a VT100 emulator (charmbracelet/x/vt SafeEmulator), which the TUI reads for rendering.

Activity detection tracks PTY byte flow: if the agent's process produced output in the last 2 seconds, it's active. Claude Code's spinner guarantees byte flow during thinking, tool execution, and response generation. The only state with zero output is idle-at-prompt.

The event system tails Claude's JSONL session logs (`~/.claude/projects/<cwd>/`) for semantic events: bd commands in tool_use results (bead claims, status transitions), assistant messages (DONE/FAIL patterns), and error sequences (consecutive failures). Events emit to a channel consumed by the TUI's event loop.

IPC uses a Unix domain socket. CLI commands (`initech send`, `initech peek`, etc.) connect to `/tmp/initech-<project>.sock` and exchange JSON request/response messages. The TUI delivers messages by writing keystrokes through the emulator, the same path as real keyboard input.

Layout is managed by a `LayoutState` struct (mode, grid dimensions, hidden panes, focus) that feeds into `computeLayout()`, which produces a `RenderPlan` consumed by the render loop. Layout persists to `.initech/layout.yaml` and restores on next startup.

## Dependencies

Build:
- [Go](https://go.dev/) 1.25+

Runtime:
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI
- [git](https://git-scm.com/)
- [beads](https://github.com/nmelo/beads) (`bd`) for issue tracking (optional, degrades gracefully)

Libraries (bundled): [cobra](https://github.com/spf13/cobra), [yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3), [charmbracelet/ultraviolet](https://github.com/charmbracelet/ultraviolet) + [x/vt](https://github.com/charmbracelet/x) (terminal emulation), [tcell](https://github.com/gdamore/tcell) (screen rendering), [creack/pty](https://github.com/creack/pty) (PTY allocation), [charmbracelet/x/ansi](https://github.com/charmbracelet/x) (CLI colors).

## License

MIT
