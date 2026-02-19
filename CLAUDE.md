# Initech

A Go CLI tool that captures Nelson's local software development patterns into a reproducible, bootstrappable system. Named after the company from Office Space.

## What This Is

Nelson runs local dev using tmux sessions where each window gets a role that mimics a software SaaS company (engineering, QA, ops, etc.). This pattern has emerged organically across multiple projects. Initech codifies it into a single tool that can start, run, and maintain projects using this model.

## Project Status: MVP Complete

All 5 phases shipped. 10 commands, 11 role templates, 97 tests across 7 packages. The tool works end-to-end: `initech init` bootstraps a project, `initech up` starts the session, `initech status` shows what's happening.

## Commands

```
initech version           # Print version
initech doctor            # Check prerequisites
initech init              # Bootstrap project (interactive or config-driven)
initech up                # Start tmux session with all agents
initech status            # Agent table: Claude detection, beads, memory
initech stop <role...>    # Kill individual agents to free memory
initech start <role...>   # Bring stopped agents back (--bead for dispatch)
initech restart <role>    # Kill + restart agent (--bead for dispatch)
initech down              # Graceful shutdown with dirty-git warnings
initech standup           # Morning standup from beads
```

## Tech Stack

- Language: Go 1.25
- Dependencies: cobra (CLI), yaml.v3 (config)
- Session management: tmux + tmuxinator
- Agent tooling: gn/gp/gm (brew tap nmelo/tap)
- Issue tracking: beads (bd CLI)
- Config format: YAML (initech.yaml)

## Package Architecture

```
cmd/             # Cobra commands (init, up, status, down, stop, start, restart, standup, doctor)
internal/
  exec/          # Runner interface + DefaultRunner + FakeRunner
  config/        # initech.yaml types, Load, Discover, Validate
  roles/         # Catalog (11 roles), Render ({{variable}}), templates (role + doc)
  scaffold/      # Directory tree creation, idempotent
  tmuxinator/    # YAML generation (main + grid sessions)
  tmux/          # Runtime: session inspection, Claude detection, memory, window mgmt
  git/           # Init, submodule, commit
```

Every package that shells out uses `exec.Runner`. Tests swap in `exec.FakeRunner`. No real tmux/git/bd needed in tests.

## Project Documents

Four documents in `docs/`, each with a hard cap of 5000 lines. Single-file, living documents.

| Document | Question | Contains |
|----------|----------|----------|
| `docs/prd.md` | **Why** does initech exist? | Problem statement, user needs, success criteria, non-goals |
| `docs/spec.md` | **What** does initech do? | Requirements, behavior, discovered patterns, acceptance criteria |
| `docs/systemdesign.md` | **How** does initech work? | Architecture, packages, data structures, build order, testing |
| `docs/roadmap.md` | **When/Who** does what get built? | Milestones, phases, success gates, agent assignments |

## Team Roles

| Role | Window | Permission | What they own |
|------|--------|-----------|---------------|
| super | super | Supervised | Dispatch, monitoring, session lifecycle |
| pm | pm | Autonomous | PRD, requirements, acceptance criteria |
| eng1 | eng1 | Autonomous | Implementation, tests |
| eng2 | eng2 | Autonomous | Implementation, tests |
| qa1 | qa1 | Autonomous | Behavioral verification |
| qa2 | qa2 | Autonomous | Behavioral verification |
| shipper | shipper | Supervised | Builds, packaging, distribution |

## Principles

### Guardrails Are Load-Bearing

Agentic coding on this project only works if the guardrails are strong. Agents cannot be trusted to self-correct without external constraints pushing back on bad output. Guardrails are not optional safety theater; they are the structural mechanism that makes multi-agent development viable.

What counts as a guardrail:
- **Tests.** Every package has tests. Agents run them before marking work done.
- **Specs.** `docs/spec.md` defines what the system does. Agents implement to spec, not to vibes.
- **Product documents.** PRDs, acceptance criteria, and user stories constrain scope.
- **Development process.** QA gates, status lifecycle. The process catches drift before it compounds.
- **Type system and compiler.** Go's type system is a guardrail. Prefer compile-time errors over runtime checks.

If an agent produces work that passes all guardrails and is still wrong, the guardrails are broken, not the agent. Fix the guardrails.

### Disposable Modules

Architecture must support continuous rewrites. Any component should be replaceable in minutes, not hours.

- Small packages with narrow interfaces
- No shared mutable state between packages
- No deep dependency chains
- Favor duplication over coupling
- Interfaces at boundaries

### Documentation as Agent Affordance

Every package and every exported function gets a Go doc comment. This is the primary mechanism by which agents understand the codebase fast enough to be useful.

### Beads Are the Process

Beads (`bd` CLI) is the exclusive work tracking system. Every unit of work is a bead.

**Bead lifecycle:**
`open` -> `in_progress` -> `ready_for_qa` -> `in_qa` -> `qa_passed` -> `ready_to_ship` -> `closed`

**Process rules (non-negotiable):**
- Agents comment their PLAN on the bead before writing code
- Agents comment DONE with QA verification steps when finished
- Engineers push to git before marking `ready_for_qa`
- Only Nelson marks `ready_to_ship` and closes beads
- `.beads/issues.jsonl` is committed alongside code changes

**Anti-patterns:**
- Never create markdown TODO lists (use bd)
- Never dispatch ungroomed beads (must have acceptance criteria)
- Never use `bd edit` (interactive; use `bd update` with flags)
- Never commit `.beads/beads.db` (JSONL only)
- Never skip `bd sync` at end of work sessions

## Build

```bash
make build              # Build binary
make test               # Run all tests
make check              # Vet + test
make release            # goreleaser release
```
