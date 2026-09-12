Investigation ini-g8n9 reproduces an all-hidden viewer that owns its agents,
has their pane objects, renders nothing, and supplies no explanation. Production
code is unchanged from `694985fad0ab3cf7d97f6ecc06c6a52adce3c325`.

The files in this directory were copied from hover's `.initech` directory on
2026-09-12. `snapshot.json` records byte counts, modification times and SHA-256
hashes. No operator files or processes were changed. These three YAML files
contain layout/assignment/fleet preferences only.

The mechanism is deterministic:

1. `LoadAssignment` and `LoadFleetScopedLayout` resolve current group ownership.
   The captured files put eng/qa in window 2, including pm/super in eng: ten
   owned agents. `computePaneOwnership` serves that set to the viewer.
2. `visiblePanesForWindow` returns all ten owned panes from the 41 received
   pane objects. Ownership and canonical identities are correct in this case.
3. `LoadFleetState` and `applyFleetProjection` load `Hidden`. Every owned agent
   is hidden. `applyLayout` calls `computeLayout`, whose first filter removes
   hidden agents and returns an empty plan. This implements the documented
   global hide behavior.
4. `viewerEmptyExplanation` tests ownership but never tests hidden state. It
   returns an empty string and logs a supposed planning defect. Its caller in
   `render.go` therefore draws no explanation. Every subsequent call repeats
   the same warning.

The normal test in `../../g8n9_investigation_test.go` checks the exact owned
set and proves all its pane objects reach the ownership filter. Unhiding only
eng1 in the isolated copied store makes the same planner render exactly eng1.
Clearing hidden state makes it render the entire owner set without changing
ownership. The test uses real RemotePane identities and production loaders,
projection and planning; it deliberately does not launch processes or streams,
because the claim is about planning after streams have arrived. It makes no
transport, cold-start, full-screen or emulator-liveness claim.

The historical warnings list eight owners (eng1–eng5, qa1–qa3), whereas the
later captured layout also assigns pm/super to eng. A second test variant
changes just those two membership inputs to core to reproduce the historical
ownership set. That is a discriminating control, not a recovered historical
snapshot. The current state explains the current blank plan and is compatible
with the earlier symptom; it does not prove when or why the agents were hidden.
The old log's window-1 pane count must not be compared to the later disk state
as though they were simultaneous observations.

From a checkout of the investigation branch, qa1 can run the passing controls:

```sh
make test GOFLAGS='-run=TestG8N9 -v'
```

To reproduce the two failed contracts, install the preserved probe temporarily.
It lives outside the default suite so this investigation does not make CI red
or silently skip an expected failure. The exclusive create below refuses to
overwrite a pre-existing test. The `finally` removes only the file it created.

```sh
python3 - <<'PY'
from pathlib import Path
import subprocess
probe = Path('internal/tui/g8n9_contract_probe_test.go')
source = Path('internal/tui/testdata/g8n9/empty_contract_test.go.txt')
with probe.open('x') as f:
    f.write(source.read_text())
try:
    result = subprocess.run(['make', 'test', 'GOFLAGS=-run=TestG8N9 -v'])
    print('make exit:', result.returncode, '(expected 2 at baseline)')
    raise SystemExit(result.returncode)
finally:
    probe.unlink()
PY
```

Observed at the baseline: the characterization and both unhide controls pass;
`TestG8N9_AllHiddenViewerExplainsItsEmptyPlan` fails with ten owned hidden
streams and an empty explanation; `TestG8N9_UnchangedEmptyStateDoesNotWarnEveryFrame`
fails with 30 warnings in 30 unchanged frames. These assert a truthful
explanation and bounded diagnostics without prescribing PM-owned copy. To
independently pin the baseline, copy only the investigation test and this
fixture directory to a separate checkout of the named SHA, then run the same
commands. No production patch is required to obtain these results.

The field warning counts were 145,414 in `initech.log.1` (75,174,956 bytes) and
3,099 in `initech.log` (7,896,961 bytes): 148,513 total. The first rotated warning
was 2026-09-11 14:40:09.033 -04:00; its last was 15:59:47.298. The current file's
range was 16:00:13.829–16:01:52.546 that day. Both files contain 2.12.0 startup
records. Logging lacks the hidden-state information needed to retrospectively
prove the hidden set at those timestamps.

The crash file contains a separate current failure. `crash-summary.json` counts
completed watchdog records in a bounded 695,236,581-byte prefix (the potentially
incomplete final report is excluded). There are 11,802 v2.12.0 records, first
dated September 4, last September 12; 11,723 have the main goroutine in
`modalMaintenance -> paneShowsModalOnScreen -> emulatorBottomText -> SafeEmulator.Width`.
These are repeated watchdog reports, not 11,802 independent crashes.
`watchdog-stacks.txt` preserves only the header and relevant goroutines from
two recent records. They show:

- Main waiting on the emulator read lock for 1,026 / 1,855 minutes.
- IPC `Pane.SendText -> sendPaneTextLocked -> SafeEmulator.SendKey` blocked in
  `io.PipeWriter.Write` while holding that emulator's write lock.
- `Pane.readLoop -> SafeEmulator.Write` waiting on the same lock, verified by
  matching emulator addresses within each report.

The pipe-reader lifetime that produced this condition is not established.
The next investigation should distinguish an exited reader, an old emulator
left after resume/restart, and a still-present reader blocked writing to a
PTY. It needs a real workload reproducing the holder/waiter chain; deliberately
omitting a pipe reader would demonstrate blocking without explaining its
origin in the product. No claim connects this hang causally to the blank viewer.

Follow-ups: ini-uz42 covers the all-hidden explanation and recovery direction;
ini-4dzh covers bounded, correctly classified diagnostics; ini-1ftp investigates
the current emulator-lock hang. No production fix belongs to ini-g8n9.
