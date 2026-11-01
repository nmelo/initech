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

In the initech rig, wrap every build in `super/tools/heavy.sh` and never open
Simulator.app.

## Layout

- `InitechApp/Pairing.swift`, `PairingStore.swift`, `PairingView.swift`: the
  pairing code (`initech://pair?host=<ip>&port=<n>&token=<t>&fp=<f>`), scanned
  as a QR or pasted, kept in the keychain on this device only.
- `InitechApp/AppLock.swift`: Face ID on open and on every return to the app.
- `InitechApp/Models.swift`, `ServiceClient.swift`: the API shapes and the
  HTTPS client: the certificate pin, the Bearer token, the feed parser.
- `InitechApp/HomeModel.swift`, `HomeView.swift`: pinned panes and the Rigs view.
- `InitechApp/PaneModel.swift`, `PaneView.swift`: the pane's conversation.
