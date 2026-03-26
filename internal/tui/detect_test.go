// detect_test.go tests semantic event detection functions: bead claim/clear
// from JSONL tool_use entries, completion/failure from tool_result and
// assistant patterns, stall/stuck timing, and deduplication.
package tui

import (
	"testing"
	"time"
)

// ── detectBeadClaim ──────────────────────────────────────────────────

func TestDetectBeadClaim_Claim(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status in_progress --assignee eng1",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "ini-18m.5" {
		t.Errorf("beadID = %q, want ini-18m.5", beadID)
	}
	if clear {
		t.Error("clear should be false on a claim")
	}
}

func TestDetectBeadClaim_ClaimFlag(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-q7x.1 --claim",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "ini-q7x.1" {
		t.Errorf("beadID = %q, want ini-q7x.1", beadID)
	}
	if clear {
		t.Error("clear should be false")
	}
}

func TestDetectBeadClaim_FailedExit(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status in_progress --assignee eng1",
			ExitCode: 1,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("failed exit should be ignored, got beadID=%q clear=%v", beadID, clear)
	}
}

func TestDetectBeadClaim_ReadyForQA(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status ready_for_qa",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if !clear {
		t.Error("clear should be true for ready_for_qa")
	}
	if beadID != "" {
		t.Errorf("beadID should be empty on clear, got %q", beadID)
	}
}

func TestDetectBeadClaim_OtherBdUpdate(t *testing.T) {
	// bd update that changes priority - not a claim
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --priority 1",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("priority update should not trigger, got beadID=%q clear=%v", beadID, clear)
	}
}

func TestDetectBeadClaim_NonBashTool(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Read",
			Content:  "bd update ini-18m.5 --status in_progress",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("non-Bash tool should not trigger, got beadID=%q clear=%v", beadID, clear)
	}
}

func TestDetectBeadClaim_NoBdUpdate(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "git status", ExitCode: 0},
		{Type: "tool_result", ToolName: "Bash", Content: "make test", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("unrelated commands should not trigger, got beadID=%q clear=%v", beadID, clear)
	}
}

// TestDetectBeadClaim_ToolUseIgnored verifies that tool_use entries (assistant
// messages) are ignored — ExitCode is always 0 on those; only tool_result
// entries carry a meaningful ExitCode (ini-a1e.2).
func TestDetectBeadClaim_ToolUseIgnored(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "assistant", // tool_use is inside assistant entries
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status in_progress",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("assistant/tool_use entries should be ignored, got beadID=%q clear=%v", beadID, clear)
	}
}

// TestDetectBeadClaim_ClaimAfterClear verifies that when a clear and a claim
// both appear in the same batch (clear first, then claim), the claim wins
// (ini-a1e.5). Previously the early return on isClear dropped the claim.
func TestDetectBeadClaim_ClaimAfterClear(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-old.1 --status ready_for_qa",
			ExitCode: 0,
		},
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-new.2 --status in_progress",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "ini-new.2" {
		t.Errorf("beadID = %q, want ini-new.2 (claim after clear should win)", beadID)
	}
	if clear {
		t.Error("clear should be false when claim comes after clear in same batch")
	}
}

// TestDetectBeadClaim_ClearAfterClaim verifies that when a claim appears first
// followed by a clear in the same batch, the clear wins (last signal wins).
func TestDetectBeadClaim_ClearAfterClaim(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-x.1 --status in_progress",
			ExitCode: 0,
		},
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-x.1 --status ready_for_qa",
			ExitCode: 0,
		},
	}
	beadID, clear := detectBeadClaim(entries)
	if !clear {
		t.Error("clear should be true when clear comes after claim in same batch")
	}
	if beadID != "" {
		t.Errorf("beadID should be empty on clear, got %q", beadID)
	}
}

func TestApplyBeadDetection_SetsBeadAndEmitsEvent(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng1", eventCh: ch}

	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status in_progress --assignee eng1",
			ExitCode: 0,
		},
	}
	p.applyBeadDetection(entries)

	if p.BeadID() != "ini-18m.5" {
		t.Errorf("BeadID() = %q, want ini-18m.5", p.BeadID())
	}
	if len(ch) != 1 {
		t.Fatalf("expected 1 event, got %d", len(ch))
	}
	ev := <-ch
	if ev.Type != EventBeadClaimed || ev.BeadID != "ini-18m.5" {
		t.Errorf("event = {%v %q}, want EventBeadClaimed/ini-18m.5", ev.Type, ev.BeadID)
	}
}

func TestApplyBeadDetection_ClearsBead(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng1", eventCh: ch}
	p.beadID = "ini-18m.5"

	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			Content:  "bd update ini-18m.5 --status ready_for_qa",
			ExitCode: 0,
		},
	}
	p.applyBeadDetection(entries)

	if p.BeadID() != "" {
		t.Errorf("BeadID() = %q, want empty after clear", p.BeadID())
	}
	// No event emitted on clear.
	if len(ch) != 0 {
		t.Errorf("expected no events on clear, got %d", len(ch))
	}
}

func TestExtractBeadID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"bd update ini-18m.5 --claim", "ini-18m.5"},
		{"bd update ini-q7x.1 --status in_progress", "ini-q7x.1"},
		{"bd update ini-noid --claim", "ini-noid"},   // root-level ID (no dot) is accepted
		{"bd update ini-r5u --claim", "ini-r5u"},     // another root-level ID
		{"bd update --status in_progress", ""},       // no ID before flags
		{"bd update abc.1 --claim", ""},              // no hyphen prefix
		{"something else entirely", ""},
	}
	for _, tt := range tests {
		got := extractBeadID(tt.input)
		if got != tt.want {
			t.Errorf("extractBeadID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ── detectCompletion ─────────────────────────────────────────────────

func TestDetectCompletion_ReadyForQA(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd update ini-test.1 --status ready_for_qa",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0]
	if ev.Type != EventBeadCompleted {
		t.Errorf("type = %v, want EventBeadCompleted", ev.Type)
	}
	if ev.BeadID != "ini-test.1" {
		t.Errorf("beadID = %q, want ini-test.1", ev.BeadID)
	}
	if ev.Pane != "eng1" {
		t.Errorf("pane = %q, want eng1", ev.Pane)
	}
}

func TestDetectCompletion_FailedExitCode(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 1,
			Content:  "bd update ini-test.1 --status ready_for_qa",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 0 {
		t.Errorf("got %d events, want 0 (exit code 1 means command failed)", len(events))
	}
}

func TestDetectCompletion_BdClose(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd close ini-test.2",
		},
	}
	events := detectCompletion(entries, "eng2")
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventBeadCompleted {
		t.Errorf("type = %v, want EventBeadCompleted", events[0].Type)
	}
	if events[0].BeadID != "ini-test.2" {
		t.Errorf("beadID = %q, want ini-test.2", events[0].BeadID)
	}
}

func TestDetectCompletion_BdClaim(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd update ini-test.3 --status in_progress",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventBeadClaimed {
		t.Errorf("type = %v, want EventBeadClaimed", events[0].Type)
	}
}

func TestDetectCompletion_AssistantDone(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:    "assistant",
			Content: "DONE: implemented the widget. Tests pass.",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventBeadCompleted {
		t.Errorf("type = %v, want EventBeadCompleted", events[0].Type)
	}
}

func TestDetectCompletion_AssistantFail(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:    "assistant",
			Content: "FAIL: cannot build, missing dependency.",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Type != EventBeadFailed {
		t.Errorf("type = %v, want EventBeadFailed", events[0].Type)
	}
}

func TestDetectCompletion_AssistantNotDone(t *testing.T) {
	// Mentions done but doesn't START with DONE: - should not trigger.
	entries := []JournalEntry{
		{
			Type:    "assistant",
			Content: "I think we're done with this. Let me check.",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 0 {
		t.Errorf("got %d events, want 0 (only prefix match counts)", len(events))
	}
}

func TestDetectCompletion_ToolUseEntryIgnored(t *testing.T) {
	// tool_use type (not tool_result) should not trigger.
	entries := []JournalEntry{
		{
			Type:     "tool_use",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd update ini-test.1 --status ready_for_qa",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 0 {
		t.Errorf("got %d events, want 0 (tool_use not tool_result)", len(events))
	}
}

func TestDetectCompletion_MultipleEntries(t *testing.T) {
	entries := []JournalEntry{
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd update ini-test.1 --status in_progress",
		},
		{
			Type:    "assistant",
			Content: "I will now implement the feature.",
		},
		{
			Type:     "tool_result",
			ToolName: "Bash",
			ExitCode: 0,
			Content:  "bd update ini-test.1 --status ready_for_qa",
		},
	}
	events := detectCompletion(entries, "eng1")
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2 (claim + completion)", len(events))
	}
	if events[0].Type != EventBeadClaimed {
		t.Errorf("events[0].Type = %v, want EventBeadClaimed", events[0].Type)
	}
	if events[1].Type != EventBeadCompleted {
		t.Errorf("events[1].Type = %v, want EventBeadCompleted", events[1].Type)
	}
}

// ── detectStall ──────────────────────────────────────────────────────

func TestDetectStall_Triggered(t *testing.T) {
	lastTime := time.Now().Add(-11 * time.Minute)
	ev := detectStall(lastTime, "ini-test.1", "eng1", DefaultStallThreshold)
	if ev == nil {
		t.Fatal("expected stall event, got nil")
	}
	if ev.Type != EventAgentStalled {
		t.Errorf("type = %v, want EventAgentStalled", ev.Type)
	}
	if ev.Pane != "eng1" {
		t.Errorf("pane = %q, want eng1", ev.Pane)
	}
	if ev.BeadID != "ini-test.1" {
		t.Errorf("beadID = %q, want ini-test.1", ev.BeadID)
	}
}

func TestDetectStall_NotTriggered_Recent(t *testing.T) {
	lastTime := time.Now().Add(-5 * time.Minute)
	ev := detectStall(lastTime, "ini-test.1", "eng1", DefaultStallThreshold)
	if ev != nil {
		t.Errorf("got stall event, want nil (only 5 min, threshold is 10)")
	}
}

func TestDetectStall_NotTriggered_NoBead(t *testing.T) {
	lastTime := time.Now().Add(-11 * time.Minute)
	ev := detectStall(lastTime, "", "eng1", DefaultStallThreshold)
	if ev != nil {
		t.Errorf("got stall event, want nil (no bead = idle agent)")
	}
}

func TestDetectStall_NotTriggered_ZeroTime(t *testing.T) {
	ev := detectStall(time.Time{}, "ini-test.1", "eng1", DefaultStallThreshold)
	if ev != nil {
		t.Errorf("got stall event, want nil (zero time = no entries seen yet)")
	}
}

func TestDetectStall_CustomThreshold(t *testing.T) {
	lastTime := time.Now().Add(-2 * time.Minute)
	ev := detectStall(lastTime, "ini-test.1", "eng1", 1*time.Minute)
	if ev == nil {
		t.Fatal("expected stall with 1m threshold for 2m-old entry, got nil")
	}
}

// ── detectStuck ──────────────────────────────────────────────────────

func TestDetectStuck_Triggered(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ExitCode: 1, Content: "error: build failed\nmore details"},
		{Type: "tool_result", ExitCode: 1, Content: "error: build failed\nmore details"},
		{Type: "tool_result", ExitCode: 1, Content: "error: build failed\nmore details"},
	}
	ev := detectStuck(entries, "eng1")
	if ev == nil {
		t.Fatal("expected stuck event, got nil")
	}
	if ev.Type != EventAgentStuck {
		t.Errorf("type = %v, want EventAgentStuck", ev.Type)
	}
	if ev.Pane != "eng1" {
		t.Errorf("pane = %q, want eng1", ev.Pane)
	}
}

func TestDetectStuck_SuccessBreaksStreak(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "tool_result", ExitCode: 0, Content: "ok"}, // success
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
	}
	ev := detectStuck(entries, "eng1")
	if ev != nil {
		t.Errorf("got stuck event, want nil (only 2 consecutive failures at the end)")
	}
}

func TestDetectStuck_BelowThreshold(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
	}
	ev := detectStuck(entries, "eng1")
	if ev != nil {
		t.Errorf("got stuck event, want nil (only 2 failures, threshold is %d)", consecutiveFailuresThreshold)
	}
}

func TestDetectStuck_NonToolEntriesBreakStreak(t *testing.T) {
	// Non-tool_result entries break the consecutive streak (ini-a1e.3).
	// Scanning backwards: [4]=failure, [3]=failure, [2]=progress → break.
	// Only 2 consecutive failures at tail; below threshold.
	entries := []JournalEntry{
		{Type: "assistant", Content: "let me try again"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "progress"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
		{Type: "tool_result", ExitCode: 1, Content: "error"},
	}
	ev := detectStuck(entries, "eng1")
	if ev != nil {
		t.Error("non-tool entry should break the streak — expected no stuck event, got one")
	}
}

func TestDetectStuck_ThreeConsecutiveAtTail(t *testing.T) {
	// Three back-to-back tool_result failures (no non-tool entries between them) triggers stuck.
	entries := []JournalEntry{
		{Type: "assistant", Content: "thinking"},
		{Type: "tool_result", ExitCode: 0, Content: "ok"},
		{Type: "tool_result", ExitCode: 1, Content: "error1"},
		{Type: "tool_result", ExitCode: 1, Content: "error2"},
		{Type: "tool_result", ExitCode: 1, Content: "error3"},
	}
	ev := detectStuck(entries, "eng1")
	if ev == nil {
		t.Fatal("expected stuck event for 3 consecutive failures at tail, got nil")
	}
	if ev.Type != EventAgentStuck {
		t.Errorf("type = %v, want EventAgentStuck", ev.Type)
	}
}

func TestDetectStuck_Empty(t *testing.T) {
	ev := detectStuck(nil, "eng1")
	if ev != nil {
		t.Errorf("got stuck event on empty journal, want nil")
	}
}

// ── dedup ─────────────────────────────────────────────────────────────

func TestDedupSuppressDuplicate(t *testing.T) {
	d := newDedup()
	ev := AgentEvent{Type: EventBeadCompleted, Pane: "eng1", BeadID: "ini-test.1"}

	if !d.shouldEmit(ev) {
		t.Fatal("first emit should be allowed")
	}
	if d.shouldEmit(ev) {
		t.Error("second emit within window should be suppressed")
	}
}

func TestDedupDifferentEventsNotSuppressed(t *testing.T) {
	d := newDedup()
	ev1 := AgentEvent{Type: EventBeadCompleted, Pane: "eng1", BeadID: "ini-test.1"}
	ev2 := AgentEvent{Type: EventBeadClaimed, Pane: "eng1", BeadID: "ini-test.1"}
	ev3 := AgentEvent{Type: EventBeadCompleted, Pane: "eng2", BeadID: "ini-test.1"}

	if !d.shouldEmit(ev1) {
		t.Error("ev1 should emit")
	}
	if !d.shouldEmit(ev2) {
		t.Error("ev2 (different type) should emit")
	}
	if !d.shouldEmit(ev3) {
		t.Error("ev3 (different pane) should emit")
	}
}

func TestDedupExpiry(t *testing.T) {
	d := newDedup()
	ev := AgentEvent{Type: EventAgentStalled, Pane: "eng1", BeadID: "ini-x.1"}

	// Manually insert a stale entry.
	key := dedupKey{pane: ev.Pane, beadID: ev.BeadID, evType: ev.Type}
	d.seen[key] = time.Now().Add(-(dedupWindow + time.Second))

	// Should be allowed since the record is expired.
	if !d.shouldEmit(ev) {
		t.Error("expired dedup entry should allow re-emission")
	}
}

func TestDedupPrune(t *testing.T) {
	d := newDedup()
	// Insert a stale entry and a fresh entry.
	old := dedupKey{pane: "eng1", beadID: "a", evType: EventBeadCompleted}
	fresh := dedupKey{pane: "eng2", beadID: "b", evType: EventBeadClaimed}
	d.seen[old] = time.Now().Add(-(dedupWindow + time.Second))
	d.seen[fresh] = time.Now()

	d.prune()

	if _, ok := d.seen[old]; ok {
		t.Error("old entry should be pruned")
	}
	if _, ok := d.seen[fresh]; !ok {
		t.Error("fresh entry should survive pruning")
	}
}
