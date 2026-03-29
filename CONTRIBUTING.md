# Contributing to initech

## Reporting Bugs

Open a [GitHub issue](https://github.com/nmelo/initech/issues). Include:
- initech version (`initech version`)
- OS and terminal
- Steps to reproduce
- What you expected vs. what happened

## Submitting PRs

1. Fork the repo and create a branch from `main`
2. Make your changes with tests (see below)
3. Run `make check` — it must pass clean
4. Open a PR against `main` with a clear description of what and why

Keep PRs focused. One logical change per PR.

## Development Setup

Requires Go 1.25+.

```bash
git clone https://github.com/nmelo/initech.git
cd initech

make build        # Build the binary
make test         # Run all tests
make check        # Vet + test (what CI runs)
```

### Pre-commit Hook

Install the pre-commit hook once per checkout — it runs `make check` before every commit and blocks commits that break tests:

```bash
make install-hooks
```

This is not optional. Don't skip it, and don't bypass it with `--no-verify`.

## Code Style

- Follow the patterns in the surrounding package. Consistency beats novelty.
- Keep packages narrow. Each package has one job. Avoid cross-package dependencies that aren't already there.
- Write Go doc comments on all exported functions, types, and methods. This is how agents (and humans) understand the codebase quickly.
- Keep methods small and focused. If a function is doing two things, split it.
- Every package that shells out uses `exec.Runner`. Tests use `exec.FakeRunner`. No real processes in tests.

## Work Tracking

initech uses [beads](https://github.com/nmelo/beads) (`bd`) for issue tracking, not GitHub Issues. GitHub Issues are for bug reports from external contributors. Internal feature work, bugs found during development, and planned improvements all live in beads.

If you're an external contributor opening a PR to fix a bug you reported, that's fine — use GitHub Issues. If you're working on the project regularly, get set up with `bd`.
