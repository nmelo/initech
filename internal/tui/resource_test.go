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
	// PID 99999999 should not exist.
	rss := pollPaneRSS(99999999)
	if rss != 0 {
		t.Errorf("pollPaneRSS(dead) = %d, want 0", rss)
	}
}

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

func TestSystemMemoryTotal(t *testing.T) {
	total, err := systemMemoryTotal()
	if err != nil {
		t.Fatalf("systemMemoryTotal() error: %v", err)
	}
	// Any real machine should have at least 512 MB (524288 KB).
	if total < 524288 {
		t.Errorf("systemMemoryTotal() = %d KB, suspiciously low", total)
	}
}

func TestSystemMemoryAvail(t *testing.T) {
	avail, err := systemMemoryAvail()
	if err != nil {
		t.Fatalf("systemMemoryAvail() error: %v", err)
	}
	// Should have at least some available memory.
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

func TestPollAllRSS_Integration(t *testing.T) {
	// Create a TUI with ipcCh=nil so runOnMain executes directly.
	tui := &TUI{
		quitCh: make(chan struct{}),
	}
	// Create a pane with the current process PID (no real PTY needed).
	p := &Pane{pid: os.Getpid()}
	tui.panes = []*Pane{p}

	tui.pollAllRSS()

	if rss := p.MemoryRSS(); rss <= 0 {
		t.Errorf("after pollAllRSS, pane RSS = %d, want > 0", rss)
	}
	if avail := tui.SystemMemoryAvailable(); avail <= 0 {
		t.Errorf("after pollAllRSS, systemMemAvail = %d, want > 0", avail)
	}
}

func TestMonitorNotStartedWhenDisabled(t *testing.T) {
	// Verify the gate: when autoSuspend is false, startMemoryMonitor
	// should not be called (tested via the Run() integration path).
	// This test just checks the boolean gate directly.
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

// TestHandleIPCSendQueuesSuspended verifies that handleIPCSend queues messages
// for a suspended pane instead of injecting them into the PTY.
func TestHandleIPCSendQueuesSuspended(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.suspended = true
	tui := &TUI{panes: []*Pane{p}}

	server, client := net.Pipe()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.handleIPCSend(server, IPCRequest{Target: "eng1", Text: "hello", Enter: true})
	}()

	// Read the response.
	scanner := bufio.NewScanner(client)
	scanner.Scan()
	var resp IPCResponse
	json.Unmarshal(scanner.Bytes(), &resp)

	if !resp.OK {
		t.Errorf("response should be OK, got error: %s", resp.Error)
	}
	if !strings.Contains(resp.Data, "queued") {
		t.Errorf("response data should contain 'queued', got: %s", resp.Data)
	}

	<-done

	// Verify message was queued, not injected.
	if p.QueueLen() != 1 {
		t.Fatalf("queue len = %d, want 1", p.QueueLen())
	}
	if p.messageQueue[0].Text != "hello" || !p.messageQueue[0].Enter {
		t.Errorf("queued msg = %+v, want {hello, true}", p.messageQueue[0])
	}
}

// TestHandleIPCSendBypassesQueueWhenNotSuspended verifies that a non-suspended
// pane receives messages via injectText, not the queue.
func TestHandleIPCSendBypassesQueueWhenNotSuspended(t *testing.T) {
	p := newEmuPane("eng1", 80, 24)
	p.suspended = false
	tui := &TUI{panes: []*Pane{p}}

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
