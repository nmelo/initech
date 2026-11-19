package schedule

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ini-fzz5: a job late by more than LateBound is skipped, once per job, and
// says so; --run-late runs it once. Fixed times throughout.

func TestLate_TheBoundIsFifteenMinutes(t *testing.T) {
	if LateBound != 15*time.Minute {
		t.Fatalf("LateBound = %v, want 15m (pm's ruling, named in the guide)", LateBound)
	}
}

// AC2: stopped across two 06:00 slots and started at 15:00: skipped, once,
// claiming the newest slot; nothing fires after.
func TestLate_ATimeJobLateBeyondTheBoundIsSkippedOnce(t *testing.T) {
	j := Job{At: "06:00", Created: at(3, 5, 0), LastRun: ptr(at(3, 6, 0))}
	started := at(5, 15, 0)
	d := j.Decide(started, started)
	if !d.Skip || d.Fire || !d.Due.Equal(at(5, 6, 0)) || d.Late != 9*time.Hour || d.Runs != 2 || !d.Missed {
		t.Fatalf("decision %+v, want a skip of 2 runs at 05 06:00, 9h late", d)
	}
	if _, _, fire := j.Check(started, started); fire {
		t.Fatal("Check fired a skipped job")
	}
	j.LastRun = &d.Due
	if d := j.Decide(started.Add(time.Minute), started); d.Skip || d.Fire {
		t.Fatalf("after the skip claimed its slot: %+v", d)
	}
}

// AC3: --run-late runs once, however many slots were missed.
func TestLate_ARunLateJobRunsOnce(t *testing.T) {
	j := Job{At: "06:00", Created: at(1, 5, 0), LastRun: ptr(at(1, 6, 0)), RunLate: true}
	started := at(5, 15, 0)
	d := j.Decide(started, started)
	if !d.Fire || d.Skip || d.Late != 9*time.Hour || d.Runs != 4 {
		t.Fatalf("decision %+v, want one run, 9h late, covering 4 slots", d)
	}
	j.LastRun = &d.Due
	if d := j.Decide(started.Add(time.Minute), started); d.Fire || d.Skip {
		t.Fatalf("a run-late job ran twice: %+v", d)
	}
}

// AC4 (nothing changes for on time or a little late): late by under the
// bound fires as today.
func TestLate_AJobLateWithinTheBoundFires(t *testing.T) {
	j := Job{At: "09:30", Created: at(4, 8, 0)}
	if d := j.Decide(at(4, 9, 44), at(4, 9, 40)); !d.Fire || d.Skip {
		t.Fatalf("14 minutes late: %+v, want it fired", d)
	}
	if d := j.Decide(at(4, 9, 30), at(4, 8, 0)); !d.Fire || d.Late != 0 {
		t.Fatalf("on time: %+v", d)
	}
}

// pm (4): lateness is judged at every check: a Mac asleep through the slot
// with initech running skips too (missed is false: it never stopped).
func TestLate_ASleptThroughSlotIsSkippedWithInitechRunning(t *testing.T) {
	j := Job{At: "09:30", Created: at(3, 8, 0), LastRun: ptr(at(3, 9, 30))}
	started := at(4, 8, 0) // running since before the slot
	d := j.Decide(at(4, 18, 30), started)
	if !d.Skip || d.Missed || d.Late != 9*time.Hour {
		t.Fatalf("decision %+v, want a skip 9h late, not missed", d)
	}
}

// pm, lead1's second cause: an hourly job across a 17h sleep with initech
// running fires 0 times (one skip), or once with run_late; it resumes from
// its newest slot.
func TestLate_AnIntervalJobAcrossASleepDoesNotCatchUp(t *testing.T) {
	started := at(4, 0, 0)
	j := Job{Every: "1h", Created: started, LastRun: ptr(at(4, 0, 0))}
	now := at(4, 17, 0)
	d := j.Decide(now, started)
	if !d.Skip || d.Fire || d.Runs != 17 || !d.Due.Equal(at(4, 17, 0)) {
		t.Fatalf("decision %+v, want one skip of 17 runs claiming 17:00", d)
	}
	j.LastRun = &d.Due
	fires := 0
	for m := 1; m <= 59; m++ {
		if d := j.Decide(now.Add(time.Duration(m)*time.Minute), started); d.Fire || d.Skip {
			fires++
		}
	}
	if fires != 0 {
		t.Fatalf("%d more fires in the hour after the skip, want 0", fires)
	}
	if d := j.Decide(at(4, 18, 0), started); !d.Fire {
		t.Fatalf("did not resume at 18:00: %+v", d)
	}
	j.RunLate, j.LastRun = true, ptr(at(4, 0, 0))
	if d := j.Decide(now, started); !d.Fire || d.Skip || !d.Due.Equal(at(4, 17, 0)) {
		t.Fatalf("run_late: %+v, want one run claiming 17:00", d)
	}
}

// Within the bound an interval job does not catch up either: a 1-minute job
// paused 10 minutes fires once, claiming its newest slot.
func TestLate_AnIntervalJobLateWithinTheBoundFiresOnce(t *testing.T) {
	started := at(4, 10, 0)
	j := Job{Every: "1m", Created: started, LastRun: ptr(at(4, 10, 0))}
	d := j.Decide(at(4, 10, 10), started)
	if !d.Fire || !d.Due.Equal(at(4, 10, 10)) {
		t.Fatalf("decision %+v, want one fire claiming 10:10", d)
	}
	j.LastRun = &d.Due
	if d := j.Decide(at(4, 10, 10).Add(30*time.Second), started); d.Fire {
		t.Fatal("caught up a second fire")
	}
}

func TestLate_TheSkipLineSaysHowLateAndHowMany(t *testing.T) {
	if got := SkipResult(9*time.Hour, 3); got != "skipped: 9h late, allowed 15m (3 runs skipped)" {
		t.Fatalf("got %q", got)
	}
	if got := SkipResult(17*time.Minute, 1); got != "skipped: 17m late, allowed 15m (1 run skipped)" {
		t.Fatalf("got %q", got)
	}
	if got := SkipResult(2*time.Hour+30*time.Minute, 2); got != "skipped: 2h30m late, allowed 15m (2 runs skipped)" {
		t.Fatalf("got %q", got)
	}
}

// AC4: a job stored by the previous release has no run_late: the safe default.
func TestLate_AnOldFileGetsTheSafeDefault(t *testing.T) {
	var f File
	old := "next_id: 1\njobs:\n  - id: s1\n    owner: super\n    to: eng1\n    at: \"06:00\"\n    message: standup\n    created: 2026-11-01T05:00:00Z\n"
	if err := yaml.Unmarshal([]byte(old), &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Jobs) != 1 || f.Jobs[0].RunLate {
		t.Fatalf("jobs %+v, want one job without run_late", f.Jobs)
	}
	out, _ := yaml.Marshal(Job{ID: "s2", Owner: "a", To: "b", At: "06:00", Message: "m"})
	if string(out) != "" && contains(string(out), "run_late") {
		t.Fatalf("a job without run_late wrote the field:\n%s", out)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
