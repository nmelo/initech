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

## Spec Document

All project specs live in a single file: `docs/spec.md`. This is the source of truth for what initech does and how it works. Rules:

- **Hard cap: 5000 lines.** Forces compact writing and prioritization. If adding a section would push past the limit, compress or cut something less important first.
- **Essential detail only.** Capture the "what" and "why" at the level needed to implement. Expand sections on demand, not preemptively.
- **Living document.** Updated as discovery proceeds and implementation reveals new constraints. Never frozen.
- **Single file.** No splitting into multiple spec docs. One place to look, one place to update.
