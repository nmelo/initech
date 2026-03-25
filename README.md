# initech

A terminal multiplexer and orchestrator for multi-agent Claude Code sessions. Each agent gets its own PTY-backed pane with VT100 emulation, activity detection, and IPC messaging. Replaces tmux with a purpose-built TUI that understands agent state.

Named after the company from Office Space.

![initech TUI](screenshot.png)

## Install

```bash
brew tap nmelo/tap && brew install initech
```

Or build from source:

```bash
git clone https://github.com/nmelo/initech.git
cd initech
make build
```

## Prerequisites

- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI
- [git](https://git-scm.com/)
- [beads](https://github.com/nmelo/beads) (`bd`) for issue tracking (optional)

Run `initech doctor` to verify.

## Quick Start

```bash
# Bootstrap a new project
mkdir myproject && cd myproject
initech init

# Launch the TUI with all agent panes
initech tui

# Send a message to an agent
initech send eng1 "check the failing test in config/"

# Read an agent's terminal output
initech peek eng1 -n 50
```

### TUI Controls

| Key | Action |
|-----|--------|
| `` ` `` (backtick) | Open command modal |
| Alt+Left/Right | Navigate between panes |
| Alt+1 | Focus mode (single pane) |
| Alt+2 | 2x2 grid |
| Alt+3 | 3x3 grid |
| Alt+4 | Main + stacked layout |
| Alt+z | Zoom/unzoom focused pane |
| Alt+s | Toggle agent status overlay |
| Alt+q | Quit |
| Mouse click | Focus pane |
| Mouse drag | Select text (copies to clipboard) |

## Commands

| Command | What it does |
|---------|-------------|
| `initech init` | Bootstrap project: config, directories, role CLAUDE.md files, beads, docs |
| `initech tui` | Launch the TUI terminal multiplexer with all agent panes |
| `initech send <role> <text>` | Send a message to an agent's pane via IPC |
| `initech peek <role>` | Read an agent's terminal output via IPC |
| `initech status` | Agent table: activity state, bead assignments, memory |
| `initech stop <role...>` | Kill individual agents to free memory |
| `initech start <role...>` | Bring stopped agents back (optional `--bead` dispatch) |
| `initech restart <role>` | Kill + restart agent (optional `--bead` dispatch) |
| `initech down` | Graceful shutdown with uncommitted-work warnings |
| `initech standup` | Morning standup: shipped, in-progress, next up (from beads) |
| `initech doctor` | Check prerequisites with versions and fix instructions |
| `initech up` | Start tmux session (legacy, requires tmux + tmuxinator) |

## What `initech init` Creates

```
myproject/
  initech.yaml              # Project config (roles, claude args, overrides)
  .beads/                   # Issue tracker (bd)
  .gitignore
  CLAUDE.md                 # Project-wide operating manual
  AGENTS.md                 # Quick reference for agents
  docs/
    prd.md                  # Why: problem, users, success criteria
    spec.md                 # What: requirements, behaviors
    systemdesign.md         # How: architecture, packages, interfaces
    roadmap.md              # When/Who: phases, milestones, gates
  super/CLAUDE.md           # Coordinator agent
  pm/CLAUDE.md              # Product manager agent
  eng1/CLAUDE.md + src/     # Engineer agent (git submodule)
  eng2/CLAUDE.md + src/     # Engineer agent (git submodule)
  qa1/CLAUDE.md + src/      # QA agent (git submodule)
  qa2/CLAUDE.md + src/      # QA agent (git submodule)
  shipper/CLAUDE.md + src/  # Release agent (git submodule)
```

## Roles

11 well-known roles with production-ready CLAUDE.md templates:

| Role | Permission | What they own |
|------|-----------|---------------|
| super | Supervised | Dispatch, monitoring, session lifecycle |
| eng1/eng2 | Autonomous | Implementation, tests, code quality |
| qa1/qa2 | Autonomous | Behavioral verification, test evidence |
| pm | Autonomous | Product truth, requirements, acceptance criteria |
| arch | Autonomous | System design, API contracts, ADRs |
| sec | Autonomous | Security posture, threat modeling |
| shipper | Supervised | Builds, packaging, distribution |
| pmm | Autonomous | External messaging, competitive intel |
| writer | Autonomous | User-facing documentation |
| ops | Autonomous | End-user workflow testing |
| growth | Autonomous | Metrics, analytics, experiments |

Unknown role names are valid and get sensible defaults (Autonomous, no src).

## Architecture

The TUI owns the runtime: PTY allocation, terminal emulation (charmbracelet/x/vt), screen rendering (tcell), layout, activity detection (Claude's JSONL session logs), and IPC (Unix domain socket).

Agents communicate via `initech send` and `initech peek`, which connect to the TUI's socket at `/tmp/initech-<project>.sock`. Messages are delivered as keystrokes through the emulator, with delivery confirmation.

## Dependencies

- [Go](https://go.dev/) 1.25+ (build only)
- [Claude Code](https://docs.anthropic.com/en/docs/claude-code) CLI for agents
- [beads](https://github.com/nmelo/beads) (`bd`) for issue tracking (optional, degrades gracefully)
- [git](https://git-scm.com/) for submodule management

Runtime libraries (bundled): [cobra](https://github.com/spf13/cobra), [yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3), [charmbracelet/x/vt](https://github.com/charmbracelet/x), [tcell](https://github.com/gdamore/tcell), [creack/pty](https://github.com/creack/pty).

## License

MIT
