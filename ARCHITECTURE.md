# Initech Architecture

Engineering reference for the initech codebase. Covers package structure, dependency graph, data flows, key types, and extension points. Last verified against 97 tests across 7 packages, all passing.

---

## 1. System Overview

Initech is a Go CLI that codifies a multi-agent development pattern where a tmux session acts as a virtual software company. Each tmux window runs an autonomous Claude Code agent with a defined role (engineer, QA, PM, etc.). Initech automates three things that were previously manual:

1. **Project bootstrap** (`init`): creates the directory tree, role-specific CLAUDE.md files, git submodules, tmuxinator configs, beads database, and project documents from a single `initech.yaml` config file.

2. **Session lifecycle** (`up`, `down`, `start`, `stop`, `restart`): starts/stops tmux sessions via tmuxinator, manages individual agent windows, and handles Claude process startup with the correct permission tiers.

3. **Observability** (`status`, `standup`, `doctor`): shows which agents are running, what bead each is working on, process memory consumption, prerequisite health checks, and daily standup generation from beads.

The tool shells out to external CLIs (tmux, tmuxinator, git, bd, gn/gp) rather than reimplementing their functionality. All external calls go through the `exec.Runner` interface, which makes the entire codebase testable without any of those tools installed.

Two direct dependencies: `github.com/spf13/cobra` (CLI framework) and `gopkg.in/yaml.v3` (config and tmuxinator YAML). Go 1.25.

---

## 2. Package Dependency Graph

```
main.go
  |
  v
cmd/           (composition root: wires packages together)
  |
  +---> internal/config       (zero internal deps)
  |
  +---> internal/roles        (zero internal deps)
  |
  +---> internal/scaffold ---> internal/config
  |                       \--> internal/roles
  |
  +---> internal/tmuxinator -> internal/config
  |                        \-> internal/roles
  |
  +---> internal/tmux -------> internal/exec
  |
  +---> internal/git --------> internal/exec
  |
  +---> internal/exec         (leaf dependency, zero internal deps)
```

Rules enforced by the structure:

- **No cycles.** Internal packages never import each other except through `exec`.
- **`internal/exec` is the leaf.** Every package that shells out depends on Runner, not on `os/exec` directly.
- **`cmd/` is the only composition root.** It imports multiple internal packages and wires them together. Internal packages never import `cmd/`.
- **`config` and `roles` have zero internal dependencies.** They are pure data/logic packages.
- **`scaffold` and `tmuxinator` depend on `config` and `roles`** but not on each other, and not on `exec`, `tmux`, or `git`.

---

## 3. Data Flow Diagrams

### 3.1 `initech init`

```
User runs: initech init
            |
            v
    +------------------+
    | Load or create   |  config.Load() or interactiveSetup()
    | initech.yaml     |  -> config.Validate()
    +------------------+
            |
            v
    +------------------+
    | Scaffold project |  scaffold.Run(project, opts)
    | directory tree   |  Creates: .gitignore, CLAUDE.md, AGENTS.md,
    +------------------+  docs/{prd,spec,systemdesign,roadmap}.md,
            |             {role}/CLAUDE.md, {role}/.claude/,
            |             {role}/playbooks/ (if NeedsPlaybooks)
            v
    +------------------+
    | git init         |  git.Init(runner, root)
    +------------------+  Skips if .git/ already exists
            |
            v
    +------------------+
    | Add submodules   |  git.AddSubmodule(runner, root, url, "role/src")
    | for NeedsSrc     |  For each role where def.NeedsSrc == true
    +------------------+  Uses first repo URL, or role_overrides.repo_name
            |
            v
    +------------------+
    | Init beads       |  runner.Run("bd", "init")
    | (if bd exists)   |  runner.Run("bd", "config", "set", "issue-prefix", prefix)
    +------------------+  Graceful degradation: prints warning if bd not found
            |
            v
    +------------------+
    | Generate         |  tmuxinator.Generate(project) -> main YAML
    | tmuxinator YAML  |  tmuxinator.GenerateGrid(project) -> grid YAML
    +------------------+  Written to ~/.config/tmuxinator/{name}.yml
            |
            v
    +------------------+
    | Initial commit   |  git.CommitAll(runner, root, "initech: bootstrap {name}")
    +------------------+
            |
            v
    Print summary of created files
```

### 3.2 `initech up`

```
User runs: initech up
            |
            v
    config.Discover(cwd) -> config.Load(path)
            |
            v
    Verify ~/.config/tmuxinator/{name}.yml exists
            |
            v
    os/exec.Command("tmuxinator", "start", name)
    with Stdin/Stdout/Stderr attached to terminal
            |
            v
    Print: "Session '{name}' started with N agents."
```

Note: `up` uses `os/exec` directly (not `exec.Runner`) because tmuxinator needs the real terminal for session creation. This is the only command that bypasses the Runner abstraction.

### 3.3 `initech down`

```
User runs: initech down [--force]
            |
            v
    config.Discover -> config.Load
            |
            v
    tmux.SessionExists(runner, name)?
    No  -> print "not running", exit
    Yes -> continue
            |
            v
    For each role with NeedsSrc:
        runner.RunInDir(role/src, "git", "status", "--porcelain")
        If dirty -> collect warning
            |
            v
    Warnings exist && !force?
    Yes -> print warnings, abort
    No  -> continue
            |
            v
    os/exec.Command("tmuxinator", "stop", name)
            |
            v
    tmux.SessionExists(runner, name+"-grid")?
    Yes -> os/exec.Command("tmuxinator", "stop", name+"-grid")
            |
            v
    Print: "Session '{name}' stopped."
```

### 3.4 `initech status`

```
User runs: initech status
            |
            v
    config.Discover -> config.Load
            |
            v
    tmux.SessionExists?
    No  -> "not running, use initech up"
    Yes -> continue
            |
            v
    tmux.ListWindows(runner, name)
        -> parses: #{window_index}|#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}
            |
            v
    Build windowMap[name] -> Window
            |
            v
    getBeadAssignments(runner)
        -> runner.Run("which", "bd")
        -> runner.Run("bd", "list", "--status", "in_progress", "--json")
        -> JSON unmarshal -> map[assignee] -> beadInfo
            |
            v
    For each role in config:
        +-- Window exists?
        |   No  -> "stopped"
        |   Yes -> tmux.IsClaudeRunning(runner, window)?
        |       No  -> "agent down"
        |       Yes -> check beadMap[role]
        |           Found  -> show bead ID, title, status
        |           Absent -> "idle"
        |       +-> tmux.GetProcessMemory(runner, window)
            |
            v
    Print table:
      Role    Claude  Bead                     Status        Mem
      super   yes     -                        -          1.3 GB
      eng1    yes     bb-3fa.5 (Settings)      in_progress 2.1 GB
      ...
```

### 3.5 `initech restart <role>`

```
User runs: initech restart eng1 [--bead bb-xxx]
            |
            v
    config.Discover -> config.Load
    Validate role exists in config
    tmux.SessionExists? -> must be true
            |
            v
    tmux.KillWindow(runner, session, role)    // ignore error if window gone
            |
            v
    tmux.NewWindow(runner, session, role)
            |
            v
    time.Sleep(500ms)                         // let shell initialize
            |
            v
    Build startup command:
        "cd {root}/{role} && (claude --continue [--dangerously-skip-permissions]
         || claude [--dangerously-skip-permissions])"
    Permission flag from roles.LookupRole(role).Permission
            |
            v
    tmux.SendKeys(runner, "session:role", startupCmd)
            |
            v
    If --bead provided:
        time.Sleep(5s)                        // let Claude initialize
        tmux.SendKeys(runner, target, "[from initech] Restarted. Resume {bead}.")
```

---

## 4. Package Reference

### 4.1 `internal/exec` -- Command Runner

**File:** `internal/exec/exec.go`, `internal/exec/fake.go`

**Responsibility:** Wraps `os/exec` with consistent error handling. This is the project's primary testing seam.

**Public API:**

```go
// Runner is the interface every package uses for external commands.
type Runner interface {
    Run(name string, args ...string) (string, error)
    RunInDir(dir, name string, args ...string) (string, error)
}

// DefaultRunner shells out to real binaries via os/exec.
type DefaultRunner struct{}

// FakeRunner records commands for testing. Not used in production.
type FakeRunner struct {
    Calls  []string  // recorded as "dir|name arg1 arg2"
    Output string    // returned for every call
    Err    error     // returned for every call
}
```

**Call recording format:** `FakeRunner.Calls` entries look like `"/project|git add -A"` (dir, pipe separator, command). When `Run` (not `RunInDir`) is used, the dir is empty: `"|git status"`.

**Error format:** `DefaultRunner` wraps errors as `"{name} {args}: {err}\n{stderr}"`.

**Tests:** 7 tests covering Run, RunInDir, error wrapping, output trimming, and FakeRunner recording.

### 4.2 `internal/config` -- Configuration

**File:** `internal/config/config.go`

**Responsibility:** Owns the `initech.yaml` schema. Reads, parses, validates, writes, and discovers config files. Knows nothing about tmux, git, scaffold, or roles.

**Key types:**

```go
type Project struct {
    Name          string                  `yaml:"project"`
    Root          string                  `yaml:"root"`
    Repos         []Repo                  `yaml:"repos,omitempty"`
    Env           map[string]string       `yaml:"env,omitempty"`
    Beads         BeadsConfig             `yaml:"beads,omitempty"`
    Roles         []string                `yaml:"roles"`
    Grid          []string                `yaml:"grid,omitempty"`
    RoleOverrides map[string]RoleOverride `yaml:"role_overrides,omitempty"`
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
    Dir       string `yaml:"dir,omitempty"`       // override directory name
    RepoName  string `yaml:"repo_name,omitempty"` // select specific repo for submodule
}
```

**Public API:**

```go
func Load(path string) (*Project, error)      // read + parse + expand ~
func Discover(dir string) (string, error)     // walk upward to find initech.yaml
func Validate(p *Project) error               // check required fields, no dupes, grid/overrides subset
func Write(path string, p *Project) error     // serialize to YAML and write
```

**Config discovery order:**
1. `./initech.yaml` in current directory
2. Walk upward to filesystem root (like `.git` discovery)

**Validation rules:**
- `Name` required, non-empty
- `Root` required, non-empty
- `Roles` required, at least one, no duplicates
- `Grid` entries must be a subset of `Roles`
- `RoleOverrides` keys must be in `Roles`

**Home expansion:** `~` prefix in `Root` is expanded to `os.UserHomeDir()` at load time.

**Tests:** 12 tests covering load, write, round-trip, home expansion, discovery (upward walk), and all validation rules.

### 4.3 `internal/roles` -- Role Catalog, Templates, and Rendering

**Files:** `catalog.go`, `render.go`, `templates.go`, `doctemplates.go`

**Responsibility:** Three things: (1) the catalog of well-known role definitions, (2) the template rendering engine, (3) the inline template constants for 11 roles and 4 document types.

#### Catalog (`catalog.go`)

```go
type PermissionTier int
const (
    Supervised PermissionTier = iota  // super, shipper
    Autonomous                         // everything else
)

type RoleDef struct {
    Name           string
    Permission     PermissionTier
    NeedsSrc       bool           // gets a git submodule at role/src/
    NeedsPlaybooks bool           // gets a playbooks/ directory
    NeedsMakefile  bool           // gets a Makefile
}

var Catalog = map[string]RoleDef{ ... }  // 13 entries

func LookupRole(name string) RoleDef    // known -> catalog entry; unknown -> Autonomous default
```

**Catalog entries (13 roles):**

| Role | Permission | NeedsSrc | NeedsPlaybooks | NeedsMakefile |
|------|-----------|----------|---------------|--------------|
| super | Supervised | false | false | false |
| eng1 | Autonomous | true | false | true |
| eng2 | Autonomous | true | false | true |
| qa1 | Autonomous | true | true | false |
| qa2 | Autonomous | true | true | false |
| shipper | Supervised | true | true | false |
| pm | Autonomous | false | false | false |
| pmm | Autonomous | false | false | false |
| arch | Autonomous | false | false | false |
| sec | Autonomous | false | false | false |
| writer | Autonomous | false | false | false |
| ops | Autonomous | false | true | false |
| growth | Autonomous | true | false | false |

**Open catalog design:** `LookupRole("designer")` returns `RoleDef{Name: "designer", Permission: Autonomous}` with all booleans false. Unknown roles are valid. This lets users define custom roles without modifying initech.

#### Rendering (`render.go`)

```go
var varPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

type RenderVars struct {
    ProjectName string  // {{project_name}}
    ProjectRoot string  // {{project_root}}
    RepoURL     string  // {{repo_url}}
    TechStack   string  // {{tech_stack}}
    BuildCmd    string  // {{build_cmd}}
    TestCmd     string  // {{test_cmd}}
    BeadsPrefix string  // {{beads_prefix}}
}

func Render(tmpl string, vars RenderVars) string       // regex substitution
func RenderString(tmpl string, key, value string) string // single variable replacement
```

**Rendering behavior:**
- Known variables with non-empty values are replaced.
- Empty values and unknown variables are left as `{{var}}` (preserved for later substitution or as documentation).
- No conditionals, no loops, no runtime parse errors.
- `RenderString` is used separately for `{{role_name}}` substitution, which is not in `RenderVars` because it varies per role within the same project.

#### Role Templates (`templates.go`)

11 inline Go string constants, one per role:

| Constant | Role | Key Sections |
|----------|------|-------------|
| `SuperTemplate` | Coordinator | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Communication, Bead Lifecycle, Project Documents, Tools |
| `EngTemplate` | Engineer (eng1, eng2, ...) | Identity, Critical Failure Modes, Decision Authority, Workflow, Code Quality, Communication, Tech Stack |
| `QATemplate` | QA (qa1, qa2, ...) | Identity, Critical Failure Modes, Workflow, Verdict Rules, Communication |
| `PMTemplate` | Product Manager | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Artifacts, Communication |
| `ArchTemplate` | Architect | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Artifacts, Communication |
| `SecTemplate` | Security | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Artifacts, Communication |
| `ShipperTemplate` | Release | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Workflow, Communication |
| `PMMTemplate` | Product Marketing | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Communication |
| `WriterTemplate` | Technical Writer | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Communication |
| `OpsTemplate` | Operations | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Communication |
| `GrowthTemplate` | Growth Engineer | Identity, Critical Failure Modes, Decision Authority, Responsibilities, Communication |

All templates use `{{project_name}}`, `{{project_root}}`, and `{{role_name}}` placeholders. Engineer and growth templates additionally use `{{tech_stack}}`, `{{build_cmd}}`, `{{test_cmd}}`. Shipper template uses `{{project_root}}/{{role_name}}/playbooks/`.

#### Document Templates (`doctemplates.go`)

4 inline string constants for the `docs/` directory:

| Constant | File | Sections |
|----------|------|----------|
| `PRDTemplate` | docs/prd.md | Problem Statement, User, Success Criteria, Non-Goals, User Journeys, Risks, Scope Boundaries |
| `SpecTemplate` | docs/spec.md | Core Model, Components, Behaviors, Data Model, Constraints |
| `SystemDesignTemplate` | docs/systemdesign.md | Module Structure, Data Structures, Core Algorithms, Command Implementations, Testing Strategy, Build Order |
| `RoadmapTemplate` | docs/roadmap.md | Phase 0 (pre-filled), Phase 1 (skeleton), Milestone Summary, Agent Allocation, Risk Gates |

The roadmap template is the only one with pre-filled content (Phase 0: Discovery and Design). All other templates contain HTML comment prompts that agents replace with project-specific content.

**Tests:** 28 tests total covering catalog completeness, permission tiers, NeedsSrc/NeedsPlaybooks/NeedsMakefile flags, lookup for known and unknown roles, render with full/partial/no variables, unknown variable preservation, all 11 role templates rendering, and all 4 document templates rendering.

### 4.4 `internal/scaffold` -- Directory Tree Creation

**File:** `internal/scaffold/scaffold.go`

**Responsibility:** Creates the project directory tree on disk from a config. Writes all files. Does not know about git, tmux, beads, or tmuxinator.

**Public API:**

```go
type Options struct {
    Force bool  // overwrite existing files
}

func Run(p *config.Project, opts Options) ([]string, error)
```

`Run` returns a list of relative paths that were created (for user-facing summary output).

**What it creates:**

- `{root}/` directory
- `{root}/.gitignore` (from inline constant `gitignoreContent`)
- `{root}/CLAUDE.md` (generated by `renderRootCLAUDE(p)`)
- `{root}/AGENTS.md` (from inline constant `agentsContent`)
- `{root}/docs/prd.md` (from `PRDTemplate`)
- `{root}/docs/spec.md` (from `SpecTemplate`)
- `{root}/docs/systemdesign.md` (from `SystemDesignTemplate`)
- `{root}/docs/roadmap.md` (from `RoadmapTemplate`)
- For each role:
  - `{root}/{role}/` directory
  - `{root}/{role}/.claude/` directory
  - `{root}/{role}/CLAUDE.md` (from role-appropriate template)
  - `{root}/{role}/playbooks/` (if `NeedsPlaybooks`)

**Idempotency:** `writeFile()` checks `os.Stat()` before writing. Existing files are skipped unless `Force == true`. This lets users safely re-run `initech init` without losing customizations.

**Template selection logic (`templateForRole`):**

```
"super"       -> SuperTemplate
"pm"          -> PMTemplate
"arch"        -> ArchTemplate
"sec"         -> SecTemplate
"shipper"     -> ShipperTemplate
"pmm"         -> PMMTemplate
"writer"      -> WriterTemplate
"ops"         -> OpsTemplate
"growth"      -> GrowthTemplate
prefix "qa"   -> QATemplate     (qa1, qa2, qa3, ...)
prefix "eng"  -> EngTemplate    (eng1, eng2, eng3, ...)
default       -> EngTemplate    (unknown roles get engineer template)
```

**Role variable substitution:** `Render(tmpl, vars)` handles standard variables, then `RenderString(content, "role_name", roleName)` handles the per-role `{{role_name}}` substitution.

**Override application:** If `RoleOverrides[role]` exists, its `TechStack`, `BuildCmd`, and `TestCmd` fields override the project-level defaults in `RenderVars` before template rendering.

**Important:** Scaffold does NOT create `{role}/src/` directories. Those are created by `git submodule add` in the init command. Creating them in scaffold would cause submodule add to fail with "already exists and is not a valid git repo."

**Tests:** 10 tests covering directory creation, file creation, root CLAUDE.md content, role template selection (super, eng, qa), document template content, idempotency, and force overwrite.

### 4.5 `internal/tmuxinator` -- YAML Generation

**File:** `internal/tmuxinator/tmuxinator.go`

**Responsibility:** Generates tmuxinator YAML configs from project config. Produces byte slices. Does not write files to disk and does not interact with tmux at runtime.

**Public API:**

```go
func Generate(p *config.Project) ([]byte, error)      // main session YAML
func GenerateGrid(p *config.Project) ([]byte, error)   // grid session YAML (nil if no grid)
```

**Internal type:**

```go
type session struct {
    Name      string            `yaml:"name"`
    Root      string            `yaml:"root"`
    PreWindow string            `yaml:"pre_window,omitempty"`
    Windows   []map[string]any  `yaml:"windows"`
}
```

**Generation rules:**
- Session name = project name
- `pre_window` = `export BEADS_DIR={root}/.beads` (only if beads prefix is configured)
- Each role gets one window entry: `{roleName}: "claude --continue [--dangerously-skip-permissions]"`
- Permission flag from `roles.LookupRole(roleName).Permission`
- Grid session name = `{project}-grid`; only includes roles listed in `config.Grid`
- `GenerateGrid` returns `nil, nil` when grid is empty

**Claude command construction (`claudeCommand`):**

```go
func claudeCommand(def roles.RoleDef) string {
    if def.Permission == roles.Autonomous {
        return "claude --continue --dangerously-skip-permissions"
    }
    return "claude --continue"
}
```

**Tests:** 8 tests covering basic YAML structure, window count, permission tiers, pre_window/BEADS_DIR, empty grid, and unknown role handling.

### 4.6 `internal/tmux` -- Runtime Session Management

**File:** `internal/tmux/tmux.go`

**Responsibility:** tmux CLI interaction at runtime. Session inspection, window listing, Claude process detection, memory measurement, window creation/destruction, and key sending. Ported from gastools.

**Key type:**

```go
type Window struct {
    Index   int
    Name    string
    PaneID  string
    PanePID string
    Command string  // pane_current_command
}
```

**Public API:**

```go
func SessionExists(runner, name string) bool
func ListWindows(runner, session string) ([]Window, error)
func IsClaudeRunning(runner, w Window) bool
func GetClaudePID(runner, w Window) string
func GetProcessMemory(runner, w Window) uint64
func KillWindow(runner, session, window string) error
func NewWindow(runner, session, window string) error
func NewWindowWithCmd(runner, session, window, shellCmd string) error
func SendKeys(runner, target, text string) error
```

**Claude detection algorithm (`IsClaudeRunning`):**

1. If `pane_current_command` is `"node"` or `"claude"` -> true
2. If command matches version pattern `^\d+\.\d+\.\d+$` (e.g., "2.1.45") -> true
3. If command is a known shell (bash, zsh, sh, fish, tcsh, ksh) and pane has a PID -> check child processes via `pgrep -P {pid} -l` for "node" or "claude" children

**Memory measurement (`GetProcessMemory`):**
1. Find Claude PID via `GetClaudePID`
2. `ps -o rss= -p {pid}` for main process RSS (in KB)
3. `pgrep -P {pid}` to find child PIDs
4. Sum `ps -o rss=` for each child
5. Return total in bytes (KB * 1024)

**SendKeys implementation:** Sends text in literal mode (`-l` flag) followed by a separate `Enter` key. Two tmux commands per SendKeys call.

**ListWindows format string:** `#{window_index}|#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}` parsed by splitting on `|`.

**Tests:** 17 tests including a custom `fakeMultiRunner` (returns different output per sequential call) for the multi-step memory measurement flow.

### 4.7 `internal/git` -- Git Operations

**File:** `internal/git/git.go`

**Responsibility:** Git CLI interaction for project bootstrap. Init, submodule add, commit. Does not know about config, tmux, or scaffold.

**Public API:**

```go
func Init(runner, dir string) error                          // git init (no-op if .git/ exists)
func AddSubmodule(runner, repoDir, url, subPath string) error // git submodule add
func CommitAll(runner, dir, message string) error             // git add -A && git commit -m
```

**Init idempotency:** Checks for `.git/` directory existence before running `git init`. Returns nil if already a repo.

**Tests:** 7 tests covering init (new repo, existing repo, error), submodule add (success, error), and commit all (success, add error).

---

## 5. Command Reference

All commands are in `cmd/`. Each file registers itself with `rootCmd` via `init()`.

| File | Command | Args | Flags | Key Dependencies |
|------|---------|------|-------|-----------------|
| `root.go` | `initech` | - | - | - |
| `root.go` | `initech version` | - | - | `Version` (ldflags) |
| `doctor.go` | `initech doctor` | - | - | `os/exec.LookPath` |
| `init.go` | `initech init` | - | `--force` | config, scaffold, git, roles, tmuxinator |
| `up.go` | `initech up` | - | - | config |
| `down.go` | `initech down` | - | `--force` | config, exec, roles, tmux |
| `status.go` | `initech status` | - | - | config, exec, tmux |
| `stop.go` | `initech stop` | `<role> [role...]` | - | config, exec, tmux |
| `start.go` | `initech start` | `<role> [role...]` | `--bead` | config, exec, roles, tmux |
| `restart.go` | `initech restart` | `<role>` | `--bead` | config, exec, roles, tmux |
| `standup.go` | `initech standup` | - | - | config, exec |

**Version injection:** `cmd.Version` is set at build time via `ldflags`:
```
-X github.com/nmelo/initech/cmd.Version=$(VERSION)
```

**Config discovery pattern:** Most commands start with `config.Discover(cwd)` followed by `config.Load(path)`. The `init` command is special: it checks for `initech.yaml` in the current directory and either loads it or runs interactive setup.

**Terminal passthrough:** `up` and `down` use `os/exec.Command` directly (not `exec.Runner`) with `Stdin/Stdout/Stderr` attached to the real terminal, because tmuxinator needs TTY access for session creation/destruction.

---

## 6. Configuration System

### 6.1 initech.yaml Schema

```yaml
project: beadbox                        # required: project name
root: ~/Desktop/Projects/beadbox        # required: absolute path (~ expanded)

repos:                                  # optional: code repos for submodules
  - url: git@github.com:nmelo/beadbox.git
    name: beadbox

env:                                    # optional: env vars for all agent windows
  BEADS_DIR: ~/Desktop/Projects/beadbox/.beads

beads:                                  # optional: beads configuration
  prefix: bb                            # issue ID prefix

roles:                                  # required: at least one role
  - super                               # order determines tmux window order
  - pm
  - eng1
  - eng2
  - qa1
  - qa2
  - shipper

grid:                                   # optional: roles for monitoring view
  - super                               # must be subset of roles
  - eng1

role_overrides:                         # optional: per-role customization
  eng1:
    tech_stack: "Next.js 16, React 19"  # substituted into {{tech_stack}}
    build_cmd: "pnpm dev"               # substituted into {{build_cmd}}
    test_cmd: "pnpm test"               # substituted into {{test_cmd}}
    dir: "engineering"                   # override directory name (window name stays "eng1")
    repo_name: "frontend"               # select specific repo from repos list
```

### 6.2 Discovery

`config.Discover(dir)` walks upward from `dir` looking for `initech.yaml`. The walk stops at filesystem root. This mirrors how Git finds `.git/` directories, so running `initech status` from a subdirectory still works.

### 6.3 Validation

`config.Validate(p)` checks:
1. `Name` non-empty
2. `Root` non-empty
3. `Roles` non-empty, no duplicates
4. Every `Grid` entry is in `Roles`
5. Every `RoleOverrides` key is in `Roles`

Validation does NOT check that `Root` exists on disk (it may not yet during init). It also does not check that repo URLs are valid or that `dir` overrides resolve to real paths.

---

## 7. Role System

### 7.1 Role Lifecycle During Bootstrap

1. `initech init` reads `roles` from config
2. For each role name, `roles.LookupRole(name)` returns a `RoleDef`
3. `RoleDef` drives scaffold decisions:
   - `NeedsSrc` -> `git submodule add` creates `{role}/src/`
   - `NeedsPlaybooks` -> `os.MkdirAll` creates `{role}/playbooks/`
   - `NeedsMakefile` -> (defined in catalog but not currently created by scaffold)
4. `scaffold.templateForRole(name)` selects the CLAUDE.md template
5. `roles.Render(template, vars)` substitutes project variables
6. `roles.RenderString(content, "role_name", name)` substitutes the role name
7. `RoleOverrides` from config are applied before rendering

### 7.2 Permission Tiers at Runtime

Permission tiers affect the Claude startup command:

| Tier | Flag | Startup Command |
|------|------|----------------|
| Supervised | (none) | `claude --continue` |
| Autonomous | `--dangerously-skip-permissions` | `claude --continue --dangerously-skip-permissions` |

This flag is used in three places:
- `tmuxinator.claudeCommand()` for session startup YAML
- `cmd/restart.go` for restart startup command
- `cmd/start.go` for start startup command

All three construct the same fallback pattern: `(claude --continue [flags] || claude [flags])`. The `--continue` flag resumes prior conversation state from `.claude/`. If no conversation exists, `claude --continue` fails, and the fallback starts a fresh session.

### 7.3 Custom Roles

The open catalog means custom role names work everywhere:
- `LookupRole("designer")` returns a default RoleDef (Autonomous, no src, no playbooks)
- `templateForRole("designer")` returns `EngTemplate` as the fallback
- Scaffold creates `designer/CLAUDE.md` using the engineer template
- Tmuxinator generates a window named "designer" with `--dangerously-skip-permissions`

To make a custom role supervised, use `role_overrides` (not currently supported for permission tier, only for tech stack/build/test). This is a gap: the only way to change a custom role's permission tier would be to add it to the catalog in source code.

---

## 8. tmux/tmuxinator Integration

### 8.1 Session Management

Initech uses tmuxinator as the session management layer. It generates tmuxinator YAML configs and shells out to `tmuxinator start/stop` for lifecycle operations. Direct tmux interaction happens only for window-level operations (status, restart, stop, start individual agents).

**Session creation flow:**
```
initech init -> tmuxinator.Generate() -> writes ~/.config/tmuxinator/{name}.yml
initech up   -> os/exec("tmuxinator", "start", name) -> tmux session created
```

**Window-level operations (via internal/tmux):**
```
initech status  -> tmux list-windows -> parse Window structs
initech stop    -> tmux kill-window
initech start   -> tmux new-window + send-keys
initech restart -> tmux kill-window + new-window + send-keys
```

### 8.2 Claude Detection

The detection logic is ported from gastools and handles three cases:

1. **Direct match:** `pane_current_command` is `"node"` or `"claude"`. This is the common case when Claude Code is the foreground process.

2. **Version pattern:** `pane_current_command` matches `^\d+\.\d+\.\d+$`. On some systems, the Claude binary name appears as its version number (e.g., "2.1.45").

3. **Shell fallback:** If the pane command is a known shell (bash, zsh, sh, fish, tcsh, ksh), Claude might be running as a child process. `pgrep -P {pane_pid} -l` lists children, and the function looks for "node" or "claude" in the output.

### 8.3 Grid View

The grid is a separate tmuxinator session (`{name}-grid`) that creates windows attaching to the main session's windows in a tiled layout. It is generated only when `config.Grid` is non-empty.

Currently, the generated grid YAML creates independent Claude windows (same `claudeCommand` pattern), not `tmux attach` panes as described in the spec. This is a divergence from the spec's design, which calls for read-only monitoring panes.

---

## 9. Testing Approach

### 9.1 FakeRunner Pattern

The core testing strategy is the `exec.Runner` interface + `exec.FakeRunner`. Every package that shells out uses `Runner`, not `os/exec` directly. Tests swap in `FakeRunner` to:

- **Verify command invocations** by inspecting `FakeRunner.Calls`
- **Simulate command output** by setting `FakeRunner.Output`
- **Simulate errors** by setting `FakeRunner.Err`

This means no test in the project requires real tmux, git, bd, or tmuxinator to be installed.

For multi-step flows (like memory measurement that calls ps and pgrep multiple times), the `tmux_test.go` file defines a `fakeMultiRunner` that returns different output for sequential calls.

### 9.2 Test Distribution

| Package | Test Count | Strategy |
|---------|-----------|----------|
| `internal/exec` | 7 | Real runner with simple commands (echo, ls) + FakeRunner verification |
| `internal/config` | 12 | Table-driven: valid/invalid YAML, discovery walk, all validation rules, round-trip |
| `internal/roles` | 28 | Catalog completeness, permission tiers, NeedsSrc/Playbooks/Makefile flags, render with all variable combinations, all 11 role templates, all 4 doc templates |
| `internal/scaffold` | 10 | Uses `t.TempDir()`: directory creation, file creation, template selection, idempotency, force overwrite |
| `internal/tmuxinator` | 8 | YAML structure validation, window count, permission tiers, pre_window, grid, unknown roles |
| `internal/tmux` | 17 | Session exists, list windows parsing, all Claude detection paths, PID extraction, memory measurement, kill/new/send-keys command verification |
| `internal/git` | 7 | FakeRunner: init (new/existing/error), submodule add, commit all |

**Total: 97 tests across 7 packages.**

### 9.3 What is NOT Tested

- `cmd/` package has no tests. Commands are tested indirectly through integration and manual testing.
- `doctor.go` uses `os/exec` directly (not Runner) for `exec.LookPath` and version detection.
- Terminal passthrough in `up.go` and `down.go` (`os/exec` with Stdin/Stdout/Stderr attached).
- Actual tmux session creation/destruction.
- Actual git clone/submodule operations.
- Actual bd CLI integration.

---

## 10. Extension Points

### 10.1 Adding a New Well-Known Role

1. Add an entry to `roles.Catalog` in `internal/roles/catalog.go`:
   ```go
   "dba": {Name: "dba", Permission: Autonomous, NeedsSrc: true},
   ```

2. Add a template constant in `internal/roles/templates.go`:
   ```go
   const DBATemplate = `# CLAUDE.md
   ## Identity
   **Database Administrator** ({{role_name}}) for {{project_name}}. ...
   `
   ```

3. Add a case in `scaffold.templateForRole()` in `internal/scaffold/scaffold.go`:
   ```go
   case "dba":
       return roles.DBATemplate
   ```

4. Add tests in `catalog_test.go` (update expected lists) and `templates_test.go` (add render test).

### 10.2 Adding a New Command

1. Create `cmd/{command}.go` following the pattern of existing commands:
   - Define a `var {command}Cmd = &cobra.Command{...}`
   - Register with `rootCmd.AddCommand` in `init()`
   - Implement `run{Command}(cmd *cobra.Command, args []string) error`
   - Start with `config.Discover(cwd) -> config.Load(path)`
   - Use `iexec.DefaultRunner{}` for external commands

2. The command has access to all internal packages. Follow the dependency flow: `cmd/` imports internal packages, never the reverse.

### 10.3 Adding a New Template Variable

1. Add the field to `roles.RenderVars` in `internal/roles/render.go`
2. Add the key-value mapping in the `lookup` map inside `Render()`
3. Use `{{variable_name}}` in templates
4. Pass the value from `config.RoleOverride` or `config.Project` in `scaffold.Run()`

### 10.4 Adding a New Config Field

1. Add the field to the appropriate struct in `internal/config/config.go` (with `yaml` tag)
2. If validation is needed, add a check in `config.Validate()`
3. Consumers read from the `config.Project` struct. No additional wiring needed; packages that receive `*config.Project` automatically see new fields.

---

## 11. Build and Distribution

**Build:**
```bash
make build    # go build with ldflags setting Version
make test     # go test ./... -count=1
make vet      # go vet ./...
make check    # vet + test
make clean    # rm -f initech
```

**Version:** Set via `ldflags` at build time: `-X github.com/nmelo/initech/cmd.Version=$(VERSION)`. Defaults to `"dev"` when built without flags.

**Release:** `make release` runs goreleaser. Targets: darwin/linux, amd64/arm64. Distribution via Homebrew (`brew tap nmelo/tap && brew install initech`).

**Binary size:** Minimal. Two direct dependencies (cobra, yaml.v3) plus their transitive deps (mousetrap, pflag). No CGO.
