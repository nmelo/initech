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

## Bug fixes need regression tests

Every bug-fix PR must include a test that fails on `main` and passes with the fix. The point isn't coverage — it's evidence: a regression test, by definition, was written to catch a specific failure mode and would have prevented this bug from shipping. Each one is load-bearing.

- Pick up a bead labeled `bug` → write the test first (or alongside the fix), confirm it fails on the unpatched code, then write the fix and watch it go green.
- Name the test so future readers can map it back to the bug (e.g. `TestRunDeliver_QaPassed_FullNoOp` for the bug whose AC said "deliver must not regress qa_passed"). The bug's name lives in the suite forever.
- The PR template has a checkbox for this. Reviewers verify the test is new (not pre-existing) and applies judgment for the rare cases where a regression test isn't practical (timing-dependent races, UI glitches). The default is "test required"; "N/A" is the explained exception, not the silent default.

## Build

```bash
make build              # Build binary
make test               # Run all tests
make check              # Vet + lint-test-names + test
make release            # goreleaser release
```

## Test naming policy (ini-ybe.1)

Test names must describe the contract being verified, not the absence of a crash. Suffixes `_NoOp`, `_NoPanic`, `_DoesNotPanic`, `_Smoke` are rejected by `make lint-test-names` (also runs in CI). Either add a real assertion (preferred) or pick a name describing what the test verifies — e.g. `TestRender_HandlesNarrowTerminal` instead of `TestRender_NarrowNoPanic`. If a test legitimately verifies a no-op contract AND has assertions proving the no-op, add `// lint:test-name-allow <reason>` directly above the function. Use sparingly. Background: the repo audit found ~5% of tests assert nothing; coverage % stays high while mutation kill rate stays near zero, and naming is the cheapest cultural lever to stop new instances accumulating.
