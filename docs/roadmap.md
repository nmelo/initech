# Initech Roadmap

Strategic sequencing: milestones, phases, success gates. Hard cap: 5000 lines.

Beads handles the tactical layer (what's ready, who's assigned, what's blocked). This document captures the strategic layer: what phases exist, what must be true before moving on, and how work flows across agents.

---

## 1. Phases

### Phase 1: Foundation

**Goal:** `initech init` + `initech up` work end-to-end. A user can bootstrap a project and start a tmux session with agents from a single config file.

**Packages to build:**
1. `internal/exec` - command runner (leaf dependency, everything needs it)
2. `internal/config` - config types, Load(), Validate()
3. `internal/roles/catalog` - role definitions, LookupRole()
4. `internal/roles/render` - {{variable}} substitution
5. `internal/roles/templates` - SuperTemplate + EngTemplate (two templates only, prove the pattern)
6. `internal/tmuxinator` - generate session YAML from config
7. `internal/scaffold` - create directories, write files
8. `internal/git` - git init, submodule add, commit
9. `cmd/root` + `cmd/init` - wire up init command
10. `cmd/up` - tmuxinator start wrapper

**Success gate:** Run `initech init` in a fresh directory with a valid `initech.yaml`, then `initech up`. A tmux session starts with super and eng1 windows, each running Claude with correct permissions and CLAUDE.md files. Manual verification by Nelson.

**Beads:** Create one epic for Phase 1. Each package is a bead under the epic. Engineers can work items 1-4 in parallel (no dependencies between them). Items 5-8 depend on 1-4. Items 9-10 depend on all prior.

### Phase 2: Visibility

**Goal:** Nelson can see what's happening without peeking into tmux windows manually.

**Packages to build:**
11. `internal/tmux` - session/window inspection, Claude detection (port from gastools)
12. `cmd/status` - agent status table with optional bead info
13. `cmd/down` - stop with uncommitted work warnings

**Success gate:** `initech status` correctly shows which agents are running, which have Claude active, and what bead each is working on (if bd is available). `initech down` warns about dirty git state before stopping.

**Depends on:** Phase 1 complete. A running session is needed to test visibility commands.

### Phase 3: Operations

**Goal:** Day-to-day session management without manual tmux commands.

**Packages to build:**
14. `cmd/restart` - kill/restart agent with optional bead re-dispatch
15. `cmd/standup` - morning standup from beads

**Success gate:** `initech restart eng1 --bead np-xxx` kills eng1's window, starts a new one with `claude --continue`, and dispatches the bead context via gn. `initech standup` prints what shipped yesterday, what's active, and what's next.

**Depends on:** Phase 2 complete. Restart needs tmux window management. Standup needs bd integration.

### Phase 4: Content

**Goal:** All 11 role templates are production quality. A bootstrapped project has CLAUDE.md files that produce correct agent behavior without manual surgery.

**Templates to complete:**
16. QATemplate, PMTemplate, ShipperTemplate (core roles)
17. ArchTemplate, SecTemplate, PMMTemplate (common roles)
18. WriterTemplate, OpsTemplate, GrowthTemplate (specialized roles)

Plus: root CLAUDE.md template, AGENTS.md template, .gitignore template, Makefile templates.

**Success gate:** Bootstrap a fresh project with all 11 roles. Start the session. Dispatch a feature bead through the full lifecycle (pm writes spec, eng implements, qa validates, shipper releases). Each agent produces correct behavior from the generated CLAUDE.md alone. Nelson reviews and approves template quality.

**Depends on:** Phase 1 complete (scaffold must work). Can run in parallel with Phases 2-3.

### Phase 5: Distribution

**Goal:** Anyone can install initech with `brew install`.

**Work:**
19. goreleaser config (darwin + linux, amd64 + arm64)
20. Homebrew formula in nmelo/homebrew-tap
21. README.md with usage examples
22. `initech version` command

**Success gate:** `brew tap nmelo/tap && brew install initech` works. `initech version` prints the correct version. `initech init --help` shows usage.

**Depends on:** Phase 3 complete (all commands exist).

---

## 2. Milestone Summary

```
Phase 1: Foundation     initech init + up work              [must ship first]
Phase 2: Visibility     status + down work                  [after Phase 1]
Phase 3: Operations     restart + standup work              [after Phase 2]
Phase 4: Content        all role templates production-ready  [parallel with 2-3]
Phase 5: Distribution   brew install works                   [after Phase 3]
```

### Dependency Graph

```
Phase 1 (Foundation)
   |
   +---> Phase 2 (Visibility) ---> Phase 3 (Operations) ---> Phase 5 (Distribution)
   |
   +---> Phase 4 (Content) [parallel track]
```

Phase 4 is the only parallelizable track. It's pure content work (writing CLAUDE.md templates) with no architectural risk. An agent can work on templates while other agents build the command infrastructure.

---

## 3. Agent Allocation

How work maps to roles in initech's own multi-agent development:

| Role | Phase 1 | Phase 2 | Phase 3 | Phase 4 | Phase 5 |
|------|---------|---------|---------|---------|---------|
| super | Coordinate all | Coordinate all | Coordinate all | Review templates | Coordinate release |
| eng1 | exec, config, roles packages | tmux package, status cmd | restart cmd | - | goreleaser |
| eng2 | scaffold, git, tmuxinator packages | down cmd | standup cmd | - | homebrew formula |
| qa1 | Test init + up flow | Test status + down | Test restart + standup | Validate templates | Test install flow |
| pm | Review spec, write beads | Write beads | Write beads | Write/review templates | Write README |
| arch | Review system design, ADRs | - | - | Review template architecture | - |

**Key constraint:** eng1 and eng2 work on different packages within the same phase. The package boundaries are designed for this: no shared state, no overlapping files.

---

## 4. Risk Gates

Checkpoints where Nelson evaluates whether to proceed, pivot, or stop.

### Gate 1: After Phase 1

**Question:** Does the generated project structure actually work? Does `initech up` produce a session where agents behave correctly?

**Pass criteria:**
- Session starts with all configured windows
- Each agent reads its CLAUDE.md and starts correctly
- Super can dispatch work to eng using gn
- Generated tmuxinator YAML matches the quality of hand-written configs

**Fail action:** Fix template quality, scaffold structure, or tmuxinator generation before moving on. Do not proceed to Phase 2 with a broken foundation.

### Gate 2: After Phase 4

**Question:** Are the templates good enough that a bootstrapped project works without manual CLAUDE.md editing?

**Pass criteria:**
- All 11 role templates reviewed and approved by Nelson
- A fresh project bootstrapped with all roles completes a full bead lifecycle (feature from spec to ship)
- Agents follow dispatch protocol, use correct tools, report back correctly

**Fail action:** Iterate templates. This is the highest-value work in the project. Templates that produce bad agent behavior defeat the purpose of the tool.

### Gate 3: Before Phase 5

**Question:** Is initech stable enough for distribution?

**Pass criteria:**
- All commands work on a real project (not just test fixtures)
- Error messages are clear and actionable
- `--help` output is useful
- No panics or unhandled errors in normal usage

**Fail action:** Fix stability issues. Don't ship a broken tool to Homebrew.

---

## 5. What Happens After MVP

Post-MVP work lives here as strategic direction, not committed scope. These become beads only when Nelson decides to pursue them.

**Likely next:**
- `initech add-role` / `initech remove-role` for modifying existing projects
- `initech dispatch <bead> <role>` for formatted dispatch messages
- `initech patrol` for automated monitoring sweeps

**Possible later:**
- Custom role template plugins (load from `~/.config/initech/templates/`)
- Remote agent support (SSH to workbench-linux, start agents there)
- `initech config` subcommand for editing initech.yaml interactively

**Unlikely but noted:**
- Multi-machine session coordination
- Metrics/analytics on agent performance
- Web dashboard
