# Initech

A Go CLI tool that captures the operator's local software development patterns into a reproducible, bootstrappable system. Named after the company from Office Space.

## What This Is

Initech is a TUI-based multi-agent orchestrator. It manages PTY-based agent panes, IPC messaging, and session lifecycle for running multiple Claude Code agents in parallel.

## Tech Stack

- Language: Go 1.25
- Dependencies: cobra (CLI), yaml.v3 (config), charmbracelet/ultraviolet + x/vt (terminal emulation), tcell (TUI), creack/pty (PTY management)
- Issue tracking: beads (bd CLI)

## Package Architecture

```
cmd/             # Cobra commands (init, up, status, down, stop, start, restart, standup, doctor, send, peek)
internal/
  exec/          # Runner interface + DefaultRunner + FakeRunner
  config/        # initech.yaml types, Load, Discover, Validate
  roles/         # Catalog (11 roles), Render ({{variable}}), templates (role + doc)
  scaffold/      # Directory tree creation, idempotent
  tmuxinator/    # YAML generation (main + grid sessions)
  tmux/          # Runtime: session inspection, Claude detection, memory, window mgmt
  tui/           # TUI: pane management, terminal emulation, IPC socket server
  git/           # Init, submodule, commit
```

Every package that shells out uses `exec.Runner`. Tests swap in `exec.FakeRunner`. No real tmux/git/bd needed in tests.

## Principles

### Disposable Modules

Architecture must support continuous rewrites. Any component should be replaceable in minutes, not hours.

- Small packages with narrow interfaces
- No shared mutable state between packages
- No deep dependency chains
- Favor duplication over coupling
- Interfaces at boundaries

### Documentation as Agent Affordance

Every package and every exported function gets a Go doc comment. This is the primary mechanism by which agents understand the codebase fast enough to be useful.

## Build

```bash
make build              # Build binary
make test               # Run all tests
make check              # Vet + test
make release            # goreleaser release
```
