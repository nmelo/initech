package tui

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/schedule"
)

// THE RIG'S OWN SCHEDULE (ini-6ib). Window 1 runs the jobs in
// .initech/schedule.yaml whenever the rig is up: a message, or the output of a
// one-line command, into a pane at a time of day or on an interval. It is not a
// job runner: one line, a fixed 30-second bound and a fixed 2 KB cut, no
// retries, no chaining.
//
// The main goroutine only kicks the scheduler, at most once a second; reading
// the file, claiming due jobs and delivering them all run off it. A due job is
// claimed by writing its last run to the file BEFORE anything is delivered, so
// a restart, even inside the same minute, cannot run it twice.

const (
	scheduleCommandBound = 30 * time.Second
	scheduleOutputCut    = 2 * 1024
	scheduleKickEvery    = time.Second
)

// scheduleHost is what the scheduler needs from window 1: delivery through the
// send path, and one line to super.
type scheduleHost interface {
	// deliver sends text to the pane named to, as from, through the send path.
	// It reports whether the message was taken, and why not.
	deliver(to, text, from string) (ok bool, why string)
	// tellSuper sends super one line from initech. It never fails silently.
	tellSuper(text string)
}

// rigScheduler runs one rig's schedule.
type rigScheduler struct {
	path, root string
	host       scheduleHost
	runLine    func(ctx context.Context, dir, line string) (string, int, error)
	now        func() time.Time
	started    time.Time
	bound      time.Duration
	// isAgent reports whether a name is an agent of this rig; nil accepts any.
	// It may hop onto the main goroutine, so it is never called from it.
	isAgent func(string) bool
	// identify names the agent behind an IPC connection; nil uses the
	// connection's own identity (identifyPostConnection). Tests set it.
	identify func(net.Conn) (string, error)

	busy     atomic.Bool
	lastKick atomic.Int64

	mu       sync.Mutex
	file     *schedule.File // The last good read.
	modTime  time.Time
	loadErr  string // The parse error last reported to super, so it is said once.
	inFlight map[string]bool
	wg       sync.WaitGroup // Deliveries in flight, for tests.
}

func newRigScheduler(root string, host scheduleHost, now func() time.Time) *rigScheduler {
	s := &rigScheduler{
		path:     schedule.Path(root),
		root:     root,
		host:     host,
		runLine:  iexec.RunLine,
		now:      now,
		started:  now(),
		bound:    scheduleCommandBound,
		file:     &schedule.File{},
		inFlight: map[string]bool{},
	}
	// The first read is the first tick's, off the main goroutine, because an
	// owner check may need the main loop running.
	return s
}

// kick runs one tick in the background unless one is running or the last was
// under a second ago. It is the only call the main goroutine makes, and it
// never waits.
func (s *rigScheduler) kick() {
	now := s.now().UnixNano()
	if now-s.lastKick.Load() < int64(scheduleKickEvery) {
		return
	}
	if !s.busy.CompareAndSwap(false, true) {
		return
	}
	s.lastKick.Store(now)
	go func() {
		defer s.busy.Store(false)
		s.tick()
	}()
}

// claim is a due job taken for one run.
type claim struct {
	job    schedule.Job
	due    time.Time
	missed bool
}

// tick reads the file if it changed, claims what is due and starts each run.
func (s *rigScheduler) tick() {
	s.reloadIfChanged()
	now := s.now()
	var claims []claim
	s.mu.Lock()
	for i := range s.file.Jobs {
		j := &s.file.Jobs[i]
		if s.inFlight[j.ID] {
			continue
		}
		due, missed, fire := j.Check(now, s.started)
		if !fire {
			continue
		}
		d := due
		j.LastRun = &d
		j.LastResult = "running"
		s.inFlight[j.ID] = true
		claims = append(claims, claim{job: *j, due: due, missed: missed})
	}
	if len(claims) > 0 {
		s.saveLocked()
	}
	s.mu.Unlock()
	for _, c := range claims {
		s.wg.Add(1)
		go func(c claim) {
			defer s.wg.Done()
			s.fire(c)
		}(c)
	}
}

// fire runs one claimed job and records what became of it.
func (s *rigScheduler) fire(c claim) {
	j := c.job
	text, cmdFailure := s.compose(c)
	ok, why := s.host.deliver(j.To, text, j.Owner)
	result := "delivered"
	switch {
	case !ok:
		result = "not delivered: " + why
		s.host.tellSuper(fmt.Sprintf("schedule %s (owner %s, to %s) did not deliver: %s",
			j.ID, j.Owner, j.To, why))
	case cmdFailure != "":
		result = "delivered; " + cmdFailure
		s.host.tellSuper(fmt.Sprintf("schedule %s (owner %s, to %s): its command %s",
			j.ID, j.Owner, j.To, cmdFailure))
	}
	s.mu.Lock()
	delete(s.inFlight, j.ID)
	for i := range s.file.Jobs {
		if s.file.Jobs[i].ID == j.ID {
			s.file.Jobs[i].LastResult = result
			s.saveLocked()
			break
		}
	}
	s.mu.Unlock()
}

// compose builds the text a run delivers, and says how a command failed.
func (s *rigScheduler) compose(c claim) (text, cmdFailure string) {
	j := c.job
	var b strings.Builder
	fmt.Fprintf(&b, "[from %s, schedule %s] ", j.Owner, j.ID)
	if c.missed {
		fmt.Fprintf(&b, "(due %s while initech was down) ", c.due.Format("2006-01-02 15:04"))
	}
	if j.Command == "" {
		b.WriteString(j.Message)
		return b.String(), ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.bound)
	defer cancel()
	out, code, err := s.runLine(ctx, s.root, j.Command)
	if len(out) > scheduleOutputCut {
		out = out[:scheduleOutputCut] + "\n[output cut at 2 KB]"
	}
	fmt.Fprintf(&b, "$ %s\n", j.Command)
	switch {
	case ctx.Err() != nil:
		cmdFailure = fmt.Sprintf("was stopped after %s", s.bound)
		fmt.Fprintf(&b, "stopped after %s\n", s.bound)
	case err != nil:
		cmdFailure = "did not run: " + err.Error()
		fmt.Fprintf(&b, "did not run: %v\n", err)
	default:
		if code != 0 {
			cmdFailure = fmt.Sprintf("exited %d", code)
		}
		fmt.Fprintf(&b, "exit %d\n", code)
	}
	b.WriteString(strings.TrimRight(out, "\n"))
	return strings.TrimRight(b.String(), "\n"), cmdFailure
}

// reloadIfChanged reads the file when it changed on disk. A file that does not
// parse keeps the last good schedule running and tells super once.
func (s *rigScheduler) reloadIfChanged() {
	fi, err := os.Stat(s.path)
	if err != nil {
		return // No file: the schedule is what we hold (empty at first).
	}
	s.mu.Lock()
	unchanged := fi.ModTime().Equal(s.modTime)
	s.mu.Unlock()
	if unchanged {
		return
	}
	f, err := schedule.Load(s.path)
	if err == nil {
		err = s.checkOwners(f)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modTime = fi.ModTime()
	if err != nil {
		if err.Error() != s.loadErr {
			s.loadErr = err.Error()
			LogWarn("schedule", "schedule.yaml refused; the last good schedule keeps running", "err", err)
			go s.host.tellSuper("schedule.yaml does not parse, so the last good schedule keeps running: " + err.Error())
		}
		return
	}
	s.loadErr = ""
	// A job running now keeps the last run it claimed.
	for i := range f.Jobs {
		for _, old := range s.file.Jobs {
			if old.ID == f.Jobs[i].ID && s.inFlight[old.ID] {
				f.Jobs[i].LastRun, f.Jobs[i].LastResult = old.LastRun, old.LastResult
			}
		}
	}
	s.file = f
}

// checkOwners refuses a file holding a job whose owner is not an agent of this
// rig, naming the line, so a hand edit has to say who it is from. A job already
// held with that owner stays: a job of an agent since removed runs until super
// removes it.
func (s *rigScheduler) checkOwners(f *schedule.File) error {
	if s.isAgent == nil {
		return nil
	}
	s.mu.Lock()
	known := map[string]string{}
	for _, j := range s.file.Jobs {
		known[j.ID] = j.Owner
	}
	s.mu.Unlock()
	for _, j := range f.Jobs {
		if known[j.ID] == j.Owner {
			continue
		}
		if !s.isAgent(j.Owner) {
			return fmt.Errorf("%s: line %d: job %q: owner %q is not an agent of this rig", s.path, j.Line, j.ID, j.Owner)
		}
	}
	return nil
}

// saveLocked writes the schedule. The caller holds mu.
func (s *rigScheduler) saveLocked() {
	if err := schedule.Save(s.path, s.file); err != nil {
		LogWarn("schedule", "could not write schedule.yaml", "err", err)
		return
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.modTime = fi.ModTime()
	}
}

// change applies fn to the current schedule and saves it, for the commands.
func (s *rigScheduler) change(fn func(*schedule.File) error) error {
	s.reloadIfChanged()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != "" {
		return fmt.Errorf("schedule.yaml does not parse, so it is not changed: %s", s.loadErr)
	}
	if err := fn(s.file); err != nil {
		return err
	}
	if err := schedule.Save(s.path, s.file); err != nil {
		return err
	}
	if fi, err := os.Stat(s.path); err == nil {
		s.modTime = fi.ModTime()
	}
	return nil
}

// snapshot is the schedule as listed, owner first, with each job's next run.
func (s *rigScheduler) snapshot() []CronListing {
	s.reloadIfChanged()
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]CronListing, 0, len(s.file.Jobs))
	for _, j := range s.file.Jobs {
		out = append(out, CronListing{Job: j, Next: j.Next(now, s.started)})
	}
	return out
}

// ── window 1 as the host ────────────────────────────────────────────

// deliver sends text to the local pane named to, as from, through the send
// path, so a dialog or do-not-disturb holds it like any other message. A
// suspended pane queues it and wakes.
func (t *TUI) deliver(to, text, from string) (bool, string) {
	var p *Pane
	if !t.runOnMain(func() { p = t.findPane(to) }) {
		return false, "initech is shutting down"
	}
	if p == nil {
		return false, fmt.Sprintf("there is no pane named %q in window 1", to)
	}
	if !p.IsAlive() && !p.IsSuspended() {
		return false, fmt.Sprintf("%s is not running", to)
	}
	v := p.SendTextVerdictFrom(text, true, sendOrigin{From: from})
	if v.Outcome == DeliveryNotDelivered {
		return false, v.Reason
	}
	return true, ""
}

// tellSuper sends super one line from initech, through the shared helper
// (initech_line.go): the send path, with its holds, on its own goroutine.
func (t *TUI) tellSuper(text string) {
	t.sendInitechLine(text)
}
