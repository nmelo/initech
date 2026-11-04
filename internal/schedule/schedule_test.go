package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// at builds a local time on 2026-11-04 (a Wednesday) for the tests' fake clock.
func at(day, hour, min int) time.Time {
	return time.Date(2026, 11, day, hour, min, 0, 0, time.Local)
}

func ptr(t time.Time) *time.Time { return &t }

// TestFile_RoundTripKeepsEveryFieldByteForByte: write, read, write again, and
// the bytes are the same; and every field is still there.
func TestFile_RoundTripKeepsEveryFieldByteForByte(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	want := &File{NextID: 2, Jobs: []Job{
		{ID: "s1", Owner: "eng1", To: "super", At: "09:30 weekdays", Message: "standup in 5",
			Created: at(4, 8, 0), LastRun: ptr(at(4, 9, 30)), LastResult: "delivered"},
		{ID: "s2", Owner: "qa1", To: "qa1", Every: "15m", Command: "bd ready --json | head -c 200",
			Created: at(4, 8, 1)},
	}}
	if err := Save(path, want); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, got); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatalf("a round trip changed the file:\n%s\n---\n%s", first, second)
	}
	g := got.Jobs[0]
	if g.ID != "s1" || g.Owner != "eng1" || g.To != "super" || g.At != "09:30 weekdays" ||
		g.Message != "standup in 5" || !g.Created.Equal(at(4, 8, 0)) || g.LastRun == nil ||
		!g.LastRun.Equal(at(4, 9, 30)) || g.LastResult != "delivered" {
		t.Fatalf("a field was lost: %+v", g)
	}
	if h := got.Jobs[1]; h.Every != "15m" || h.Command != "bd ready --json | head -c 200" || h.LastRun != nil {
		t.Fatalf("a field was lost: %+v", h)
	}
	if got.NextID != 2 {
		t.Fatalf("next_id %d, want 2", got.NextID)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v, want 0600", fi.Mode().Perm())
	}
}

// TestLoad_AFileThatDoesNotParseNamesTheLine: the caller keeps its last good
// schedule; the error says where to look.
func TestLoad_AFileThatDoesNotParseNamesTheLine(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"bad yaml": "next_id: 1\njobs:\n  - id: s1\n    to: [unclosed\n",
		"bad job":  "next_id: 2\njobs:\n  - id: s1\n    owner: eng1\n    to: super\n    every: 5m\n    message: hi\n    created: 2026-11-04T08:00:00Z\n  - id: s2\n    owner: eng1\n    to: super\n    every: 10s\n    message: too fast\n    created: 2026-11-04T08:00:00Z\n",
		"dup id":   "next_id: 2\njobs:\n  - id: s1\n    owner: a\n    to: b\n    every: 5m\n    message: x\n    created: 2026-11-04T08:00:00Z\n  - id: s1\n    owner: a\n    to: b\n    every: 5m\n    message: y\n    created: 2026-11-04T08:00:00Z\n",
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "line ") {
			t.Errorf("%s: err %v, want an error naming the line", name, err)
		}
	}
	if _, err := Load(filepath.Join(dir, "bad_job.yaml")); err == nil || !strings.Contains(err.Error(), "line 9") {
		t.Errorf("the bad job's error %v does not name line 9", err)
	}
}

// TestLoad_NothingIsAnEmptySchedule: no file, an empty file.
func TestLoad_NothingIsAnEmptySchedule(t *testing.T) {
	dir := t.TempDir()
	if f, err := Load(filepath.Join(dir, "missing.yaml")); err != nil || len(f.Jobs) != 0 {
		t.Fatalf("a missing file: %+v %v", f, err)
	}
	empty := filepath.Join(dir, "empty.yaml")
	os.WriteFile(empty, nil, 0o600)
	if f, err := Load(empty); err != nil || len(f.Jobs) != 0 {
		t.Fatalf("an empty file: %+v %v", f, err)
	}
}

func TestValidate_RefusesWhatIsNotAJob(t *testing.T) {
	ok := Job{Owner: "eng1", To: "super", Every: "5m", Message: "x"}
	for name, mut := range map[string]func(*Job){
		"no pane":           func(j *Job) { j.To = "" },
		"no owner":          func(j *Job) { j.Owner = "" },
		"both at and every": func(j *Job) { j.At = "09:00" },
		"neither":           func(j *Job) { j.Every = "" },
		"both kinds":        func(j *Job) { j.Command = "ls" },
		"two lines":         func(j *Job) { j.Message = ""; j.Command = "ls\nrm x" },
		"too short":         func(j *Job) { j.Every = "59s" },
		"bad time":          func(j *Job) { j.Every = ""; j.At = "25:00" },
		"bad day":           func(j *Job) { j.Every = ""; j.At = "09:00 funday" },
	} {
		j := ok
		mut(&j)
		if j.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good job refused: %v", err)
	}
}

func TestRemove_OnlyTheOwnerOrSuper(t *testing.T) {
	f := &File{}
	id, err := f.Add(Job{Owner: "eng1", To: "super", Every: "5m", Message: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Remove(id, "eng2"); err == nil || !strings.Contains(err.Error(), "eng1") {
		t.Fatalf("another agent: err %v, want a refusal naming eng1", err)
	}
	if _, err := f.Remove(id, "eng1"); err != nil || len(f.Jobs) != 0 {
		t.Fatalf("the owner: err %v, %d jobs left", err, len(f.Jobs))
	}
	id, _ = f.Add(Job{Owner: "eng1", To: "super", Every: "5m", Message: "x"})
	if _, err := f.Remove(id, "super"); err != nil {
		t.Fatalf("super: %v", err)
	}
	if _, err := f.Remove("s99", "super"); err == nil {
		t.Fatal("a job that does not exist was removed")
	}
	if id != "s2" {
		t.Fatalf("ids are not reused: got %s, want s2", id)
	}
}

// TestCheck_ATimeJobFiresOnceAtItsTime is AC2 with a fake clock.
func TestCheck_ATimeJobFiresOnceAtItsTime(t *testing.T) {
	started := at(4, 8, 0)
	j := Job{At: "09:30", Created: at(4, 8, 0)}
	if _, _, fire := j.Check(at(4, 9, 29), started); fire {
		t.Fatal("fired a minute early")
	}
	due, missed, fire := j.Check(at(4, 9, 30), started)
	if !fire || missed || !due.Equal(at(4, 9, 30)) {
		t.Fatalf("at its time: fire %v missed %v due %v", fire, missed, due)
	}
	j.LastRun = &due
	for _, now := range []time.Time{at(4, 9, 30), at(4, 9, 31), at(4, 23, 59)} {
		if _, _, fire := j.Check(now, started); fire {
			t.Fatalf("fired twice for one due time, at %v", now)
		}
	}
	if due, _, fire := j.Check(at(5, 9, 30), started); !fire || !due.Equal(at(5, 9, 30)) {
		t.Fatal("did not fire the next day")
	}
}

// TestCheck_ARestartInsideTheMinuteDoesNotRunItAgain: the last run is the
// guard, whatever the start time.
func TestCheck_ARestartInsideTheMinuteDoesNotRunItAgain(t *testing.T) {
	j := Job{At: "09:30", Created: at(4, 8, 0), LastRun: ptr(at(4, 9, 30))}
	if _, _, fire := j.Check(at(4, 9, 30).Add(40*time.Second), at(4, 9, 30).Add(20*time.Second)); fire {
		t.Fatal("a restart inside the minute ran the job again")
	}
}

// TestCheck_AJobDueWhileDownRunsOnceAndSaysSo.
func TestCheck_AJobDueWhileDownRunsOnceAndSaysSo(t *testing.T) {
	j := Job{At: "09:30", Created: at(3, 8, 0), LastRun: ptr(at(3, 9, 30))}
	started := at(5, 11, 0) // Down across two due times.
	due, missed, fire := j.Check(started, started)
	if !fire || !missed || !due.Equal(at(5, 9, 30)) {
		t.Fatalf("after the restart: fire %v missed %v due %v, want one missed run for the latest", fire, missed, due)
	}
	j.LastRun = &due
	if _, _, fire := j.Check(started.Add(time.Minute), started); fire {
		t.Fatal("the missed run ran twice")
	}
}

// TestCheck_ATimeJobCreatedAfterItsTimeWaitsForTomorrow.
func TestCheck_ATimeJobCreatedAfterItsTimeWaitsForTomorrow(t *testing.T) {
	j := Job{At: "09:30", Created: at(4, 10, 0)}
	if _, _, fire := j.Check(at(4, 10, 1), at(4, 8, 0)); fire {
		t.Fatal("a job added at 10:00 for 09:30 fired at once")
	}
}

func TestCheck_TheDayPatternIsHonoured(t *testing.T) {
	j := Job{At: "09:30 weekdays", Created: at(6, 0, 0)} // Friday the 6th; Saturday the 7th.
	if _, _, fire := j.Check(at(6, 9, 30), at(6, 0, 0)); !fire {
		t.Fatal("a weekdays job did not fire on a Friday")
	}
	j.LastRun = ptr(at(6, 9, 30)) // Friday ran.
	for _, now := range []time.Time{at(7, 9, 30), at(8, 9, 30)} {
		if _, _, fire := j.Check(now, at(6, 0, 0)); fire {
			t.Fatalf("a weekdays job fired on %v", now.Weekday())
		}
	}
	if due, _, fire := j.Check(at(9, 9, 30), at(6, 0, 0)); !fire || due.Weekday() != time.Monday {
		t.Fatal("a weekdays job did not fire on the Monday")
	}
}

// TestCheck_AnIntervalJobFiresAtEachIntervalAndResumesAfterARestart.
func TestCheck_AnIntervalJobFiresAtEachIntervalAndResumesAfterARestart(t *testing.T) {
	started := at(4, 8, 0)
	j := Job{Every: "15m", Created: at(4, 8, 0)}
	if _, _, fire := j.Check(at(4, 8, 14), started); fire {
		t.Fatal("fired before the interval")
	}
	due, _, fire := j.Check(at(4, 8, 15), started)
	if !fire || !due.Equal(at(4, 8, 15)) {
		t.Fatal("did not fire at the interval")
	}
	j.LastRun = &due
	if _, _, fire := j.Check(at(4, 8, 29), started); fire {
		t.Fatal("fired early on the second interval")
	}
	if _, _, fire := j.Check(at(4, 8, 30), started); !fire {
		t.Fatal("did not fire on the second interval")
	}
	// Down from 08:30 to 12:00: it resumes from the start, no catch-up.
	j.LastRun = ptr(at(4, 8, 30))
	restarted := at(4, 12, 0)
	if _, missed, fire := j.Check(restarted, restarted); fire || missed {
		t.Fatal("an interval job caught up after a restart")
	}
	if _, _, fire := j.Check(at(4, 12, 15), restarted); !fire {
		t.Fatal("an interval job did not resume")
	}
}

func TestNext_ListsTheNextRun(t *testing.T) {
	j := Job{At: "09:30", Created: at(4, 8, 0)}
	if got := j.Next(at(4, 10, 0), at(4, 8, 0)); !got.Equal(at(4, 9, 30)) {
		// Due and not yet run: its next run is now, the due time.
		t.Fatalf("a due job lists %v", got)
	}
	j.LastRun = ptr(at(4, 9, 30))
	if got := j.Next(at(4, 10, 0), at(4, 8, 0)); !got.Equal(at(5, 9, 30)) {
		t.Fatalf("next %v, want tomorrow 09:30", got)
	}
	k := Job{Every: "1h", Created: at(4, 8, 0)}
	if got := k.Next(at(4, 8, 10), at(4, 8, 0)); !got.Equal(at(4, 9, 0)) {
		t.Fatalf("next %v, want 09:00", got)
	}
}
