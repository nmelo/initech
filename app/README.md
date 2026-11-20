# initech for iPhone and iPad

The SwiftUI client of `initech app-serve`, the service on the Mac. One app for
iPhone and iPad, in a terminal's look: a bar of four screens (the panes you
pinned and named, every rig's panes, what waits on you or is stuck, and
progress), and each pane as a conversation you can type into.

## Build and test (headless)

Xcode only; nothing else to install. The committed `InitechApp.xcodeproj` is
generated from `project.yml`; after changing the file list or a setting, run
`xcodegen generate --spec app/project.yml` and commit both.

The model logic lives in a local Swift package, `app/InitechCore`: it imports
no UIKit or SwiftUI, and its tests build and run on a Mac in seconds, with no
simulator:

```bash
sysctl -n vm.loadavg   # wait while the 5-minute figure is above 30
cd app/InitechCore && ~/Desktop/Projects/initech/super/tools/heavy.sh swift test
```

These tests never open a socket, so they may run on the shared Mac; a server
test does not belong in the package. The scheme runs them on the simulator
too, so `app-tests` covers them. The tests that need real HTTP, TLS or the pin
stay in `InitechAppTests`, and the simulator UI tests remain the final proof of
any change.

```bash
# Create a simulator once (any iPhone or iPad device type, iOS 26 or later):
xcrun simctl create my-iphone "iPhone 17 Pro" com.apple.CoreSimulator.SimRuntime.iOS-27-0

# Build and run the unit tests; xcodebuild boots the simulator without a window:
xcodebuild test -project app/InitechApp.xcodeproj -scheme InitechApp \
  -destination "id=<simulator udid>"

# Shut the simulator down afterwards:
xcrun simctl shutdown <simulator udid>
```

The tests run against `FixtureServer`, a real HTTPS server on the loopback
inside the test bundle (a self-signed test-only certificate, pinned like the
Mac's) that serves the app service's v1 API: the Bearer token gate, rigs,
status, pins, the conversation pages, send, keys and the SSE feed with
Last-Event-ID replay.

`InitechAppUITests/ScreenshotTests` drives the real app, paired with the
fixture through a debug-only launch argument (`-uitest-pairing <link>`,
which also skips Face ID; release builds do not contain it), to home and to
a pane at a dialog. With `TEST_RUNNER_SHOT_DIR=<dir>` set, it stops at each
screen until `<dir>/<stop>.done` exists, so `xcrun simctl io <udid>
screenshot` can take it.

The app's tests run on the Mac runner that CI already uses (`MAC_RUNNER`),
as the `app-tests` workflow, never on the rig's own Mac when that runner can
take them:

```bash
app/scripts/app-tests-run.sh <branch|commit> iphone   # then the same for ipad
```

It refuses, starting nothing, when no runner with that label is online or the
repo variable `APP_TESTS` is not `on`; the variable is set only once a load
sample shows the runner's machine can take one simulator. GitHub starts a run
only at a branch, so a commit id is run at `main`, with the id for the job to
check out, when `main` contains it; a commit `main` does not contain is refused
in one line (exit 3): push it to a branch and give the branch. A run GitHub
refuses is one line and exit 4. The job tests a named commit only when the
branch it was started on contains it.

### Pictures of a run

A run can name screens, and uploads one PNG of each:

```bash
app/scripts/app-tests-run.sh <branch|commit> iphone home,pane InitechAppUITests/ScreenshotTests
gh run download <run id> --repo nmelo/initech -n app-screens-<run id> -D <a new, empty folder>
```

The third argument is the screens, comma separated; the fourth, optional, is
the tests to run, as xcodebuild's `-only-testing` takes them, so a look does
not wait for the whole suite. A screen is a stop a UI test makes or a view a
test draws:

| Screen | What it shows | Test |
|---|---|---|
| `home` | the Panes list with five pins | `InitechAppUITests/ScreenshotTests` |
| `pane` | a pane at a dialog, a sent message and a pressed key | `InitechAppUITests/ScreenshotTests` |
| `panes` | the Panes list with its bottom bar | `InitechAppTests/ScreenshotTests` |
| `blockers` | the Blockers list, one row of each kind | `InitechAppTests/ScreenshotTests` |
| `progress` | the Progress list, one effort | `InitechAppTests/ScreenshotTests` |

`sim-test.sh` takes each picture with `xcrun simctl io screenshot` while the
test waits at the stop, one second after the test has checked what is on
screen. The run's summary lists each screen with its file name. A named screen
with no picture fails the run, by name (`sim-test.sh` exits 10, and the job's
own count fails the job): a wrong name, a test that never reached its stop, or
a capture that failed. A run that names no screen takes none.

The pictures come from the test fixtures only. The repository is public and so
are its artifacts, so `shots.sh` refuses a `live-` screen and any named screen
in a run paired with a live service, and the artifact is kept one day. A new
screen is a new stop in `InitechAppUITests/ScreenshotTests` (`try stop("<name>")`,
after the test has checked the screen), drawn from fixtures. A run waits at the
shared load gate (`.github/actions/load-gate`: while that machine's 5-minute
load is over 24, during another project's nightly window, and while any
simulator is booted there; after 20 minutes it fails the job, naming the
load and the line it waits under, so the runner frees and the rerun is a
choice),
runs one device on a fresh simulator through `sim-test.sh` once its first boot
has finished, and fails if a crash report from the app, its test runners,
SpringBoard or PosterBoard names that simulator (a report from any other
process there is named as a warning: nobody sits at that machine), or if any
xcodebuild or simulator process it started is still running. Pull requests
never run it.

One simulator runs on that machine at a time, and the host lock in
`sim-test.sh` is what keeps it so. A lock is taken over only when no process
has its holder's pid; a holder this user may not signal is alive. The workflow's concurrency group is per ref,
so a run pending on one ref is never replaced by a run on another; a newer run
on the same ref replaces an older pending one. Runs on different refs can
overlap, and the later one waits at the lock, saying once a minute which run
holds it. After 20 minutes it fails, naming the holder. A waiting job holds a
Mac runner.

The shared Mac runs no simulator and no test binary that opens a server:
both run on the runner's machine only, through the job above. A new binary
that listens, even on the loopback (FixtureServer, a harness built with
`swiftc`), can make macOS ask the person at the screen whether it may use the
local network, and on the shared Mac that question lands on the operator's
screen. Here, compile only, under `super/tools/heavy.sh`:

```bash
~/Desktop/Projects/initech/super/tools/heavy.sh xcodebuild build-for-testing -project InitechApp.xcodeproj \
  -scheme InitechApp -destination 'generic/platform=iOS Simulator' CODE_SIGNING_ALLOWED=NO
```

`sim-test.sh` creates a simulator for one run alone, runs `xcodebuild test` on
it with `-parallel-testing-enabled NO`, then shuts it down and deletes it,
however the run ends. A simulator kept between runs carries its state, and one
such device crashed PosterBoard at every boot. A new crash report from the
run's device fails the run with exit 9 (`CRASH_FATAL=all`, the default; the
job sets `ui`, as above).

The tests are bounded at 18 minutes and the first boot at 7; neither bound is
widened. The run collects no test diagnostics (`-collect-test-diagnostics never`):
with them, xcodebuild runs `simctl diagnose` on the device after the last test
and waits for it, ten minutes on the runners' machine, which reached the
18-minute bound. A failing run still has its log, the crash-report check and
the result bundle. When xcodebuild ends, `sim-test.sh` prints its exit code, how long it
ran and how long before that its log was last written; a passing run ends a few
seconds after its last suite, with `** TEST SUCCEEDED **`. If xcodebuild prints
nothing for a minute right after a suite, `hang-probe.sh` writes once what it
is doing to `xcodebuild.log.hang` (its process tree by command name, what of
the app and its test runner still runs in the device, the newest result
bundle's size and age) and a sample of its threads to
`xcodebuild.log.hang.sample`; the job uploads both with the log. The job's
summary lines print however the run ended.

## A TestFlight build

```bash
~/Desktop/Projects/initech/super/tools/heavy.sh app/scripts/testflight.sh /tmp/tf
```

Archives the checkout, exports it for App Store Connect and uploads it, with
the keys in `initech.yaml` `app_store_connect`, by path only and never printed.
The archive signs with the CI key; the export and the upload use the release
key, an Admin key, because only an Admin key may use the cloud-managed
distribution certificate. The release key file must be at
`~/.appstoreconnect/private_keys/AuthKey_<release_key_id>.p8`, where `altool`
looks. `--check` runs the checks alone: each key file present and mode 600,
the release key where `altool` looks; a failed check refuses with exit 3. A
stage that fails stops the script and names its log; nothing is retried. Raise
`CURRENT_PROJECT_VERSION` in `project.yml` before each new upload: App Store
Connect takes each build number once per version.

The app uses only Apple's system encryption, so `Info.plist` sets
`ITSAppUsesNonExemptEncryption` to false and a build needs no export-compliance
answer in App Store Connect. Adding encryption of its own goes back to Nelson
first.

## Layout

- `InitechCore/Sources/InitechCore/`: the package: the API shapes
  (`Models.swift`), the HTTPS client (`ServiceClient.swift`: the certificate
  pin, the Bearer token, the feed parser), `Pairing.swift`, `Log.swift` and
  `FollowState.swift`, what a new entry does to the conversation.
- `InitechCore/Sources/InitechCore/Pairing.swift`, `InitechApp/PairingStore.swift`,
  `PairingView.swift`: the
  pairing code (`initech://pair?host=<ip>&port=<n>&token=<t>&fp=<f>`), scanned
  as a QR or pasted, kept in the keychain on this device only.
- `InitechApp/AppLock.swift`: the app locks when it reaches the background, and
  asks for Face ID once when it is back in front (at launch too), never as it
  leaves; after a cancel the Unlock button asks. The rule is
  `InitechCore/.../LockAsk.swift`.
- `icon/`: the app icon's source: a 3D model of a red stapler stapling the terminal prompt (`stapler.py`, for
  Blender) and the step that puts it on its background and writes the icon asset (`compose.py`). `icon/README.md`
  says how to make the icon again and what each setting changes.
- `InitechApp/Theme.swift`: the app's look, a terminal's: near-black, monospaced, one red for what can be
  pressed, a colour per pane state. Screens take their colours and type from here.
- `InitechApp/HomeModel.swift`, `HomeView.swift`: the list side. It draws its own prompt header and a bottom bar
  of four keys, one per screen (`ListTab`): Panes, Rigs, Blockers, Progress. Panes ends with the build line:
  version, build, commit. No screen opens as a sheet.
- `InitechApp/RigsView.swift`: Rigs. Every rig's header always shows; scrolling opens one rig at a time
  (`InitechCore/.../RigStack.swift` is the arithmetic), and a row slides left to pin or unpin.
- `InitechApp/BlockersView.swift`, `ProgressView.swift`, `OperatorScreens.swift`: Blockers (rigs with nothing to
  say share one line) and Progress.
- `InitechApp/PaneModel.swift`, `PaneView.swift`: the pane's conversation, under its own header with the way
  back. A run of tool calls is one line (`InitechCore/.../ToolSummary.swift`). Above the input line, which ends the
  screen, sits the pane's list strip when its agent set one (`strip` in the status answer): what waits on you, one
  line per item.
