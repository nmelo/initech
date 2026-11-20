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

The PR template (auto-populated by GitHub) has a regression-test checkbox; see the next section for what reviewers expect.

## Bug fixes need regression tests

Every bug-fix PR must include a test that fails on `main` and passes with the fix. The test exists as evidence that the specific failure mode is now covered — by definition load-bearing, so the suite's mutation kill rate grows organically over time.

- The author checks the regression-test box in the PR template.
- The reviewer verifies the test is **new** (not pre-existing) and, ideally, that reverting the fix locally causes it to fail.
- If a regression test isn't practical (e.g., timing-dependent races, UI rendering glitches that resist unit tests), the PR author explains in the summary and the reviewer applies judgment. The default is "test required"; "N/A" is the explained exception, not the silent default.

## Development Setup

Requires Go 1.27+.

```bash
git clone https://github.com/nmelo/initech.git
cd initech

make build        # Build the binary
make test         # Run all tests
make check        # Vet + test (what CI runs)
```

### Hooks

Install the hooks once per checkout. `make install-hooks` points `core.hooksPath` at the versioned `scripts/hooks`:

```bash
make install-hooks
```

`pre-commit` runs `make check-fast` (build, vets, lints, censuses) and blocks a commit that does not pass.

`reference-transaction` keeps names that belong only to a private rig out of this public repository. Every commit, amend, merge, rebase and cherry-pick moves a branch through it, `--no-verify` included. It checks the lines each branch update adds, and the message of each new commit that no remote-tracking branch holds yet, against a list kept outside every tree at `~/.config/initech/private-terms`: one Perl-compatible regex per line, matched without regard to case, mode 0600. A match refuses the update and prints `path:line: pattern N`, never the matched text; rewrite the line to a placeholder and commit again. With no list, or a list with no pattern, it refuses every branch update, because a check with nothing to look for would pass everything. `make private-scan` runs the same check over every tracked file.

A capture from another rig (a pane screen, a footer, a transcript, a log) enters `testdata` only as a fixture rewritten from it: every name, path, bead id, host, account and piece of text swapped, keeping only the shape the test needs.

This is not optional. Don't disable the hooks, point `core.hooksPath` elsewhere, or change the repository other than by a commit from a hooked checkout.

## Code Style

- Follow the patterns in the surrounding package. Consistency beats novelty.
- Keep packages narrow. Each package has one job. Avoid cross-package dependencies that aren't already there.
- Write Go doc comments on all exported functions, types, and methods. This is how agents (and humans) understand the codebase quickly.
- Keep methods small and focused. If a function is doing two things, split it.
- Every package that shells out uses `exec.Runner`. Tests use `exec.FakeRunner`. No real processes in tests.

## Work Tracking

initech uses [beads](https://github.com/nmelo/beads) (`bd`) for issue tracking, not GitHub Issues. GitHub Issues are for bug reports from external contributors. Internal feature work, bugs found during development, and planned improvements all live in beads.

If you're an external contributor opening a PR to fix a bug you reported, that's fine — use GitHub Issues. If you're working on the project regularly, get set up with `bd`.
