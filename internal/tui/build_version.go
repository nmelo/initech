package tui

import "regexp"

// releaseVersionRe is the shape goreleaser stamps from a release tag: X.Y.Z,
// with an optional leading v for a build made with make's VERSION=vX.Y.Z.
var releaseVersionRe = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)

// IsReleaseVersion reports whether v is a plain release number. Anything else
// (empty, the default "dev", a hand-set string, a prerelease such as
// 2.50.0-rc1) is a dev build. This is the one meaning of dev build: the
// session record's folder and the delivery guard both ask here, and the doubt
// falls on the side where a test build cannot reach the spend ledger or a live
// session.
func IsReleaseVersion(v string) bool { return releaseVersionRe.MatchString(v) }
