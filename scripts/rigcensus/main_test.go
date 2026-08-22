package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseGates runs the gate resolver over one synthetic test file.
func parseGates(t *testing.T, src string) map[string][]string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return gatesByTest(f)
}

// The blind spot this tool found in itself: the gate is declared in a helper,
// so a scan that reads only Test bodies sees an ungated test and reports OK.
// INITECH_NOTIF_PROBE is the real instance -- requireNotifProbe(t) reads the
// env and skips on the test's behalf.
func TestGatesByTest_GateDeclaredInHelperIsAttributedToItsCaller(t *testing.T) {
	got := parseGates(t, `package tui
import ("os"; "testing")
func requireProbe(t *testing.T) {
	t.Helper()
	if os.Getenv("INITECH_NOTIF_PROBE") != "1" { t.Skip("probe off") }
}
func TestProbe_Channel(t *testing.T) { requireProbe(t); _ = 1 }
`)
	envs := got["TestProbe_Channel"]
	if len(envs) != 1 || envs[0] != "INITECH_NOTIF_PROBE" {
		t.Fatalf("helper-declared gate not attributed to caller: got %v, want [INITECH_NOTIF_PROBE]", envs)
	}
}

// Gates reached through a chain of helpers resolve too -- the scan is a
// fixpoint, not a single hop.
func TestGatesByTest_GateReachedThroughTwoHelpers(t *testing.T) {
	got := parseGates(t, `package tui
import ("os"; "testing")
func inner(t *testing.T) { if os.Getenv("INITECH_DEEP") == "" { t.Skipf("off") } }
func outer(t *testing.T) { inner(t) }
func TestDeep_Rig(t *testing.T) { outer(t) }
`)
	if envs := got["TestDeep_Rig"]; len(envs) != 1 || envs[0] != "INITECH_DEEP" {
		t.Fatalf("two-hop gate not resolved: got %v", envs)
	}
}

// A var that is read but never gated on is a config knob, not a rig gate.
// INITECH_RIG_ARTIFACTS picks an output directory; reporting it as an unrun rig
// would make the tool cry wolf forever.
func TestGatesByTest_EnvReadWithoutSkipIsNotAGate(t *testing.T) {
	got := parseGates(t, `package tui
import ("os"; "testing")
func TestRig_WritesArtifacts(t *testing.T) {
	dir := os.Getenv("INITECH_RIG_ARTIFACTS")
	_ = dir
}
`)
	if envs, ok := got["TestRig_WritesArtifacts"]; ok {
		t.Fatalf("config knob reported as a gate: %v", envs)
	}
}

// A helper that skips for an unrelated reason must not launder every env the
// test reads into a gate.
func TestGatesByTest_SkipInCallerDoesNotGateAnUnrelatedKnob(t *testing.T) {
	got := parseGates(t, `package tui
import ("os"; "testing")
func TestMixed(t *testing.T) {
	if os.Getenv("INITECH_REAL") != "1" { t.Skip("gated") }
	_ = os.Getenv("INITECH_RIG_ARTIFACTS")
}
`)
	envs := got["TestMixed"]
	// Both are read inside a function that skips, so both are reported. This is
	// the tool's deliberate conservative edge: it over-reports rather than
	// under-reports, and the exemption file is where a human says which is
	// which. Assert the gate is present rather than that the knob is absent.
	found := false
	for _, e := range envs {
		if e == "INITECH_REAL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("real gate lost: got %v", envs)
	}
}

// The propagation loop earns its place only on this shape: a helper that READS
// the env but delegates the actual skip one level deeper. Without propagation
// that helper is "reads env, never skips" -- a config knob -- and the rig goes
// invisible. Found by mutating the loop out and watching the other gate tests
// stay green: they all use helpers that read and skip in the same body.
func TestGatesByTest_HelperReadsEnvAndDelegatesTheSkipDeeper(t *testing.T) {
	got := parseGates(t, `package tui
import ("os"; "testing")
func skipRig(t *testing.T, why string) { t.Skip(why) }
func requireRig(t *testing.T) {
	if os.Getenv("INITECH_SPLIT") != "1" { skipRig(t, "rig off") }
}
func TestSplit_Rig(t *testing.T) { requireRig(t) }
`)
	if envs := got["TestSplit_Rig"]; len(envs) != 1 || envs[0] != "INITECH_SPLIT" {
		t.Fatalf("gate lost when the skip lives deeper than the env read: got %v", envs)
	}
}

func writeCensusFixture(t *testing.T, testSrc, workflow, exemptions string) (dir, wf, ex string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "tui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rig_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	wf = filepath.Join(root, "ci.yml")
	if err := os.WriteFile(wf, []byte(workflow), 0o644); err != nil {
		t.Fatal(err)
	}
	ex = filepath.Join(root, "exempt.txt")
	if err := os.WriteFile(ex, []byte(exemptions), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, wf, ex
}

const oneGatedRig = `package tui
import ("os"; "testing")
func TestNewRig_Composed(t *testing.T) {
	if os.Getenv("INITECH_NEWRIG") != "1" { t.Skip("gated") }
}
`

// The bead's verification requirement, as a test: adding a new env-gated rig
// without wiring it into CI must FAIL.
func TestRun_UnwiredGatedRigFails(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_OTHER: "1"
        run: go test -run 'OtherRig' ./...
`, "")
	err := run(dir, wf, ex, false)
	if err == nil {
		t.Fatal("unwired gated rig passed the census; the whole tool is inert")
	}
	if !strings.Contains(err.Error(), "INITECH_NEWRIG") {
		t.Fatalf("failure does not name the unrun rig: %v", err)
	}
}

// Both halves are load-bearing: env alone is a green job that runs nothing.
// This is the 35AK shape -- wired into env:, left out of the selector.
func TestRun_EnvSetButSelectorExcludesItFails(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'SomeOtherRig' ./...
`, "")
	if err := run(dir, wf, ex, false); err == nil {
		t.Fatal("env-without-selector counted as coverage; that is exactly the 35AK near-miss")
	}
}

// ...and a selector alone, with no env, skips at the gate.
func TestRun_SelectorMatchesButEnvUnsetFails(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        run: go test -run 'TestNewRig_Composed' ./...
`, "")
	if err := run(dir, wf, ex, false); err == nil {
		t.Fatal("selector-without-env counted as coverage; the rig would skip")
	}
}

func TestRun_EnvAndSelectorTogetherIsCoverage(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'TestNewRig_Composed' ./...
`, "")
	if err := run(dir, wf, ex, false); err != nil {
		t.Fatalf("correctly wired rig reported uncovered: %v", err)
	}
}

// A -short step skips every gated rig by construction, so it must never count
// as coverage however its env is set. This is the ini-4bf2 lesson wired in: a
// gate that runs half the suite reports on half the claim.
func TestRun_ShortStepIsNotCoverage(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  build-and-test:
    steps:
      - name: Test
        env:
          INITECH_NEWRIG: "1"
        run: go test -short -run 'TestNewRig_Composed' ./...
`, "")
	if err := run(dir, wf, ex, false); err == nil {
		t.Fatal("-short step counted as rig coverage")
	}
}

func TestRun_ExemptionWithReasonSatisfiesTheCensus(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs: {}
`, "INITECH_NEWRIG  # needs a live authenticated claude. TRIGGER: on a Claude bump, by whoever bumps\n")
	if err := run(dir, wf, ex, false); err != nil {
		t.Fatalf("documented exemption rejected: %v", err)
	}
}

// An exemption without a reason is an accident with a comment character in it.
func TestRun_ExemptionWithoutReasonIsRejected(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, "jobs: {}\n", "INITECH_NEWRIG\n")
	err := run(dir, wf, ex, false)
	if err == nil || !strings.Contains(err.Error(), "no reason") {
		t.Fatalf("reasonless exemption accepted: %v", err)
	}
}

// An exemption that says why CI skips it but never says when a human runs it
// instead is how "dropped" turns into "forgotten". super's condition on the
// ini-0lko ruling, enforced in the parser rather than left to memory.
func TestRun_ExemptionWithoutRunTriggerIsRejected(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, "jobs: {}\n",
		"INITECH_NEWRIG  # needs a live authenticated claude, cannot run on a runner\n")
	err := run(dir, wf, ex, false)
	if err == nil || !strings.Contains(err.Error(), "run trigger") {
		t.Fatalf("triggerless exemption accepted: %v", err)
	}
}

// A leftover exemption claims a decision about something that no longer exists,
// and the next reader trusts it.
func TestRun_StaleExemptionFails(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'TestNewRig_Composed' ./...
`, "INITECH_LONG_GONE  # deleted two releases ago\n")
	err := run(dir, wf, ex, false)
	if err == nil || !strings.Contains(err.Error(), "INITECH_LONG_GONE") {
		t.Fatalf("stale exemption not reported: %v", err)
	}
}

// The census must hold over the REAL repo, not just synthetic fixtures --
// otherwise it is a tool that only ever grades its own homework.
func TestRun_RepositoryInventoryIsFullyCovered(t *testing.T) {
	root := repoRoot(t)
	err := run(
		filepath.Join(root, "internal", "tui"),
		filepath.Join(root, ".github", "workflows", "ci.yml"),
		filepath.Join(root, ".github", "rig-census-exemptions.txt"),
		false,
	)
	if err != nil {
		t.Fatalf("live inventory census failed:\n%v", err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("go.mod not found above the test's working directory")
	return ""
}
