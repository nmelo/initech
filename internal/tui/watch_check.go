package tui

// watch_check.go is Watches 2B (ini-20r2): the conditions a watch waits on and
// the pass that judges them. A fixed registry of kinds; a watch names a kind
// and its parameters, never a command. The store (internal/watch) keeps the
// watches; this file decides when each one ends, and how.
//
// What each kind reads, and how often:
//
//	bead_status    the bead poll's status and updated_at, every poll (no new bd call)
//	bead_comment   bd comments for that bead, only when its comment_count rose,
//	               one read per bead per pass for every watch on it
//	deadline       nothing; it fires at its deadline
//	send_delivered the send's receipt (Watches 2E), every pass
//	send_held      the send's receipt, every pass
//
// The pass runs after each bead poll and at the nearest deadline.

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/nmelo/initech/internal/progress"
	"github.com/nmelo/initech/internal/watch"
)

// The states a send's receipt can be in (Watches 2E's ReceiptState).
const (
	ReceiptHeld      = "held"
	ReceiptTyped     = "typed"
	ReceiptWithdrawn = "withdrawn"
	ReceiptDropped   = "dropped"
	ReceiptUnknown   = "unknown"
)

// ReceiptState is one held send's receipt as Watches 2E records it: the
// agent it went to, its id, who sent it, why it was held, its state, since
// when it was held and when it ended (typed, withdrawn or dropped). Why is
// the reason initech dropped it.
type ReceiptState struct {
	Pane, ID, From, Kind string
	State                string
	Why                  string
	Since, At            time.Time
}

// watchReceipt reads a receipt for the pass and for set: the receipt book
// Watches 2E keeps (receipts.go). A test may replace it.
var watchReceipt = func(t *TUI, pane, id string) ReceiptState {
	return t.receiptState(pane, id)
}

// World is what a pass reads, gathered once and read-only.
type World interface {
	Now() time.Time
	// BeadsKnown is false until the bead poll has answered once.
	BeadsKnown() bool
	// HasBeads is false in a rig without bd, where no poll ever runs.
	HasBeads() bool
	Bead(id string) (bdBead, bool)
	// BeadsReadAt is when the newest answered poll read the beads.
	BeadsReadAt() time.Time
	Comments(id string) ([]progress.Comment, bool)
	// CommentsReadAt is when a bead's comments were last read; zero if never.
	CommentsReadAt(id string) time.Time
	Receipt(pane, id string) ReceiptState
}

// Verdict is one judgement of a condition. At is the evidence's own time,
// never the pass time. ReadAt is when the data judged was read: a watch ends
// MISSED only on data read at or after its deadline, so an old poll or a pass
// while bd is down can fire a watch but never miss it (lead1, ini-20r2).
// Unknown: the evidence could not be read, so it cannot fire. Never: it can
// never come true, so the watch ends missed at once; TellSuper with it when
// initech, not the owner, caused that.
type Verdict struct {
	True      bool
	At        time.Time
	ReadAt    time.Time
	Detail    string
	Unknown   bool
	Never     bool
	TellSuper bool
}

// Checker is one kind of condition.
type Checker interface {
	Kind() watch.Kind
	// Validate refuses a watch at set, with the line the setter reads.
	Validate(params map[string]string, owner string, w World) error
	// Key names the shared check: watches with one Key and one set time are
	// judged once.
	Key(params map[string]string) string
	Judge(params map[string]string, set time.Time, w World) Verdict
}

// watchCheckers is the fixed registry. Later kinds (idle, outside checks)
// are added here and to World, never as free-form commands.
var watchCheckers = map[watch.Kind]Checker{
	watch.KindBeadStatus:    beadStatusCheck{},
	watch.KindBeadComment:   beadCommentCheck{},
	watch.KindDeadline:      deadlineCheck{},
	watch.KindSendDelivered: sendCheck{kind: watch.KindSendDelivered},
	watch.KindSendHeld:      sendCheck{kind: watch.KindSendHeld},
}

var (
	watchBeadIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	watchStatusRe = regexp.MustCompile(`^[a-z][a-z_]*$`)
)

// needParams refuses a missing or extra parameter, naming it.
func needParams(params map[string]string, names ...string) error {
	for _, n := range names {
		if params[n] == "" {
			return fmt.Errorf("the watch needs %s", n)
		}
	}
	if len(params) > len(names) {
		allowed := map[string]bool{}
		for _, n := range names {
			allowed[n] = true
		}
		for k := range params {
			if !allowed[k] {
				return fmt.Errorf("the watch takes no %s", k)
			}
		}
	}
	return nil
}

// validBead refuses a bead watch in a rig without bd (no poll would ever
// judge it), a malformed id, and once bd has answered, a bead the rig does
// not have.
func validBead(id string, w World) error {
	if !w.HasBeads() {
		return fmt.Errorf("this rig has no beads")
	}
	if !watchBeadIDRe.MatchString(id) {
		return fmt.Errorf("%q is not a bead id", id)
	}
	if w.BeadsKnown() {
		if _, ok := w.Bead(id); !ok {
			return fmt.Errorf("no bead %s in this rig", id)
		}
	}
	return nil
}

// bead_status: the bead reaches a named status.
type beadStatusCheck struct{}

func (beadStatusCheck) Kind() watch.Kind { return watch.KindBeadStatus }

func (beadStatusCheck) Validate(p map[string]string, _ string, w World) error {
	if err := needParams(p, "bead", "status"); err != nil {
		return err
	}
	if !watchStatusRe.MatchString(p["status"]) {
		return fmt.Errorf("%q is not a bead status", p["status"])
	}
	if err := validBead(p["bead"], w); err != nil {
		return err
	}
	// pm, ini-20r2 gap 1: the agent already knows the answer.
	if b, ok := w.Bead(p["bead"]); ok && b.Status == p["status"] {
		return fmt.Errorf("%s is already %s", p["bead"], p["status"])
	}
	return nil
}

func (beadStatusCheck) Key(p map[string]string) string {
	return "bead_status|" + p["bead"] + "|" + p["status"]
}

// The evidence time is closed_at for closed, exact; for any other status it
// is updated_at, which any later edit moves. Every answered poll judges every
// watch, so a status seen by a poll before the deadline fires then; the limit
// is a status no poll saw before the deadline (window 1 or bd down) and an
// edit after it: that watch ends MISSED though the bead reached it in time,
// since bd keeps no status-change time.
func (beadStatusCheck) Judge(p map[string]string, _ time.Time, w World) Verdict {
	var read time.Time
	if w.BeadsKnown() {
		read = w.BeadsReadAt()
	}
	b, ok := w.Bead(p["bead"])
	if !ok {
		return Verdict{Unknown: true, ReadAt: read}
	}
	if b.Status != p["status"] {
		return Verdict{ReadAt: read}
	}
	at := b.UpdatedAt
	if b.Status == "closed" && !b.ClosedAt.IsZero() {
		at = b.ClosedAt
	}
	if at.IsZero() {
		at = read
	}
	return Verdict{True: true, At: at, ReadAt: read, Detail: p["bead"] + " reached " + p["status"]}
}

// bead_comment: a comment on the bead by a named agent, after the watch was set.
type beadCommentCheck struct{}

func (beadCommentCheck) Kind() watch.Kind { return watch.KindBeadComment }

func (beadCommentCheck) Validate(p map[string]string, owner string, w World) error {
	if err := needParams(p, "bead", "author"); err != nil {
		return err
	}
	// pm, ini-20r2 gap 2: the agent controls that condition.
	if p["author"] == owner {
		return fmt.Errorf("a watch cannot wait for your own comment")
	}
	return validBead(p["bead"], w)
}

func (beadCommentCheck) Key(p map[string]string) string {
	return "bead_comment|" + p["bead"] + "|" + p["author"]
}

func (beadCommentCheck) Judge(p map[string]string, set time.Time, w World) Verdict {
	cs, ok := w.Comments(p["bead"])
	read := w.CommentsReadAt(p["bead"])
	if !ok {
		return Verdict{Unknown: true, ReadAt: read}
	}
	var first time.Time
	for _, c := range cs {
		if c.Author == p["author"] && c.CreatedAt.After(set) && (first.IsZero() || c.CreatedAt.Before(first)) {
			first = c.CreatedAt
		}
	}
	if first.IsZero() {
		return Verdict{ReadAt: read}
	}
	return Verdict{True: true, At: first, ReadAt: read, Detail: p["author"] + " commented on " + p["bead"]}
}

// deadline: nothing but the time; the pass fires it at its deadline.
type deadlineCheck struct{}

func (deadlineCheck) Kind() watch.Kind { return watch.KindDeadline }

func (deadlineCheck) Validate(p map[string]string, _ string, _ World) error {
	return needParams(p)
}

func (deadlineCheck) Key(map[string]string) string { return "deadline" }

func (deadlineCheck) Judge(map[string]string, time.Time, World) Verdict { return Verdict{} }

// send_delivered and send_held: the owner's own held send to another agent.
type sendCheck struct{ kind watch.Kind }

func (c sendCheck) Kind() watch.Kind { return c.kind }

func (c sendCheck) names() []string {
	if c.kind == watch.KindSendHeld {
		return []string{"pane", "receipt", "for"}
	}
	return []string{"pane", "receipt"}
}

func (c sendCheck) Validate(p map[string]string, owner string, w World) error {
	if err := needParams(p, c.names()...); err != nil {
		return err
	}
	// S2's self-firing refusal: a watch line goes to the owner, so a watch on
	// a send to itself would be triggered by its own delivery.
	if p["pane"] == owner {
		return fmt.Errorf("a watch cannot wait on a send to yourself")
	}
	if c.kind == watch.KindSendHeld {
		d, err := time.ParseDuration(p["for"])
		if err != nil || d <= 0 {
			return fmt.Errorf("for must be a length of time, such as 15m, not %q", p["for"])
		}
	}
	r := w.Receipt(p["pane"], p["receipt"])
	switch r.State {
	case ReceiptUnknown, "":
		return fmt.Errorf("no held send %s to %s", p["receipt"], p["pane"])
	case ReceiptTyped:
		return fmt.Errorf("your send to %s was already delivered", p["pane"])
	case ReceiptWithdrawn, ReceiptDropped:
		return fmt.Errorf("your send to %s was already %s", p["pane"], r.State)
	}
	if r.From != owner {
		return fmt.Errorf("send %s to %s is not yours", p["receipt"], p["pane"])
	}
	return nil
}

func (c sendCheck) Key(p map[string]string) string {
	k := string(c.kind) + "|" + p["pane"] + "|" + p["receipt"]
	if c.kind == watch.KindSendHeld {
		k += "|" + p["for"]
	}
	return k
}

func (c sendCheck) Judge(p map[string]string, _ time.Time, w World) Verdict {
	// Receipts are held in memory, so they are read now.
	v := c.judge(p, w)
	v.ReadAt = w.Now()
	return v
}

func (c sendCheck) judge(p map[string]string, w World) Verdict {
	r := w.Receipt(p["pane"], p["receipt"])
	to := p["pane"]
	switch r.State {
	case ReceiptWithdrawn:
		// pm, ini-20r2 gap 3: it can never fire; the owner did it, so no super.
		return Verdict{Never: true, Detail: "your send to " + to + " was withdrawn"}
	case ReceiptDropped:
		d := "your send to " + to + " was dropped"
		if r.Why != "" {
			d += ": " + r.Why
		}
		return Verdict{Never: true, TellSuper: true, Detail: d}
	case ReceiptTyped:
		if c.kind == watch.KindSendDelivered {
			return Verdict{True: true, At: r.At, Detail: "your send to " + to + " was delivered"}
		}
		// Delivered before it was held for long enough: it can never be
		// still held, so it ends now, by gap 3's rule.
		return Verdict{Never: true, Detail: fmt.Sprintf("your send to %s was delivered after %s", to, shortAge(r.At.Sub(r.Since).Round(time.Second)))}
	case ReceiptHeld:
		if c.kind == watch.KindSendDelivered {
			return Verdict{}
		}
		d, _ := time.ParseDuration(p["for"])
		if at := r.Since.Add(d); !w.Now().Before(at) {
			return Verdict{True: true, At: at, Detail: fmt.Sprintf("your send to %s is still held after %s", to, p["for"])}
		}
		return Verdict{}
	}
	return Verdict{Unknown: true, Detail: "the delivery is unknown since the restart"}
}

// watchEnder is the part of the store a pass uses (internal/watch, Watches 2A).
type watchEnder interface {
	Snapshot(now time.Time) []watch.Watch
	End(id string, state watch.State, detail string, evidenceAt, now time.Time) error
}

// judgeWatches ends every waiting watch whose condition came true by its
// deadline (FIRED, at the evidence's time), or whose deadline passed first
// (MISSED), or that can never come true (MISSED at once). A deadline-only
// watch fires at its deadline. A watch of a kind this build does not know is
// left as it is. toSuper, if set, is told the id of a watch that ended
// because initech dropped its send. The pass has one clock: w.Now().
func judgeWatches(s watchEnder, w World, toSuper func(id string)) {
	now := w.Now()
	seen := map[string]Verdict{}
	for _, wt := range s.Snapshot(now) {
		if wt.State != watch.Waiting {
			continue
		}
		c, ok := watchCheckers[wt.Kind]
		if !ok {
			continue
		}
		if wt.Kind == watch.KindDeadline {
			if !now.Before(wt.Deadline) {
				endWatch(s, wt.ID, watch.Fired, "its deadline came", wt.Deadline, now)
			}
			continue
		}
		key := c.Key(wt.Params) + "@" + wt.Set.UTC().Format(time.RFC3339Nano)
		v, done := seen[key]
		if !done {
			v = c.Judge(wt.Params, wt.Set, w)
			seen[key] = v
		}
		switch {
		case v.Never:
			endWatch(s, wt.ID, watch.Missed, v.Detail, now, now)
			if v.TellSuper && toSuper != nil {
				toSuper(wt.ID)
			}
		case v.True && !v.At.After(wt.Deadline):
			endWatch(s, wt.ID, watch.Fired, v.Detail, v.At, now)
		case now.After(wt.Deadline) && !v.ReadAt.Before(wt.Deadline):
			d := "its deadline passed"
			if v.Unknown && v.Detail != "" {
				d += "; " + v.Detail
			}
			endWatch(s, wt.ID, watch.Missed, d, wt.Deadline, now)
		}
	}
}

// endWatch ends one watch; a refusal (it ended already) is logged, never
// retried: the store's exactly-once rule stands.
func endWatch(s watchEnder, id string, st watch.State, detail string, at, now time.Time) {
	if err := s.End(id, st, detail, at, now); err != nil {
		LogWarn("watch", "watch not ended", "watch", id, "state", string(st), "err", err)
	}
}

// cachedComments is one bead's comments as the last read gave them.
type cachedComments struct {
	count int
	cs    []progress.Comment
	ok    bool
	at    time.Time // when the comments were read
}

// cachedCommentsWorld reads each bead's comments at most once per pass.
type cachedCommentsWorld struct {
	World
	cache map[string]cachedComments
}

func (w *cachedCommentsWorld) Comments(id string) ([]progress.Comment, bool) {
	if c, ok := w.cache[id]; ok {
		return c.cs, c.ok
	}
	cs, ok := w.World.Comments(id)
	w.cache[id] = cachedComments{cs: cs, ok: ok}
	return cs, ok
}

// readWatchComments returns the comment cache for the watched beads: prev's
// entry kept for a bead whose comment_count has not risen since its last good
// read, a fresh bd comments read (bounded by beadPollTimeout) for one that
// rose or was never read, and nothing for an unwatched bead. A failed read
// keeps prev's comments, marked not ok, and is tried at the next poll. It
// never changes prev and touches no TUI state: the poll calls it off the main
// goroutine and the main goroutine publishes the result (ini-20r2: the
// deadline timer's pass reads the cache on the main goroutine at any time).
func readWatchComments(dir string, watched map[string]bool, beads []bdBead, prev map[string]cachedComments, now time.Time) map[string]cachedComments {
	out := make(map[string]cachedComments, len(watched))
	for _, b := range beads {
		if !watched[b.ID] {
			continue
		}
		old, read := prev[b.ID]
		if read && old.ok && b.CommentCount <= old.count {
			old.at = now // read as unchanged by this poll
			out[b.ID] = old
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), beadPollTimeout)
		cs, err := bdBeadComments(ctx, dir, b.ID)
		cancel()
		if err != nil {
			LogWarn("watch", "comments not read; the bead's comment watches wait for the next poll", "bead", progress.Clean(b.ID))
			out[b.ID] = cachedComments{count: old.count, cs: old.cs, ok: false, at: old.at}
			continue
		}
		out[b.ID] = cachedComments{count: b.CommentCount, cs: cs, ok: true, at: now}
	}
	return out
}

// noteWatchBeads keeps the newest answered bead poll for the watches' World;
// an unanswered poll keeps the last one. Main goroutine.
func (t *TUI) noteWatchBeads(beads []bdBead, answered bool, now time.Time) {
	if !answered {
		return
	}
	t.watchBeadsAt = now
	m := make(map[string]bdBead, len(beads))
	for _, b := range beads {
		m[b.ID] = b
	}
	t.watchBeads = m
}

// passWorld is the World a pass and a set read: the newest bead poll, the
// comments read for the comment watches, and the receipts.
type passWorld struct {
	t   *TUI
	now time.Time
}

func (w passWorld) Now() time.Time   { return w.now }
func (w passWorld) BeadsKnown() bool { return w.t.watchBeads != nil }
func (w passWorld) HasBeads() bool {
	return w.t.watchBeads != nil || projectHasBeads(w.t.projectRoot)
}
func (w passWorld) BeadsReadAt() time.Time { return w.t.watchBeadsAt }
func (w passWorld) CommentsReadAt(id string) time.Time {
	return w.t.watchComments[id].at
}
func (w passWorld) Bead(id string) (bdBead, bool) {
	b, ok := w.t.watchBeads[id]
	return b, ok
}
func (w passWorld) Comments(id string) ([]progress.Comment, bool) {
	c, ok := w.t.watchComments[id]
	if !ok || !c.ok {
		return c.cs, false
	}
	return c.cs, true
}
func (w passWorld) Receipt(pane, id string) ReceiptState { return watchReceipt(w.t, pane, id) }

// watchWorld is the World for a pass or a set at now. Main goroutine.
func (t *TUI) watchWorld(now time.Time) World { return passWorld{t: t, now: now} }

// validateWatch refuses a watch of kind with params for owner, at set, with
// the line the setter reads. Main goroutine.
func (t *TUI) validateWatch(kind watch.Kind, params map[string]string, owner string, now time.Time) error {
	c, ok := watchCheckers[kind]
	if !ok {
		return fmt.Errorf("no watch condition %q", string(kind))
	}
	return c.Validate(params, owner, t.watchWorld(now))
}

// watchValidate is validateWatch for an IPC goroutine (Watches 2D's set):
// it runs on the main goroutine against the newest bead poll. Never call it
// from the main goroutine.
func (t *TUI) watchValidate(kind watch.Kind, params map[string]string, owner string) error {
	var err error
	if !t.runOnMain(func() { err = t.validateWatch(kind, params, owner, time.Now()) }) {
		return fmt.Errorf("TUI shutting down")
	}
	return err
}

// watchAfterFunc arms the deadline timer and returns its stop; watchNow is
// the timer's clock. Package variables so tests stand in for both.
var (
	watchAfterFunc = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
	watchNow       = time.Now
)

// watchCommentBeads is the beads that waiting comment watches name, for the
// poll to read their comments. Main goroutine.
func (t *TUI) watchCommentBeads(now time.Time) map[string]bool {
	if t.watches == nil {
		return nil
	}
	out := map[string]bool{}
	for _, w := range t.watches.Snapshot(now) {
		if w.State == watch.Waiting && w.Kind == watch.KindBeadComment {
			out[w.Params["bead"]] = true
		}
	}
	return out
}

// judgeWatchPass judges window 1's waiting watches against the newest
// World. It runs before watchesTick, so an ending is written in the same
// tick. A window without a store does nothing. Main goroutine.
func (t *TUI) judgeWatchPass(now time.Time) {
	if t.watches == nil {
		return
	}
	judgeWatches(t.watches, t.watchWorld(now), t.onWatchDropped)
}

// armWatchTimer sets the one timer at the nearest waiting deadline after
// now, replacing the last, so a deadline ends between bead polls; with no
// waiting deadline it sets none. Main goroutine.
func (t *TUI) armWatchTimer(now time.Time) {
	if t.watchTimerStop != nil {
		t.watchTimerStop()
		t.watchTimerStop = nil
	}
	if t.watches == nil {
		return
	}
	var next time.Time
	for _, w := range t.watches.Snapshot(now) {
		if w.State == watch.Waiting && w.Deadline.After(now) && (next.IsZero() || w.Deadline.Before(next)) {
			next = w.Deadline
		}
	}
	if next.IsZero() {
		return
	}
	t.watchTimerStop = watchAfterFunc(next.Sub(now), func() {
		t.runOnMain(func() {
			n := watchNow()
			t.judgeWatchPass(n)
			t.watchesTick(n)
			t.armWatchTimer(n)
		})
	})
}

// watchTicker makes the no-bd watch tick; a package variable so tests stand
// in for the real ticker.
var watchTicker = func(d time.Duration) (<-chan time.Time, func()) {
	tk := time.NewTicker(d)
	return tk.C, tk.Stop
}

// startWatchTicker gives window 1 of a rig without bd the watch tick the bead
// poll gives a rig with bd: every beadPollInterval the pass, the store's tick
// (history pruned, a held write retried) and the deadline timer. A rig with
// bd, or a second window, starts nothing.
func (t *TUI) startWatchTicker(quit <-chan struct{}) {
	if !t.isFleetAuthority() || projectHasBeads(t.projectRoot) {
		return
	}
	ticks, stop := watchTicker(beadPollInterval)
	t.safeGo(func() {
		defer stop()
		t.watchTickLoop(quit, ticks)
	})
}

// watchTickLoop runs one watch tick per tick until quit closes.
func (t *TUI) watchTickLoop(quit <-chan struct{}, ticks <-chan time.Time) {
	for {
		select {
		case <-quit:
			return
		case now := <-ticks:
			t.runOnMain(func() {
				t.judgeWatchPass(now)
				t.watchesTick(now)
				t.armWatchTimer(now)
			})
		}
	}
}
