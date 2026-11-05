package tui

// machine_ceiling.go is the machine ceiling in the TUI: every window reads
// the whole Mac once a minute (the awake Claude sessions of every rig, free
// memory, the 5-minute load) and steps one gate; window 1 also writes this
// rig's awake count for the other rigs to read. Every start and wake of an
// agent asks the gate first, through admitAgent, and at the ceiling it is
// refused with the words, in the pane and in status. The gate never stops,
// suspends or signals anything: a refusal only leaves an agent unstarted.
//
// A send to a suspended agent at the ceiling is held on the pane's own
// queue, the sender is told it was deferred, and when the gate clears the
// panes holding such mail are woken one at a time through resumePane, which
// drains each queue once.

import (
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/machine"
	"github.com/nmelo/initech/internal/rignumbers"
)

// osGetpid is this process's pid, for its fleet file.
var osGetpid = os.Getpid

// ceilingReads are the machine reads a gate step makes. Package defaults in
// production; tests stand in for every one.
type ceilingReads struct {
	dir       func() (string, error)  // the machine folder (machine.Dir)
	procTable func() (string, error)  // ps output, for the awake count
	freeKB    func() (int64, error)   // reclaimable memory in KB
	totalKB   func() (int64, error)   // total memory in KB
	swapKB    func() (int64, error)   // swap in use in KB
	load5     func() (float64, error) // the 5-minute load
	alive     func(pid int) bool      // whether another rig's window 1 runs
}

// ceilingDefaults makes the reads a TUI uses when none were set. The
// package's TestMain points it at reads that cannot see this machine, so no
// test writes the operator's fleet folder or meets this machine's ceiling.
var ceilingDefaults = defaultCeilingReads

func defaultCeilingReads() ceilingReads {
	return ceilingReads{
		dir:       machine.Dir,
		procTable: readProcTable,
		freeKB:    machine.ReclaimableKB,
		totalKB:   machine.TotalKB,
		swapKB:    machine.SwapUsedKB,
		load5:     machine.Load5,
		alive:     machine.PIDAlive,
	}
}

// ceilingState is the TUI's machine ceiling.
type ceilingState struct {
	gate machine.Gate
	last time.Time   // main loop only: when the last step started
	busy atomic.Bool // a step is running; a second never starts beside it

	mu      sync.Mutex
	reads   *ceilingReads   // nil until first use: the defaults
	ceiling machine.Ceiling // the last ceiling machine.yaml gave
	loaded  bool            // ceiling holds a good load
	logged  map[string]bool // what was unreadable and already logged once
	waking  atomic.Bool     // a wake of held mail is running
}

// ErrAtCeiling is a start or wake the machine ceiling refused. Words is the
// line the pane and status show.
type ErrAtCeiling struct {
	Agent string
	Words string
}

func (e *ErrAtCeiling) Error() string { return e.Words }

// isAtCeiling reports whether err is a ceiling refusal.
func isAtCeiling(err error) bool {
	var e *ErrAtCeiling
	return errors.As(err, &e)
}

// kbToGB converts a memory figure in KB to GB.
func kbToGB(kb int64) float64 { return float64(kb) / (1024 * 1024) }

// orNoReading gives a read that returned no error and no figure an error.
func orNoReading(err error) error {
	if err != nil {
		return err
	}
	return errors.New("no figure reported")
}

// paneSighting is what the awake count needs of a pane, read on main.
type paneSighting struct {
	pid              int
	alive, suspended bool
}

// countAwake is how many sightings are awake: alive, not suspended, with a
// claude process in their tree. A dead pane, a parked one, an empty one
// (pid 0) and a shell with no claude under it do not count.
func countAwake(tree *procTree, panes []paneSighting) int {
	n := 0
	for _, s := range panes {
		if s.alive && !s.suspended && s.pid > 0 && tree.hasClaude(s.pid) {
			n++
		}
	}
	return n
}

// ceilingReadsLocked returns the reads, defaulting them on first use.
func (c *ceilingState) readsLocked() ceilingReads {
	if c.reads == nil {
		r := ceilingDefaults()
		c.reads = &r
	}
	return *c.reads
}

// noteUnreadable logs a number the step could not read, once until it
// reads again.
func (c *ceilingState) noteUnreadable(what string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.logged == nil {
		c.logged = map[string]bool{}
	}
	if err == nil {
		delete(c.logged, what)
		return
	}
	if c.logged[what] {
		return
	}
	c.logged[what] = true
	LogWarn("ceiling", "a machine number is unreadable; the ceiling does not engage on it", "number", what, "err", err)
}

// localSightings reads every local pane's pid and state. Main loop only.
func (t *TUI) localSightings() []paneSighting {
	var out []paneSighting
	for _, pv := range t.panes {
		if p, ok := pv.(*Pane); ok {
			out = append(out, paneSighting{pid: p.pid, alive: p.IsAlive(), suspended: p.IsSuspended()})
		}
	}
	return out
}

// rigName is this rig's name for its fleet file.
func (t *TUI) rigName() string {
	if t.project != nil && t.project.Name != "" {
		return t.project.Name
	}
	return "initech"
}

// stepCeiling reads the machine and steps the gate once. It may wait on ps,
// vm_stat and sysctl, so it runs off the main loop except at boot, before
// the first agent starts. sightings were read on main.
func (t *TUI) stepCeiling(sightings []paneSighting, now time.Time) (engaged, cleared bool) {
	c := &t.ceiling
	c.mu.Lock()
	reads := c.readsLocked()
	c.mu.Unlock()

	r := machine.Reading{At: now}
	dir, dirErr := reads.dir()
	c.noteUnreadable("machine folder", dirErr)

	// The ceiling: a bad file keeps the last good ceiling (the defaults
	// before one), and says so once.
	totalGB := 0.0
	if kb, err := reads.totalKB(); err == nil && kb > 0 {
		totalGB = kbToGB(kb)
		c.noteUnreadable("total memory", nil)
	} else {
		c.noteUnreadable("total memory", orNoReading(err))
	}
	ceiling := machine.CeilingFor(totalGB)
	if dirErr == nil {
		got, err := machine.LoadCeiling(dir, totalGB)
		c.noteUnreadable("machine.yaml", err)
		c.mu.Lock()
		if err == nil {
			c.ceiling, c.loaded = got, true
		}
		if c.loaded {
			ceiling = c.ceiling
		}
		c.mu.Unlock()
	}

	// This rig's awake count from the process table, then the other rigs'.
	out, err := reads.procTable()
	c.noteUnreadable("process table", err)
	if err == nil {
		own := countAwake(parseProcTable(out, now), sightings)
		r.Awake, r.AwakeOK = own, true
		if dirErr == nil {
			exclude := ""
			if t.isFleetAuthority() {
				exclude = t.rigName()
				werr := machine.WriteFleet(dir, machine.FleetFile{Rig: exclude, PID: osGetpid(), Time: now, Awake: own})
				c.noteUnreadable("fleet file write", werr)
			} else {
				// A later window's agents are not in window 1's file; its
				// own count stands beside every rig's file.
				exclude = ""
			}
			r.Awake += machine.SumOthers(dir, exclude, now, reads.alive)
		}
	}

	if kb, err := reads.freeKB(); err == nil && kb > 0 {
		r.FreeGB, r.FreeOK = kbToGB(kb), true
		c.noteUnreadable("free memory", nil)
	} else {
		c.noteUnreadable("free memory", orNoReading(err))
	}
	if kb, err := reads.swapKB(); err == nil {
		r.SwapGB, r.SwapOK = kbToGB(kb), true
		c.noteUnreadable("swap", nil)
	} else {
		c.noteUnreadable("swap", err)
	}
	if l, err := reads.load5(); err == nil {
		r.Load, r.LoadOK = l, true
		c.noteUnreadable("load", nil)
	} else {
		c.noteUnreadable("load", err)
	}

	engaged, cleared = c.gate.Step(r, ceiling, now)
	if cleared {
		LogInfo("ceiling", "the machine ceiling cleared", "line", machine.StatusLine(r, ceiling))
	}
	return engaged, cleared
}

// refreshMachineCeiling runs from the main loop's tick: at most once a
// minute and never beside a step still running, it reads the machine off
// the main loop and, when the gate clears, wakes the panes holding mail it
// held.
func (t *TUI) refreshMachineCeiling(now time.Time) {
	c := &t.ceiling
	if !c.last.IsZero() && now.Sub(c.last) < machine.WriteEvery {
		return
	}
	if !c.busy.CompareAndSwap(false, true) {
		return
	}
	c.last = now
	sightings := t.localSightings()
	t.safeGo(func() {
		defer c.busy.Store(false)
		if _, cleared := t.stepCeiling(sightings, now); cleared {
			t.wakeCeilingHeld()
		}
	})
}

// admitAgent asks the gate to start or wake agent by path (start, resume,
// restart, add, boot, the wake a send causes). On a refusal it records the
// words on the pane, when there is one, and returns *ErrAtCeiling; it never
// touches the pane's process.
func (t *TUI) admitAgent(p *Pane, agent, path string) error {
	ok, words := t.ceiling.gate.Admit()
	if ok {
		if p != nil {
			p.setCeilingNote("")
		}
		return nil
	}
	if p != nil {
		p.setCeilingNote(words)
	}
	LogInfo("ceiling", "start refused at the machine ceiling", "agent", agent, "path", path, "words", words)
	return &ErrAtCeiling{Agent: agent, Words: words}
}

// wakeCeilingHeld wakes, one at a time, every suspended pane holding mail
// the ceiling held. resumePane asks the gate again for each, so a wake that
// would reach the ceiling again stops there with its mail still queued.
// A pane refused with no mail stays as it is: a refused start is not
// retried by initech.
func (t *TUI) wakeCeilingHeld() {
	if !t.ceiling.waking.CompareAndSwap(false, true) {
		return
	}
	var held []*Pane
	if !t.runOnMain(func() {
		for _, pv := range t.panes {
			p, ok := pv.(*Pane)
			if !ok {
				continue
			}
			if p.ceilingHeldMail() {
				held = append(held, p)
			} else {
				// The Mac is under now: a pane refused with no mail stops
				// saying it is at the ceiling. It is not started again.
				p.setCeilingNote("")
			}
		}
	}) {
		t.ceiling.waking.Store(false)
		return
	}
	t.safeGo(func() {
		defer t.fireOnWakeComplete()
		defer t.ceiling.waking.Store(false)
		for _, p := range held {
			if err := t.resumePane(p, "ceiling cleared"); err != nil {
				LogInfo("ceiling", "held mail still waits", "agent", p.Name(), "err", err)
				if isAtCeiling(err) {
					return
				}
			}
		}
	})
}

// FleetSnapshot is the gate's last reading as status's fleet fields, or nil
// before the first reading. Safe from any goroutine.
func (t *TUI) FleetSnapshot() *rignumbers.Fleet {
	r, c, engaged, seen := t.ceiling.gate.Snapshot()
	if !seen {
		return nil
	}
	return FleetFromReading(r, c, engaged)
}

// FleetFromReading is status's fleet fields for one reading.
func FleetFromReading(r machine.Reading, c machine.Ceiling, engaged bool) *rignumbers.Fleet {
	f := &rignumbers.Fleet{
		AtCeiling: engaged,
		Ceiling:   rignumbers.FleetCeiling{Sessions: c.Sessions, FreeMemoryGB: c.FreeMemoryGB, FreeSource: c.FreeSource, Load: c.Load},
		Line:      machine.StatusLine(r, c),
	}
	if r.AwakeOK {
		f.Awake = &r.Awake
	}
	if r.FreeOK {
		gb := float64(int(r.FreeGB*10+0.5)) / 10
		f.FreeGB = &gb
	}
	if r.LoadOK {
		f.Load = &r.Load
	}
	if r.SwapOK {
		gb := float64(int(r.SwapGB*10+0.5)) / 10
		f.SwapGB = &gb
	}
	return f
}

// ReadFleetNow reads the machine once, as a status call with no window 1
// does: every rig's file, free memory and load, against machine.yaml. The
// result is not a gate: AtCeiling is whether this one reading is over.
func ReadFleetNow(now time.Time) *rignumbers.Fleet {
	reads := ceilingDefaults()
	r := machine.Reading{At: now}
	totalGB := 0.0
	if kb, err := reads.totalKB(); err == nil && kb > 0 {
		totalGB = kbToGB(kb)
	}
	ceiling := machine.CeilingFor(totalGB)
	if dir, err := reads.dir(); err == nil {
		if c, err := machine.LoadCeiling(dir, totalGB); err == nil {
			ceiling = c
		}
		r.Awake, r.AwakeOK = machine.SumOthers(dir, "", now, reads.alive), true
	}
	if kb, err := reads.freeKB(); err == nil && kb > 0 {
		r.FreeGB, r.FreeOK = kbToGB(kb), true
	}
	if kb, err := reads.swapKB(); err == nil {
		r.SwapGB, r.SwapOK = kbToGB(kb), true
	}
	if l, err := reads.load5(); err == nil {
		r.Load, r.LoadOK = l, true
	}
	return FleetFromReading(r, ceiling, machine.Over(r, ceiling))
}

// ceilingNote is the refusal words a pane shows, or "".
func (p *Pane) ceilingNoteText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ceilingNote
}

// setCeilingNote records (or, with "", clears) the refusal the pane shows.
// A refusal on a suspended pane holding mail marks that mail as held by the
// ceiling, so the gate's clearing wakes it.
func (p *Pane) setCeilingNote(words string) {
	p.mu.Lock()
	changed := p.ceilingNote != words
	p.ceilingNote = words
	if words == "" {
		p.ceilingHeld = false
	} else if p.suspended && len(p.messageQueue) > 0 {
		p.ceilingHeld = true
	}
	p.mu.Unlock()
	if changed {
		p.markFrameChanged() // the note is drawn in the body
	}
}

// drawCeilingNote draws the refusal words over the bottom rows of a pane
// that is not running, wrapped to its width, so the pane says why it did
// not start. Drawn after the frame cache is taken, so the cache keeps only
// the agent's own screen.
func (p *Pane) drawCeilingNote(s *clampedScreen, cr Region, cols, rows int) {
	words := p.ceilingNoteText()
	if words == "" || cols < 1 || rows < 1 || (p.IsAlive() && !p.IsSuspended()) {
		return
	}
	text := []rune(" " + words + " ")
	lines := (len(text) + cols - 1) / cols
	if lines > rows {
		lines = rows
	}
	style := tcell.StyleDefault.Background(tcell.ColorDarkRed).Foreground(tcell.ColorWhite).Bold(true)
	for l := 0; l < lines; l++ {
		y := cr.Y + rows - lines + l
		for col := 0; col < cols; col++ {
			ch := ' '
			if i := l*cols + col; i < len(text) {
				ch = text[i]
			}
			s.SetContent(cr.X+col, y, ch, nil, style)
		}
	}
}

// ceilingHeldMail reports whether the pane is suspended with mail the
// ceiling held.
func (p *Pane) ceilingHeldMail() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ceilingHeld && p.suspended && len(p.messageQueue) > 0
}
