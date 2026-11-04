package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const seqMake = `test-race:
	@rc=0; \
	for k in $$(seq 1 6); do \
	  go run ./scripts/shardtests run -race -timeout 30m -shards 6 -shard $$k -out race-record/shard$$k 2>&1 || rc=1; \
	done; \
	go run ./scripts/shardtests census -shards 6 $$(seq -f 'race-record/shard%g' 1 6) || rc=1; \
	exit $$rc
`

const oneRaceJob = `jobs:
  race:
    steps:
      - run: make test-race
  other:
    steps:
      - run: go test ./...
`

// shapeHas fails unless exactly the problems named by wants appear.
func shapeHas(t *testing.T, probs []string, wants ...string) {
	t.Helper()
	if len(probs) != len(wants) {
		t.Fatalf("problems %q, want %d naming %q", probs, len(wants), wants)
	}
	for i, w := range wants {
		if !strings.Contains(probs[i], w) {
			t.Errorf("problem %q does not name %q", probs[i], w)
		}
	}
}

// The repository as it is: the six race shards run one after another in one
// job, so the check is green.
func TestRaceShardShape_TheRepositoryRunsTheShardsOneAtATime(t *testing.T) {
	root := repoRootForWorkflows(t)
	mk, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	wf, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "rigs.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if probs := raceShardShape(mk, wf); len(probs) > 0 {
		t.Fatalf("the race shards would not run one at a time:\n%s", strings.Join(probs, "\n"))
	}
}

func TestRaceShardShape_ASequentialRecipeInOneJobPasses(t *testing.T) {
	shapeHas(t, raceShardShape([]byte(seqMake), []byte(oneRaceJob)))
}

func TestRaceShardShape_ABackgroundedShardFails(t *testing.T) {
	mk := strings.Replace(seqMake, "2>&1 || rc=1; \\", "& \\", 1)
	shapeHas(t, raceShardShape([]byte(mk), []byte(oneRaceJob)), "in parallel")
}

func TestRaceShardShape_ShardsAsPrerequisitesFail(t *testing.T) {
	mk := "test-race: race-1 race-2\n\t@echo done\nrace-1:\n\tgo run ./scripts/shardtests run -race -shards 2 -shard 1 -out a\n"
	shapeHas(t, raceShardShape([]byte(mk), []byte(oneRaceJob)), `"race-1 race-2"`, "does not run")
}

func TestRaceShardShape_XargsInParallelFails(t *testing.T) {
	mk := "test-race:\n\tseq 1 6 | xargs -P 6 -I{} go run ./scripts/shardtests run -race -shards 6 -shard {} -out r{}\n"
	shapeHas(t, raceShardShape([]byte(mk), []byte(oneRaceJob)), "in parallel")
}

func TestRaceShardShape_TwoRaceJobsInRigsFail(t *testing.T) {
	wf := oneRaceJob + "  race-b:\n    steps:\n      - run: go run ./scripts/shardtests run -race -shards 6 -shard 2 -out b\n"
	shapeHas(t, raceShardShape([]byte(seqMake), []byte(wf)), "race, race-b")
}

func TestRaceShardShape_AMatrixRaceJobFails(t *testing.T) {
	wf := "jobs:\n  race:\n    strategy:\n      matrix:\n        shard: [1, 2, 3]\n    steps:\n      - run: make test-race\n"
	shapeHas(t, raceShardShape([]byte(seqMake), []byte(wf)), "job race runs the race shards under a strategy matrix")
}

// Feed it nothing: no test-race target and no race job never pass by absence.
func TestRaceShardShape_NoTargetAndNoJobFail(t *testing.T) {
	shapeHas(t, raceShardShape([]byte("build:\n\tgo build .\n"), []byte("jobs: {}\n")),
		"no test-race target", "no job running the race shards")
}
