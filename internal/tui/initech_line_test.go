package tui

import (
	"sync"
	"testing"
	"time"
)

// lineRecorder stands in for the send path and records each line by pane.
type lineRecorder struct {
	mu    sync.Mutex
	lines []string
	seen  chan struct{}
}

func recordInitechLines(t *testing.T) *lineRecorder {
	t.Helper()
	r := &lineRecorder{seen: make(chan struct{}, 16)}
	orig := initechSend
	t.Cleanup(func() { initechSend = orig })
	initechSend = func(p *Pane, text string, o sendOrigin) {
		r.mu.Lock()
		r.lines = append(r.lines, p.name+"|"+o.From+"|"+text)
		r.mu.Unlock()
		r.seen <- struct{}{}
	}
	return r
}

func (r *lineRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

// waitFor waits up to a second for n lines, then returns what came.
func (r *lineRecorder) waitFor(n int) []string {
	deadline := time.After(time.Second)
	for len(r.all()) < n {
		select {
		case <-r.seen:
		case <-deadline:
			return r.all()
		}
	}
	return r.all()
}

func TestInitechLine_GoesToSuperFromInitech(t *testing.T) {
	r := recordInitechLines(t)
	tui := newTestTUI(testPane("eng1"), testPane("super"))
	tui.sendInitechLine("☾ ini-x waits on tag v1: tag v1 exists; move it on")
	got := r.waitFor(1)
	if len(got) != 1 || got[0] != "super|initech|[from initech] ☾ ini-x waits on tag v1: tag v1 exists; move it on" {
		t.Fatalf("lines %q", got)
	}
}

// No super pane, or two: nothing is sent to a guess.
func TestInitechLine_NoneOrTwoSuperPanesSendsNothing(t *testing.T) {
	for _, panes := range [][]*Pane{{testPane("eng1")}, {testPane("super"), testPane("super")}} {
		r := recordInitechLines(t)
		tui := newTestTUI(panes...)
		tui.sendInitechLine("x")
		time.Sleep(50 * time.Millisecond)
		if got := r.all(); len(got) != 0 {
			t.Errorf("%d super panes: sent %q", len(panes)-1+boolInt(panes[0].name == "super"), got)
		}
	}
}

// It never blocks the caller: the send runs on its own goroutine, so a send
// path that does not return still leaves the caller free. The test waits for
// the send to start and to end before it restores the seam.
func TestInitechLine_DoesNotBlockItsCaller(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	orig := initechSend
	t.Cleanup(func() { initechSend = orig })
	initechSend = func(p *Pane, text string, o sendOrigin) {
		close(entered)
		<-release
		close(finished)
	}
	tui := newTestTUI(testPane("super"))
	done := make(chan struct{})
	go func() { tui.sendInitechLine("x"); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("sendInitechLine blocked while the send path held")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("the send never started")
	}
	close(release)
	<-finished
}

// The real send path carries initech as the sender, so do-not-disturb holds
// it as it holds an agent: it waits, never goes through as the operator does.
func TestInitechLine_DoNotDisturbHoldsInitech(t *testing.T) {
	p := testPane("super")
	p.startDND()
	var broke bool
	if got, _ := p.dndOrigin(sendOrigin{From: initechSender}, &broke); got != dndOriginWaits {
		t.Errorf("do-not-disturb rules %v on initech, want it held", got)
	}
	// Positive twin: the operator goes through, so the check can tell them apart.
	if got, _ := p.dndOrigin(sendOrigin{From: operatorSender}, &broke); got != dndOriginThrough {
		t.Errorf("the operator is held: %v", got)
	}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
