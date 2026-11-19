package tui

// The release gate on watches (ini-n0to). Main holds the store, the commands
// and the conditions, but not delivery (2C, ini-kbpo); a release cut before
// delivery lands would let agents set watches whose endings never reach them.
// So a release build refuses every watch action and the modal, and a dev build
// keeps everything. Temporary: 2C's delivery commit deletes this file, its two
// call sites (handleIPCWatch and openWatchesModal) and its tests.

// watchesOffLine is the one line a release build answers.
const watchesOffLine = "watches are not in this release"

// watchesInThisBuild reports whether this build runs watches: a dev build
// does, a release build does not. An empty version is a dev build, as
// IsReleaseVersion decides.
func watchesInThisBuild(version string) bool { return !IsReleaseVersion(version) }
