package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func timerPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "timers.json")
}

func TestTimerStore_AddAndList(t *testing.T) {
	ts := NewTimerStore(timerPath(t))

	now := time.Now()
	ts.Add("eng1", "", "first", true, now.Add(3*time.Hour))
	ts.Add("eng2", "", "second", true, now.Add(1*time.Hour))
	ts.Add("qa1", "", "third", false, now.Add(2*time.Hour))

	list := ts.List()
	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}
	// Should be sorted by FireAt: second (1h), third (2h), first (3h).
	if list[0].Target != "eng2" {
		t.Errorf("list[0].Target = %q, want 'eng2' (earliest)", list[0].Target)
	}
	if list[1].Target != "qa1" {
		t.Errorf("list[1].Target = %q, want 'qa1'", list[1].Target)
	}
	if list[2].Target != "eng1" {
		t.Errorf("list[2].Target = %q, want 'eng1' (latest)", list[2].Target)
	}
}

func TestTimerStore_IDGeneration(t *testing.T) {
	ts := NewTimerStore(timerPath(t))

	t1 := ts.Add("a", "", "x", true, time.Now().Add(time.Hour))
	t2 := ts.Add("b", "", "y", true, time.Now().Add(time.Hour))
	t3 := ts.Add("c", "", "z", true, time.Now().Add(time.Hour))

	if t1.ID != "at-1" {
		t.Errorf("first ID = %q, want 'at-1'", t1.ID)
	}
	if t2.ID != "at-2" {
		t.Errorf("second ID = %q, want 'at-2'", t2.ID)
	}
	if t3.ID != "at-3" {
		t.Errorf("third ID = %q, want 'at-3'", t3.ID)
	}
}

func TestTimerStore_Cancel(t *testing.T) {
	ts := NewTimerStore(timerPath(t))

	ts.Add("eng1", "", "msg1", true, time.Now().Add(time.Hour))
	t2 := ts.Add("eng2", "", "msg2", true, time.Now().Add(2*time.Hour))
	ts.Add("qa1", "", "msg3", true, time.Now().Add(3*time.Hour))

	canceled, err := ts.Cancel(t2.ID)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if canceled.Target != "eng2" {
		t.Errorf("canceled.Target = %q, want 'eng2'", canceled.Target)
	}

	list := ts.List()
	if len(list) != 2 {
		t.Fatalf("List after cancel = %d, want 2", len(list))
	}
	for _, tm := range list {
		if tm.ID == t2.ID {
			t.Error("canceled timer should not appear in List")
		}
	}
}

func TestTimerStore_CancelNotFound(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	_, err := ts.Cancel("at-999")
	if err == nil {
		t.Error("Cancel nonexistent should return error")
	}
}

func TestTimerStore_FireDue(t *testing.T) {
	ts := NewTimerStore(timerPath(t))

	now := time.Now()
	ts.Add("eng1", "", "past1", true, now.Add(-2*time.Hour))
	ts.Add("eng2", "", "past2", true, now.Add(-1*time.Hour))
	ts.Add("qa1", "", "future", true, now.Add(1*time.Hour))

	due := ts.FireDue(now)
	if len(due) != 2 {
		t.Fatalf("FireDue = %d, want 2", len(due))
	}

	remaining := ts.List()
	if len(remaining) != 1 {
		t.Fatalf("remaining = %d, want 1", len(remaining))
	}
	if remaining[0].Target != "qa1" {
		t.Errorf("remaining target = %q, want 'qa1'", remaining[0].Target)
	}
}

func TestTimerStore_FireDueEmpty(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	due := ts.FireDue(time.Now())
	if due != nil {
		t.Errorf("FireDue on empty store should return nil, got %v", due)
	}
}

func TestTimerStore_FireDueNoneDue(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	ts.Add("eng1", "", "future", true, time.Now().Add(time.Hour))
	due := ts.FireDue(time.Now())
	if due != nil {
		t.Errorf("FireDue with no due timers should return nil, got %v", due)
	}
	if ts.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", ts.Pending())
	}
}

func TestTimerStore_Pending(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	if ts.Pending() != 0 {
		t.Errorf("Pending on empty store = %d, want 0", ts.Pending())
	}
	ts.Add("a", "", "x", true, time.Now().Add(time.Hour))
	ts.Add("b", "", "y", true, time.Now().Add(time.Hour))
	if ts.Pending() != 2 {
		t.Errorf("Pending = %d, want 2", ts.Pending())
	}
}

func TestTimerStore_Persistence(t *testing.T) {
	path := timerPath(t)
	ts1 := NewTimerStore(path)

	ts1.Add("eng1", "workbench", "persistent msg", true, time.Now().Add(time.Hour))
	ts1.Add("qa1", "", "another", false, time.Now().Add(2*time.Hour))

	// Create a new store pointing at the same file.
	ts2 := NewTimerStore(path)
	list := ts2.List()
	if len(list) != 2 {
		t.Fatalf("loaded %d timers, want 2", len(list))
	}
	if list[0].Target != "eng1" {
		t.Errorf("loaded[0].Target = %q, want 'eng1'", list[0].Target)
	}
	if list[0].Host != "workbench" {
		t.Errorf("loaded[0].Host = %q, want 'workbench'", list[0].Host)
	}
	if list[0].Text != "persistent msg" {
		t.Errorf("loaded[0].Text = %q, want 'persistent msg'", list[0].Text)
	}
}

func TestTimerStore_NextIDPersists(t *testing.T) {
	path := timerPath(t)

	ts1 := NewTimerStore(path)
	ts1.Add("a", "", "x", true, time.Now().Add(time.Hour))
	ts1.Add("b", "", "y", true, time.Now().Add(time.Hour))
	// nextID should be 3 after two adds.

	ts2 := NewTimerStore(path)
	t3 := ts2.Add("c", "", "z", true, time.Now().Add(time.Hour))
	if t3.ID != "at-3" {
		t.Errorf("after reload, next ID = %q, want 'at-3'", t3.ID)
	}
}

func TestTimerStore_NextIDNeverReuses(t *testing.T) {
	path := timerPath(t)

	ts := NewTimerStore(path)
	t1 := ts.Add("a", "", "x", true, time.Now().Add(time.Hour))
	ts.Cancel(t1.ID)
	t2 := ts.Add("b", "", "y", true, time.Now().Add(time.Hour))

	if t2.ID != "at-2" {
		t.Errorf("after cancel+add, ID = %q, want 'at-2' (no reuse)", t2.ID)
	}
}

func TestTimerStore_AtomicWrite(t *testing.T) {
	path := timerPath(t)
	ts := NewTimerStore(path)
	ts.Add("eng1", "", "msg", true, time.Now().Add(time.Hour))

	// File should exist and be valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read timers.json: %v", err)
	}
	var f timerFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("invalid JSON in timers.json: %v", err)
	}
	if f.NextID != 2 {
		t.Errorf("next_id = %d, want 2", f.NextID)
	}
	if len(f.Timers) != 1 {
		t.Errorf("timers = %d, want 1", len(f.Timers))
	}

	// Temp file should be cleaned up.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after save")
	}
}

func TestTimerStore_HostField(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	timer := ts.Add("eng1", "workbench", "remote msg", true, time.Now().Add(time.Hour))
	if timer.Host != "workbench" {
		t.Errorf("Host = %q, want 'workbench'", timer.Host)
	}
	list := ts.List()
	if list[0].Host != "workbench" {
		t.Errorf("List[0].Host = %q, want 'workbench'", list[0].Host)
	}
}

func TestTimerStore_MissingFile(t *testing.T) {
	ts := NewTimerStore("/nonexistent/path/timers.json")
	if ts.Pending() != 0 {
		t.Errorf("Pending on missing file = %d, want 0", ts.Pending())
	}
}
