# initech for iPhone and iPad

The SwiftUI client of `initech app-serve`, the service on the Mac. One app for
iPhone and iPad: a home list of the panes you pinned and named, each pane as a
conversation you can type into, and a key strip for a pane stuck at a dialog.

## Build and test (headless)

Xcode only; nothing else to install. The committed `InitechApp.xcodeproj` is
generated from `project.yml`; after changing the file list or a setting, run
`xcodegen generate --spec app/project.yml` and commit both.

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
app/scripts/app-tests-run.sh <sha> iphone   # then the same for ipad
```

It refuses, starting nothing, when no runner with that label is online or the
repo variable `APP_TESTS` is not `on`; the variable is set only once a load
sample shows the runner's machine can take one simulator. A run waits at the
shared load gate (`.github/actions/load-gate`: while that machine's 5-minute
load is over 6, during another project's nightly window, and while any
simulator is booted there; after 20 minutes it goes ahead with a warning),
runs one device on a fresh simulator through `sim-test.sh` once its first boot
has finished, and fails if a crash report from the app, its test runners,
SpringBoard or PosterBoard names that simulator (a report from any other
process there is named as a warning: nobody sits at that machine), or if any
xcodebuild or simulator process it started is still running. One simulator
runs on that machine at a time; pull requests never run it.

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

- `InitechApp/Pairing.swift`, `PairingStore.swift`, `PairingView.swift`: the
  pairing code (`initech://pair?host=<ip>&port=<n>&token=<t>&fp=<f>`), scanned
  as a QR or pasted, kept in the keychain on this device only.
- `InitechApp/AppLock.swift`: Face ID on open and on every return to the app.
- `InitechApp/Models.swift`, `ServiceClient.swift`: the API shapes and the
  HTTPS client: the certificate pin, the Bearer token, the feed parser.
- `InitechApp/HomeModel.swift`, `HomeView.swift`: pinned panes and the Rigs view.
- `InitechApp/PaneModel.swift`, `PaneView.swift`: the pane's conversation.
