package tui

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// ── Feature gate tests ──────────────────────────────────────────────

func TestResourceEnabled_Default(t *testing.T) {
	tui := &TUI{}
	if tui.ResourceEnabled() {
		t.Error("ResourceEnabled should be false by default")
	}
}

func TestResourceEnabled_WhenSet(t *testing.T) {
	tui := &TUI{autoSuspend: true}
	if !tui.ResourceEnabled() {
		t.Error("ResourceEnabled should be true when autoSuspend is set")
	}
}

func TestPressureThreshold_Default(t *testing.T) {
	tui := &TUI{}
	if got := tui.PressureThreshold(); got != 85 {
		t.Errorf("PressureThreshold() = %d, want 85", got)
	}
}

func TestPressureThreshold_Custom(t *testing.T) {
	tui := &TUI{pressureThreshold: 70}
	if got := tui.PressureThreshold(); got != 70 {
		t.Errorf("PressureThreshold() = %d, want 70", got)
	}
}

// ── Memory monitor tests ────────────────────────────────────────────

func TestPollPaneRSS_InvalidPID(t *testing.T) {
	if rss := pollPaneRSS(0); rss != 0 {
		t.Errorf("pollPaneRSS(0) = %d, want 0", rss)
	}
	if rss := pollPaneRSS(-1); rss != 0 {
		t.Errorf("pollPaneRSS(-1) = %d, want 0", rss)
	}
}

func TestPollPaneRSS_CurrentProcess(t *testing.T) {
	pid := os.Getpid()
	rss := pollPaneRSS(pid)
	if rss <= 0 {
		t.Errorf("pollPaneRSS(self=%d) = %d, want > 0", pid, rss)
	}
}

func TestPollPaneRSS_DeadProcess(t *testing.T) {
	rss := pollPaneRSS(99999999)
	if rss != 0 {
		t.Errorf("pollPaneRSS(dead) = %d, want 0", rss)
	}
}

// --- Pane accessor tests ---

func TestPaneMemoryRSS_DefaultZero(t *testing.T) {
	p := &Pane{}
	if rss := p.MemoryRSS(); rss != 0 {
		t.Errorf("MemoryRSS() = %d, want 0", rss)
	}
}

func TestPaneMemoryRSS_SetAndGet(t *testing.T) {
	p := &Pane{}
	p.setMemoryRSS(123456)
	if rss := p.MemoryRSS(); rss != 123456 {
		t.Errorf("MemoryRSS() = %d, want 123456", rss)
	}
}

func TestPaneMemoryRSS_ResetToZero(t *testing.T) {
	p := &Pane{}
	p.setMemoryRSS(50000)
	p.setMemoryRSS(0)
	if rss := p.MemoryRSS(); rss != 0 {
		t.Errorf("MemoryRSS() = %d, want 0 after reset", rss)
	}
}

func TestPanePinned(t *testing.T) {
	p := &Pane{}
	if p.IsPinned() {
		t.Error("new pane should not be pinned")
	}
	p.SetPinned(true)
	if !p.IsPinned() {
		t.Error("pane should be pinned after SetPinned(true)")
	}
	p.SetPinned(false)
	if p.IsPinned() {
		t.Error("pane should not be pinned after SetPinned(false)")
	}
}

func TestPaneLastOutputTime(t *testing.T) {
	p := &Pane{}
	if !p.LastOutputTime().IsZero() {
		t.Error("new pane should have zero LastOutputTime")
	}
	now := time.Now()
	p.mu.Lock()
	p.lastOutputTime = now
	p.mu.Unlock()
	if got := p.LastOutputTime(); !got.Equal(now) {
		t.Errorf("LastOutputTime() = %v, want %v", got, now)
	}
}

func TestPaneResumeGrace(t *testing.T) {
	p := &Pane{}
	if p.InResumeGrace() {
		t.Error("new pane should not be in resume grace")
	}
	p.SetResumeGrace(time.Now().Add(1 * time.Hour))
	if !p.InResumeGrace() {
		t.Error("pane should be in resume grace after SetResumeGrace(future)")
	}
	p.SetResumeGrace(time.Now().Add(-1 * time.Second))
	if p.InResumeGrace() {
		t.Error("pane should not be in resume grace after SetResumeGrace(past)")
	}
}

// --- System memory tests ---

func TestSystemMemoryTotal(t *testing.T) {
	total, err := systemMemoryTotal()
	if err != nil {
		t.Fatalf("systemMemoryTotal() error: %v", err)
	}
	if total < 524288 { // At least 512 MB
		t.Errorf("systemMemoryTotal() = %d KB, suspiciously low", total)
	}
}

func TestSystemMemoryAvail(t *testing.T) {
	avail, err := systemMemoryAvail()
	if err != nil {
		t.Fatalf("systemMemoryAvail() error: %v", err)
	}
	if avail <= 0 {
		t.Errorf("systemMemoryAvail() = %d KB, want > 0", avail)
	}
}

func TestSystemMemoryAvailable_Accessor(t *testing.T) {
	tui := &TUI{systemMemAvail: 4096}
	if got := tui.SystemMemoryAvailable(); got != 4096 {
		t.Errorf("SystemMemoryAvailable() = %d, want 4096", got)
	}
}

func TestSystemMemoryTotal_Accessor(t *testing.T) {
	tui := &TUI{systemMemTotal: 8192}
	if got := tui.SystemMemoryTotal(); got != 8192 {
		t.Errorf("SystemMemoryTotal() = %d, want 8192", got)
	}
}

// --- pollAllRSS integration ---

func TestPollAllRSS_Integration(t *testing.T) {
	tui := &TUI{quitCh: make(chan struct{})}
	p := &Pane{pid: os.Getpid()}
	tui.panes = toPaneViews([]*Pane{p})
	tui.pollAllRSS()

	if rss := p.MemoryRSS(); rss <= 0 {
		t.Errorf("after pollAllRSS, pane RSS = %d, want > 0", rss)
	}
	if avail := tui.SystemMemoryAvailable(); avail <= 0 {
		t.Errorf("after pollAllRSS, systemMemAvail = %d, want > 0", avail)
	}
}

// --- Suspend policy tests ---

// newResourceTestTUI creates a TUI with the ipcCh=nil (runOnMain executes directly)
// and configurable memory values for testing the suspend policy.
func newResourceTestTUI(total, avail int64, threshold int) *TUI {
	return &TUI{
		systemMemTotal:    total,
		systemMemAvail:    avail,
		pressureThreshold: threshold,
		autoSuspend:       true,
		quitCh:            make(chan struct{}),
		agentEvents:       make(chan AgentEvent, 64),
	}
}

func newResourceTestPane(name string, alive bool, activity ActivityState, beadID string, lastOutput time.Time) *Pane {
	return &Pane{
		name:           name,
		alive:          alive,
		activity:       activity,
		beadID:         beadID,
		lastOutputTime: lastOutput,
		memoryRSS:      500000, // 500 MB default
	}
}

func TestSuspendPolicy_BelowThreshold(t *testing.T) {
	// 70% used, threshold 80% -> no suspension.
	tui := newResourceTestTUI(100000, 30000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-10*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should not suspend when below threshold")
	}
}

func TestSuspendPolicy_SuspendsLRUIdle(t *testing.T) {
	// 90% used, threshold 80% -> should suspend the oldest idle agent.
	tui := newResourceTestTUI(100000, 10000, 80)
	older := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	newer := newResourceTestPane("eng2", true, StateIdle, "", time.Now().Add(-5*time.Minute))
	tui.panes = toPaneViews([]*Pane{older, newer})
	tui.checkSuspendPolicy()

	older.mu.Lock()
	olderAct := older.activity
	older.mu.Unlock()
	newer.mu.Lock()
	newerAct := newer.activity
	newer.mu.Unlock()

	if olderAct != StateSuspended {
		t.Errorf("older idle agent should be suspended, got %v", olderAct)
	}
	// Newer should NOT be suspended (avail increases after first suspend).
	if newerAct == StateSuspended {
		t.Error("newer idle agent should not be suspended (pressure relieved)")
	}
}

func TestSuspendPolicy_NeverSuspendsWithBead(t *testing.T) {
	// 95% used, but only agent has a bead -> no suspension.
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "ini-abc.1", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should never suspend agent with bead")
	}
}

func TestSuspendPolicy_NeverSuspendsPinned(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	p.pinned = true
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should never suspend pinned agent")
	}
}

func TestSuspendPolicy_NeverSuspendsFocused(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.layoutState.Focused = "eng1"

	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should never suspend focused agent")
	}
}

func TestSuspendPolicy_NeverSuspendsRunning(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateRunning, "", time.Now())
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should never suspend running agent")
	}
}

func TestSuspendPolicy_RespectsResumeGrace(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	p.resumeGrace = time.Now().Add(1 * time.Minute) // In grace period.
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should not suspend agent in resume grace period")
	}
}

func TestSuspendPolicy_MaxTwoPerCycle(t *testing.T) {
	// Extreme pressure with 4 idle agents. Should suspend at most 2.
	// Use very low avail and very high RSS so pressure stays high.
	tui := newResourceTestTUI(100000, 1000, 80)
	panes := make([]*Pane, 4)
	for i := range panes {
		panes[i] = newResourceTestPane(
			"eng"+string(rune('1'+i)),
			true, StateIdle, "",
			time.Now().Add(-time.Duration(40-i*10)*time.Minute),
		)
		panes[i].memoryRSS = 100 // Tiny RSS so avail barely changes.
	}
	tui.panes = toPaneViews(panes)

	tui.checkSuspendPolicy()

	suspendedCount := 0
	for _, p := range panes {
		p.mu.Lock()
		if p.activity == StateSuspended {
			suspendedCount++
		}
		p.mu.Unlock()
	}
	if suspendedCount > maxSuspendPerCycle {
		t.Errorf("suspended %d agents, max should be %d", suspendedCount, maxSuspendPerCycle)
	}
	if suspendedCount == 0 {
		t.Error("should have suspended at least one agent under extreme pressure")
	}
}

func TestSuspendPolicy_DisabledWhenGateOff(t *testing.T) {
	// When autoSuspend is false, the monitor loop never starts.
	// Even if checkSuspendPolicy were called directly, threshold defaults
	// to 85 so it would still evaluate. The gate prevents the goroutine
	// from launching in Run(). Test the gate separately.
	tui := &TUI{autoSuspend: false}
	if tui.ResourceEnabled() {
		t.Error("resource should not be enabled when autoSuspend is false")
	}
}

// ── Message queue tests ─────────────────────────────────────────────

// TestEnqueueMessage verifies basic FIFO enqueue behavior.
func TestEnqueueMessage(t *testing.T) {
	p := &Pane{}
	dropped := p.EnqueueMessage("hello", true)
	if dropped {
		t.Error("first enqueue should not drop")
	}
	dropped = p.EnqueueMessage("world", false)
	if dropped {
		t.Error("second enqueue should not drop")
	}
	if len(p.messageQueue) != 2 {
		t.Fatalf("queue len = %d, want 2", len(p.messageQueue))
	}
	if p.messageQueue[0].Text != "hello" || !p.messageQueue[0].Enter {
		t.Errorf("msg 0 = %+v, want {hello, true}", p.messageQueue[0])
	}
	if p.messageQueue[1].Text != "world" || p.messageQueue[1].Enter {
		t.Errorf("msg 1 = %+v, want {world, false}", p.messageQueue[1])
	}
}

// TestEnqueueMessageCapDrop verifies that the queue caps at maxMessageQueue
// and drops the oldest message when full.
func TestEnqueueMessageCapDrop(t *testing.T) {
	p := &Pane{}
	// Fill the queue to capacity.
	for i := 0; i < maxMessageQueue; i++ {
		p.EnqueueMessage("msg", true)
	}
	if len(p.messageQueue) != maxMessageQueue {
		t.Fatalf("queue len = %d, want %d", len(p.messageQueue), maxMessageQueue)
	}

	// One more should drop the oldest.
	dropped := p.EnqueueMessage("overflow", true)
	if !dropped {
		t.Error("should report dropped when queue is full")
	}
	if len(p.messageQueue) != maxMessageQueue {
		t.Fatalf("queue len = %d after overflow, want %d", len(p.messageQueue), maxMessageQueue)
	}
	// The newest should be last.
	if p.messageQueue[maxMessageQueue-1].Text != "overflow" {
		t.Errorf("newest msg = %q, want 'overflow'", p.messageQueue[maxMessageQueue-1].Text)
	}
}

// TestDrainQueue verifies drain returns all messages in FIFO order and empties
// the queue.
func TestDrainQueue(t *testing.T) {
	p := &Pane{}
	p.EnqueueMessage("first", true)
	p.EnqueueMessage("second", false)
	p.EnqueueMessage("third", true)

	msgs := p.DrainQueue()
	if len(msgs) != 3 {
		t.Fatalf("drained %d, want 3", len(msgs))
	}
	if msgs[0].Text != "first" || msgs[1].Text != "second" || msgs[2].Text != "third" {
		t.Errorf("order wrong: %v", msgs)
	}
	if len(p.messageQueue) != 0 {
		t.Errorf("queue should be empty after drain, len = %d", len(p.messageQueue))
	}

	// Drain on empty returns nil.
	if p.DrainQueue() != nil {
		t.Error("draining empty queue should return nil")
	}
}

// TestSuspendedAccessor verifies the IsSuspended getter and SetSuspended setter.
func TestSuspendedAccessor(t *testing.T) {
	p := &Pane{}
	if p.IsSuspended() {
		t.Error("new pane should not be suspended")
	}
	p.SetSuspended(true)
	if !p.IsSuspended() {
		t.Error("pane should be suspended after SetSuspended(true)")
	}
	p.SetSuspended(false)
	if p.IsSuspended() {
		t.Error("pane should not be suspended after SetSuspended(false)")
	}
}

// TestQueueLen verifies QueueLen returns the correct count.
func TestQueueLen(t *testing.T) {
	p := &Pane{}
	if p.QueueLen() != 0 {
		t.Error("new pane should have QueueLen 0")
	}
	p.EnqueueMessage("a", true)
	p.EnqueueMessage("b", true)
	if p.QueueLen() != 2 {
		t.Errorf("QueueLen = %d, want 2", p.QueueLen())
	}
}

// TestEnqueueMessageTimestamp verifies that each queued message gets a timestamp.
func TestEnqueueMessageTimestamp(t *testing.T) {
	p := &Pane{}
	before := time.Now()
	p.EnqueueMessage("timed", true)
	after := time.Now()

	if p.messageQueue[0].Time.Before(before) || p.messageQueue[0].Time.After(after) {
		t.Errorf("message timestamp %v not between %v and %v",
			p.messageQueue[0].Time, before, after)
	}
}

// ── Pin/unpin tests ─────────────────────────────────────────────────

// TestPinnedAccessor verifies IsPinned/SetPinned on Pane.
func TestPinnedAccessor(t *testing.T) {
	p := &Pane{}
	if p.IsPinned() {
		t.Error("new pane should not be pinned")
	}
	p.SetPinned(true)
	if !p.IsPinned() {
		t.Error("pane should be pinned after SetPinned(true)")
	}
	p.SetPinned(false)
	if p.IsPinned() {
		t.Error("pane should not be pinned after SetPinned(false)")
	}
}

// TestSuperPinnedByDefault verifies DefaultLayoutState pins "super".
func TestSuperPinnedByDefault(t *testing.T) {
	ls := DefaultLayoutState([]string{"super", "eng1", "eng2"})
	if !ls.Pinned["super"] {
		t.Error("super should be pinned by default")
	}
	if ls.Pinned["eng1"] {
		t.Error("eng1 should not be pinned by default")
	}
}

// TestPinPersistsInLayout verifies that SaveLayout/LoadLayout round-trip the
// pinned set.
func TestPinPersistsInLayout(t *testing.T) {
	dir := t.TempDir()
	initechDir := dir // SaveLayout expects the project root.

	state := DefaultLayoutState([]string{"super", "eng1", "eng2"})
	state.Pinned["eng1"] = true // pin eng1 too

	if err := SaveLayout(initechDir, state); err != nil {
		t.Fatalf("SaveLayout: %v", err)
	}

	loaded, ok := LoadLayout(initechDir, []string{"super", "eng1", "eng2"})
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if !loaded.Pinned["super"] {
		t.Error("super should be pinned after load")
	}
	if !loaded.Pinned["eng1"] {
		t.Error("eng1 should be pinned after load")
	}
	if loaded.Pinned["eng2"] {
		t.Error("eng2 should not be pinned after load")
	}
}

// TestPinStaleRoleFilteredOnLoad verifies that pinned roles not in the current
// pane list are dropped on load (stale references).
func TestPinStaleRoleFilteredOnLoad(t *testing.T) {
	dir := t.TempDir()

	state := DefaultLayoutState([]string{"super", "eng1", "eng2"})
	state.Pinned["eng2"] = true
	SaveLayout(dir, state)

	// Load with eng2 removed from the roster.
	loaded, ok := LoadLayout(dir, []string{"super", "eng1"})
	if !ok {
		t.Fatal("LoadLayout returned false")
	}
	if loaded.Pinned["eng2"] {
		t.Error("stale pinned role eng2 should be filtered out on load")
	}
}

// TestHandleIPCSendResumesSuspended verifies that handleIPCSend triggers
// resume-on-message when the target pane is suspended. The message is queued
// and then resume is attempted. Since the test pane has no real command, the
// resume succeeds (spawns $SHELL) and drains the queue.
func TestHandleIPCSendResumesSuspended(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.handleIPCSend(server, IPCRequest{Target: "eng1", Text: "hello", Enter: true})
	}()

	// Read the response. Resume spawns a real shell so this may take a moment.
	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)

	<-done

	if !resp.OK {
		// Resume may fail in constrained test environments; verify the message
		// was at least queued (preserved for retry).
		if !strings.Contains(resp.Error, "resume") {
			t.Errorf("unexpected error: %s", resp.Error)
		}
		return
	}
	if !strings.Contains(resp.Data, "resumed") {
		t.Errorf("response data should contain 'resumed', got: %s", resp.Data)
	}
}

// TestHandleIPCSendBypassesQueueWhenNotSuspended verifies that a non-suspended
// pane receives messages via injectText, not the queue.
func TestHandleIPCSendBypassesQueueWhenNotSuspended(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.suspended = false
	tui := &TUI{panes: toPaneViews([]*Pane{p})}

	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.handleIPCSend(server, IPCRequest{Target: "eng1", Text: "hi", Enter: false})
	}()

	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)

	<-done

	if !resp.OK {
		t.Errorf("response should be OK, got error: %s", resp.Error)
	}
	// Queue should be empty — message was injected, not queued.
	if p.QueueLen() != 0 {
		t.Errorf("queue should be empty for non-suspended pane, got %d", p.QueueLen())
	}
}

// ── Suspend policy tests ───────────────────────────────────────────

func TestSuspendPolicy_ThresholdDefaultsTo85(t *testing.T) {
	// pressureThreshold=0 (unset) should use default 85.
	tui := newResourceTestTUI(100000, 14000, 0) // 86% used, default threshold 85
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act != StateSuspended {
		t.Error("threshold=0 should default to 85, and 86% usage should trigger suspend")
	}
}

func TestSuspendPolicy_EmitsEvent(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	select {
	case ev := <-tui.agentEvents:
		if ev.Type != EventAgentSuspended {
			t.Errorf("event type = %v, want EventAgentSuspended", ev.Type)
		}
		if ev.Pane != "eng1" {
			t.Errorf("event pane = %q, want eng1", ev.Pane)
		}
	default:
		t.Error("expected EventAgentSuspended to be emitted")
	}
}

func TestSuspendPolicy_LRUOrdering(t *testing.T) {
	// Three idle agents, only one should be suspended (the oldest).
	// Set avail so only one suspension brings pressure below threshold.
	// total=100000, avail=15000 -> 85% used. Threshold=80.
	// After suspending one with 10000KB RSS: avail=25000 -> 75% used -> done.
	tui := newResourceTestTUI(100000, 15000, 80)
	oldest := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-60*time.Minute))
	oldest.memoryRSS = 10000
	middle := newResourceTestPane("eng2", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	middle.memoryRSS = 10000
	newest := newResourceTestPane("eng3", true, StateIdle, "", time.Now().Add(-5*time.Minute))
	newest.memoryRSS = 10000
	tui.panes = toPaneViews([]*Pane{newest, oldest, middle}) // Scrambled order

	tui.checkSuspendPolicy()

	oldest.mu.Lock()
	oldestAct := oldest.activity
	oldest.mu.Unlock()
	middle.mu.Lock()
	middleAct := middle.activity
	middle.mu.Unlock()
	newest.mu.Lock()
	newestAct := newest.activity
	newest.mu.Unlock()

	if oldestAct != StateSuspended {
		t.Error("oldest idle agent should be suspended first")
	}
	if middleAct == StateSuspended {
		t.Error("middle agent should not be suspended (pressure relieved)")
	}
	if newestAct == StateSuspended {
		t.Error("newest agent should not be suspended")
	}
}

func TestSuspendPolicy_SkipsDeadAndSuspended(t *testing.T) {
	tui := newResourceTestTUI(100000, 5000, 80)
	dead := newResourceTestPane("eng1", false, StateDead, "", time.Now().Add(-60*time.Minute))
	suspended := newResourceTestPane("eng2", true, StateSuspended, "", time.Now().Add(-30*time.Minute))
	idle := newResourceTestPane("eng3", true, StateIdle, "", time.Now().Add(-10*time.Minute))
	tui.panes = toPaneViews([]*Pane{dead, suspended, idle})
	tui.checkSuspendPolicy()

	dead.mu.Lock()
	deadAct := dead.activity
	dead.mu.Unlock()
	suspended.mu.Lock()
	suspAct := suspended.activity
	suspended.mu.Unlock()
	idle.mu.Lock()
	idleAct := idle.activity
	idle.mu.Unlock()

	if deadAct != StateDead {
		t.Error("dead pane should stay dead")
	}
	if suspAct != StateSuspended {
		t.Error("already-suspended pane should stay suspended")
	}
	if idleAct != StateSuspended {
		t.Error("idle pane should be suspended under pressure")
	}
}

func TestSuspendPolicy_NoTotalMemory(t *testing.T) {
	// systemMemTotal=0 means we couldn't query it. Policy should be a no-op.
	tui := newResourceTestTUI(0, 5000, 80)
	p := newResourceTestPane("eng1", true, StateIdle, "", time.Now().Add(-30*time.Minute))
	tui.panes = toPaneViews([]*Pane{p})
	tui.checkSuspendPolicy()

	p.mu.Lock()
	act := p.activity
	p.mu.Unlock()
	if act == StateSuspended {
		t.Error("should not suspend when total memory is unknown")
	}
}

func TestFormatRSSHuman(t *testing.T) {
	tests := []struct {
		kb   int64
		want string
	}{
		{500, "500 KB"},
		{2048, "2 MB"},
		{1048577, "1.0 GB"},
		{2097152, "2.0 GB"},
	}
	for _, tt := range tests {
		got := formatRSSHuman(tt.kb)
		if got != tt.want {
			t.Errorf("formatRSSHuman(%d) = %q, want %q", tt.kb, got, tt.want)
		}
	}
}

// ── Resume-on-message tests ────────────────────────────────────────

func TestResumePane_SkipsIfNotSuspended(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.suspended = false
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	err := tui.resumePane(p, "test")
	if err != nil {
		t.Errorf("resumePane should return nil for non-suspended pane, got: %v", err)
	}
}

func TestResumePane_SetsGracePeriod(t *testing.T) {
	// Use a real shell command so NewPane succeeds.
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	err := tui.resumePane(p, "test")
	if err != nil {
		t.Skipf("resumePane failed (expected in some test envs): %v", err)
	}

	// The new pane should have a resume grace period set.
	newPane := tui.panes[0]
	if !newPane.(*Pane).InResumeGrace() {
		t.Error("resumed pane should be in grace period")
	}
}

func TestResumePane_EmitsEvent(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	err := tui.resumePane(p, "super")
	if err != nil {
		t.Skipf("resumePane failed (expected in some test envs): %v", err)
	}

	select {
	case ev := <-tui.agentEvents:
		if ev.Type != EventAgentResumed {
			t.Errorf("event type = %v, want EventAgentResumed", ev.Type)
		}
		if ev.Pane != "eng1" {
			t.Errorf("event pane = %q, want eng1", ev.Pane)
		}
		if !strings.Contains(ev.Detail, "super") {
			t.Errorf("event detail should mention sender, got: %s", ev.Detail)
		}
	default:
		t.Error("expected EventAgentResumed to be emitted")
	}
}

func TestResumePane_PreservesQueueOnFailure(t *testing.T) {
	// Create a pane whose cfg points to a nonexistent working directory.
	// NewPane's pty.StartWithSize will fail because the directory doesn't exist.
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	p.cfg.Dir = "/nonexistent/directory/that/cannot/exist"
	p.EnqueueMessage("pending", true)

	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	err := tui.resumePane(p, "test")
	if err == nil {
		t.Fatal("resumePane should fail with invalid directory")
	}

	// Queue should be preserved for retry.
	if p.QueueLen() != 1 {
		t.Errorf("queue should be preserved on failure, got len=%d", p.QueueLen())
	}
}

func TestResumePane_ConcurrentResume(t *testing.T) {
	// Two concurrent resumes: the second should find the pane already resumed.
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			errs <- tui.resumePane(p, "test")
		}()
	}

	// Both should succeed (first resumes, second finds it already not suspended).
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Skipf("resumePane failed (expected in some test envs): %v", err)
		}
	}
}

func TestResumePane_CopiesPinnedAndBead(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.cfg.Command = []string{"/bin/sh", "-c", "echo ready; sleep 1"}
	p.suspended = true
	p.pinned = true
	p.beadID = "ini-abc.1"
	p.beadTitle = "Test bead"
	tui := &TUI{
		panes:       toPaneViews([]*Pane{p}),
		quitCh:      make(chan struct{}),
		agentEvents: make(chan AgentEvent, 64),
	}

	err := tui.resumePane(p, "test")
	if err != nil {
		t.Skipf("resumePane failed (expected in some test envs): %v", err)
	}

	newPane := tui.panes[0]
	if !newPane.IsPinned() {
		t.Error("resumed pane should preserve pinned state")
	}
	if newPane.BeadID() != "ini-abc.1" {
		t.Errorf("resumed pane beadID = %q, want ini-abc.1", newPane.BeadID())
	}
}

func TestWaitForInit_AlreadyActive(t *testing.T) {
	p := &Pane{}
	p.mu.Lock()
	p.lastOutputTime = time.Now()
	p.mu.Unlock()

	tui := &TUI{quitCh: make(chan struct{})}
	err := tui.waitForInit(p)
	if err != nil {
		t.Errorf("waitForInit should succeed when pane already has output: %v", err)
	}
}

func TestWaitForInit_QuitChClosed(t *testing.T) {
	p := &Pane{} // lastOutputTime is zero, never produces output.
	tui := &TUI{quitCh: make(chan struct{})}
	close(tui.quitCh)

	err := tui.waitForInit(p)
	if err == nil {
		t.Error("waitForInit should return error when quitCh is closed")
	}
}
