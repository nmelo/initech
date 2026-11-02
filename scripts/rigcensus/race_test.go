package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// raceFixture writes one gated rig, a workflow and a race exemption file, and
// runs the race census over them. The CI exemption file holds notRun.
func raceFixture(t *testing.T, workflow, raceExempt, notRun string) error {
	t.Helper()
	dir, wf, ex := writeCensusFixture(t, oneGatedRig, workflow, notRun)
	path := filepath.Join(t.TempDir(), "race.txt")
	if err := os.WriteFile(path, []byte(raceExempt), 0o644); err != nil {
		t.Fatal(err)
	}
	return runRace(dir, wf, ex, path, false)
}

// The shape this census exists for: the job runs under -race, the gate is unset,
// the rig skips, and the job is green. The base workflow is exactly this.
func TestRunRace_RaceStepWithoutTheGateIsNotCoverage(t *testing.T) {
	err := raceFixture(t, `
jobs:
  race:
    steps:
      - name: Test (race detector)
        run: make test-race
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'TestNewRig_Composed' ./...
`, "", "")
	if err == nil {
		t.Fatal("a race step with the gate unset counted as coverage; the rig skips under it")
	}
	if !strings.Contains(err.Error(), "INITECH_NEWRIG") {
		t.Fatalf("failure does not name the rig: %v", err)
	}
}

func TestRunRace_GateAndSelectorInARaceStepIsCoverage(t *testing.T) {
	err := raceFixture(t, `
jobs:
  race-rigs:
    steps:
      - name: Rigs under race
        env:
          INITECH_NEWRIG: "1"
        run: go test -race -run 'TestNewRig_Composed' ./...
`, "", "")
	if err != nil {
		t.Fatalf("a wired race step reported uncovered: %v", err)
	}
}

// make test-race is a race step too: with the gate set and no -run, it runs
// everything.
func TestRunRace_MakeTestRaceWithTheGateSetIsCoverage(t *testing.T) {
	err := raceFixture(t, `
jobs:
  race:
    steps:
      - name: Test (race detector)
        env:
          INITECH_NEWRIG: "1"
        run: make test-race
`, "", "")
	if err != nil {
		t.Fatalf("make test-race with the gate set reported uncovered: %v", err)
	}
}

func TestRunRace_RaceStepSelectingOtherRigsIsNotCoverage(t *testing.T) {
	err := raceFixture(t, `
jobs:
  race-rigs:
    steps:
      - name: Rigs under race
        env:
          INITECH_NEWRIG: "1"
        run: go test -race -run 'SomeOtherRig' ./...
`, "", "")
	if err == nil {
		t.Fatal("a race step that does not select the rig counted as coverage")
	}
}

// A word that merely contains -race is not the flag.
func TestRunRace_ARunValueContainingRaceIsNotTheFlag(t *testing.T) {
	err := raceFixture(t, `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'TestNewRig_Composed|Thing-race' ./...
`, "", "")
	if err == nil {
		t.Fatal("a -run value with -race in it counted as the race detector")
	}
}

// Behind a dispatch input the step runs only when asked, so no nightly and no
// cut sees it, at the job's if or the step's.
func TestRunRace_OnDemandStepIsNotCoverage(t *testing.T) {
	for name, workflow := range map[string]string{
		"job": `
jobs:
  race-rigs:
    if: ${{ inputs.race_rigs }}
    steps:
      - name: Rigs under race
        env:
          INITECH_NEWRIG: "1"
        run: go test -race -run 'TestNewRig_Composed' ./...
`,
		"step": `
jobs:
  race-rigs:
    steps:
      - name: Rigs under race
        if: ${{ inputs.race_rigs }}
        env:
          INITECH_NEWRIG: "1"
        run: go test -race -run 'TestNewRig_Composed' ./...
`,
	} {
		if err := raceFixture(t, workflow, "", ""); err == nil {
			t.Errorf("%s-level on-demand step counted as the gate", name)
		}
	}
}

const noRaceWorkflow = `
jobs:
  composed-rigs:
    steps:
      - name: Rigs
        env:
          INITECH_NEWRIG: "1"
        run: go test -run 'TestNewRig_Composed' ./...
`

func TestRunRace_ExemptionWithReasonAndTriggerSatisfiesTheCensus(t *testing.T) {
	err := raceFixture(t, noRaceWorkflow, "INITECH_NEWRIG # memory: over the slice. TRIGGER: by hand\n", "")
	if err != nil {
		t.Fatalf("exempted rig reported uncovered: %v", err)
	}
}

func TestRunRace_ExemptionWithoutTriggerIsRejected(t *testing.T) {
	err := raceFixture(t, noRaceWorkflow, "INITECH_NEWRIG # memory: over the slice\n", "")
	if err == nil || !strings.Contains(err.Error(), "TRIGGER") {
		t.Fatalf("exemption with no trigger: got %v", err)
	}
}

// A rig CI does not run at all is already named with its reason.
func TestRunRace_RigExemptFromCIIsOutsideThePass(t *testing.T) {
	err := raceFixture(t, "jobs: {}\n", "", "INITECH_NEWRIG # needs a real Claude. TRIGGER: by hand\n")
	if err != nil {
		t.Fatalf("a rig CI never runs was asked to run under -race: %v", err)
	}
}

// The on-demand exemptions must go the moment the step joins the gate.
func TestRunRace_ExemptionForARigTheGateRunsFails(t *testing.T) {
	err := raceFixture(t, `
jobs:
  race-rigs:
    steps:
      - name: Rigs under race
        env:
          INITECH_NEWRIG: "1"
        run: go test -race -run 'TestNewRig_Composed' ./...
`, "INITECH_NEWRIG # memory. TRIGGER: by hand\n", "")
	if err == nil || !strings.Contains(err.Error(), "INITECH_NEWRIG") {
		t.Fatalf("redundant exemption passed: %v", err)
	}
}

func TestRunRace_StaleExemptionFails(t *testing.T) {
	err := raceFixture(t, noRaceWorkflow,
		"INITECH_NEWRIG # memory. TRIGGER: by hand\nINITECH_GONE # memory. TRIGGER: by hand\n", "")
	if err == nil || !strings.Contains(err.Error(), "INITECH_GONE") {
		t.Fatalf("stale exemption passed: %v", err)
	}
}

// Feed it nothing: no gates and no steps is a clean pass, never a crash, and a
// missing exemption file means no exemptions.
func TestRunRace_EmptyInventoryPasses(t *testing.T) {
	dir, wf, ex := writeCensusFixture(t, "package tui\n", "jobs: {}\n", "")
	if err := runRace(dir, wf, ex, filepath.Join(t.TempDir(), "absent.txt"), false); err != nil {
		t.Fatalf("empty inventory: %v", err)
	}
}

func TestRunRace_RepositoryInventoryIsFullyAccountedFor(t *testing.T) {
	root := repoRoot(t)
	err := runRace(
		filepath.Join(root, "internal", "tui"),
		filepath.Join(root, ".github", "workflows", "ci.yml")+","+
			filepath.Join(root, ".github", "workflows", "rigs.yml"),
		filepath.Join(root, ".github", "rig-census-exemptions.txt"),
		filepath.Join(root, ".github", "rig-race-exemptions.txt"),
		false,
	)
	if err != nil {
		t.Fatalf("live race census failed:\n%v", err)
	}
}
