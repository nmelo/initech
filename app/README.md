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
sample shows the runner's machine can take one simulator. A run waits for that
machine's load (`app/scripts/wait-load.sh`, 5-minute load under 30 and
1-minute under 50, refusing after 15 minutes), runs one device on a fresh
simulator through `sim-test.sh`, and fails if a crash report names that
simulator or if any xcodebuild or simulator process it started is still
running. One app job runs at a time; pull requests never run it.

On this Mac, run tests only through `app/scripts/sim-test.sh` under
`super/tools/heavy.sh`, one at a time, and never open Simulator.app:

```bash
~/Desktop/Projects/initech/super/tools/heavy.sh app/scripts/sim-test.sh iphone /tmp/run.log
```

It creates a simulator for that run alone, runs `xcodebuild test` on it with
`-parallel-testing-enabled NO`, then shuts it down and deletes it, however the
run ends. A simulator kept between runs carries its state, and one such
device crashed PosterBoard at every boot. A new crash report from the run's
device fails the run with exit 9: stop and tell super.

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

## Layout

- `InitechApp/Pairing.swift`, `PairingStore.swift`, `PairingView.swift`: the
  pairing code (`initech://pair?host=<ip>&port=<n>&token=<t>&fp=<f>`), scanned
  as a QR or pasted, kept in the keychain on this device only.
- `InitechApp/AppLock.swift`: Face ID on open and on every return to the app.
- `InitechApp/Models.swift`, `ServiceClient.swift`: the API shapes and the
  HTTPS client: the certificate pin, the Bearer token, the feed parser.
- `InitechApp/HomeModel.swift`, `HomeView.swift`: pinned panes and the Rigs view.
- `InitechApp/PaneModel.swift`, `PaneView.swift`: the pane's conversation.
