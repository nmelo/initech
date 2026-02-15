# Initech

A Go CLI tool that captures Nelson's local software development patterns into a reproducible, bootstrappable system. Named after the company from Office Space.

## What This Is

Nelson runs local dev using tmux sessions where each window gets a role that mimics a software SaaS company (engineering, QA, ops, etc.). This pattern has emerged organically across multiple projects. Initech codifies it into a single tool that can start, run, and maintain projects using this model.

## Project Status: Discovery Phase

We are extracting common patterns from Nelson's existing projects before writing code. The discovery must happen first so we build the right abstractions.

## Discovery Targets

Survey these existing projects under `~/Desktop/Projects/` for recurring patterns:

### 1. Session Architecture
- How are tmux sessions structured per project?
- What windows exist and what roles do they serve?
- Are there bootstrap scripts that spin up sessions?
- How are sessions named, organized, torn down?

### 2. Role Definitions
- What roles repeat across projects (e.g., lead, engineer, reviewer, ops)?
- What tools/permissions does each role get?
- How are roles configured (CLAUDE.md, .claude/agents/, inline)?
- Do roles have dependencies on each other?

### 3. Agent Coordination
- How do agents in different windows communicate? (gn, gp, gm)
- What dispatch protocols exist?
- How is work assigned, tracked, and completed?
- What does the "manager" or "lead" role actually do?

### 4. Project Bootstrap
- What files/dirs get created when starting a new project?
- What's the common skeleton (CLAUDE.md, .claude/, Makefile, etc.)?
- Are there templates or generators in use?
- What external services get wired up (git, CI, monitoring)?

### 5. Lifecycle Management
- How does a typical dev session start and end?
- How is state preserved between sessions?
- What cleanup happens? What persists?
- How do long-running tasks survive session restarts?

## Intended CLI Shape (Rough)

```
initech init <project>        # Bootstrap a new project with the standard structure
initech up                    # Start tmux session with all roles
initech down                  # Gracefully shut down the session
initech status                # Show what's running, who's doing what
initech role <name>           # Manage role definitions
initech dispatch <task>       # Assign work to the right role
```

This is speculative. The actual CLI surface should emerge from what the discovery reveals.

## Tech Stack

- Language: Go
- Session management: tmux (via CLI, not a Go library unless justified)
- Agent tooling: Builds on existing gn/gp/gm tools (brew tap nmelo/tap)
- Config format: TBD (YAML, TOML, or something else based on what feels right)

## Principles

- Speed-to-value: get a working `initech up` before perfecting anything
- Extract, don't invent: patterns come from real usage, not imagination
- Composable: roles, sessions, and dispatch should be independently useful
- No magic: the tool should be transparent about what it does (print commands, show state)

### Guardrails Are Load-Bearing

Agentic coding on this project only works if the guardrails are strong. Agents cannot be trusted to self-correct without external constraints pushing back on bad output. Guardrails are not optional safety theater; they are the structural mechanism that makes multi-agent development viable.

What counts as a guardrail:
- **Tests.** Every package has tests. Agents run them before marking work done. Tests that don't exist yet get written before the feature code.
- **Specs.** `docs/spec.md` defines what the system does. Agents implement to spec, not to vibes. If the spec is ambiguous, fix the spec first.
- **Product documents.** PRDs, acceptance criteria, and user stories constrain scope. Agents cannot invent requirements.
- **Development process.** PR review, QA gates, status lifecycle. The process exists to catch drift before it compounds.
- **Type system and compiler.** Go's type system is a guardrail. Use it. Prefer compile-time errors over runtime checks.

If an agent produces work that passes all guardrails and is still wrong, the guardrails are broken, not the agent. Fix the guardrails.

### Disposable Modules

Architecture must support continuous rewrites. Any component should be replaceable in minutes, not hours. This is not aspirational; it is a hard constraint on how code is structured.

Rules:
- **Small packages with narrow interfaces.** Each package does one thing. The interface boundary is the contract; internals are throwaway.
- **No shared mutable state between packages.** Communication happens through function calls and return values, not shared globals or package-level vars.
- **No deep dependency chains.** If replacing package A requires touching packages B, C, and D, the architecture is wrong. Refactor until replacement is local.
- **Favor duplication over coupling.** Two packages with similar helper functions is better than a shared utility package that creates invisible dependencies.
- **No backward-compatibility shims.** When rewriting a module, delete the old one entirely. Update all callers. The codebase is small enough that this is always feasible.
- **Interfaces at boundaries.** Packages expose interfaces, callers depend on interfaces. Swapping implementations is a one-line change.

The test for good modularity: can an agent rewrite this package using only the spec, the interface definition, and the tests? If yes, the module is correctly scoped. If no, it's too coupled or too large.

### Documentation as Agent Affordance

Every package and every exported function gets a Go doc comment. No exceptions. This is not for humans browsing godoc; it is the primary mechanism by which agents understand the codebase fast enough to be useful.

An agent landing in a package for the first time should understand from the doc comments alone:
- **What this package does** (package-level doc comment)
- **What each function does** (not how, just what)
- **What the caller is responsible for** (preconditions, ownership, lifecycle)
- **What changes here would break** (downstream consumers, invariants, concurrency assumptions)

Specific standards:
- **Package comments** start with `// Package <name>` and explain purpose, scope boundaries, and key design decisions in 3-8 lines.
- **Exported functions** document behavior, not implementation. Include: what it returns on error, whether it's safe for concurrent use, and any side effects (file I/O, network, process exec).
- **Interfaces** document the contract, not the current implementation. State what an implementer must guarantee.
- **Non-obvious unexported functions** get comments too. If the function name alone doesn't make the behavior obvious, document it.
- **No restating the obvious.** `// Close closes the session` adds nothing. `// Close terminates all agent windows in the tmux session and blocks until each process exits` tells an agent what will actually happen.

Good documentation is agility. Agility is life.

### Beads Are the Process

Beads (`bd` CLI) is the exclusive work tracking system. No markdown TODO lists, no ad hoc task tracking, no "I'll remember to do that later." Every unit of work is a bead. Agents rely completely on bead contents to guide their actions.

**Beads are both execution guidelines and documentation.** A well-written bead tells an agent everything it needs to start working: what the problem is, what success looks like, what approach to take, and where the relevant spec lives. A poorly written bead produces garbage output. Bead quality directly controls agent output quality.

**Quality standards for bead content:**
- **Titles** are specific and action-oriented. "Fix auth token expiry race condition", not "Fix bug".
- **Description** states the problem and context. For complex work, include tested API queries, sample responses, and desired output format so the bead is resumable across sessions.
- **User story** is mandatory on all feature and bug beads: As a / I want / So that.
- **Acceptance criteria** describe outcomes, not implementation steps. Test: if you rewrote the solution with a different approach, would the criteria still apply? If not, they belong in the design field.
- **Design field** captures the implementation approach, architecture decisions, trade-offs. Separate from acceptance.
- **spec_id** links to the authoritative spec section. Agents follow the spec, not their imagination.

**Process rules (non-negotiable):**
- Agents comment their PLAN on the bead before writing code.
- Agents comment DONE with QA verification steps when finished.
- Engineers push to git before marking `ready_for_qa`.
- Only Nelson marks `ready_to_ship` and closes beads.
- Discovered work during implementation gets a new bead linked with `discovered-from`.
- `.beads/issues.jsonl` is committed alongside code changes. The DB file is gitignored.

**Bead lifecycle:**
`open` -> `in_progress` -> `ready_for_qa` -> `in_qa` -> `qa_passed` -> `ready_to_ship` -> `closed`

**Initech integration with beads:**
- `initech init` sets up `.beads/` in the project
- Session startup exports `BEADS_DIR` to all agent windows
- Agents use `bd` commands for all work tracking
- The dispatch protocol includes bead ID and claim instructions
- Initech's own development uses beads to track its own work

**Anti-patterns:**
- Never create markdown TODO lists (use bd)
- Never dispatch ungroomed beads (must have acceptance criteria)
- Never use `bd edit` (interactive; use `bd update` with flags)
- Never commit `.beads/beads.db` (JSONL only)
- Never skip `bd sync` at end of work sessions

## Project Documents

Four documents in `docs/`, each with a hard cap of 5000 lines. Single-file, living documents. Compact and essential.

| Document | Question | Contains |
|----------|----------|----------|
| `docs/prd.md` | **Why** does initech exist? | Problem statement, user needs, success criteria, non-goals |
| `docs/spec.md` | **What** does initech do? | Requirements, behavior, discovered patterns, acceptance criteria |
| `docs/systemdesign.md` | **How** does initech work? | Architecture, packages, data structures, build order, testing |
| `docs/roadmap.md` | **When/Who** does what get built? | Milestones, phases, success gates, agent assignments, strategic sequencing |

Beads handles tactical execution (what's ready now, who's assigned, what's blocked). The roadmap captures strategic sequencing: which milestones exist, what gates must pass before moving to the next phase, and how work is allocated across agents.

**Rules (apply to all three):**
- **Hard cap: 5000 lines.** Forces compact writing and prioritization. If adding a section would push past the limit, compress or cut something less important first.
- **Essential detail only.** Capture at the level needed to implement. Expand on demand, not preemptively.
- **Living documents.** Updated as discovery proceeds and implementation reveals new constraints. Never frozen.
- **Single file each.** No splitting. One place to look per concern, one place to update.
