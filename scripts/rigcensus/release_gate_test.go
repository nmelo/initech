package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestReleaseGate_BuildsOnlyAndRunsNoTests pins release.yml to a build: it runs
// no tests and only the goreleaser job runs on ubuntu (ini-c5a.5). The full
// suite still gates every release, once, in CI on the tested platform (macOS,
// ini-ibsm): the release job's guard (scripts/releaseguard, whose own test pins
// the guard before goreleaser) refuses a tag without a green push run carrying
// ci.yml's whole job set, full-suite-census included, so the ini-4bf2 gap (a
// gate that ran half the suite) cannot reopen. A test job here would re-run a
// suite already green at the commit; one on ubuntu would gate on an untested
// platform (ini-govx).
func TestReleaseGate_BuildsOnlyAndRunsNoTests(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// job name → runs-on, by walking top-level job blocks.
	jobRe := regexp.MustCompile(`(?m)^  ([a-zA-Z0-9_-]+):\n(?:(?:    .*|\s*)\n)*?    runs-on: ([^\n]+)`)
	runsOn := map[string]string{}
	for _, m := range jobRe.FindAllStringSubmatch(string(raw), -1) {
		runsOn[m[1]] = m[2]
	}
	if _, ok := runsOn["release"]; !ok {
		t.Fatalf("release.yml has no release job; jobs found: %v", runsOn)
	}
	for job, os := range runsOn {
		if os == "ubuntu-latest" && job != "release" {
			t.Errorf("job %q runs on ubuntu-latest; only the goreleaser build job may", job)
		}
	}
	testRun := regexp.MustCompile(`(?m)^\s+run: .*\b(make test|go test)\b`)
	if m := testRun.Find(raw); m != nil {
		t.Fatalf("release.yml runs tests (%q); the suite runs in CI and the guard checks it green at the tagged commit", strings.TrimSpace(string(m)))
	}
}
