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

### 1.7 Naming Convention

**One name per agent.** The role name is used everywhere: tmux window name, directory name, and messaging identity (gn/gp/ga target). No aliasing, no indirection.

The user chooses role names at project init time. Initech uses that name for the directory, the tmux window, and all tooling references.

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

Discovered from: CLAUDE.md files across beadbox, cobalt, nayutal_app agent directories.

### 2.1 Role Architecture

Roles are organized into tiers based on their relationship to work:

```
Coordinator Tier     super
                       |
                  dispatches to
                       |
    +--------+---------+---------+--------+
    |        |         |         |        |
Spec Tier   pm       arch      sec      pmm
    |
    | acceptance criteria flow down
    |
Impl Tier   eng1     eng2
    |
    | ready_for_qa flows up
    |
Valid Tier  qa1      qa2      [qa3]
    |
    | qa_passed flows up
    |
Ship Tier   shipper
    |
    | additional specialized roles
    |
Support     writer   ops      growth
```

**Fundamental rule:** roles do not cross domain boundaries. Knowledge gaps in a role's CLAUDE.md are bugs to fix, not reasons to reach into another role's territory.

### 2.2 Role Definitions

Each role has: identity (what it does), constraints (what it cannot do), artifacts (what it owns), and interaction protocol (how it talks to other roles).

#### super (Coordinator)

**Identity:** Owns three things: (1) work coordination (dispatch tasks, manage bead lifecycle), (2) document alignment (keep specs and CLAUDE.md files current), (3) institutional memory (codify patterns into CLAUDE.md).

**Constraints:**
- Never implements work. If it falls in an agent's domain, dispatch it.
- Never marks `ready_to_ship` or closes beads (Nelson-exclusive actions).
- Bead creation and prioritization require Nelson's approval.

**Artifacts:** `standup.md` (working memory that survives context compaction), document freshness inventory (tiered: alignment-critical, agent operating systems, reference).

**Protocol:**
- Session start: read CLAUDE.md, read standup.md, ask Nelson priorities.
- Dispatch: read bead, clear agent context (`gn -c`), send dispatch with title + ID + claim instruction.
- Monitor: peek every 5-10 minutes, verify agent started within 2 minutes of dispatch.
- Close: brief Nelson with what was done, acceptance criteria checklist, downstream beads. Wait for explicit approval.
- Learning: observe Nelson's decisions, detect patterns, surface as questions, codify when confirmed.

**Permission tier:** Supervised (no `--dangerously-skip-permissions`).

#### eng (Engineer)

**Identity:** Implements features, fixes bugs, writes tests. Owns implementation quality: correctness, test coverage, spec conformance. Parallel instances (eng1, eng2) with identical responsibilities and isolated git worktrees.

**Constraints:**
- Cannot decide what to build (PM/Nelson).
- Cannot validate in production (QA).
- Cannot handle releases (shipper).
- Cannot close beads.

**Artifacts:** Source code in isolated `src/` worktree, Makefile with build/test targets.

**Protocol:**
- Receive work: claim bead (`bd update <id> --claim --actor eng1`), read full bead + referenced docs.
- Plan first: comment PLAN on bead before writing any code (numbered steps, files, test approach).
- Complete: comment DONE with QA verification steps (commands to run, expected output, acceptance criteria mapping). Include commit hash.
- Gate: push to git before marking `ready_for_qa`. Verify with `git fetch && git log`.
- Report: `ga -w super "[from eng1] <title> (<id>) complete. Pushed <commit>."`

**Permission tier:** Autonomous (`--dangerously-skip-permissions`).

**eng2 mirrors eng1.** Same CLAUDE.md, same responsibilities. Separate worktree for parallel work without conflicts.

#### qa (Quality Assurance)

**Identity:** Validates features before release. Can block releases for critical/high severity bugs. Owns validation quality: whether what shipped actually works as specified. Parallel instances (qa1, qa2, optionally qa3).

**Constraints:**
- Cannot modify code or write production code.
- Cannot make product decisions.
- Cannot write documentation.
- Cannot guess testing approach. If eng's DONE comment is insufficient, reject the bead back to `in_progress`.

**Artifacts:** Test evidence (commands executed, actual output observed), playbooks in `playbooks/`.

**Protocol:**
- Receive work: claim bead (`bd update <id> --claim --status in_qa --actor qa1`).
- Validate: behavioral verification only. Run actual software, observe actual behavior. Code review alone is not QA.
- Verdict: comment with PASS or FAIL as first word (Nelson scans quickly), followed by evidence.
- Gate: all acceptance criteria met AND no critical/high bugs = PASS. One unmet criterion = FAIL.
- Report: `ga -w super "[from qa1] <title> (<id>): PASS/FAIL. <summary>."`
- Unrelated bugs found during testing: PASS the bead, file separate bug bead.

**Permission tier:** Autonomous.

#### pm (Product Manager)

**Identity:** Owns product truth: what to build, why it matters, whether shipped features solve user problems. Defines requirements, acceptance criteria, user stories. Reviews eng beads for requirement survival (not implementation approach).

**Constraints:**
- Cannot design systems or implement code.
- Cannot prescribe implementation approach.
- Cannot make silent spec changes (all through Nelson).
- Cannot close beads.

**Artifacts:** PRDs, specs, workflow documents, release notes content, bead grooming.

**Permission tier:** Autonomous.

#### arch (Architect)

**Identity:** Owns the shape of the system: domain model, API contracts, security architecture, design decisions. Bridges product (WHAT) and engineering (HOW). Ensures eng output matches architectural intent. Documents decisions in ADRs so they don't get relitigated.

**Constraints:**
- Cannot implement code.
- Cannot create beads against unspecified desired state (spec first, then bead).
- Cannot override sec's risk scores without evidence-based calibration.
- Cannot close beads.

**Artifacts:** Domain model, system design, security architecture, ADRs (`arch/decisions/`), research findings.

**Permission tier:** Autonomous.

#### sec (Security)

**Identity:** Owns security posture assessment. Thinks like an attacker, finds weaknesses the team doesn't see. Scores risks at theoretical maximum; arch calibrates to business context.

**Constraints:**
- Cannot implement code or design systems.
- Cannot self-censor findings ("we're just a PoC"). Score honestly, let arch calibrate.
- Cannot close beads.
- Must provide enrichment data (exploitability, attack surface, preconditions) for arch's calibration.

**Artifacts:** Security model, threat modeling documents, vulnerability triage, detection effectiveness reviews.

**Permission tier:** Autonomous.

#### shipper (Release)

**Identity:** Owns the path from compiled code to user-installable artifacts. Handles builds, packages, distribution channels, version management. Executes release process after all gates pass.

**Constraints:**
- Cannot write application code (eng owns that).
- Cannot decide what ships or version numbers (Nelson decides).
- Must verify all beads are `ready_to_ship` before release (hard gate).
- Cannot close beads.

**Artifacts:** Release configs (goreleaser, nfpm, Dockerfiles), CI workflows, distribution channel management, playbooks in `playbooks/`.

**Permission tier:** Supervised (no `--dangerously-skip-permissions`).

#### pmm (Product Marketing)

**Identity:** Owns external positioning, messaging, competitive intelligence. Translates product reality into public-facing content. All external communications are drafts until Nelson approves.

**Constraints:**
- Cannot define what to build (PM).
- Cannot implement features (eng).
- Cannot approve external communications (Nelson).

**Artifacts:** Market positioning docs, competitive research, website copy, changelog, README.

**Permission tier:** Autonomous.

#### writer (Technical Writer)

**Identity:** Owns user-facing documentation: setup guides, reference docs, tutorials, troubleshooting. Must verify guides by cloning and building fresh (not using eng's existing build).

**Constraints:**
- Cannot commit significant content changes without Nelson approval.
- Cannot close beads.

**Artifacts:** Documentation files, verification workspace with fresh clones.

**Permission tier:** Autonomous.

#### ops (Operations)

**Identity:** Owns the user experience perspective. Tests software as an end user would, on real hardware, following real workflows. Validates install, launch, and use flows.

**Artifacts:** Operational playbooks, UX test results.

**Permission tier:** Autonomous.

#### growth (Growth Engineer)

**Identity:** Owns metrics, analytics instrumentation, and growth loops. Defines event taxonomy, analyzes funnels, proposes experiments. Provides data that informs PM's prioritization.

**Constraints:**
- Cannot define product direction (PM).
- Cannot write marketing copy (PMM).
- Event taxonomy must not contain PII.

**Artifacts:** Analytics dashboards, event taxonomy, experiment documentation, findings log.

**Permission tier:** Autonomous.

### 2.3 Role Knowledge Domains

Each role knows specific things and explicitly does not know others. This separation is load-bearing.

| Role | Knows | Does NOT Know |
|------|-------|---------------|
| pm | What to build, why it matters | Implementation details |
| arch | System shape, tradeoffs, contracts | Business rationale, release state |
| eng | How it's built, code, APIs | Business rationale |
| qa | Whether it works (behavioral) | Implementation, business rationale |
| sec | Whether it's secure, risk scores | Implementation, release state |
| shipper | Release state, artifacts | Implementation, product decisions |
| pmm | Market context, positioning | Implementation details |
| writer | User-facing experience | Implementation, business rationale |
| ops | How it feels to use | Implementation, product strategy |
| growth | Whether it's working (metrics) | Implementation, product direction |

### 2.4 CLAUDE.md Structure Per Role

Every agent directory contains a CLAUDE.md that follows this structure:

```
# Role Identity (1-2 sentences: who you are, what you own)

## Critical Failure Modes (what goes wrong if you drift)

## Decision Authority (what you decide vs ask Nelson vs never do)

## Responsibilities (what you do, in priority order)

## Constraints (what you explicitly cannot do)

## Artifacts (files and directories you own)

## Workflow (step-by-step for your primary work loop)

## Communication (how to receive work, report back, escalate)

## Cross-Role Boundaries (who knows what, how to interact)

## [Role-Specific Sections] (testing methodology for QA, release process for shipper, etc.)
```

The CLAUDE.md is the role's operating system. An agent with no prior context should be able to read it and start working correctly. If a new agent reads the CLAUDE.md and produces bad output, the CLAUDE.md is broken.

### 2.5 What Initech Must Generate Per Role

When bootstrapping a project, initech creates each agent directory with:

1. **CLAUDE.md** generated from a role template, customized with project-specific details (project name, tech stack, build commands, repo URLs).
2. **src/** git worktree (for eng, qa, shipper, and any role that needs source access).
3. **playbooks/** directory (for qa, shipper, ops).
4. **Makefile** with role-appropriate targets (for eng: build, test, lint; for qa: test suites).
5. **.claude/** directory for Claude Code session state.

Role templates are the core asset of initech. They encode the institutional knowledge from Nelson's projects into reusable starting points.

## 3. Agent Coordination

Discovered from: super/CLAUDE.md dispatch protocols, agent CLAUDE.md response patterns, gastools source, bead status rules across beadbox, cobalt, nayutal_app.

### 3.1 Communication Tools

Four tools with distinct semantics. Tool selection is not a preference; it's a protocol.

| Tool | Mechanic | When to use |
|------|----------|-------------|
| `gn -c` (nudge + clear) | Sends `/clear`, waits, then delivers message | New task assignment. Gives agent fresh context. |
| `gn` (nudge) | Sends text + Escape to interrupt | Urgent/blocking issues. Agent needs to stop and look. |
| `ga` (add) | Queues text without interrupting | Follow-ups, non-urgent updates to busy agents. |
| `gp` (peek) | Reads output, sends nothing | Check if agent is busy/stuck before deciding gn vs ga. |
| `gm` (mail) | Persistent message via beads DB | Durable messages that survive session restarts. |

**Protocol:** Always `gp <agent> -n 5` before sending. If busy, use `ga`. If free, use `gn`.

### 3.2 Dispatch Protocol

Super dispatches work to agents. Every dispatch follows the same structure.

**Before dispatching:**
1. Read the bead: `bd show <id>` (assess complexity, spot interdependencies).
2. Verify bead is groomed: clear description, acceptance criteria, referenced specs.
3. Never dispatch ungroomed beads. If underspecified, fix the bead first.

**Dispatch message format (all roles):**
```bash
gn --clear -w <agent> "[from super] You have work: <bead-title> (<bead-id>). <brief context>. Claim with: bd update <id> --claim --actor <agent>. ga me when done."
```

**Role-specific additions:**
- **QA dispatch** includes: branch name, commit hash, "PASS or FAIL verdict required."
- **Shipper dispatch** includes: bead list, target version, "All verified ready_to_ship."
- **Ops dispatch** includes: download URL for artifacts, playbook path.

**Rules:**
- Always include bead title + ID (never ID alone).
- Always include claim instruction.
- Always include callback instruction ("ga me when done").
- Use full absolute paths for any file references outside the agent's workspace.
- One bead per dispatch. Never batch multiple complex beads to one agent.

### 3.3 Agent Response Protocol

Every agent reports back to super using `ga` (queue without interrupting).

**Engineer completion:**
```bash
# Comment on bead first
bd comments add <id> --author eng1 "DONE: <what was done>. Commit: <hash>"
# Then report to super
ga -w super "[from eng1] <title> (<id>) complete. <summary>. Pushed <commit>."
```

**QA verdict:**
```bash
# Comment on bead first (verdict as FIRST WORD)
bd comments add <id> --author qa1 "PASS: <one-line summary>\n\n<evidence>"
# Then report to super
ga -w super "[from qa1] <title> (<id>): PASS. <summary>."
```

**All agents:** use `ga` (not `gn`) to report back. Super should not be interrupted by completion reports.

### 3.4 Bead Status Lifecycle

Work flows through a rigid gate sequence. Each transition has a specific owner and preconditions.

```
open ──(eng claims)──> in_progress ──(eng pushes, tests pass)──> ready_for_qa
                                                                      |
                                         (qa claims)──> in_qa ──(all criteria met)──> qa_passed
                                                                                          |
                                                          (Nelson approves)──> ready_to_ship
                                                                                          |
                                                              (shipper releases)──> closed
```

**Transition rules:**

| Transition | Who | Preconditions |
|-----------|-----|---------------|
| open -> in_progress | eng (via `bd update --claim`) | Bead is groomed, dispatched by super |
| in_progress -> ready_for_qa | eng | Code complete, tests pass, committed + pushed to origin, PLAN and DONE comments on bead |
| ready_for_qa -> in_qa | qa (via `bd update --claim --status in_qa`) | Dispatched by super, code is on origin |
| in_qa -> qa_passed | qa | All acceptance criteria verified via behavioral testing, verdict comment on bead |
| qa_passed -> ready_to_ship | Nelson only | Nelson reviews QA report, approves |
| ready_to_ship -> closed | Nelson only (super executes `bd close`) | Release complete, verified |

**Hard rules:**
- Only Nelson sets `ready_to_ship`. No agent can do this.
- Only Nelson closes beads. Super executes `bd close` after Nelson's explicit approval.
- Engineer must push before marking `ready_for_qa`. QA cannot validate unpushed code.
- QA verdict must be PASS or FAIL as the first word of the comment. No ambiguity.

### 3.5 Monitoring and Patrol

Super monitors agents proactively, not reactively.

**Patrol frequency:** Every 5-10 minutes when tasks are in_progress.

**Patrol sweep per agent:**
1. Peek: `gp -s <session> <agent> -n 15`
2. Assess: working normally? stuck? dead?
3. Check bead: claimed? PLAN commented? status correct? pushed?
4. Nudge if gaps: send specific correction for missed process steps.

**Stuck detection signals:**
- Stuck at permission prompt
- Error loop (same error repeating)
- Idle when should be working
- Looping (repeating same action)
- Missing bead comments (no PLAN, no DONE)
- Status not updated (not claimed, not in_progress)
- Code not pushed before marking ready_for_qa

**After dispatching:** Verify agent started within 2 minutes.

### 3.6 Agent Revival

When an agent is stuck or dead, super kills and restarts it.

```bash
# 1. Kill the stuck window
tmux kill-window -t <session>:<agent>

# 2. Create new window with the same name
tmux new-window -t <session> -n <agent>

# 3. Start Claude with env vars and continue
tmux send-keys -t <session>:<agent> \
  "cd ~/Desktop/Projects/<project>/<agent> && claude --continue" Enter

# 4. Wait for init, then re-dispatch
sleep 5 && gn -w <agent> "[from super] Restarted. Resume <bead-id>: <context>"
```

`claude --continue` picks up prior conversation state from `.claude/`. The agent reads its CLAUDE.md on startup and can resume work.

### 3.7 Parallel Work Coordination

Multiple agents work simultaneously. Super tracks all in-flight work.

**Layer-based execution:** Work is organized into dependency layers. Beads in the same layer can run in parallel. QA validates layer N while eng builds layer N+1.

**Progress tracking:** Super maintains agent status and QA rollup tables:

```
Agent status:
| Agent | Task              | Status      |
|-------|-------------------|-------------|
| eng1  | Layer 4: Middleware| in_progress |
| eng2  | Free              |             |
| qa1   | Layer 2-3 valid.  | in_progress |
| qa2   | Free              |             |

QA rollup:
| Layer | Beads                    | Status      |
|-------|--------------------------|-------------|
| 0     | si-d2y.3.12, .19, .22   | qa_passed   |
| 1     | si-d2y.3.13, .14        | qa_passed   |
| 2-3   | si-d2y.3.15, .16, .17   | in_progress |
```

**Free agent awareness:** Super bold-marks free agents to spot dispatch opportunities.

**Epic coordination:**
- `bd ready --parent <epic-id>` finds unblocked children ready for dispatch.
- `bd swarm validate <epic-id>` checks for missing fields or broken dependencies.
- `bd blocked --parent <epic-id>` surfaces blocked children that need attention.

### 3.8 Bead Grouping: Epics and Convoys

**Epic:** Scope container. Children can ship independently as they complete. Epic is done when all children are done.

**Convoy:** Release train. ALL children must be `ready_to_ship` before ANY of them ship. Used for coordinated releases where partial shipping would break things.

**Dependency types:**
- `blocks` - A must complete before B can start.
- `parent-child` - Hierarchical (epic/subtask structure).
- `related` - Logical connection, not blocking.
- `discovered-from` - Found during implementation of another bead.

### 3.9 Closing Protocol

Only Nelson closes beads. Super briefs Nelson and waits for explicit approval.

**Closing briefing format:**
1. What was done (summary of implementation).
2. Acceptance criteria checklist (each item: pass/fail).
3. Key findings or decisions made.
4. What needs to persist (downstream beads, follow-up work).

After Nelson approves: `bd close <id> --reason "<summary>"`

### 3.10 Working Memory

Super maintains `standup.md` as persistent working memory that survives context compaction.

**Content:**
- Active work per agent and status
- Pending QA/promotion queue
- Ready-to-ship backlog
- Recent completions
- Release planning notes

**Session start protocol:** (1) Read CLAUDE.md, (2) Read standup.md, (3) Ask Nelson priorities.

**Update triggers:** After completing epics, releases, or major milestones. Each update gets a dated section header.

### 3.11 What Initech Must Support for Coordination

1. **Dispatch command** that formats and sends the correct message template to the right agent window, given a bead ID and target role.
2. **Patrol command** that peeks all active agents and reports status (working/stuck/idle).
3. **Status dashboard** showing agent status table and QA rollup from beads.
4. **Revival command** that kills and restarts a specific agent window with correct env vars and re-dispatches their current bead.
5. **Integration with `bd`** for finding ready work, checking bead status, and validating dispatch preconditions (is the bead groomed?).

## 4. Project Bootstrap

Discovered from: root file structure, .gitmodules, .beads configs, and CLAUDE.md files across beadbox, cobalt, nayutal_app.

### 4.1 Two-Repo Architecture

A key discovery: the project root is a **coordination repo**, not a code repo. Code lives in git submodules inside agent directories. The coordination repo tracks:

- Agent CLAUDE.md files (role instructions)
- Project-level CLAUDE.md and ARCHITECTURE.md
- Beads database export (`.beads/issues.jsonl`)
- Agent playbooks, specs, docs
- `.gitmodules` mapping agents to code repos

This separation means the coordination layer (roles, process, tracking) is versioned independently from the code itself.

### 4.2 Project Root Structure

Every project follows this skeleton:

```
<project>/
  .beads/
    beads.db              # SQLite (gitignored)
    beads.db-shm          # SQLite runtime (gitignored)
    beads.db-wal          # SQLite runtime (gitignored)
    issues.jsonl          # JSONL export (git tracked, sync point)
    config.yaml           # Beads config (issue prefix, etc.)
  .claude/
    settings.local.json   # Local MCP/permission config (gitignored)
  .git/
  .gitignore
  .gitmodules             # Maps agent dirs to code repos
  CLAUDE.md               # Project-wide protocols, architecture, commands
  AGENTS.md               # Quick reference: bd commands, landing-the-plane checklist
  docs/
    prd.md                # Why: problem statement, users, success criteria, journeys
    spec.md               # What: requirements, behaviors, acceptance criteria
    systemdesign.md       # How: architecture, packages, data structures, build order
    roadmap.md            # When/Who: phases, milestones, success gates, agent allocation
  super/                  # Coordinator agent directory
  eng1/                   # Engineer agent directory
  eng2/                   # Engineer agent directory
  qa1/                    # QA agent directory
  qa2/                    # QA agent directory
  shipper/                # Shipper agent directory
  pm/                     # PM agent directory
  ...                     # Additional roles as needed
```

### 4.3 .gitignore Pattern

Track coordination artifacts. Ignore code (it's in submodules), runtime files, and local configs.

```gitignore
# Source code lives in submodules
node_modules/
.next/
target/
bin/

# Beads runtime (JSONL is tracked, DB is not)
.beads/*.db-wal
.beads/*.db-shm
.beads/daemon*.log*

# Local agent config
*/.mcp.json

# OS artifacts
.DS_Store
```

### 4.4 Git Submodule Configuration

Each agent that needs source code access gets a submodule pointing to the code repo.

**Pattern A (single code repo):** All agents point to the same repo. Used when the project is one codebase.
```gitmodules
[submodule "eng1/src"]
  path = eng1/src
  url = git@github.com:user/project.git

[submodule "eng2/src"]
  path = eng2/src
  url = git@github.com:user/project.git

[submodule "qa1/src"]
  path = qa1/src
  url = git@github.com:user/project.git
```

**Pattern B (multi-repo):** Some agents point to different repos. Used when roles work on different codebases (e.g., PMM works on a marketing site, eng works on the main product).

Each submodule gives the agent an **isolated working tree**. eng1 and eng2 can edit the same files without conflicts because they're separate checkouts of the same repo.

### 4.5 Beads Initialization

```bash
bd init                               # Creates .beads/ with SQLite DB
bd config set issue-prefix <prefix>   # Optional: set bead ID prefix (e.g., "nyt")
```

The `.beads/config.yaml` is minimal. Most projects use defaults. The one common customization is `issue-prefix` for project-specific bead IDs.

### 4.6 Project CLAUDE.md Content

The root CLAUDE.md is the project-wide operating manual. It contains:

1. **Project identity** - what this is, who's involved, key context
2. **Folder structure** - which agent dirs exist, what they contain
3. **Project documents** - links to docs/prd.md, docs/spec.md, docs/systemdesign.md, docs/roadmap.md with the four-document table (why/what/how/when-who)
4. **Issue tracking protocol** - bd commands, status workflow, bead quality rules
5. **Communication protocols** - gn/ga/gp usage, dispatch patterns
6. **Tech stack specifics** - build commands, test commands, deployment targets
7. **Current epic/branch** - what the team is working on right now

Size ranges from 8KB (focused projects) to 30KB (complex projects with multiple subsystems).

### 4.7 AGENTS.md Content

A quick-reference cheatsheet at the project root. Contains:

1. **bd command reference** - ready, show, update, close, comments
2. **Landing the plane checklist** - end-of-session protocol
3. **Common workflows** - claim, complete, dispatch patterns

Kept short (1-5KB) so agents can scan it fast.

### 4.8 Project Documents (docs/)

Every project gets a `docs/` directory with four structured documents. Each captures a different concern, has a hard cap of 5000 lines, and is a living single-file document updated throughout the project lifecycle.

| Document | Question it answers | Contents |
|----------|-------------------|----------|
| `prd.md` | **Why** does this exist? | Problem statement, user identity, success criteria, non-goals, user journeys, risks, scope boundaries |
| `spec.md` | **What** does this do? | Requirements, behaviors, discovered patterns, acceptance criteria |
| `systemdesign.md` | **How** does this work? | Architecture, packages, data structures, interfaces, build order, testing strategy |
| `roadmap.md` | **When/Who** does what get built? | Phases, milestones, success gates, agent allocation, dependency graph |

**Rules (apply to all four):**
- Hard cap: 5000 lines. Forces compact writing. If a section would push past the limit, compress or cut something less important first.
- Essential detail only. Expand on demand, not preemptively.
- Living documents. Updated as discovery proceeds and implementation reveals new constraints.
- Single file each. One place to look per concern.

**Document ownership by role:**
- **pm** owns `prd.md`. Writes problem statement, success criteria, user journeys.
- **arch** owns `systemdesign.md`. Writes architecture, package design, interface boundaries.
- **super** owns `spec.md` and `roadmap.md`. Orchestrates discovery (spec) and sequencing (roadmap).
- All roles read all documents. The owner writes; others contribute through beads and review.

**Scaffolded content:** `initech init` generates each document with pre-structured section headers and writing guidance. The headers provide the skeleton; agents fill in project-specific content during the discovery and design phase. The scaffolded documents are not blank files and not filled-in files. They are structured prompts that tell the agent exactly what goes in each section.

**Phase 0 convention:** The scaffolded `roadmap.md` starts with a pre-written "Phase 0: Discovery and Design" that sequences document creation as the first work. This means `initech up` immediately gives super a plan: fill in the four documents before writing code. Every project starts the same way.

### 4.9 What Initech Must Do for Bootstrap

`initech init` must:

1. **Create the coordination repo** - `git init`, root `.gitignore`, root CLAUDE.md, AGENTS.md.
2. **Initialize beads** - `.beads/` directory, config with project prefix.
3. **Create agent directories** - one per role in the project config, each with role-appropriate CLAUDE.md from templates.
4. **Set up git submodules** - for each role that needs source access, add a submodule pointing to the code repo(s).
5. **Scaffold project documents** - create `docs/` with `prd.md`, `spec.md`, `systemdesign.md`, `roadmap.md` from document templates.
6. **Create tmuxinator config** - main session YAML + optional grid YAML, written to `~/.config/tmuxinator/`.
7. **Set up .claude/ configs** - local settings for MCP servers and permissions per agent.
8. **Initial commit** - stage coordination files, commit.

The user provides: project name, code repo URL(s), which roles to include, and any project-specific env vars.

---

## 5. Lifecycle Management

Discovered from: standup.md files, session protocols in super CLAUDE.md, agent restart patterns, and context persistence mechanisms across beadbox, cobalt, nayutal_app.

### 5.1 Session Start

**Tmuxinator start:** `tmuxinator start <project>` creates the tmux session, opens all agent windows, each runs `claude --continue` in its working directory.

**Super's first actions (in order):**
1. Read `super/CLAUDE.md` (role protocol).
2. Read `super/standup.md` (recover working memory from last session).
3. Ask Nelson: "What's the priority today?" (don't assume from beads or previous context; priorities shift).

**Other agents:** start with `claude --continue`, read their CLAUDE.md, and wait for dispatch from super.

### 5.2 State Persistence Model

No daemons, no crons, no background processes. State persists through explicit files and git.

| State type | Stored in | Survives |
|-----------|-----------|----------|
| Role instructions | Agent CLAUDE.md | Everything (git tracked) |
| Working memory | super/standup.md | Context compaction, session restarts |
| Implementation state | Git commits in agent src/ worktrees | Everything |
| Coordination record | Bead comments (bd) | Everything (JSONL git tracked) |
| Conversation state | .claude/ directory | Session restarts (via `--continue`) |
| Agent context | Claude's context window | Nothing (cleared on restart or compaction) |

The system is designed so that **losing agent context is cheap**. Everything important is written to files or beads. An agent can be killed and restarted from scratch; it reads CLAUDE.md, gets a dispatch with bead ID, reads the bead, and picks up where it left off.

### 5.3 Working Memory (standup.md)

Super maintains `super/standup.md` as the bridge across context compaction events and session boundaries.

**Format:**
```markdown
# Super Standup

## 2026-02-09

### Active Work
- **eng1**: bb-9fa.2 (General tab: theme picker) - in_progress
- **eng2**: bb-9fa.3 (Feedback tab: form UI) - in_progress

### Pending QA / Promotion
- bb-9fa.1 (Settings dialog shell): qa_passed, needs Nelson's ready_to_ship

### Release Planning
- Next: v1.2.0 after settings epic ships

### Recent Completions
- bb-9fa.0 shipped, qa_passed

---

## 2026-02-08
...
```

**Update triggers:** After completing epics, releases, or major milestones. Each update gets a dated section header. Previous days kept for reference.

**On session start:** This is the most important file after CLAUDE.md. Super reads it first to know what was in flight, what's pending, and what recently shipped.

### 5.4 Context Clearing vs Continuation

Two opposing operations for agent context:

**Clear (`gn -c`):** Wipes agent context, gives fresh start with only CLAUDE.md. Used before dispatching new work. Without clearing, agents accumulate stale context from previous tasks, waste tokens, and make worse decisions.

**Continue (`claude --continue`):** Resumes prior conversation from `.claude/` state. Used when restarting a killed agent that should pick up where it left off.

**Decision rule:** New task = clear. Crashed agent = continue + re-dispatch.

### 5.5 Agent Restart (Revival)

When an agent crashes, hangs, or gets stuck:

```bash
tmux kill-window -t <session>:<agent>
tmux new-window -t <session> -n <agent>
tmux send-keys -t <session>:<agent> \
  "cd ~/Desktop/Projects/<project>/<agent> && claude --continue" Enter
sleep 5
gn -w <agent> "[from super] Restarted. Resume <bead-id>: <context>"
```

The 5-second sleep gives Claude time to initialize and read CLAUDE.md before receiving the dispatch.

### 5.6 Session End (Landing the Plane)

End-of-session protocol from AGENTS.md, mandatory for all agents:

1. **File issues** for remaining work (beads for anything unfinished).
2. **Run quality gates** - tests, linters, builds.
3. **Update bead status** - mark in_progress beads accurately.
4. **Push to remote** - work is NOT complete until `git push` succeeds. Never stop before pushing. Never say "ready to push when you are."
5. **Clean up** - stash uncommitted experiments, prune dead branches.
6. **Verify** - all changes committed AND pushed.
7. **Hand off** - update standup.md with context for next session.

### 5.7 Session Stop

**Tmuxinator stop:** `tmuxinator stop <project>` kills all windows and the session. Agents don't get graceful shutdown; they're terminated.

State that survives:
- `.claude/` in each agent dir (conversation state for `--continue`)
- `super/standup.md` (working memory)
- Git commits (implementation state)
- Beads (coordination record)

State that doesn't survive:
- Agent context windows (rebuilt from CLAUDE.md on next start)
- In-memory working state (must be written to standup.md or bead comments before stop)

### 5.8 Morning Standup Generation

Super can auto-generate a standup summary from beads:

```bash
bd list --closed-after <yesterday> --all --limit 0   # What shipped
bd list --status in_progress                          # Active work
bd list --ready --limit 5                             # What's next
```

Output format for Slack/comms:
```
## Project Daily - [Date]

### What's New
[Features shipped, capabilities added]

### In Progress
[Active work with brief context]

### Next Up
[What's coming]
```

### 5.9 What Initech Must Support for Lifecycle

1. **`initech up`** - Start tmux session via tmuxinator, verify all agents initialized, report status.
2. **`initech down`** - Optionally trigger landing-the-plane for active agents before killing the session. At minimum, warn if agents have uncommitted/unpushed work.
3. **`initech status`** - Show which agents are running, what bead each is working on, and current bead status.
4. **`initech restart <role>`** - Kill and restart a specific agent window, with `--continue` and optional bead re-dispatch.
5. **`initech standup`** - Generate morning standup from beads (what shipped, what's active, what's next).
