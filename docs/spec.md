# Initech Spec

Single source of truth for the initech CLI. Hard cap: 5000 lines.

---

## 1. Session Architecture

Discovered from: beadbox, cobalt, nayutal, secure-infra tmuxinator configs and project structures.

### 1.1 Core Model

A project runs as a **tmux session** where each **window** is an autonomous Claude Code agent with a defined role. The session name matches the project name. Tmuxinator manages lifecycle (start/stop).

```
tmux session "beadbox"
  |-- window: super      (coordinator, no skip-permissions)
  |-- window: pmm        (product marketing, skip-permissions)
  |-- window: pm         (product manager, skip-permissions)
  |-- window: eng1       (engineer, skip-permissions)
  |-- window: eng2       (engineer, skip-permissions)
  |-- window: qa1        (QA, skip-permissions)
  |-- window: qa2        (QA, skip-permissions)
  |-- window: shipper    (release, no skip-permissions)
  ...
```

### 1.2 Tmuxinator Config Pattern

Every project follows this identical structure:

```yaml
name: <project>
root: ~/Desktop/Projects/<project>

pre_window: export BEADS_DIR=~/Desktop/Projects/<project>/.beads  # optional

on_project_first_start: |
  if tmux has-session -t <project> 2>/dev/null; then
    echo "ERROR: Session '<project>' already exists."
    exit 1
  fi

startup_window: super   # or "coord"

windows:
  - <role>:
      panes:
        - cd ~/Desktop/Projects/<project>/<role-dir> && claude --continue [--dangerously-skip-permissions]
```

**Key observations:**
- One pane per window (no splits). Each window = one agent.
- `claude --continue` resumes prior conversation state.
- Some roles get `--dangerously-skip-permissions`, others don't (see 1.5).
- `on_project_first_start` guard prevents duplicate sessions.
- `pre_window` sets env vars (BEADS_DIR) available to all windows.

### 1.3 Grid View (Companion Session)

A second tmuxinator config creates a read-only monitoring view by attaching to the main session's windows in a tiled layout:

```yaml
name: <project>-grid
root: ~/Desktop/Projects/<project>

on_project_first_start: |
  if ! tmux has-session -t <project> 2>/dev/null; then
    echo "ERROR: Session '<project>' not running."
    exit 1
  fi

startup_window: grid
windows:
  - grid:
      layout: tiled
      panes:
        - tmux attach -t <project>:super
        - tmux attach -t <project>:eng1
        - tmux attach -t <project>:qa1
        - tmux attach -t <project>:shipper
```

The grid is a subset of the main session's windows. Not every agent appears in the grid; it shows the ones worth monitoring at a glance.

### 1.4 Agent Working Directory Structure

Each agent gets its own directory inside the project root. The directory contains:

```
<project>/
  super/
    CLAUDE.md          # Role instructions (large, 10-30KB)
    standup.md         # Working memory, current state
    .claude/           # Claude Code session state
    .mcp.json          # MCP server config (optional)
  eng1/
    CLAUDE.md          # Role instructions
    AGENTS.md          # Agent-specific additional context (optional)
    Makefile           # Build/test commands for this agent
    src/               # Git submodule/worktree of the actual codebase
      .claude/         # Nested Claude state for the src context
    docs/              # Agent-local docs (optional)
  shipper/
    CLAUDE.md          # Role instructions
    playbooks/         # Operational runbooks
    src/               # Git submodule/worktree
  qa1/
    CLAUDE.md
    src/               # Git submodule/worktree (read-only access pattern)
  ...
```

**Critical pattern:** Engineers and shippers get their own `src/` directory that is a **git submodule** (gitdir points to `../../.git/modules/<role>/src`). This gives each agent an isolated working tree so they don't conflict on file changes. QA agents also get src/ for validation but follow a read-only convention.

### 1.5 Permission Model

Two tiers of Claude Code permissions observed:

| Tier | Flag | Roles | Rationale |
|------|------|-------|-----------|
| Supervised | (none) | super, shipper | High-blast-radius actions: dispatching work, releasing software. Nelson wants visibility. |
| Autonomous | `--dangerously-skip-permissions` | eng, qa, pm, pmm, arch, sec, growth | Implementation/analysis work. Stopping for permission prompts kills throughput. |

**Invariant:** super never gets skip-permissions. super is the only agent that coordinates all others, so Nelson wants to see what it's doing.

### 1.6 Observed Role Catalog

Across four projects, these roles appear:

| Role | beadbox | cobalt | nayutal | secure-infra | Function |
|------|---------|--------|---------|--------------|----------|
| super | x | x | x | x | Coordinator/dispatcher |
| eng1 | x | x | x | x | Engineer (parallel) |
| eng2 | x | x | x | x | Engineer (parallel) |
| qa1 | x | x | x | x | QA (parallel) |
| qa2 | x | x | x | x | QA (parallel) |
| qa3 | | x | | | QA (parallel, extra) |
| shipper | x | x | x | x | Release/packaging |
| pm | x | x | x | x | Product manager |
| pmm | x | x | | x | Product marketing |
| arch | | x | x | x | Architect |
| writer | | x | | x | Technical writer |
| ops | | x | | x | Operations/UX testing |
| sec | | x | x | x | Security |
| growth | x | | | | Growth/analytics |

**Core roles (present in all 4):** super, eng1, eng2, qa1, qa2, shipper, pm
**Common roles (3 of 4):** pmm, arch, sec
**Specialized (1-2):** writer, ops, growth, qa3

### 1.7 Window Naming vs Directory Naming

The tmux window name and directory name sometimes differ:

| Window name | Directory name | Project |
|-------------|---------------|---------|
| pm | product | cobalt, secure-infra |
| ops | operator | secure-infra |
| writer | technical-writer | cobalt, secure-infra |
| eng1 | eng | secure-infra |

The window name is the canonical agent identity (used by gn/gp/ga for messaging). The directory name is a filesystem detail. Initech should normalize this: the window name IS the directory name.

### 1.8 Environment Variables

`pre_window` in tmuxinator sets env vars available to all agent windows:

| Variable | Purpose | Example |
|----------|---------|---------|
| BEADS_DIR | Path to beads issue tracker DB | `~/Desktop/Projects/cobalt/.beads` |

Additional env vars may be needed per project (API keys, service URLs). The pattern is: set them in `pre_window` so every agent inherits them.

### 1.9 Session Lifecycle

**Start:** `tmuxinator start <project>` creates session, opens all windows, each runs `claude --continue`.

**Monitor:** `tmuxinator start <project>-grid` opens a tiled view of key agents.

**Stop:** `tmuxinator stop <project>` kills all windows/agents. Claude state persists in `.claude/` dirs for `--continue` on next start.

**Restart single agent:**
```bash
tmux kill-window -t <project>:<role>
tmux new-window -t <project> -n <role>
tmux send-keys -t <project>:<role> "cd ~/Desktop/Projects/<project>/<role> && claude --continue" Enter
sleep 5 && gn -w <role> "[from super] Restarted. Resume work on <context>"
```

### 1.10 Communication Layer

Four tools with distinct semantics:

| Tool | Action | Urgency | Default target |
|------|--------|---------|---------------|
| `gn` (nudge) | Send text + Escape to interrupt | High | All windows |
| `ga` (add) | Queue text without interrupt | Low | Claude windows only |
| `gp` (peek) | Read output, no input sent | N/A | Single window |
| `gm` (mail) | Persistent message via beads DB | Varies | Named recipient |

**Protocol:** Always `gp <agent> -n 5` before sending to check if busy. If busy, use `ga`; if free, use `gn`.

**Dispatch pattern:**
```bash
gn --clear -w eng1 "[from super] You have work: <title> (<id>). Claim with: bd update <id> --claim --actor eng1. ga me when done."
```

### 1.11 What Initech Must Generate

For `initech init` or `initech up` to work, it needs to produce:

1. **Tmuxinator YAML** from a project config (role list, project root, env vars)
2. **Agent directories** with CLAUDE.md stubs appropriate to each role
3. **Git worktrees or submodules** for roles that need isolated source copies (eng, shipper, qa)
4. **Optional grid config** with a configurable subset of roles to monitor

---

## 2. Role Definitions

*Pending discovery. Will capture: role responsibilities, CLAUDE.md structure per role, permission boundaries, tool access.*

## 3. Agent Coordination

*Pending discovery. Will capture: dispatch protocol, work tracking (beads), status lifecycle, escalation patterns.*

## 4. Project Bootstrap

*Pending discovery. Will capture: directory scaffold, config files, git setup, external service wiring.*

## 5. Lifecycle Management

*Pending discovery. Will capture: session start/stop, state persistence, cleanup, long-running task handling.*
