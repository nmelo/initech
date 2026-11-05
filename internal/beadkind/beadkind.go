// Package beadkind says what kind of change last moved each bead: filed,
// claimed, a comment, qa, waiting, blocked or nelson (commit comes from the
// commits that name a bead, never from here). It reads the beads events
// journal when the rig has turned it on, and checks it on every poll; when
// the journal misses a move or misbehaves, it says so once in the log and
// once to super and reads bd history instead for the rest of the session.
// The box that shows the kinds reads Kind and never knows which path ran.
//
// Every bd call is read-only (--readonly) and bounded; the package never
// writes to bd and never turns the journal on.
package beadkind

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the kind of a bead's last change.
type Kind string

// The kinds, in the words the panel shows.
const (
	Filed   Kind = "filed"
	Claimed Kind = "claimed"
	Comment Kind = "comment"
	Commit  Kind = "commit" // set by the commit read, never by this package
	QA      Kind = "qa"
	Waiting Kind = "waiting"
	Blocked Kind = "blocked"
	Nelson  Kind = "nelson"
)

// Mode is which path reads the kinds.
type Mode int

const (
	// ModeUnknown is before the first poll.
	ModeUnknown Mode = iota
	// ModeJournal reads the beads events journal.
	ModeJournal
	// ModePlanB reads bd history for the beads that moved in the window.
	ModePlanB
)

const (
	// CallBudget bounds every bd call.
	CallBudget = 5 * time.Second
	// Window is how far back a move counts.
	Window = 30 * time.Minute
	// PlanBCap is how many beads plan B reads in one poll.
	PlanBCap = 20
	// PageSize and PagesPerPoll bound the journal read of one poll.
	PageSize     = 500
	PagesPerPoll = 4
)

// Runner runs one command and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Bead is one bead as the last bd list poll saw it.
type Bead struct {
	ID        string
	Status    string
	Assignee  string
	UpdatedAt time.Time
}

// Options are what a Reader needs from its host.
type Options struct {
	Run       Runner
	Log       func(line string) // one line in the log
	TellSuper func(line string) // one line to super
}

type state struct{ status, assignee string }

// Reader keeps the kinds. Safe from any goroutine; Poll calls are expected
// one at a time.
type Reader struct {
	opt Options

	mu       sync.Mutex
	mode     Mode
	kinds    map[string]Kind
	seq      int64
	caughtUp bool
	started  time.Time                 // the first poll's now
	firstRow time.Time                 // the oldest journal row read
	prev     map[string]state          // last state seen per bead
	rowSeen  map[string]map[int64]bool // per bead: the updated_at (unix) of each journal row
	lastPoll map[string]time.Time      // per bead: updated_at at the last poll
	readAt   map[string]time.Time      // plan B: updated_at when last read
}

// NewReader makes a Reader.
func NewReader(opt Options) *Reader {
	return &Reader{opt: opt, kinds: map[string]Kind{}, prev: map[string]state{},
		rowSeen: map[string]map[int64]bool{}, lastPoll: map[string]time.Time{}, readAt: map[string]time.Time{}}
}

// Kind is the kind of id's last change, if one is known.
func (r *Reader) Kind(id string) (Kind, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k, ok := r.kinds[id]
	return k, ok
}

// Mode is the path in use.
func (r *Reader) Mode() Mode {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.mode
}

func (r *Reader) bd(ctx context.Context, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, CallBudget)
	defer cancel()
	return r.opt.Run(cctx, "bd", append([]string{"--readonly"}, args...)...)
}

// Poll reads what changed since the last poll. beads is the poll's bd list,
// taken before Poll is called, so every write it shows is already in the
// journal; now is the poll's time.
func (r *Reader) Poll(ctx context.Context, beads []Bead, now time.Time) {
	r.mu.Lock()
	if r.started.IsZero() {
		r.started = now
	}
	mode := r.mode
	r.mu.Unlock()

	if mode == ModeUnknown {
		out, err := r.bd(ctx, "config", "get", "events-journal")
		switch {
		case err != nil:
			r.switchToPlanB("the journal setting could not be read: " + err.Error())
		case strings.TrimSpace(string(out)) == "true":
			r.setMode(ModeJournal)
		default:
			r.setMode(ModePlanB) // off: plan B, nothing to say
		}
	}
	if r.Mode() == ModeJournal {
		if reason := r.readJournal(ctx, beads, now); reason != "" {
			r.switchToPlanB(reason)
		}
	}
	if r.Mode() == ModePlanB {
		r.readHistory(ctx, beads, now)
	}
	r.mu.Lock()
	for _, b := range beads {
		r.lastPoll[b.ID] = b.UpdatedAt
		if _, ok := r.prev[b.ID]; !ok {
			r.prev[b.ID] = state{b.Status, b.Assignee}
		}
	}
	r.mu.Unlock()
}

func (r *Reader) setMode(m Mode) {
	r.mu.Lock()
	r.mode = m
	r.mu.Unlock()
}

// switchToPlanB moves the session to plan B, saying why once in the log and
// once to super.
func (r *Reader) switchToPlanB(reason string) {
	r.mu.Lock()
	if r.mode == ModePlanB {
		r.mu.Unlock()
		return
	}
	r.mode = ModePlanB
	r.mu.Unlock()
	if r.opt.Log != nil {
		r.opt.Log("change kinds: reading bd history for the rest of the session, the beads journal " + reason)
	}
	if r.opt.TellSuper != nil {
		r.opt.TellSuper("rig panel: change kinds now come from bd history for this session; the beads journal " + reason)
	}
}

// journalRow is the part of a journal record the kinds need.
type journalRow struct {
	Seq     int64  `json:"seq"`
	Op      string `json:"op"`
	IssueID string `json:"issue_id"`
	TS      string `json:"ts"`
	Issue   *struct {
		Status    string `json:"status"`
		Assignee  string `json:"assignee"`
		UpdatedAt string `json:"updated_at"`
	} `json:"issue"`
}

// readJournal reads up to PagesPerPoll pages after the last seq, applies
// them, and once caught up checks every move in the window has its row. It
// returns why the journal cannot be trusted, or "".
func (r *Reader) readJournal(ctx context.Context, beads []Bead, now time.Time) string {
	for page := 0; page < PagesPerPoll; page++ {
		r.mu.Lock()
		since := r.seq
		r.mu.Unlock()
		out, err := r.bd(ctx, "events", "tail", "--since", strconv.FormatInt(since, 10), "--limit", strconv.Itoa(PageSize), "--json")
		if err != nil {
			return "could not be read: " + firstLine(err.Error())
		}
		n := 0
		for _, line := range strings.Split(string(out), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row journalRow
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				return "gave a row it could not read: " + firstLine(err.Error())
			}
			if row.Seq <= since || row.IssueID == "" {
				return fmt.Sprintf("gave row %d out of order after %d", row.Seq, since)
			}
			r.apply(row)
			since = row.Seq
			n++
		}
		r.mu.Lock()
		r.seq = since
		if n < PageSize {
			r.caughtUp = true
		}
		caught := r.caughtUp
		r.mu.Unlock()
		if caught {
			break
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.caughtUp {
		return "" // still catching up: nothing to judge yet
	}
	floor := now.Add(-Window)
	// Moves before the journal's first row, or before this reader started
	// when the journal is empty, cannot have rows; they are not judged.
	since := r.started
	if !r.firstRow.IsZero() && r.firstRow.Before(since) {
		since = r.firstRow
	}
	if since.After(floor) {
		floor = since
	}
	for _, b := range beads {
		if b.UpdatedAt.Before(floor) {
			continue
		}
		if last, ok := r.lastPoll[b.ID]; ok && last.Equal(b.UpdatedAt) {
			continue
		}
		if !r.rowSeen[b.ID][b.UpdatedAt.Unix()] {
			return "has no row for " + b.ID + "'s change at " + b.UpdatedAt.UTC().Format(time.RFC3339) + " (a row that came by bd dolt pull, or another machine's write)"
		}
	}
	return ""
}

// apply records one journal row. Caller does not hold r.mu.
func (r *Reader) apply(row journalRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ts, err := time.Parse(time.RFC3339, row.TS); err == nil && (r.firstRow.IsZero() || ts.Before(r.firstRow)) {
		r.firstRow = ts
	}
	if row.Issue == nil {
		return // a delete: the bead leaves the panel
	}
	now := state{row.Issue.Status, row.Issue.Assignee}
	if up, err := time.Parse(time.RFC3339, row.Issue.UpdatedAt); err == nil {
		if r.rowSeen[row.IssueID] == nil {
			r.rowSeen[row.IssueID] = map[int64]bool{}
		}
		r.rowSeen[row.IssueID][up.Unix()] = true
	}
	switch row.Op {
	case "create":
		r.kinds[row.IssueID] = Filed
	case "comment":
		r.kinds[row.IssueID] = Comment
	case "update":
		if prev, ok := r.prev[row.IssueID]; ok {
			if k, ok := kindOfUpdate(prev, now); ok {
				r.kinds[row.IssueID] = k
			}
		}
	}
	r.prev[row.IssueID] = now
}

// kindOfUpdate maps a change of status and assignee to its kind, by ruling:
// an assignee set with in_progress is claimed; a status to ready_for_qa,
// in_qa or qa_passed is qa; to waiting, blocked or waiting_on_nelson is
// waiting, blocked or nelson. Anything else is not a kind.
func kindOfUpdate(prev, now state) (Kind, bool) {
	if now.status == "in_progress" && now.assignee != "" && (prev.status != "in_progress" || prev.assignee != now.assignee) {
		return Claimed, true
	}
	if now.status == prev.status {
		return "", false
	}
	switch now.status {
	case "ready_for_qa", "in_qa", "qa_passed":
		return QA, true
	case "waiting":
		return Waiting, true
	case "blocked":
		return Blocked, true
	case "waiting_on_nelson":
		return Nelson, true
	}
	return "", false
}

// historyEntry is one version from bd history --json.
type historyEntry struct {
	Issue map[string]any `json:"Issue"`
}

// readHistory reads, newest move first, at most PlanBCap beads whose move
// is in the window and that moved since they were last read. A bead not
// read keeps its last kind.
func (r *Reader) readHistory(ctx context.Context, beads []Bead, now time.Time) {
	floor := now.Add(-Window)
	r.mu.Lock()
	var due []Bead
	for _, b := range beads {
		if b.UpdatedAt.Before(floor) {
			continue
		}
		if last, ok := r.readAt[b.ID]; ok && last.Equal(b.UpdatedAt) {
			continue
		}
		due = append(due, b)
	}
	r.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool { return due[i].UpdatedAt.After(due[j].UpdatedAt) })
	if len(due) > PlanBCap {
		due = due[:PlanBCap]
	}
	for _, b := range due {
		out, err := r.bd(ctx, "history", b.ID, "--json", "--limit", "2")
		if err != nil {
			continue // keeps its last kind; tried again next poll
		}
		var hist []historyEntry
		if json.Unmarshal(out, &hist) != nil || len(hist) == 0 {
			continue
		}
		k, ok := kindOfVersions(hist)
		r.mu.Lock()
		if ok {
			r.kinds[b.ID] = k
		}
		r.readAt[b.ID] = b.UpdatedAt
		r.mu.Unlock()
	}
}

// kindOfVersions is the kind from the newest two versions: one version is
// filed; a status or assignee change maps as kindOfUpdate does; two versions
// that differ in nothing but the update time are a comment (an inference: a
// commit that changed no field of the issue, which a dependency or label
// change also makes).
func kindOfVersions(hist []historyEntry) (Kind, bool) {
	if len(hist) == 1 {
		return Filed, true
	}
	newer, older := hist[0].Issue, hist[1].Issue
	ns := state{str(newer["status"]), str(newer["assignee"])}
	os := state{str(older["status"]), str(older["assignee"])}
	if k, ok := kindOfUpdate(os, ns); ok {
		return k, true
	}
	if sameApartFrom(newer, older, "updated_at") {
		return Comment, true
	}
	return "", false
}

func sameApartFrom(a, b map[string]any, skip string) bool {
	strip := func(m map[string]any) map[string]any {
		out := make(map[string]any, len(m))
		for k, v := range m {
			if k != skip {
				out[k] = v
			}
		}
		return out
	}
	return reflect.DeepEqual(strip(a), strip(b))
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
