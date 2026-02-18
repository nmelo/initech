# Initech System Design

The "how" companion to `spec.md` (the "what"). Hard cap: 5000 lines.

---

## 1. Module Structure

### 1.1 Go Module

```
module github.com/nmelo/initech

go 1.23

require (
    github.com/spf13/cobra v1.8.x
    gopkg.in/yaml.v3 v3.0.x
)
```

Two direct dependencies. Cobra for CLI, yaml.v3 for config and tmuxinator YAML. No viper, no template engines, no UI libraries.

### 1.2 Package Layout

```
initech/
  main.go                          # 10 lines, delegates to cmd.Execute()
  cmd/
    root.go                        # Root command, version, global flags
    init.go                        # initech init
    up.go                          # initech up
    down.go                        # initech down
    status.go                      # initech status
    restart.go                     # initech restart <role>
    standup.go                     # initech standup
  internal/
    config/
      config.go                    # Project config types, Load(), Save(), Validate()
      config_test.go
      defaults.go                  # Default role configs, permission tiers
    scaffold/
      scaffold.go                  # Create dirs, write files, idempotent ops
      scaffold_test.go
    tmuxinator/
      generate.go                  # Generate tmuxinator YAML from config
      generate_test.go
    roles/
      templates.go                 # Inline role CLAUDE.md templates (string constants)
      render.go                    # {{variable}} substitution via regex
      render_test.go
      catalog.go                   # Well-known role definitions
    tmux/
      tmux.go                      # Shell out to tmux: sessions, windows, send-keys
      tmux_test.go
    git/
      git.go                       # Shell out to git: init, submodule add, commit
      git_test.go
    exec/
      exec.go                      # Shared command runner, wraps os/exec
      exec_test.go
  docs/
    spec.md                        # What initech does (discovery output)
    systemdesign.md                # How initech works (this file)
```

### 1.3 Package Responsibilities

Each package owns one thing. No cross-contamination.

**`internal/exec`** - Wraps `os/exec` with consistent error handling. Every package that shells out uses this, not `exec.Command` directly.

```go
type Runner interface {
    Run(name string, args ...string) (string, error)
    RunInDir(dir, name string, args ...string) (string, error)
}
```

**`internal/config`** - Owns the `initech.yaml` schema. Reads, writes, validates. Exposes types. Does not know about files on disk, tmux, or git.

**`internal/scaffold`** - Owns directory and file creation. Given a config, creates the project tree including `docs/` with all four project document templates. Idempotent: checks `os.Stat()` before writing. Does not know about git, tmux, or beads.

**`internal/tmuxinator`** - Owns tmuxinator YAML generation. Given a config, produces session YAML and grid YAML as `[]byte`. Does not know about scaffold, git, or tmux runtime.

**`internal/roles`** - Owns role templates and the catalog of well-known roles. Templates are inline string constants. Render does `{{variable}}` substitution via regex. Catalog maps role names to metadata (permission tier, needs_src, needs_playbooks). Does not know about files, config, or tmux.

**`internal/tmux`** - Owns tmux CLI interaction at runtime. Session exists, list windows, kill window, new window, send-keys. Ported from gastools `internal/tmux/tmux.go`. Does not know about config or scaffold.

**`internal/git`** - Owns git CLI interaction. Init repo, add submodules, commit. Does not know about config or tmux.

### 1.4 Interface Boundaries

Interfaces where swapping is useful. Default implementations shell out to real CLIs. Tests swap in fakes.

```go
// internal/tmux/tmux.go
type SessionManager interface {
    SessionExists(name string) bool
    ListWindows(session string) ([]Window, error)
    KillWindow(session, window string) error
    NewWindow(session, window string) error
    SendKeys(target, text string) error
}
```

The `exec.Runner` interface is the most important boundary. Every package that runs external commands depends on Runner, not on `os/exec` directly. This makes the entire project testable without real tmux, git, or bd installed.

### 1.5 Dependency Flow

```
cmd/*  -->  internal/config
       -->  internal/scaffold  -->  internal/exec
       -->  internal/tmuxinator
       -->  internal/roles
       -->  internal/tmux      -->  internal/exec
       -->  internal/git       -->  internal/exec
```

No cycles. `internal/exec` is the leaf dependency. `cmd/` is the composition root that wires packages together. Internal packages never import each other except through `exec`.

---

## 2. Configuration

### 2.1 Config File

`initech.yaml` at the project root. Created by `initech init`, read by all other commands.

```yaml
# initech.yaml
project: beadbox
root: ~/Desktop/Projects/beadbox

# Code repositories (agents get submodules pointing to these)
repos:
  - url: git@github.com:nmelo/beadbox.git
    name: beadbox

# Environment variables inherited by all agent windows
env:
  BEADS_DIR: ~/Desktop/Projects/beadbox/.beads

# Beads issue tracker config
beads:
  prefix: bb

# Roles to include (order determines tmux window order)
roles:
  - super
  - pm
  - pmm
  - eng1
  - eng2
  - qa1
  - qa2
  - shipper

# Grid view: which roles appear in the monitoring session
grid:
  - super
  - eng1
  - qa1
  - shipper

# Optional per-role overrides (most roles use catalog defaults)
role_overrides:
  eng1:
    tech_stack: "Next.js 16, React 19, Tauri v2"
    build_cmd: "pnpm dev"
    test_cmd: "pnpm test"
```

### 2.2 Go Types

```go
type Project struct {
    Name          string                   `yaml:"project"`
    Root          string                   `yaml:"root"`
    Repos         []Repo                   `yaml:"repos"`
    Env           map[string]string        `yaml:"env,omitempty"`
    Beads         BeadsConfig              `yaml:"beads,omitempty"`
    Roles         []string                 `yaml:"roles"`
    Grid          []string                 `yaml:"grid,omitempty"`
    RoleOverrides map[string]RoleOverride  `yaml:"role_overrides,omitempty"`
}

type Repo struct {
    URL  string `yaml:"url"`
    Name string `yaml:"name"`
}

type BeadsConfig struct {
    Prefix string `yaml:"prefix,omitempty"`
}

type RoleOverride struct {
    TechStack string `yaml:"tech_stack,omitempty"`
    BuildCmd  string `yaml:"build_cmd,omitempty"`
    TestCmd   string `yaml:"test_cmd,omitempty"`
    Dir       string `yaml:"dir,omitempty"`
    RepoName  string `yaml:"repo_name,omitempty"`
}
```

### 2.3 Config Discovery

1. `--config` flag (explicit path)
2. `./initech.yaml` (current directory)
3. Walk upward to find `initech.yaml` (like `.git` discovery)

### 2.4 Validation Rules

- `project` required, non-empty
- `root` required, must be expandable path
- `roles` required, at least one role
- If any role has `NeedsSrc` (from catalog), at least one repo must be defined
- `grid` entries must be a subset of `roles`
- `role_overrides` keys must be in `roles`

---

## 3. Role System

### 3.1 Role Catalog

Maps role names to metadata that drives scaffold and tmuxinator generation.

```go
type PermissionTier int

const (
    Supervised PermissionTier = iota
    Autonomous
)

type RoleDef struct {
    Name           string
    Permission     PermissionTier
    NeedsSrc       bool
    NeedsPlaybooks bool
    NeedsMakefile  bool
    Template       string  // which template constant to use
}

var Catalog = map[string]RoleDef{
    "super":   {Permission: Supervised, NeedsSrc: false},
    "eng1":    {Permission: Autonomous, NeedsSrc: true,  NeedsMakefile: true},
    "eng2":    {Permission: Autonomous, NeedsSrc: true,  NeedsMakefile: true},
    "qa1":     {Permission: Autonomous, NeedsSrc: true,  NeedsPlaybooks: true},
    "qa2":     {Permission: Autonomous, NeedsSrc: true,  NeedsPlaybooks: true},
    "shipper": {Permission: Supervised, NeedsSrc: true,  NeedsPlaybooks: true},
    "pm":      {Permission: Autonomous, NeedsSrc: false},
    "pmm":     {Permission: Autonomous, NeedsSrc: false},
    "arch":    {Permission: Autonomous, NeedsSrc: false},
    "sec":     {Permission: Autonomous, NeedsSrc: false},
    "writer":  {Permission: Autonomous, NeedsSrc: false},
    "ops":     {Permission: Autonomous, NeedsPlaybooks: true},
    "growth":  {Permission: Autonomous, NeedsSrc: true},
}
```

**Open catalog:** Unknown role names are valid. `LookupRole("designer")` returns a default Autonomous role with no src, no playbooks. The catalog provides good defaults, not a closed set.

### 3.2 Template System

Templates are inline Go string constants. No external files, no `embed`, no `text/template`.

```go
// internal/roles/templates.go
const SuperTemplate = `# CLAUDE.md

## Identity

**Supervisor** for {{project_name}}. You own three things:
...
`

const EngTemplate = `# CLAUDE.md
...
`
```

One constant per role. File will be ~2000-2500 lines total across all 11 role templates.

### 3.3 Variable Substitution

Regex-based `{{variable}}` replacement, matching the beads pattern.

```go
var varPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

type RenderVars struct {
    ProjectName string
    ProjectRoot string
    RepoURL     string
    TechStack   string
    BuildCmd    string
    TestCmd     string
    BeadsPrefix string
}

func Render(template string, vars RenderVars) string {
    lookup := map[string]string{
        "project_name": vars.ProjectName,
        "project_root": vars.ProjectRoot,
        "repo_url":     vars.RepoURL,
        "tech_stack":   vars.TechStack,
        "build_cmd":    vars.BuildCmd,
        "test_cmd":     vars.TestCmd,
        "beads_prefix": vars.BeadsPrefix,
    }
    return varPattern.ReplaceAllStringFunc(template, func(match string) string {
        key := varPattern.FindStringSubmatch(match)[1]
        if val, ok := lookup[key]; ok && val != "" {
            return val
        }
        return match
    })
}
```

Unknown variables left as-is. No runtime parse errors. No conditionals or loops in templates; if a template needs branching, the template is doing too much.

### 3.4 Template Content Strategy

Each role template follows the structure from spec section 2.4:

1. Identity (who you are, what you own)
2. Critical Failure Modes
3. Decision Authority (what you decide vs ask Nelson vs never do)
4. Responsibilities
5. Constraints (what you explicitly cannot do)
6. Artifacts
7. Workflow (primary work loop)
8. Communication (receive work, report back, escalate)
9. Cross-Role Boundaries
10. Role-Specific Sections

Templates are 100-200 lines each. They provide the skeleton. Project-specific detail gets added over time as the project evolves.

### 3.5 Document Templates

Four inline string constants for the project documents. Same pattern as role templates: Go string constants with `{{variable}}` substitution.

```go
// internal/roles/doctemplates.go

const PRDTemplate = `# {{project_name}} PRD

The "why" companion to spec.md (the what) and systemdesign.md (the how). Hard cap: 5000 lines.

---

## 1. Problem Statement

### 1.1 The Problem
<!-- What pain exists today? Who feels it? Why hasn't it been solved? -->

### 1.2 Why Now
<!-- What changed to make this the right time? -->

---

## 2. User

### 2.1 Primary User
<!-- Who is this for? Technical proficiency, domain, environment. -->

### 2.2 Secondary Users (Future)
<!-- Who might use this later? Not a priority for MVP. -->

---

## 3. Success Criteria

### 3.1 Core Success
<!-- 3-5 concrete conditions that define "this worked." -->

### 3.2 Measurable Checks
<!-- Observable, testable validation criteria. -->

---

## 4. Non-Goals
<!-- Things this project explicitly does not do. -->

---

## 5. User Journeys
<!-- Step-by-step scenarios showing actual CLI usage or interaction. -->

---

## 6. Risks
<!-- What could go wrong? Mitigation for each. -->

---

## 7. Scope Boundaries

### 7.1 MVP Scope (Build This)
### 7.2 Post-MVP (Build Later, If Needed)
### 7.3 Never Build
`

const SpecTemplate = `# {{project_name}} Spec

Single source of truth for what {{project_name}} does. Hard cap: 5000 lines.

---

## 1. Core Model
<!-- What is the fundamental abstraction? How does the system work at the highest level? -->

---

## 2. Components
<!-- What are the major pieces? What does each one do? -->

---

## 3. Behaviors
<!-- What does the system do in response to user actions? Input -> Output for each. -->

---

## 4. Data Model
<!-- What data exists? Where does it live? What format? -->

---

## 5. Constraints
<!-- Hard limits, invariants, things that must always be true. -->
`

const SystemDesignTemplate = `# {{project_name}} System Design

The "how" companion to spec.md (the "what"). Hard cap: 5000 lines.

---

## 1. Module Structure
<!-- Package layout, dependency graph, interface boundaries. -->

---

## 2. Data Structures
<!-- Key types, config format, storage format. -->

---

## 3. Core Algorithms
<!-- Non-obvious logic. Template rendering, state machines, coordination. -->

---

## 4. Command Implementations
<!-- For each command: flow, inputs, outputs, error cases. -->

---

## 5. Testing Strategy
<!-- What gets tested, how, what the test boundaries are. -->

---

## 6. Build Order
<!-- What to build first, dependency chain, parallelizable work. -->
`

const RoadmapTemplate = `# {{project_name}} Roadmap

Strategic sequencing: milestones, phases, success gates. Hard cap: 5000 lines.

Beads handles the tactical layer (what's ready, who's assigned, what's blocked). This document captures the strategic layer.

---

## 1. Phases

### Phase 0: Discovery and Design

**Goal:** All four project documents are written and reviewed. The team has a shared understanding of what to build, why, how, and in what order.

**Work:**
1. PM writes prd.md (problem, users, success criteria, journeys)
2. Super orchestrates spec discovery (survey existing patterns, define behaviors)
3. Arch writes systemdesign.md (architecture, packages, interfaces, build order)
4. Super writes roadmap.md phases 1+ (milestones, gates, agent allocation)

**Success gate:** Nelson reviews all four documents. Team can answer: what are we building, why, how, and what ships first?

**Beads:** Create one epic for Phase 0. Four beads: Write PRD, Write Spec, Write System Design, Write Roadmap. PM and Arch can work in parallel once the problem statement is clear.

### Phase 1: [First Milestone]

**Goal:**
**Packages to build:**
**Success gate:**
**Beads:**

---

## 2. Milestone Summary
## 3. Agent Allocation
## 4. Risk Gates
`
```

Document templates are 30-60 lines each. The HTML comments inside each section serve as writing prompts that agents replace with actual content. The `roadmap.md` template is the only one with pre-filled content (Phase 0) because the discovery sequence is the same for every project.

---

## 4. Command Implementations

### 4.1 `initech init`

Bootstrap a new project.

**Flow:**

1. If `initech.yaml` exists, load it. Otherwise, run interactive setup:
   - Project name (default: current directory name)
   - Project root (default: `~/Desktop/Projects/<name>`)
   - Code repo URL(s)
   - Roles (default: core7 = super, pm, eng1, eng2, qa1, qa2, shipper)
   - Beads prefix (default: first 2-3 chars of project name)
   - Write `initech.yaml`

2. Validate config.

3. Create project root: `os.MkdirAll(root, 0755)`

4. Initialize git: `git init` (skip if `.git/` exists)

5. Write `.gitignore` from template.

6. Initialize beads: `bd init` then `bd config set issue-prefix <prefix>` (skip if `bd` not found, print warning)

7. For each role:
   - Create `<role>/` directory
   - Write `<role>/CLAUDE.md` from rendered template
   - Create `<role>/.claude/` directory
   - If NeedsSrc: `git submodule add <repo-url> <role>/src`
   - If NeedsPlaybooks: create `<role>/playbooks/`
   - If NeedsMakefile: write `<role>/Makefile` from template

8. Write root `CLAUDE.md` from project template.

9. Write root `AGENTS.md` from agents template.

10. Scaffold project documents:
    - Create `docs/` directory
    - Write `docs/prd.md` from PRDTemplate
    - Write `docs/spec.md` from SpecTemplate
    - Write `docs/systemdesign.md` from SystemDesignTemplate
    - Write `docs/roadmap.md` from RoadmapTemplate

11. Generate and write tmuxinator configs to `~/.config/tmuxinator/`.

12. Initial commit: `git add -A && git commit -m "initech: bootstrap <project>"`

13. Print summary.

**Idempotency:** Every file write checks `os.Stat()` first. Existing files are not overwritten unless `--force` is passed.

**Graceful degradation:** If `bd` is not installed, skip beads init and print warning. If `tmuxinator` is not installed, still generate the YAML (it's just a file) and print instructions.

### 4.2 `initech up`

Start the tmux session.

```
1. Load initech.yaml
2. Check tmux session exists
   - If yes: error with hint ("use tmux attach or initech down first")
3. Run: tmuxinator start <project>
4. Wait 2s for windows to init
5. Print: "Session '<project>' started with N agents"
6. If --grid: also run tmuxinator start <project>-grid
```

### 4.3 `initech down`

Stop the session with safety checks.

```
1. Load initech.yaml
2. Check session exists (if not: "not running")
3. For each role with src/:
   - git -C <role>/src status --porcelain
   - If dirty: print warning
4. If warnings and no --force: abort with hint
5. Run: tmuxinator stop <project>
6. If grid running: tmuxinator stop <project>-grid
7. Print: "Session stopped."
```

### 4.4 `initech status`

Show running agents and their bead status.

```
1. Load initech.yaml
2. Check session exists (if not: "not running, use initech up")
3. tmux list-windows -t <project> -F "#{window_index}|#{window_name}|#{pane_current_command}"
4. For each window: detect if Claude is running (port gastools pattern)
5. If bd available: bd list --status in_progress --json
   - Match agents to beads by assignee field
6. Print status table:

   Session: beadbox (running, 9 agents)

     Role      Claude  Bead                              Status
     super     yes     -                                 -
     eng1      yes     bb-3fa.5 (Settings dialog)        in_progress
     eng2      yes     bb-3fa.6 (Theme picker)           in_progress
     qa1       yes     bb-3fa.3 (Feedback form)          in_qa
     qa2       no      -                                 agent down
     shipper   yes     -                                 idle
```

### 4.5 `initech restart <role>`

Kill and restart a specific agent.

```
1. Load initech.yaml
2. Validate <role> exists in config
3. Check session exists
4. tmux kill-window -t <project>:<role>
5. tmux new-window -t <project> -n <role>
6. Build startup command from config/catalog (dir, permission flag)
7. tmux send-keys -t <project>:<role> "cd <dir> && claude --continue [flags]" Enter
8. sleep 5
9. If --bead <id>: gn -w <role> "[from initech] Restarted. Resume <id>."
10. Print: "Restarted <role>."
```

### 4.6 `initech standup`

Generate morning standup from beads.

```
1. Load initech.yaml
2. Calculate yesterday's date
3. Run bd queries (all --json):
   - bd list --closed-after <yesterday> --all --limit 0  (shipped)
   - bd list --status in_progress                         (active)
   - bd list --ready --limit 5                            (next up)
4. Parse JSON
5. Print formatted standup:

   ## beadbox Daily - 2026-02-15

   ### What's New
   - bb-3fa.1: Settings dialog shell (shipped)

   ### In Progress
   - bb-3fa.5: Settings dialog (eng1, in_progress)
   - bb-3fa.6: Theme picker (eng2, in_progress)

   ### Next Up
   - bb-3fa.7: Export functionality
```

---

## 5. Tmuxinator Generation

### 5.1 Main Session

Given a config, produce tmuxinator YAML matching the pattern from spec section 1.2.

```go
type TmuxinatorConfig struct {
    Name                string           `yaml:"name"`
    Root                string           `yaml:"root"`
    PreWindow           string           `yaml:"pre_window,omitempty"`
    OnProjectFirstStart string           `yaml:"on_project_first_start"`
    StartupWindow       string           `yaml:"startup_window"`
    Windows             []map[string]any `yaml:"windows"`
}
```

**Generation rules:**
- `pre_window` built from `cfg.Env` (one `export K=V` per line)
- `startup_window` is the first role in the list (convention: super)
- Each window gets one pane with `cd <dir> && claude --continue [--dangerously-skip-permissions]`
- Permission flag comes from the role catalog
- `on_project_first_start` includes the duplicate session guard

**Output path:** `~/.config/tmuxinator/<project>.yml`

### 5.2 Grid Companion

```yaml
name: <project>-grid
startup_window: grid
windows:
  - grid:
      layout: tiled
      panes:
        - tmux attach -t <project>:super
        - tmux attach -t <project>:eng1
        ...
```

Uses `cfg.Grid` for the subset. If grid is empty, omit the grid config entirely.

**Output path:** `~/.config/tmuxinator/<project>-grid.yml`

---

## 6. Scaffold Details

### 6.1 File Permissions

| Type | Mode | Rationale |
|------|------|-----------|
| Directories | 0755 | Standard, readable by all |
| CLAUDE.md, AGENTS.md | 0644 | Documentation, readable |
| .gitignore | 0644 | Config, readable |
| initech.yaml | 0644 | Project config, readable |
| Makefile | 0644 | Build config, readable |
| .beads/config.yaml | 0600 | May contain sensitive config |

### 6.2 .gitignore Template

```gitignore
# Source code lives in submodules
node_modules/
.next/
target/
bin/
dist/

# Beads runtime (JSONL is tracked, DB is not)
.beads/*.db
.beads/*.db-wal
.beads/*.db-shm
.beads/daemon*.log*
.beads/*.lock

# Local agent config
*/.mcp.json
*/.claude/

# OS artifacts
.DS_Store
*.swp
```

### 6.3 Root CLAUDE.md Template

Generated with project-specific values. Contains:
- Project identity and team composition
- Folder structure diagram (from role list)
- Communication protocols (gn/ga/gp reference)
- Bead lifecycle and bd command reference
- Status workflow diagram

### 6.4 AGENTS.md Template

Quick-reference cheatsheet. Contains:
- bd command reference (ready, show, update, close, comments)
- Landing the plane checklist
- Common workflows (claim, complete, dispatch)

---

## 7. tmux Integration

### 7.1 Ported from gastools

The `internal/tmux` package reuses patterns from `gastools/gasnudge/internal/tmux/tmux.go`:

```go
type Window struct {
    Index   int
    Name    string
    PaneID  string
    Command string
}

func run(args ...string) (string, error)          // exec tmux command
func SessionExists(name string) bool               // has-session
func ListWindows(session string) ([]Window, error)  // list-windows with format
func IsClaudeRunning(w Window) bool                 // process detection
func KillWindow(session, window string) error       // kill-window
func NewWindow(session, window string) error         // new-window
func SendKeys(target, text string) error             // send-keys with retry
```

**Claude detection** (matching gastools exactly):
1. `pane_current_command` is `node` or `claude`
2. Command matches version pattern `^\d+\.\d+\.\d+$`
3. If pane runs a shell, check child processes via `pgrep -P`

### 7.2 bd Integration

Shell out to `bd` CLI. JSON output is the stable contract.

```go
// Query beads via bd CLI
func ListInProgress(runner exec.Runner) ([]Bead, error) {
    out, err := runner.Run("bd", "list", "--status", "in_progress", "--json")
    // parse JSON...
}
```

If `bd` is not in PATH, degrade gracefully: status shows "beads: unavailable", standup prints "bd not found".

---

## 8. Build Order

Priority: get `initech init` + `initech up` working end-to-end first. Everything else layers on top.

### Phase 1: Core (init + up)

| Order | Package | What | Why first |
|-------|---------|------|-----------|
| 1 | `internal/exec` | Command runner | Leaf dep, everything needs it |
| 2 | `internal/config` | Config types, Load, Validate | Data structures everything uses |
| 3 | `internal/roles/catalog` | Role definitions, LookupRole | Scaffold and tmuxinator need it |
| 4 | `internal/roles/render` | {{variable}} substitution | Templates need it |
| 5 | `internal/roles/templates` | SuperTemplate + EngTemplate only | Prove the pattern with 2 templates |
| 6 | `internal/tmuxinator` | Generate session YAML | initech up needs it |
| 7 | `internal/scaffold` | Create dirs, write files | initech init needs it |
| 8 | `internal/git` | git init, submodule add, commit | initech init needs it |
| 9 | `cmd/root` + `cmd/init` | Wire up init command | First working command |
| 10 | `cmd/up` | tmuxinator start wrapper | First working session |

**Milestone:** `initech init myproject && initech up` starts a tmux session with agents.

### Phase 2: Visibility (status + down)

| Order | Package | What |
|-------|---------|------|
| 11 | `internal/tmux` | Session/window inspection, Claude detection |
| 12 | `cmd/status` | Agent status table with optional bead info |
| 13 | `cmd/down` | Stop with uncommitted work warnings |

### Phase 3: Operations (restart + standup)

| Order | Package | What |
|-------|---------|------|
| 14 | `cmd/restart` | Kill/restart agent with optional bead re-dispatch |
| 15 | `cmd/standup` | Morning standup from beads |

### Phase 4: Content (remaining templates)

| Order | What |
|-------|------|
| 16 | QATemplate, PMTemplate, ShipperTemplate |
| 17 | ArchTemplate, SecTemplate, PMMTemplate |
| 18 | WriterTemplate, OpsTemplate, GrowthTemplate |

### Phase 5: Polish

| Order | What |
|-------|------|
| 19 | Grid generation + `--grid` flag |
| 20 | Interactive init flow (no existing initech.yaml) |
| 21 | goreleaser config + homebrew formula |
| 22 | `--force` flag on down, `--bead` flag on restart |

---

## 9. Testing Strategy

### 9.1 Unit Tests Per Package

Every package gets `_test.go` files. The `exec.Runner` interface enables testing without real external tools.

**`internal/exec`** - Real runner tests with simple commands (`echo`, `true`, `false`). Also provides `FakeRunner` that records calls.

**`internal/config`** - Table-driven:
- Valid config round-trips (marshal/unmarshal)
- Missing required fields fail validation
- Defaults applied correctly
- Role overrides merge with catalog

**`internal/roles`** - Render tests:
- All variables substituted
- Missing variables left as `{{var}}`
- Catalog lookup: known roles return correct metadata, unknown roles return defaults

**`internal/tmuxinator`** - Golden file comparison:
- Minimal config produces correct YAML
- Env vars in pre_window
- Permission tiers produce correct flags
- Grid config generates tiled layout

**`internal/scaffold`** - Uses `t.TempDir()`:
- Creates expected directory tree
- Second run is idempotent
- `--force` overwrites existing
- NeedsSrc/NeedsPlaybooks respected

**`internal/tmux`** - Fake runner:
- SessionExists parses tmux output correctly
- ListWindows parses format string
- IsClaudeRunning handles all detection cases

**`internal/git`** - Fake runner:
- Correct args for init, submodule add, commit

### 9.2 Integration Test

One test that runs the full init flow:
- Temp directory
- Minimal initech.yaml
- Run scaffold (with fake runner for git/bd)
- Verify complete directory tree
- Verify CLAUDE.md content has project-specific values
- Verify tmuxinator YAML structure

### 9.3 What We Don't Test

- Actual tmux sessions (requires tmux running)
- Actual bd CLI (requires beads installed)
- Actual tmuxinator (requires it installed)
- Actual git submodule operations (slow, network-dependent)

All of these are behind the `exec.Runner` interface. Production uses the real runner. Tests use fakes.

---

## 10. Design Decisions

### 10.1 Single config file

`initech.yaml` holds the full project definition. Every command reads the same file. Alternatives considered: per-command configs, XDG directories, dotfile conventions. Rejected because: a single file at the project root is discoverable, version-controlled, and matches tmuxinator's pattern.

### 10.2 Open role catalog

Unknown role names are valid. `LookupRole("designer")` returns a default. The catalog provides good defaults, not a closed set. This means users can define custom roles without modifying initech's source.

### 10.3 Shell out to bd

`bd` integration via CLI, not library import. Reasons: (1) bd's internal API isn't stable, (2) initech stays decoupled from bd's release cycle, (3) `bd list --json` is a stable contract. The `exec.Runner` wraps all external calls.

### 10.4 Inline string templates

Templates are Go string constants, not embedded files. Matching the beads pattern. They're grep-able, compile-checked, and version-controlled with no build step. Templates don't need conditionals or loops; if they do, they're doing too much.

### 10.5 Regex substitution over text/template

`{{variable}}` replaced via `regexp.ReplaceAllStringFunc`. Simpler, no runtime parse errors, no conditional/loop complexity. Matches beads pattern exactly.

### 10.6 Two submodule patterns

Config supports single-repo (all agents share one URL) and multi-repo (role_overrides.repo_name selects). Handles beadbox (single repo) and cobalt (different repos per role).

### 10.7 Dir override for naming divergence

`role_overrides[role].dir` lets the tmux window name differ from the directory name. The window name (the role) is canonical for messaging. The directory can be anything. Handles the cobalt pattern (pm -> product/).

### 10.8 Graceful degradation

If `bd` is not installed: skip beads init, print warning. If `tmuxinator` is not installed: generate the YAML anyway, print instructions. The scaffold works without external tools.

---

## 11. Release and Distribution

### 11.1 goreleaser

Follow the gastools pattern:

```yaml
version: 2
project_name: initech

builds:
  - binary: initech
    env: [CGO_ENABLED=0]
    goos: [darwin, linux]
    goarch: [amd64, arm64]
    ldflags: [-s -w -X main.Version={{.Version}}]

archives:
  - format: tar.gz
    name_template: "{{.ProjectName}}_{{.Os}}_{{.Arch}}"

brews:
  - repository:
      owner: nmelo
      name: homebrew-tap
    directory: Formula
    description: Bootstrap and manage multi-agent development sessions
    install: bin.install "initech"
```

### 11.2 Install

```bash
brew tap nmelo/tap && brew install initech
```

Or from source:

```bash
go install github.com/nmelo/initech@latest
```

---

## 12. Future Considerations

Not in MVP scope, but noted for later:

- **`initech config`** subcommand for editing initech.yaml without a text editor
- **`initech add-role <name>`** to add a role to an existing project
- **`initech remove-role <name>`** to remove a role
- **`initech dispatch <bead-id> <role>`** to format and send dispatch messages
- **`initech patrol`** to peek all agents and report status
- **Plugin system** for custom role templates beyond the built-in catalog
- **Remote session support** for running agents on remote machines (workbench-linux, etc.)
- **Metrics** collection on agent throughput, bead cycle time, etc.
