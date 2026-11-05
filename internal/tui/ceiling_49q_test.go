package tui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/machine"
)

// The machine ceiling (ini-49q): every start and wake asks one gate, and at
// the ceiling it is refused with the words, in the pane and in status; a
// send to a suspended agent is held and delivered once the ceiling clears;
// nothing running is ever stopped.

// fakeMachine stands in for every machine read a gate step makes.
type fakeMachine struct {
	mu      sync.Mutex
	dir     string
	ps      string
	psErr   error
	freeKB  int64
	totalKB int64
	swapKB  int64
	memErr  error
	load    float64
	ldErr   error
}

func (f *fakeMachine) set(load float64) {
	f.mu.Lock()
	f.load = load
	f.mu.Unlock()
}

func (f *fakeMachine) reads() *ceilingReads {
	return &ceilingReads{
		dir: func() (string, error) { return f.dir, nil },
		procTable: func() (string, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.ps, f.psErr
		},
		freeKB: func() (int64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.freeKB, f.memErr
		},
		totalKB: func() (int64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.totalKB == 0 {
				return 128 * 1024 * 1024, nil // the default holds at its 8 GB cap
			}
			return f.totalKB, nil
		},
		swapKB: func() (int64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.swapKB, nil
		},
		load5: func() (float64, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.load, f.ldErr
		},
		alive: func(int) bool { return true },
	}
}

// ceilingAt is a fake machine at load: 20 GB free, no other rigs, the
// default ceiling (30 / 8 GB / 30).
func ceilingAt(t *testing.T, load float64) *fakeMachine {
	t.Helper()
	return &fakeMachine{dir: t.TempDir(), freeKB: 20 * 1024 * 1024, load: load}
}

// ceilingTUI is a test TUI whose ceiling reads fm, stepped once at now.
func ceilingTUI(t *testing.T, fm *fakeMachine, now time.Time, panes ...*Pane) *TUI {
	t.Helper()
	tui := newTestTUI(panes...)
	tui.agentEvents = make(chan AgentEvent, 64)
	tui.ceiling.reads = fm.reads()
	tui.stepCeiling(nil, now)
	return tui
}

// capturePty is a pipe the pane reads its first output from, recording
// every byte the pane writes to its agent.
type capturePty struct {
	*spawnPty
	mu  sync.Mutex
	got bytes.Buffer
}

func (c *capturePty) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got.Write(b)
}

func (c *capturePty) written() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.got.String()
}

// captureSpawner is fakePaneSpawner with a capturePty, so a test can read
// what was delivered to the woken agent.
func captureSpawner(t *testing.T, calls *atomic.Int32, made *[]*capturePty, mu *sync.Mutex) func(PaneConfig, int, int) (*Pane, error) {
	t.Helper()
	return func(cfg PaneConfig, rows, cols int) (*Pane, error) {
		cp := &capturePty{spawnPty: newSpawnPty(t)} // in memory (ini-t0y1)
		p := NewParkedPane(cfg, rows, cols)
		p.ptmx = cp
		p.alive = true
		p.suspended = false
		p.activity = StateIdle
		calls.Add(1)
		mu.Lock()
		*made = append(*made, cp)
		mu.Unlock()
		return p, nil
	}
}

const ceilingWordsAtLoad99 = "not started: the Mac is at its ceiling: 0 awake sessions, 20.0 GB free, load 99; ceiling 30 / 8 GB / 30"

// AC1: what counts as awake.
func TestCeiling_AwakeCountsEveryLiveClaudeStateAndNothingElse(t *testing.T) {
	// pid 100..104 run claude (directly or under a wrapper); 105 is a shell
	// with no claude under it.
	ps := strings.Join([]string{
		"100 1 01:00 claude",
		"101 1 01:00 sh", "201 101 01:00 claude", // claude under a wrapper
		"102 1 01:00 claude",
		"103 1 01:00 claude",
		"104 1 01:00 claude",
		"105 1 01:00 zsh",
		"106 1 01:00 claude", // a suspended pane's leftover would not be counted
		"107 1 01:00 claude",
	}, "\n")
	tree := parseProcTable(ps, time.Now())
	panes := []paneSighting{
		{pid: 100, alive: true},                  // running
		{pid: 101, alive: true},                  // background
		{pid: 102, alive: true},                  // idle
		{pid: 103, alive: true},                  // waiting on a dialog
		{pid: 104, alive: true},                  // pending input
		{pid: 105, alive: true},                  // a shell: not Claude
		{pid: 106, alive: true, suspended: true}, // suspended
		{pid: 107, alive: false},                 // dead
		{pid: 0, alive: true},                    // a pane with no process
	}
	if got := countAwake(tree, panes); got != 5 {
		t.Fatalf("awake = %d, want 5 (the five live Claude panes; not the shell, the suspended, the dead or the empty)", got)
	}
}

// AC1 through the step: window 1 writes its own count (0600) and adds every
// other live, fresh rig's file; a stale file is ignored.
func TestCeiling_TheStepSumsTheOtherRigsAndWritesItsOwn(t *testing.T) {
	fm := ceilingAt(t, 5)
	fm.ps = "500 1 01:00 claude\n"
	now := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
	for _, f := range []machine.FleetFile{
		{Rig: "hover", PID: 2, Time: now.Add(-time.Minute), Awake: 7},
		{Rig: "old", PID: 3, Time: now.Add(-machine.StaleAfter), Awake: 40},
	} {
		if err := machine.WriteFleet(fm.dir, f); err != nil {
			t.Fatal(err)
		}
	}
	p := testPane("eng1")
	p.pid = 500
	tui := newTestTUI(p)
	tui.ceiling.reads = fm.reads()
	tui.windowID = WindowOne
	tui.stepCeiling(tui.localSightings(), now)

	r, _, engaged, _ := tui.ceiling.gate.Snapshot()
	if !r.AwakeOK || r.Awake != 8 || engaged {
		t.Fatalf("reading = %+v engaged=%v, want 8 awake (1 here + hover 7; old is stale), not engaged", r, engaged)
	}
	fi, err := os.Stat(filepath.Join(fm.dir, machine.FleetDir, "initech.json"))
	if err != nil {
		t.Fatalf("window 1 wrote no fleet file: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("fleet file mode %o, want 600", fi.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(fm.dir, machine.FleetDir, "initech.json"))
	var own machine.FleetFile
	if json.Unmarshal(data, &own) != nil || own.Awake != 1 || own.PID != os.Getpid() {
		t.Fatalf("own fleet file = %s, want awake 1 and this pid", data)
	}
}

// AC5: written once a minute from the tick, never more often.
func TestCeiling_TheTickStepsOnceAMinute(t *testing.T) {
	fm := ceilingAt(t, 5)
	tui := newTestTUI()
	tui.ceiling.reads = fm.reads()
	var steps atomic.Int32
	tui.ceiling.reads.load5 = func() (float64, error) { steps.Add(1); return 5, nil }
	t0 := time.Now()
	for _, d := range []time.Duration{0, time.Second, 30 * time.Second, 59 * time.Second, time.Minute, 90 * time.Second, 2 * time.Minute} {
		tui.refreshMachineCeiling(t0.Add(d))
		deadline := time.Now().Add(5 * time.Second)
		for tui.ceiling.busy.Load() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	if n := steps.Load(); n != 3 {
		t.Fatalf("%d steps over two minutes of ticks, want 3 (at 0, 1:00, 2:00)", n)
	}
}

func ipcCall(t *testing.T, handle func(net.Conn)) IPCResponse {
	t.Helper()
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan struct{})
	go func() { defer close(done); handle(server) }()
	sc := bufio.NewScanner(client)
	sc.Scan()
	var resp IPCResponse
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("reply %q: %v", sc.Text(), err)
	}
	<-done
	return resp
}

// AC2: resume at the ceiling is refused with the words; nothing spawns, the
// pane stays parked, and the pane and status carry the words.
func TestCeiling_ResumeIsRefusedWithTheWords(t *testing.T) {
	parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	tui := ceilingTUI(t, ceilingAt(t, 99), time.Now(), parked)
	var spawned atomic.Int32
	tui.paneSpawner = fakePaneSpawner(t, &spawned)

	resp := ipcCall(t, func(c net.Conn) { tui.handleIPCResume(c, IPCRequest{Target: "eng1"}) })
	if resp.OK || resp.Error != ceilingWordsAtLoad99 {
		t.Fatalf("resume at the ceiling = %+v, want refused with %q", resp, ceilingWordsAtLoad99)
	}
	if spawned.Load() != 0 || tui.panes[0] != parked || !parked.IsSuspended() {
		t.Fatal("a refused resume spawned or swapped the pane")
	}
	if parked.ceilingNoteText() != ceilingWordsAtLoad99 {
		t.Fatalf("pane note = %q", parked.ceilingNoteText())
	}
	infos, _, _ := tui.allPanesWithViews()
	if infos[0].Ceiling != ceilingWordsAtLoad99 {
		t.Fatalf("status pane info ceiling = %q, want the words", infos[0].Ceiling)
	}
}

// AC2's twin: under the ceiling resume behaves as today.
func TestCeiling_UnderTheCeilingResumeWakesAsBefore(t *testing.T) {
	parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	tui := ceilingTUI(t, ceilingAt(t, 5), time.Now(), parked)
	var spawned atomic.Int32
	tui.paneSpawner = fakePaneSpawner(t, &spawned)

	resp := ipcCall(t, func(c net.Conn) { tui.handleIPCResume(c, IPCRequest{Target: "eng1"}) })
	if !resp.OK || spawned.Load() != 1 {
		t.Fatalf("resume under the ceiling = %+v spawned=%d, want woken", resp, spawned.Load())
	}
	np := tui.panes[0].(*Pane)
	t.Cleanup(func() { np.Close() })
	if np.ceilingNoteText() != "" {
		t.Fatalf("a woken pane carries a ceiling note %q", np.ceilingNoteText())
	}
}

// AC2: start of a stopped agent at the ceiling is refused with the words.
func TestCeiling_StartIsRefusedWithTheWords(t *testing.T) {
	dead := testPane("eng1")
	dead.alive = false
	tui := ceilingTUI(t, ceilingAt(t, 99), time.Now(), dead)
	resp := ipcCall(t, func(c net.Conn) { tui.handleIPCStart(c, IPCRequest{Target: "eng1"}) })
	if resp.OK || resp.Error != ceilingWordsAtLoad99 {
		t.Fatalf("start at the ceiling = %+v", resp)
	}
	if tui.panes[0] != dead || dead.ceilingNoteText() != ceilingWordsAtLoad99 {
		t.Fatal("a refused start replaced the pane or left no words on it")
	}
}

// AC2 and AC4: restart at the ceiling is refused before the running agent
// is touched: it is not closed, not marked closing, its mail stays.
func TestCeiling_RestartIsRefusedAndTheRunningAgentIsUntouched(t *testing.T) {
	running := testPane("eng1")
	running.EnqueueMessage("waiting mail", true)
	tui := ceilingTUI(t, ceilingAt(t, 99), time.Now(), running)
	resp := ipcCall(t, func(c net.Conn) { tui.handleIPCRestart(c, IPCRequest{Target: "eng1"}) })
	if resp.OK || resp.Error != ceilingWordsAtLoad99 {
		t.Fatalf("restart at the ceiling = %+v", resp)
	}
	if tui.panes[0] != running || !running.IsAlive() || running.isClosing() || running.QueueLen() != 1 {
		t.Fatalf("a refused restart touched the running agent: alive=%v closing=%v queue=%d",
			running.IsAlive(), running.isClosing(), running.QueueLen())
	}
	// The words are not drawn over a running agent.
	infos, _, _ := tui.allPanesWithViews()
	if infos[0].Ceiling != "" {
		t.Fatalf("a running agent shows a ceiling refusal in status: %q", infos[0].Ceiling)
	}
}

// AC2: add at the ceiling is refused with the words and makes no pane.
func TestCeiling_AddIsRefusedWithTheWords(t *testing.T) {
	tui := ceilingTUI(t, ceilingAt(t, 99), time.Now())
	dir := t.TempDir()
	tui.paneConfigBuilder = func(name string) (PaneConfig, error) {
		return PaneConfig{Name: name, Dir: dir, Command: []string{"sh", "-c", "sleep 5"}}, nil
	}
	err := tui.addPane("eng9")
	if err == nil || err.Error() != ceilingWordsAtLoad99 {
		t.Fatalf("add at the ceiling = %v", err)
	}
	if len(tui.panes) != 0 {
		t.Fatal("a refused add made a pane")
	}
}

// AC2: the boot's timed starts, the keystroke and the grid wake all go
// through resumePane; at the ceiling none of them spawns.
func TestCeiling_EveryWakePathIsRefusedAtTheCeiling(t *testing.T) {
	for _, trigger := range []string{"startup stagger", "keystroke", "agents grid", "queued message"} {
		parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
		tui := ceilingTUI(t, ceilingAt(t, 99), time.Now(), parked)
		var spawned atomic.Int32
		tui.paneSpawner = fakePaneSpawner(t, &spawned)
		err := tui.resumePane(parked, trigger)
		var e *ErrAtCeiling
		if !errors.As(err, &e) || e.Words != ceilingWordsAtLoad99 {
			t.Errorf("%s: err = %v, want the ceiling refusal", trigger, err)
		}
		if spawned.Load() != 0 || !parked.IsSuspended() {
			t.Errorf("%s: the wake spawned at the ceiling", trigger)
		}
	}
}

// AC3: a send to a suspended agent at the ceiling is deferred with the
// words, held on the queue, and delivered exactly once after the ceiling
// has been under for a full minute and the agent wakes.
func TestCeiling_ASendIsHeldAndDeliveredOnceAfterTheCeilingClears(t *testing.T) {
	if testing.Short() {
		t.Skip("delivers through the real send path, which waits on the pane's quiet")
	}
	sendBubble(t, func(t *testing.T) {
		fm := ceilingAt(t, 99)
		t0 := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
		parked := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
		tui := ceilingTUI(t, fm, t0, parked)
		var spawned atomic.Int32
		var made []*capturePty
		var mu sync.Mutex
		tui.paneSpawner = captureSpawner(t, &spawned, &made, &mu)

		resp := ipcCall(t, func(c net.Conn) {
			tui.handleIPCSend(c, IPCRequest{Target: "eng1", Text: "held-ceiling-message", Enter: true})
		})
		if !resp.OK || resp.Delivery != DeliveryDeferred || resp.Data != CeilingDeferral {
			t.Fatalf("send at the ceiling = %+v, want deferred with %q", resp, CeilingDeferral)
		}
		if parked.QueueLen() != 1 || spawned.Load() != 0 {
			t.Fatalf("held send: queue %d spawned %d, want 1 queued, nothing spawned", parked.QueueLen(), spawned.Load())
		}

		// Under for less than a minute: still held.
		fm.set(5)
		if _, cleared := tui.stepCeiling(nil, t0.Add(time.Minute)); cleared {
			t.Fatal("the first reading under cleared the ceiling")
		}
		if _, cleared := tui.stepCeiling(nil, t0.Add(time.Minute+59*time.Second)); cleared {
			t.Fatal("59 seconds under cleared the ceiling")
		}
		if parked.QueueLen() != 1 || spawned.Load() != 0 {
			t.Fatal("the held mail moved before the ceiling cleared")
		}
		// The minute tick clears it and wakes the held mail on its own.
		waitForAsyncWake(t, tui, func() { tui.refreshMachineCeiling(t0.Add(2 * time.Minute)) }, 20*time.Second)
		if tui.ceiling.gate.Engaged() {
			t.Fatal("a full minute under did not clear the ceiling")
		}

		if spawned.Load() != 1 {
			t.Fatalf("the clearing woke %d pane(s), want 1", spawned.Load())
		}
		np := tui.panes[0].(*Pane)
		t.Cleanup(func() { np.Close() })
		mu.Lock()
		got := made[0].written()
		mu.Unlock()
		if n := strings.Count(got, "held-ceiling-message"); n != 1 {
			t.Fatalf("the held message was delivered %d times, want once; wrote %q", n, got)
		}
		// A second clearing finds nothing held and delivers nothing again.
		waitForAsyncWake(t, tui, tui.wakeCeilingHeld, 5*time.Second)
		if spawned.Load() != 1 || strings.Count(made[0].written(), "held-ceiling-message") != 1 {
			t.Fatal("a second clearing delivered the message again")
		}
	})
}

// AC3, the fast half for the -short suite: the held mail is marked held and
// the clearing selects exactly it.
func TestCeiling_TheHeldMailIsMarkedAndOnlyItIsWoken(t *testing.T) {
	held := NewParkedPane(PaneConfig{Name: "eng1"}, 10, 40)
	quiet := NewParkedPane(PaneConfig{Name: "eng2"}, 10, 40) // refused, no mail
	tui := ceilingTUI(t, ceilingAt(t, 99), time.Now(), held, quiet)
	var spawned atomic.Int32
	tui.paneSpawner = fakePaneSpawner(t, &spawned)

	resp := ipcCall(t, func(c net.Conn) {
		tui.handleIPCSend(c, IPCRequest{Target: "eng1", Text: "x", Enter: true})
	})
	if resp.Delivery != DeliveryDeferred || resp.Data != CeilingDeferral {
		t.Fatalf("send at the ceiling = %+v", resp)
	}
	_ = tui.resumePane(quiet, "keystroke")
	if !held.ceilingHeldMail() {
		t.Fatal("the held send's pane is not marked as holding ceiling mail")
	}
	if quiet.ceilingHeldMail() {
		t.Fatal("a refused pane with no mail is marked as holding mail")
	}
	if held.QueueLen() != 1 {
		t.Fatalf("held queue = %d, want 1: the ceiling dropped the message", held.QueueLen())
	}
}

// Feed it nothing: no machine folder, no process table, no memory, no load:
// the ceiling does not engage, the unreadable numbers are logged once, and
// a start goes through.
func TestCeiling_NothingReadableNeverEngagesIt(t *testing.T) {
	bad := errors.New("unreadable")
	tui := newTestTUI()
	tui.ceiling.reads = &ceilingReads{
		dir:       func() (string, error) { return "", bad },
		procTable: func() (string, error) { return "", bad },
		freeKB:    func() (int64, error) { return 0, bad },
		totalKB:   func() (int64, error) { return 0, bad },
		swapKB:    func() (int64, error) { return 0, bad },
		load5:     func() (float64, error) { return 0, bad },
		alive:     func(int) bool { return true },
	}
	tui.stepCeiling(nil, time.Now())
	tui.stepCeiling(nil, time.Now().Add(time.Minute))
	if tui.ceiling.gate.Engaged() {
		t.Fatal("unreadable numbers engaged the ceiling")
	}
	if err := tui.admitAgent(nil, "eng1", "start"); err != nil {
		t.Fatalf("a start with nothing readable was refused: %v", err)
	}
	tui.ceiling.mu.Lock()
	logged := len(tui.ceiling.logged)
	tui.ceiling.mu.Unlock()
	if logged != 6 {
		t.Fatalf("%d unreadable numbers recorded as logged, want 6 (folder, process table, total, free, swap, load)", logged)
	}
}

// Feed it nothing: an empty fleet folder is this rig alone.
func TestCeiling_OneRigAloneCountsOnlyItself(t *testing.T) {
	fm := ceilingAt(t, 5)
	fm.dir = filepath.Join(t.TempDir(), "never-made")
	fm.ps = "500 1 01:00 claude\n"
	p := testPane("eng1")
	p.pid = 500
	tui := newTestTUI(p)
	tui.ceiling.reads = fm.reads()
	tui.stepCeiling(tui.localSightings(), time.Now())
	if r, _, _, _ := tui.ceiling.gate.Snapshot(); !r.AwakeOK || r.Awake != 1 {
		t.Fatalf("reading = %+v, want 1 awake", r)
	}
}

// The pane says why: the words are drawn in a refused pane's body.
func TestCeiling_TheRefusedPaneShowsTheWords(t *testing.T) {
	tui, screen := newTestTUIWithScreen("eng1")
	p := tui.panes[0].(*Pane)
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
	p.setCeilingNote("not started: the Mac is at its ceiling")
	tui.applyLayout()
	p.Render(screen, false, false, 1, Selection{})
	screen.Show()
	cells, w, h := screen.GetContents()
	var text strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if r := cells[y*w+x].Runes; len(r) > 0 {
				text.WriteRune(r[0])
			}
		}
	}
	if !strings.Contains(text.String(), "at its ceiling") {
		t.Fatal("the refused pane's screen does not carry the words")
	}
}

// Clearing: a pane refused with no mail stops showing the words once the
// Mac is under, and is not started again.
func TestCeiling_ClearingWipesTheWordsAndStartsNothing(t *testing.T) {
	fm := ceilingAt(t, 99)
	t0 := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
	quiet := NewParkedPane(PaneConfig{Name: "eng2"}, 10, 40)
	tui := ceilingTUI(t, fm, t0, quiet)
	var spawned atomic.Int32
	tui.paneSpawner = fakePaneSpawner(t, &spawned)
	_ = tui.resumePane(quiet, "keystroke")
	if quiet.ceilingNoteText() == "" {
		t.Fatal("the refused pane carries no words")
	}
	fm.set(5)
	tui.stepCeiling(nil, t0.Add(time.Minute))
	waitForAsyncWake(t, tui, func() { tui.refreshMachineCeiling(t0.Add(2 * time.Minute)) }, 5*time.Second)
	if quiet.ceilingNoteText() != "" {
		t.Fatalf("after the ceiling cleared the pane still says %q", quiet.ceilingNoteText())
	}
	if spawned.Load() != 0 || !quiet.IsSuspended() {
		t.Fatal("the clearing started a refused agent: a refused start is not retried")
	}
}

// The small machine: with no machine.yaml the free memory limit is 20% of
// total, named with its source, and 4 GB reclaimable on 16 GB is under it.
func TestCeiling_TheStepUsesTheDefaultForThisMachinesMemory(t *testing.T) {
	fm := ceilingAt(t, 5)
	fm.totalKB, fm.freeKB = 16*1024*1024, 4*1024*1024
	tui := newTestTUI()
	tui.ceiling.reads = fm.reads()
	if engaged, _ := tui.stepCeiling(nil, time.Now()); engaged {
		t.Fatal("4 GB reclaimable on a 16 GB machine engaged the ceiling: its limit is 3.2 GB")
	}
	if got := tui.FleetSnapshot().Line; !strings.Contains(got, "ceiling 30 / 3.2 GB (20% of 16 GB) / 30") {
		t.Fatalf("fleet line %q does not name the value and its source", got)
	}
	fm.freeKB = 3 * 1024 * 1024
	if engaged, _ := tui.stepCeiling(nil, time.Now()); !engaged {
		t.Fatal("3 GB reclaimable on a 16 GB machine did not engage the ceiling")
	}
}

// Swap is information: any amount leaves the gate alone, and it is on the
// fleet line and in the JSON fields.
func TestCeiling_SwapIsShownAndNeverEngagesIt(t *testing.T) {
	fm := ceilingAt(t, 5)
	fm.swapKB = 500 * 1024 * 1024
	tui := newTestTUI()
	tui.ceiling.reads = fm.reads()
	if engaged, _ := tui.stepCeiling(nil, time.Now()); engaged {
		t.Fatal("swap engaged the ceiling")
	}
	f := tui.FleetSnapshot()
	if !strings.Contains(f.Line, "swap 500.0 GB") || f.SwapGB == nil || *f.SwapGB != 500 {
		t.Fatalf("fleet = line %q swap %v", f.Line, f.SwapGB)
	}
}
