package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestReleaseGate_TestJobRunsOnTheTestedPlatform pins release.yml's gating
// test job to macOS (ini-govx). ini-ibsm made macOS the only tested platform
// and ci.yml followed; release.yml did not, and v2.13.0's tag was blocked by
// an ubuntu-only timing red with CI green. Any job in release.yml that runs
// the suite must run where the suite is tested; the goreleaser job is a
// build and may stay on ubuntu.
func TestReleaseGate_TestJobRunsOnTheTestedPlatform(t *testing.T) {
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
	if got, ok := runsOn["test"]; !ok {
		t.Fatalf("release.yml has no test job; jobs found: %v", runsOn)
	} else if got != "macos-latest" {
		t.Fatalf("release.yml test job runs on %q, want macos-latest: the release gate must run on the tested platform (ini-ibsm, ini-govx)", got)
	}
	// Anchored to the WHOLE step (shipper, ini-govx review): a prefix match
	// on "make test" also accepted a downgrade to plain `make test`, which is
	// exactly the -short half-suite gap ini-4bf2 shipped behind twice.
	testStep := regexp.MustCompile(`(?m)^\s+run: make test-full\s*$`)
	for job, os := range runsOn {
		if os == "ubuntu-latest" && job != "release" {
			t.Errorf("job %q runs on ubuntu-latest; only the goreleaser build job may", job)
		}
	}
	if !testStep.Match(raw) {
		t.Fatal("release.yml no longer runs make test-full anywhere; the ini-4bf2 full-suite gate is gone")
	}
}
