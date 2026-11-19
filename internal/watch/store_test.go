package watch

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 19, 17, 0, 0, 0, time.UTC)

// openAt opens a store at a fresh path, loaded at now.
func openAt(t *testing.T, now time.Time) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watches.yaml")
	s, note := Open(path, now)
	if note != "" {
		t.Fatalf("a missing file gave a note: %q", note)
	}
	return s, path
}

func aWatch(owner, bead string) Watch {
	return Watch{Owner: owner, Kind: KindBeadStatus, Params: map[string]string{"bead": bead, "status": "ready_for_qa"},
		Why: "read " + bead, Deadline: t0.Add(time.Hour)}
}

func get(t *testing.T, s *Store, id string) Watch {
	t.Helper()
	for _, w := range s.Snapshot(t0) {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("no watch %s", id)
	return Watch{}
}

// failingWrites makes every write fail until the returned func is called.
func failingWrites(s *Store) (restore func()) {
	orig := s.write
	s.write = func(string, []byte) error { return errors.New("disk full") }
	return func() { s.write = orig }
}

// ini-0xkk: a watch is waiting once added, with an id, the time it was set
// and its owner's own fields.
func TestStore_AddMakesAWaitingWatch(t *testing.T) {
	s, _ := openAt(t, t0)
	id, err := s.Add(aWatch("eng8", "ini-x"), t0)
	if err != nil || id != "w1" {
		t.Fatalf("add: %q %v", id, err)
	}
	w := get(t, s, id)
	if w.State != Waiting || !w.Set.Equal(t0) || w.Owner != "eng8" || w.Ended != (time.Time{}) {
		t.Errorf("added watch %+v", w)
	}
	if id2, _ := s.Add(aWatch("eng8", "ini-y"), t0); id2 != "w2" {
		t.Errorf("second id %q, want w2", id2)
	}
}

// Mandatory parts and bounds: each refusal is one line and stores nothing.
func TestStore_AddRefusesWhatTheNoteForbids(t *testing.T) {
	s, _ := openAt(t, t0)
	long := strings.Repeat("x", MaxWhy+1)
	for name, w := range map[string]Watch{
		"no reason":           {Owner: "eng8", Kind: KindDeadline, Deadline: t0.Add(time.Hour)},
		"reason too long":     {Owner: "eng8", Kind: KindDeadline, Why: long, Deadline: t0.Add(time.Hour)},
		"reason with newline": {Owner: "eng8", Kind: KindDeadline, Why: "a\nb", Deadline: t0.Add(time.Hour)},
		"no deadline":         {Owner: "eng8", Kind: KindDeadline, Why: "x"},
		"deadline now":        {Owner: "eng8", Kind: KindDeadline, Why: "x", Deadline: t0},
		"deadline past":       {Owner: "eng8", Kind: KindDeadline, Why: "x", Deadline: t0.Add(-time.Minute)},
		"no owner":            {Kind: KindDeadline, Why: "x", Deadline: t0.Add(time.Hour)},
		"no kind":             {Owner: "eng8", Why: "x", Deadline: t0.Add(time.Hour)},
	} {
		if _, err := s.Add(w, t0); err == nil || strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: err %v, want a one-line refusal", name, err)
		}
	}
	if n := len(s.Snapshot(t0)); n != 0 {
		t.Errorf("refusals stored %d watches", n)
	}
	// Exactly 200 characters on one line is allowed (the twin).
	if _, err := s.Add(Watch{Owner: "eng8", Kind: KindDeadline, Why: strings.Repeat("x", MaxWhy), Deadline: t0.Add(time.Hour)}, t0); err != nil {
		t.Errorf("a 200-character reason was refused: %v", err)
	}
}

// The 21st waiting watch is refused; ended ones do not count; another owner
// has its own twenty. A duplicate of the owner's own waiting watch is refused
// naming it.
func TestStore_TheCapAndDuplicates(t *testing.T) {
	s, _ := openAt(t, t0)
	for i := 0; i < MaxWaitingPerOwner; i++ {
		if _, err := s.Add(aWatch("eng8", "ini-"+string(rune('a'+i))), t0); err != nil {
			t.Fatalf("watch %d: %v", i+1, err)
		}
	}
	if _, err := s.Add(aWatch("eng8", "ini-zz"), t0); err == nil {
		t.Fatal("the 21st waiting watch was set")
	}
	if _, err := s.Add(aWatch("eng2", "ini-zz"), t0); err != nil {
		t.Errorf("another owner was capped by eng8's watches: %v", err)
	}
	if err := s.Cancel("w1", "eng8", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(aWatch("eng8", "ini-zz"), t0); err != nil {
		t.Errorf("an ended watch counted against the cap: %v", err)
	}
	_, err := s.Add(aWatch("eng8", "ini-b"), t0)
	if err == nil || !strings.Contains(err.Error(), "w2") {
		t.Errorf("duplicate of w2: %v, want a refusal naming w2", err)
	}
}

// A watch ends exactly once: fired or missed, never overwritten; cancel is
// the owner's own, and only while it waits.
func TestStore_AWatchEndsExactlyOnce(t *testing.T) {
	s, _ := openAt(t, t0)
	a, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	b, _ := s.Add(aWatch("eng8", "ini-b"), t0)
	c, _ := s.Add(aWatch("eng8", "ini-c"), t0)
	if err := s.End(a, Fired, "ini-a reached ready_for_qa", t0.Add(time.Minute), t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.End(a, Missed, "deadline passed", t0.Add(2*time.Hour), t0.Add(2*time.Hour)); err == nil {
		t.Error("a fired watch was ended again")
	}
	if w := get(t, s, a); w.State != Fired || w.Detail != "ini-a reached ready_for_qa" {
		t.Errorf("the first ending was overwritten: %+v", w)
	}
	if err := s.End(b, Cancelled, "", t0, t0); err == nil {
		t.Error("End accepted cancelled; only Cancel cancels")
	}
	if err := s.End(b, Waiting, "", t0, t0); err == nil {
		t.Error("End accepted waiting")
	}
	if err := s.Cancel(c, "eng2", t0); err == nil {
		t.Error("another agent cancelled eng8's watch")
	}
	if err := s.Cancel(a, "eng8", t0); err == nil {
		t.Error("a fired watch was cancelled")
	}
	if err := s.Cancel(c, "eng8", t0); err != nil || get(t, s, c).State != Cancelled {
		t.Errorf("own cancel: %v", err)
	}
	if err := s.End("w99", Fired, "", t0, t0); err == nil {
		t.Error("an unknown id was ended")
	}
}

// Owed: fired and missed endings on disk, not yet handed or sent to super.
// Cancelled is never owed. Handed, or sent to super, is no longer owed.
func TestStore_OwedIsWhatStillHasALineToSend(t *testing.T) {
	s, _ := openAt(t, t0)
	f, _ := s.Add(aWatch("eng8", "ini-f"), t0)
	m, _ := s.Add(aWatch("eng8", "ini-m"), t0)
	c, _ := s.Add(aWatch("eng8", "ini-c"), t0)
	sup, _ := s.Add(aWatch("gone", "ini-s"), t0)
	_ = s.End(f, Fired, "x", t0, t0)
	_ = s.End(m, Missed, "deadline passed", t0, t0)
	_ = s.Cancel(c, "eng8", t0)
	_ = s.End(sup, Fired, "x", t0, t0)
	owed := ids(s.Owed())
	if owed != "w1,w2,w4" {
		t.Fatalf("owed %q, want w1,w2,w4", owed)
	}
	if err := s.MarkHanded(f, "r1", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkToSuper(sup, t0); err != nil {
		t.Fatal(err)
	}
	if owed := ids(s.Owed()); owed != "w2" {
		t.Errorf("owed %q after handing w1 and sending w4 to super, want w2", owed)
	}
	if err := s.MarkHanded("w3", "r", t0); err == nil {
		t.Error("a cancelled watch was handed to delivery")
	}
}

func ids(ws []Watch) string {
	var out []string
	for _, w := range ws {
		out = append(out, w.ID)
	}
	return strings.Join(out, ",")
}

// pm's ruling 2: history lasts one hour after delivery or cancel, never
// before delivery: a fired watch not yet delivered stays however old.
func TestStore_PruneKeepsAnOwedEndingAndAnHourOfHistory(t *testing.T) {
	s, _ := openAt(t, t0)
	owed, _ := s.Add(aWatch("eng8", "ini-o"), t0)
	handed, _ := s.Add(aWatch("eng8", "ini-h"), t0)
	done, _ := s.Add(aWatch("eng8", "ini-d"), t0)
	cancelled, _ := s.Add(aWatch("eng8", "ini-c"), t0)
	waiting, _ := s.Add(Watch{Owner: "eng8", Kind: KindDeadline, Why: "w", Deadline: t0.Add(48 * time.Hour)}, t0)
	for _, id := range []string{owed, handed, done} {
		_ = s.End(id, Fired, "x", t0, t0)
	}
	_ = s.MarkHanded(handed, "r", t0)
	_ = s.MarkHanded(done, "r", t0)
	_ = s.MarkDelivered(done, t0.Add(10*time.Minute))
	_ = s.Cancel(cancelled, "eng8", t0.Add(5*time.Minute))

	s.Prune(t0.Add(64 * time.Minute)) // cancel +59m, delivery +54m: all kept
	if got := ids(s.Snapshot(t0)); got != "w1,w2,w3,w4,w5" {
		t.Fatalf("pruned early: %q", got)
	}
	s.Prune(t0.Add(66 * time.Minute)) // cancel +61m: gone; delivery +56m: kept
	if got := ids(s.Snapshot(t0)); got != "w1,w2,w3,w5" {
		t.Fatalf("at +66m %q, want the cancel gone", got)
	}
	s.Prune(t0.Add(24 * time.Hour))
	if got := ids(s.Snapshot(t0)); got != "w1,w2,w5" {
		t.Errorf("a day later %q: want the owed w1, the handed-not-delivered w2 and the waiting w5 kept", got)
	}
	_ = waiting
}

// The file: written whole, 0600, read back as it was. A rig restart is a
// reload: every watch and the next id come back.
func TestStore_KeptAcrossARestart(t *testing.T) {
	s, path := openAt(t, t0)
	a, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	b, _ := s.Add(aWatch("eng2", "ini-b"), t0)
	_ = s.End(b, Fired, "ini-b reached ready_for_qa", t0.Add(time.Minute), t0.Add(time.Minute))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v, want 0600", info, err)
	}
	again, note := Open(path, t0.Add(30*time.Minute))
	if note != "" {
		t.Fatalf("reload note %q", note)
	}
	if got := ids(again.Snapshot(t0)); got != "w1,w2" {
		t.Fatalf("after a restart %q", got)
	}
	if w := get(t, again, a); w.State != Waiting || w.Why != "read ini-a" || !w.Deadline.Equal(t0.Add(time.Hour)) || w.Params["bead"] != "ini-a" {
		t.Errorf("waiting watch after restart %+v", w)
	}
	if ids(again.Owed()) != "w2" {
		t.Errorf("the owed ending was not owed after a restart: %q", ids(again.Owed()))
	}
	if id, _ := again.Add(aWatch("eng8", "ini-c"), t0.Add(30*time.Minute)); id != "w3" {
		t.Errorf("next id after restart %q, want w3 (ids never reused)", id)
	}
}

// pm's ruling 1: an ending decided after a start, whose deadline or evidence
// is older than that start, is late; nobody passes it.
func TestStore_LateFromTheLoadTime(t *testing.T) {
	s, path := openAt(t, t0)
	a, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	b, _ := s.Add(aWatch("eng8", "ini-b"), t0)
	start := t0.Add(2 * time.Hour) // window 1 was down in between
	again, _ := Open(path, start)
	_ = again.End(a, Missed, "deadline passed", t0.Add(time.Hour), start)
	_ = again.End(b, Fired, "seen now", start.Add(time.Minute), start.Add(time.Minute))
	if w := get(t, again, a); !w.Late {
		t.Error("a deadline that passed while window 1 was down is not late")
	}
	if w := get(t, again, b); w.Late {
		t.Error("evidence after the start was marked late")
	}
}

// A file that does not parse, another version, or a state outside the four is
// set aside whole, never deleted, and the store starts empty; feed it nothing:
// no file and an empty file are an empty store with no note.
func TestStore_ABadFileIsSetAsideNeverHalfRead(t *testing.T) {
	for name, body := range map[string]string{
		"not yaml":      "version: 1\nwatches: [\n",
		"other version": "version: 2\nnext_id: 1\nwatches: []\n",
		"bad state":     "version: 1\nnext_id: 2\nwatches:\n  - {id: w1, owner: eng8, kind: deadline, why: x, state: sleeping}\n",
		"no id":         "version: 1\nnext_id: 2\nwatches:\n  - {owner: eng8, kind: deadline, why: x, state: waiting}\n",
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "watches.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		s, note := Open(path, t0)
		if note == "" || len(s.Snapshot(t0)) != 0 {
			t.Errorf("%s: note %q, %d watches", name, note, len(s.Snapshot(t0)))
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s: the bad file is still in place", name)
		}
		entries, _ := os.ReadDir(dir)
		kept := false
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "watches.yaml.bad-") {
				if b, _ := os.ReadFile(filepath.Join(dir, e.Name())); string(b) == body {
					kept = true
				}
			}
		}
		if !kept {
			t.Errorf("%s: the bad file was not kept whole beside it: %v", name, entries)
		}
	}
	empty := filepath.Join(t.TempDir(), "watches.yaml")
	_ = os.WriteFile(empty, nil, 0o600)
	for _, p := range []string{empty, filepath.Join(t.TempDir(), "none.yaml")} {
		if s, note := Open(p, t0); note != "" || len(s.Snapshot(t0)) != 0 {
			t.Errorf("%s: note %q", filepath.Base(p), note)
		}
	}
}

// A watch of a kind this build does not know is kept through a write, listed
// with its kind, and never owed (nothing judges it).
func TestStore_AnUnknownKindIsKept(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watches.yaml")
	body := "version: 1\nnext_id: 2\nwatches:\n  - {id: w1, owner: eng8, kind: ci_run, params: {run: \"42\"}, why: x, deadline: 2026-11-19T18:00:00Z, set: 2026-11-19T17:00:00Z, state: waiting}\n"
	_ = os.WriteFile(path, []byte(body), 0o600)
	s, note := Open(path, t0)
	if note != "" {
		t.Fatalf("an unknown kind set the file aside: %q", note)
	}
	if _, err := s.Add(aWatch("eng8", "ini-a"), t0); err != nil { // forces a write
		t.Fatal(err)
	}
	again, _ := Open(path, t0)
	if w := get(t, again, "w1"); w.Kind != "ci_run" || w.State != Waiting || w.Params["run"] != "42" {
		t.Errorf("unknown kind after a write: %+v", w)
	}
}

// pm's ruling 3: a failed write undoes an Add and a Cancel (nothing changes
// in memory or on disk); an ending stays in memory, is not owed (no line goes
// out before it is on disk), and is written by the next pass.
func TestStore_AFailedWriteUndoesAddAndCancelAndHoldsAnEnding(t *testing.T) {
	s, path := openAt(t, t0)
	keep, _ := s.Add(aWatch("eng8", "ini-k"), t0)
	restore := failingWrites(s)
	if _, err := s.Add(aWatch("eng8", "ini-a"), t0); err == nil {
		t.Fatal("an Add whose write failed was accepted")
	}
	if err := s.Cancel(keep, "eng8", t0); err == nil {
		t.Fatal("a Cancel whose write failed was accepted")
	}
	if got := ids(s.Snapshot(t0)); got != "w1" || get(t, s, keep).State != Waiting {
		t.Fatalf("after failed writes %q, %v", got, get(t, s, keep).State)
	}
	if on, _ := Open(path, t0); get(t, on, keep).State != Waiting {
		t.Fatal("the file lost the cancelled-then-undone watch")
	}
	_ = s.End(keep, Fired, "x", t0, t0)
	if get(t, s, keep).State != Fired {
		t.Fatal("an ending whose write failed was dropped from memory")
	}
	if n := len(s.Owed()); n != 0 {
		t.Fatalf("an ending not yet on disk is owed: a line would go out before it is written (%d)", n)
	}
	if err := s.MarkHanded(keep, "r", t0); err == nil {
		t.Fatal("an ending not on disk was handed to delivery")
	}
	restore()
	if tr := s.Pass(t0.Add(10 * time.Second)); tr.Err != nil {
		t.Fatalf("the pass did not write the held ending: %v", tr.Err)
	}
	if ids(s.Owed()) != "w1" {
		t.Errorf("after the pass wrote it, owed %q", ids(s.Owed()))
	}
	if on, _ := Open(path, t0); get(t, on, keep).State != Fired {
		t.Error("the ending is not on disk after the pass")
	}
}

// A failed MarkHanded is undone too, so delivery never sends a line the file
// does not record as handed: a crash cannot deliver it twice.
func TestStore_AFailedHandIsUndone(t *testing.T) {
	s, _ := openAt(t, t0)
	id, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	_ = s.End(id, Fired, "x", t0, t0)
	restore := failingWrites(s)
	if err := s.MarkHanded(id, "r", t0); err == nil {
		t.Fatal("a hand whose write failed was accepted")
	}
	if ids(s.Owed()) != "w1" {
		t.Error("the undone hand left the ending not owed")
	}
	restore()
}

// Writes that keep failing: the pass logs the first failure once and tells
// super once after five minutes, naming the file; a later success clears it.
func TestStore_PassTellsSuperOnceAfterFiveMinutesOfFailures(t *testing.T) {
	s, path := openAt(t, t0)
	id, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	restore := failingWrites(s)
	_ = s.End(id, Fired, "x", t0, t0)
	logs, tells := 0, 0
	for sec := 0; sec <= 600; sec += 10 {
		tr := s.Pass(t0.Add(time.Duration(sec) * time.Second))
		if tr.Log {
			logs++
		}
		if tr.TellSuper {
			tells++
			if sec < 300 || !strings.Contains(tr.Err.Error()+tr.File, filepath.Base(path)) {
				t.Errorf("told super at +%ds, file %q", sec, tr.File)
			}
		}
	}
	if logs != 1 || tells != 1 {
		t.Errorf("over ten failing minutes: %d log lines, %d tells, want 1 and 1", logs, tells)
	}
	restore()
	if tr := s.Pass(t0.Add(11 * time.Minute)); tr.Err != nil || tr.Log || tr.TellSuper {
		t.Errorf("after recovery %+v", tr)
	}
	if tr := s.Pass(t0.Add(12 * time.Minute)); tr.Err != nil {
		t.Errorf("a clean pass: %+v", tr)
	}
}

// Feed it nothing: a pass with nothing to write writes nothing.
func TestStore_APassWithNothingToWriteDoesNotWrite(t *testing.T) {
	s, _ := openAt(t, t0)
	n := 0
	s.write = func(string, []byte) error { n++; return nil }
	s.Pass(t0)
	if n != 0 {
		t.Errorf("an idle pass wrote %d times", n)
	}
}

// WaitingFor counts an owner's waiting watches, for "kept N watches".
func TestStore_WaitingForCountsAnOwnersWaitingWatches(t *testing.T) {
	s, _ := openAt(t, t0)
	a, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	_, _ = s.Add(aWatch("eng8", "ini-b"), t0)
	_, _ = s.Add(aWatch("eng2", "ini-c"), t0)
	_ = s.End(a, Fired, "x", t0, t0)
	if s.WaitingFor("eng8") != 1 || s.WaitingFor("eng2") != 1 || s.WaitingFor("nobody") != 0 {
		t.Errorf("waiting %d %d %d", s.WaitingFor("eng8"), s.WaitingFor("eng2"), s.WaitingFor("nobody"))
	}
}

// Snapshot is a copy: changing it changes nothing stored.
func TestStore_SnapshotIsACopy(t *testing.T) {
	s, _ := openAt(t, t0)
	id, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	snap := s.Snapshot(t0)
	snap[0].State = Fired
	snap[0].Params["bead"] = "other"
	if w := get(t, s, id); w.State != Waiting || w.Params["bead"] != "ini-a" {
		t.Errorf("the snapshot reached into the store: %+v", w)
	}
}

// Ids are never reused, whatever next_id says: a file whose next_id is behind
// its highest id gives the next id after that one.
func TestStore_IdsAreNeverReusedWhateverNextIDSays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watches.yaml")
	body := "version: 1\nnext_id: 1\nwatches:\n  - {id: w5, owner: eng8, kind: deadline, why: x, deadline: 2026-11-19T19:00:00Z, set: 2026-11-19T17:00:00Z, state: cancelled, ended: 2026-11-19T17:00:00Z}\n"
	_ = os.WriteFile(path, []byte(body), 0o600)
	s, note := Open(path, t0)
	if note != "" {
		t.Fatal(note)
	}
	if id, err := s.Add(aWatch("eng8", "ini-a"), t0); err != nil || id != "w6" {
		t.Errorf("id %q %v, want w6", id, err)
	}
}

// lead1's read: a deadline-only watch has no params, so its deadline is what
// tells two apart. Two at different times are both set; the same time again
// is a duplicate (the twin).
func TestStore_DeadlineWatchesAtDifferentTimesAreNotDuplicates(t *testing.T) {
	s, _ := openAt(t, t0)
	at := func(d time.Duration) Watch {
		return Watch{Owner: "eng8", Kind: KindDeadline, Why: "check in", Deadline: t0.Add(d)}
	}
	if _, err := s.Add(at(time.Hour), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(at(2*time.Hour), t0); err != nil {
		t.Errorf("a second deadline watch at another time was refused: %v", err)
	}
	if _, err := s.Add(at(time.Hour), t0); err == nil || !strings.Contains(err.Error(), "w1") {
		t.Errorf("the same deadline again: %v, want a duplicate naming w1", err)
	}
}

// lead1's read: late means it ended while window 1 was down, which only a
// watch set before this start can have done. A watch set after the start
// whose evidence is older (seen at its first judging) is not late.
func TestStore_AWatchSetAfterTheStartIsNeverLate(t *testing.T) {
	s, path := openAt(t, t0)
	before, _ := s.Add(aWatch("eng8", "ini-a"), t0)
	start := t0.Add(10 * time.Minute)
	again, _ := Open(path, start)
	after, err := again.Add(aWatch("eng8", "ini-b"), start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_ = again.End(after, Fired, "already there", t0.Add(5*time.Minute), start.Add(2*time.Minute))
	_ = again.End(before, Fired, "while down", t0.Add(5*time.Minute), start.Add(2*time.Minute))
	if get(t, again, after).Late {
		t.Error("a watch set after the start was marked late")
	}
	if !get(t, again, before).Late {
		t.Error("the twin: a watch set before the start, ending on evidence from while it was down, is not late")
	}
}
