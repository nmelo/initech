package tui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-dbqy: agents hosted by initech serve get the maintenance a TUI pane
// gets, through the same functions (maintenance.go). Before this the daemon
// ran none of it: held mail waited forever on a quiet pane, deadlines were
// never reported, dialog announcements were never read, Activity never moved.

// daemonPane is a pane as RunDaemon builds it: no event channel, so every
// outcome the tick emits must still go out without one.
func daemonPane(t *testing.T) *Pane {
	t.Helper()
	p, _, _ := heldMailPane(t, "eng1")
	p.eventCh = nil
	return p
}

// dbqyHeld is a daemon hosting one pane with two messages held behind a
// corroborated dialog, as two sends during a dialog leave them.
func dbqyHeld(t *testing.T) (*Daemon, *Pane) {
	t.Helper()
	p := daemonPane(t)
	p.latchDialogOpen("Claude needs your permission")
	p.noteDialogSighting()
	p.SendText("[from super] one", true)
	p.SendText("[from super] two", true)
	if q := p.QueuedMessageCount(); q != 2 {
		t.Fatalf("precondition: %d held, want 2", q)
	}
	return &Daemon{panes: []*Pane{p}}, p
}

// Criterion 1: past the held-mail deadline the daemon reports the held mail,
// as the TUI does, and does not keep it.
func TestDaemonMaintenance_HeldMailIsReportedPastItsDeadline(t *testing.T) {
	logs := captureLogs(t)
	d, p := dbqyHeld(t)
	now := time.Now()
	d.maintain(now)
	if q := p.QueuedMessageCount(); q != 2 {
		t.Fatalf("before the deadline %d held, want both still held", q)
	}
	d.maintain(now.Add(heldMessageDeadline + time.Minute))
	if q := p.QueuedMessageCount(); q != 0 {
		t.Fatalf("past the deadline %d still held: the daemon kept the mail silently", q)
	}
	if n := countLines(logs, "held message NOT DELIVERED"); n != 2 {
		t.Errorf("%d NOT DELIVERED lines in the daemon log, want one per held message (2)", n)
	}
}

// The ticker RunDaemon starts, not only the method: held mail older than the
// deadline is reported within a few ticks.
func TestDaemonMaintenance_TheTickerRunDaemonStartsResolvesHeldMail(t *testing.T) {
	d, p := dbqyHeld(t)
	p.mu.Lock()
	for i := range p.messageQueue {
		p.messageQueue[i].Time = time.Now().Add(-heldMessageDeadline - time.Minute)
	}
	p.mu.Unlock()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { d.runMaintenance(stop, 5*time.Millisecond); close(done) }()
	defer func() { close(stop); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && p.QueuedMessageCount() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if q := p.QueuedMessageCount(); q != 0 {
		t.Fatalf("the daemon's ticker left %d held messages past their deadline", q)
	}
}

// Criterion 2: a withheld Enter past its deadline is reported, not kept.
func TestDaemonMaintenance_AWithheldSubmitPastItsDeadlineIsReported(t *testing.T) {
	logs := captureLogs(t)
	p := daemonPane(t)
	ps := newPendingSubmit("[from super] hello", "bracketed", "")
	ps.deadline = time.Now().Add(-time.Second)
	p.mu.Lock()
	p.pendingSubmit = ps
	p.mu.Unlock()
	(&Daemon{panes: []*Pane{p}}).maintain(time.Now())
	p.mu.Lock()
	left := p.pendingSubmit
	p.mu.Unlock()
	if left != nil {
		t.Fatal("a withheld submit past its deadline is still pending on the daemon")
	}
	if !strings.Contains(logs.String(), "NOT DELIVERED") {
		t.Error("the withheld submit was dropped without a NOT DELIVERED line")
	}
}

// Criterion 3: the OSC 777 mailbox is read on the daemon. An announcement
// raises the latch; the real prompt on screen corroborates it and holds
// mail; the resting composer, held for the idle window, clears it.
func TestDaemonMaintenance_DialogAnnouncementsAreRead(t *testing.T) {
	p := daemonPane(t)
	p.emu = claudeScreen(t, 40, zrdtScreen(t, "haiku_write_prompt.txt"))
	p.attn = &attentionSignal{}
	registerAttentionOSC(p)
	d := &Daemon{panes: []*Pane{p}}
	p.emu.Write([]byte("\x1b]777;notify;Claude Code;Claude needs your permission\x07"))
	now := time.Now()
	d.maintain(now)
	if !p.dialogLatched() {
		t.Fatal("the daemon never read the dialog announcement: no latch")
	}
	if waiting, _, _ := p.WaitingInput(); !waiting {
		t.Error("the daemon raised no needs-input state for the announced dialog")
	}
	p.SendText("[from super] held", true)
	if q := p.QueuedMessageCount(); q != 1 {
		t.Fatalf("a send under the corroborated dialog: %d held, want 1", q)
	}
	p.emu = claudeScreen(t, 40, zrdtScreen(t, "haiku_after_esc.txt"))
	for s := 1; s <= int(idlePromptStableWindow/time.Second)+2; s++ {
		d.maintain(now.Add(time.Duration(s) * time.Second))
	}
	if p.dialogLatched() {
		t.Error("the latch stayed after the dialog left and the composer rested past the idle window")
	}
}

// Criterion 4: Activity moves with output on the daemon, so the busy grace
// on stop (Close grants it when Activity is running) and status read it.
func TestDaemonMaintenance_ActivityFollowsOutput(t *testing.T) {
	p := daemonPane(t)
	d := &Daemon{panes: []*Pane{p}}
	p.mu.Lock()
	p.lastOutputTime = time.Now()
	p.activity = StateIdle
	p.mu.Unlock()
	d.maintain(time.Now())
	if a := p.Activity(); a != StateRunning {
		t.Fatalf("a pane with fresh output reads %v on the daemon, want running", a)
	}
	p.mu.Lock()
	p.lastOutputTime = time.Now().Add(-time.Minute)
	p.mu.Unlock()
	d.maintain(time.Now())
	if a := p.Activity(); a != StateIdle {
		t.Fatalf("a pane quiet for a minute reads %v on the daemon, want idle", a)
	}
}

// Single-flight: ticks never run beside each other (run under -race).
func TestDaemonMaintenance_TicksNeverOverlap(t *testing.T) {
	d, _ := dbqyHeld(t)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := time.Now()
			for i := 0; i < 50; i++ {
				d.maintain(base.Add(time.Duration(g*100+i) * time.Second))
			}
		}(g)
	}
	wg.Wait()
}

// Feed it nothing: a daemon with no panes, and a pane closed between ticks
// (its held mail is reported as the TUI reports a dead pane's).
func TestDaemonMaintenance_NothingOrAClosedPane(t *testing.T) {
	(&Daemon{}).maintain(time.Now())
	logs := captureLogs(t)
	d, p := dbqyHeld(t)
	p.Close()
	d.maintain(time.Now())
	if q := p.QueuedMessageCount(); q != 0 {
		t.Fatalf("a closed pane still holds %d", q)
	}
	if !strings.Contains(logs.String(), "process exited") {
		t.Error("a closed pane's held mail was not reported")
	}
}

// Criterion 5, a census: the per-pane maintenance calls appear in one place,
// maintenance.go, and both hosts reach them only through it.
func TestDaemonMaintenance_OneDefinitionOfMaintenance(t *testing.T) {
	leaves := map[string]bool{
		"refreshWaitingState": true, "refreshTier2WaitingState": true, "updateActivity": true,
		"resolveHeldMail": true, "sweepWithheldSubmitsIn": true,
	}
	allowed := map[string]bool{"maintainPaneFrame": true, "maintainModal": true, "sweepWithheldSubmits": true}
	calls := map[string]map[string]bool{} // function -> callees of interest
	fset := token.NewFileSet()
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range af.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch c := call.Fun.(type) {
				case *ast.SelectorExpr:
					name = c.Sel.Name
				case *ast.Ident:
					name = c.Name
				}
				if calls[fn.Name.Name] == nil {
					calls[fn.Name.Name] = map[string]bool{}
				}
				calls[fn.Name.Name][name] = true
				if leaves[name] && !allowed[fn.Name.Name] {
					t.Errorf("%s calls %s outside maintenance.go: a second definition of maintenance", fn.Name.Name, name)
				}
				return true
			})
		}
	}
	if !calls["renderFrame"]["maintainPaneFrame"] || !calls["modalMaintenance"]["maintainModal"] {
		t.Error("the TUI no longer reaches maintenance through maintainPaneFrame and maintainModal")
	}
	if !calls["maintain"]["maintainPaneFrame"] || !calls["maintain"]["maintainModal"] {
		t.Error("the daemon's tick does not run maintainPaneFrame and maintainModal")
	}
	if !calls["RunDaemon"]["runMaintenance"] {
		t.Error("RunDaemon does not start the maintenance ticker")
	}
}

// The tick runs on its own goroutine beside the daemon's others: sends from
// IPC handlers, agent output, and the status and handshake reads. On the TUI
// all of this ran on one goroutine, so this is what -race must see together.
func TestDaemonMaintenance_TickBesideSendsOutputAndReads(t *testing.T) {
	p := daemonPane(t)
	d := &Daemon{panes: []*Pane{p}}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	run := func(f func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				f(i)
			}
		}()
	}
	base := time.Now()
	run(func(i int) { d.maintain(base.Add(time.Duration(i) * 300 * time.Millisecond)) })
	run(func(i int) {
		if i%50 == 0 {
			p.latchDialogOpen("Claude needs your permission")
		}
		p.SendText("[from super] x", true)
	})
	run(func(int) { // readLoop's steps for one chunk of output
		p.mu.Lock()
		p.lastOutputTime = time.Now()
		p.mu.Unlock()
		p.renderMu.Lock()
		p.emu.Write([]byte("tick\r\n"))
		p.renderMu.Unlock()
		p.maybeDrainModalQueue()
		p.maybeRetryWithheldSubmit()
	})
	run(func(int) { _ = p.Activity(); _ = waitingStateOf(p); _ = p.QueuedMessageCount() })
	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}
