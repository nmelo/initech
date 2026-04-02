package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

func timerPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "timers.json")
}

func mustAdd(t *testing.T, ts *TimerStore, target, host, text string, enter bool, fireAt time.Time) Timer {
	t.Helper()
	timer, err := ts.Add(target, host, text, enter, fireAt)
	if err != nil {
		t.Fatalf("Add(%q): %v", target, err)
	}
	return timer
}

func TestTimerStore_AddAndList(t *testing.T) {
	ts := NewTimerStore(timerPath(t))

	now := time.Now()
	mustAdd(t, ts, "eng1", "", "first", true, now.Add(3*time.Hour))
	mustAdd(t, ts, "eng2", "", "second", true, now.Add(1*time.Hour))
	mustAdd(t, ts, "qa1", "", "third", false, now.Add(2*time.Hour))

	list := ts.List()
	if len(list) != 3 {
		t.Fatalf("List len = %d, want 3", len(list))
	}
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

	t1 := mustAdd(t, ts, "a", "", "x", true, time.Now().Add(time.Hour))
	t2 := mustAdd(t, ts, "b", "", "y", true, time.Now().Add(time.Hour))
	t3 := mustAdd(t, ts, "c", "", "z", true, time.Now().Add(time.Hour))

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

	mustAdd(t, ts, "eng1", "", "msg1", true, time.Now().Add(time.Hour))
	t2 := mustAdd(t, ts, "eng2", "", "msg2", true, time.Now().Add(2*time.Hour))
	mustAdd(t, ts, "qa1", "", "msg3", true, time.Now().Add(3*time.Hour))

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
	mustAdd(t, ts, "eng1", "", "past1", true, now.Add(-2*time.Hour))
	mustAdd(t, ts, "eng2", "", "past2", true, now.Add(-1*time.Hour))
	mustAdd(t, ts, "qa1", "", "future", true, now.Add(1*time.Hour))

	due, err := ts.FireDue(now)
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
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
	due, err := ts.FireDue(time.Now())
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
	if due != nil {
		t.Errorf("FireDue on empty store should return nil, got %v", due)
	}
}

func TestTimerStore_FireDueNoneDue(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	mustAdd(t, ts, "eng1", "", "future", true, time.Now().Add(time.Hour))
	due, err := ts.FireDue(time.Now())
	if err != nil {
		t.Fatalf("FireDue: %v", err)
	}
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
	mustAdd(t, ts, "a", "", "x", true, time.Now().Add(time.Hour))
	mustAdd(t, ts, "b", "", "y", true, time.Now().Add(time.Hour))
	if ts.Pending() != 2 {
		t.Errorf("Pending = %d, want 2", ts.Pending())
	}
}

func TestTimerStore_Persistence(t *testing.T) {
	path := timerPath(t)
	ts1 := NewTimerStore(path)

	mustAdd(t, ts1, "eng1", "workbench", "persistent msg", true, time.Now().Add(time.Hour))
	mustAdd(t, ts1, "qa1", "", "another", false, time.Now().Add(2*time.Hour))

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
}

func TestTimerStore_NextIDPersists(t *testing.T) {
	path := timerPath(t)

	ts1 := NewTimerStore(path)
	mustAdd(t, ts1, "a", "", "x", true, time.Now().Add(time.Hour))
	mustAdd(t, ts1, "b", "", "y", true, time.Now().Add(time.Hour))

	ts2 := NewTimerStore(path)
	t3 := mustAdd(t, ts2, "c", "", "z", true, time.Now().Add(time.Hour))
	if t3.ID != "at-3" {
		t.Errorf("after reload, next ID = %q, want 'at-3'", t3.ID)
	}
}

func TestTimerStore_NextIDNeverReuses(t *testing.T) {
	path := timerPath(t)

	ts := NewTimerStore(path)
	t1 := mustAdd(t, ts, "a", "", "x", true, time.Now().Add(time.Hour))
	ts.Cancel(t1.ID)
	t2 := mustAdd(t, ts, "b", "", "y", true, time.Now().Add(time.Hour))

	if t2.ID != "at-2" {
		t.Errorf("after cancel+add, ID = %q, want 'at-2' (no reuse)", t2.ID)
	}
}

func TestTimerStore_AtomicWrite(t *testing.T) {
	path := timerPath(t)
	ts := NewTimerStore(path)
	mustAdd(t, ts, "eng1", "", "msg", true, time.Now().Add(time.Hour))

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
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after save")
	}
}

func TestTimerStore_HostField(t *testing.T) {
	ts := NewTimerStore(timerPath(t))
	timer := mustAdd(t, ts, "eng1", "workbench", "remote msg", true, time.Now().Add(time.Hour))
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

// TestTimerStore_CorruptFileStartsEmpty verifies that a corrupt timer file
// results in an empty store (not a crash) and logs a warning.
func TestTimerStore_CorruptFileStartsEmpty(t *testing.T) {
	path := timerPath(t)
	os.WriteFile(path, []byte("not valid json {{{"), 0600)

	ts := NewTimerStore(path)
	if ts.Pending() != 0 {
		t.Errorf("Pending on corrupt file = %d, want 0", ts.Pending())
	}
}

// TestTimerStore_SaveFailureRollsBackAdd verifies that when save fails,
// Add rolls back the in-memory state and returns an error.
func TestTimerStore_SaveFailureRollsBackAdd(t *testing.T) {
	// Use a read-only directory so writes fail.
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "timers.json")
	ts := NewTimerStore(path)
	// First add succeeds (creates the directory).
	mustAdd(t, ts, "a", "", "x", true, time.Now().Add(time.Hour))

	// Make the directory read-only so the next save fails.
	subDir := filepath.Join(dir, "sub")
	os.Chmod(subDir, 0500)
	defer os.Chmod(subDir, 0700) // Restore for cleanup.

	_, err := ts.Add("b", "", "y", true, time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("Add should fail when save is impossible")
	}

	// In-memory state should be rolled back: still only 1 timer.
	if ts.Pending() != 1 {
		t.Errorf("Pending after failed Add = %d, want 1 (rollback)", ts.Pending())
	}
}

// TestTimerStore_SaveFailureRollsBackCancel verifies that when save fails,
// Cancel rolls back the in-memory removal and returns an error.
func TestTimerStore_SaveFailureRollsBackCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "timers.json")
	ts := NewTimerStore(path)
	timer := mustAdd(t, ts, "a", "", "x", true, time.Now().Add(time.Hour))

	subDir := filepath.Join(dir, "sub")
	os.Chmod(subDir, 0500)
	defer os.Chmod(subDir, 0700)

	_, err := ts.Cancel(timer.ID)
	if err == nil {
		t.Fatal("Cancel should fail when save is impossible")
	}

	// Timer should still be present (rollback).
	if ts.Pending() != 1 {
		t.Errorf("Pending after failed Cancel = %d, want 1 (rollback)", ts.Pending())
	}
}

// TestTimerStore_FireDueSaveError verifies that FireDue returns due timers
// AND the save error, so the caller can fire them but knows persistence broke.
func TestTimerStore_FireDueSaveError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "timers.json")
	ts := NewTimerStore(path)
	mustAdd(t, ts, "eng1", "", "past", true, time.Now().Add(-time.Hour))
	mustAdd(t, ts, "eng2", "", "future", true, time.Now().Add(time.Hour))

	subDir := filepath.Join(dir, "sub")
	os.Chmod(subDir, 0500)
	defer os.Chmod(subDir, 0700)

	due, err := ts.FireDue(time.Now())
	if err == nil {
		t.Fatal("FireDue should return error when save fails")
	}
	if len(due) != 1 {
		t.Errorf("FireDue should still return due timers, got %d", len(due))
	}
	if due[0].Target != "eng1" {
		t.Errorf("due[0].Target = %q, want 'eng1'", due[0].Target)
	}
}

// ── fireScheduledSend (TUI) ────────────────────────────────────────

func TestFireScheduledSend_LocalAgent(t *testing.T) {
	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	p := &Pane{name: "eng1", emu: emu, alive: true}
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		agentEvents: make(chan AgentEvent, 8),
	}

	timer := Timer{
		ID:     "at-1",
		Target: "eng1",
		Text:   "test message",
		Enter:  true,
		FireAt: time.Now().Add(-time.Second),
	}

	tui.fireScheduledSend(timer)
}

func TestFireScheduledSend_MissingAgent(t *testing.T) {
	tui := &TUI{
		panes: toPaneViews([]*Pane{}),
	}

	timer := Timer{
		ID:     "at-1",
		Target: "nonexistent",
		Text:   "msg",
		FireAt: time.Now(),
	}

	tui.fireScheduledSend(timer)
}

func TestFireScheduledSend_DeadAgent(t *testing.T) {
	p := &Pane{name: "eng1", emu: vt.NewSafeEmulator(10, 5), alive: false}
	tui := &TUI{
		panes: toPaneViews([]*Pane{p}),
	}

	timer := Timer{
		ID:     "at-1",
		Target: "eng1",
		Text:   "msg",
		FireAt: time.Now(),
	}

	tui.fireScheduledSend(timer)
}

func TestFireTimers_Integration(t *testing.T) {
	path := timerPath(t)
	ts := NewTimerStore(path)
	mustAdd(t, ts, "eng1", "", "fire me", true, time.Now().Add(-time.Second))
	mustAdd(t, ts, "eng2", "", "not yet", true, time.Now().Add(time.Hour))

	emu := vt.NewSafeEmulator(80, 24)
	go func() {
		buf := make([]byte, 256)
		for {
			if _, err := emu.Read(buf); err != nil {
				return
			}
		}
	}()

	tui := &TUI{
		panes:       toPaneViews([]*Pane{{name: "eng1", emu: emu, alive: true}}),
		timers:      ts,
		agentEvents: make(chan AgentEvent, 8),
	}

	tui.fireTimers()

	if ts.Pending() != 1 {
		t.Errorf("Pending after fire = %d, want 1 (eng2's timer remains)", ts.Pending())
	}
	remaining := ts.List()
	if remaining[0].Target != "eng2" {
		t.Errorf("remaining target = %q, want 'eng2'", remaining[0].Target)
	}
}

func TestFireTimers_NilStore(t *testing.T) {
	tui := &TUI{timers: nil}
	tui.fireTimers() // must not panic
}
