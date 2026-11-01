package tui

// session_beads.go writes the session-bead record (ini-wra.2): which Claude
// session was on which role and bead, and when, so the cost ledger (cla)
// charges spend to the bead that was worked instead of guessing it. The
// format was agreed with the claude-accounts side and is not ours to extend:
// a new field never changes the meaning of an old one, and no field is added
// without that agreement.
//
// One file per rig and month, <rig root>/.initech/session-beads-YYYY-MM.jsonl
// (the folder that holds initech.sock), one JSON object per line, appended and
// never rewritten. One line is one interval:
//
//	{"v":1,"rig":"initech","host":"nmbp","session_id":"0f3c...","role":"eng2",
//	 "bead":"ini-cnp","source":"bd","from":"2026-10-31T23:50:12Z","to":null}
//
// An interval closes when the bead, the session id or the role changes, or the
// pane stops or is suspended. A still-open interval is written with "to": null
// and closed by a later line with the same session_id, role, bead and from;
// the reader keys on (session_id, from) and the latest line wins. A crash
// leaves intervals open and the next start never rewrites them.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sessionBeadKeepDays is how long a month's file stays after the month ends.
const sessionBeadKeepDays = 90

// sessionBeadFileRe matches the record's monthly files and nothing else: the
// only files the record ever removes.
var sessionBeadFileRe = regexp.MustCompile(`^session-beads-(\d{4})-(\d{2})\.jsonl$`)

// sessionBeadFile is the month's file name for t, by its UTC month.
func sessionBeadFile(t time.Time) string {
	return "session-beads-" + t.UTC().Format("2006-01") + ".jsonl"
}

// sessionBeadLine is one line of the record, keys in the agreed order.
type sessionBeadLine struct {
	V         int     `json:"v"`
	Rig       string  `json:"rig"`
	Host      string  `json:"host"`
	SessionID string  `json:"session_id"`
	Role      string  `json:"role"`
	Bead      *string `json:"bead"`
	Source    string  `json:"source"`
	From      string  `json:"from"`
	To        *string `json:"to"`
}

// recordTime is t as the record writes it: UTC, RFC 3339, whole seconds.
func recordTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// paneWork is what the record needs from one pane, read at once under its
// lock.
type paneWork struct {
	key       any // the pane; its interval survives a rename, which closes it
	role      string
	sessionID string
	bead      string // "" is a stretch with no bead (null in the record)
	source    string // "bd" or "manual"
	from      time.Time
	working   bool // alive and not suspended
	frozen    bool // bd did not answer and no override is in force: no new intervals
}

// openInterval is an interval the recorder has written open.
type openInterval struct {
	sessionID, role, bead, source string
	from                          time.Time
}

func (o *openInterval) sameAs(w paneWork) bool {
	return o.sessionID == w.sessionID && o.role == w.role && o.bead == w.bead && o.source == w.source
}

// sessionBeadRecorder keeps each pane's open interval and appends the record.
// Used from one goroutine (the main loop, or a daemon's maintenance loop).
type sessionBeadRecorder struct {
	dir, rig, host string
	open           map[any]*openInterval
	month          string // the UTC month of the last write, for the keep sweep
	failing        bool   // the last write failed and was logged
}

// newSessionBeadRecorder records into root's .initech folder.
func newSessionBeadRecorder(root, rig string) *sessionBeadRecorder {
	host, _ := os.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	return &sessionBeadRecorder{
		dir:  filepath.Join(root, ".initech"),
		rig:  rig,
		host: host,
		open: map[any]*openInterval{},
	}
}

// clampTime is t held within [lo, hi].
func clampTime(t, lo, hi time.Time) time.Time {
	if t.Before(lo) {
		return lo
	}
	if t.After(hi) {
		return hi
	}
	return t
}

// reconcile brings the record in line with the panes as they are at now:
// it closes intervals that ended and opens the ones that began. A pane not
// in works has stopped or gone.
func (r *sessionBeadRecorder) reconcile(works []paneWork, now time.Time) {
	seen := make(map[any]bool, len(works))
	for _, w := range works {
		seen[w.key] = true
		cur := r.open[w.key]
		want := w.working && w.sessionID != ""
		if want && w.frozen {
			// bd is unreachable: write nothing new until it answers, never a
			// guess. The open interval stands while the session and role
			// do; a stop, a suspend or a new session still closes it.
			if cur != nil && cur.sessionID == w.sessionID && cur.role == w.role {
				continue
			}
			want = false
		}
		if cur != nil && want && cur.sameAs(w) {
			continue
		}
		var lower time.Time
		if cur != nil {
			to := now
			if want && w.from.After(cur.from) {
				// The new interval starts when its bead or session did, and
				// the old one ends there: no second is charged twice. A from
				// no later than the old interval's (a role renamed, a wake
				// with a bead older than the stop) says nothing about when
				// the change came, so it came now.
				to = clampTime(w.from, cur.from, now)
			}
			r.closeInterval(w.key, cur, to, now)
			lower = to
		}
		if want {
			from := clampTime(w.from, lower, now)
			o := &openInterval{sessionID: w.sessionID, role: w.role, bead: w.bead, source: w.source, from: from}
			r.open[w.key] = o
			r.write(r.line(o, nil), now)
		}
	}
	for k, cur := range r.open {
		if !seen[k] {
			r.closeInterval(k, cur, now, now)
		}
	}
}

// closeAll writes the closing line of every open interval: a clean shutdown.
func (r *sessionBeadRecorder) closeAll(now time.Time) {
	for k, cur := range r.open {
		r.closeInterval(k, cur, now, now)
	}
}

func (r *sessionBeadRecorder) closeInterval(k any, cur *openInterval, to, now time.Time) {
	delete(r.open, k)
	r.write(r.line(cur, &to), now)
}

func (r *sessionBeadRecorder) line(o *openInterval, to *time.Time) sessionBeadLine {
	l := sessionBeadLine{V: 1, Rig: r.rig, Host: r.host, SessionID: o.sessionID, Role: o.role, Source: o.source, From: recordTime(o.from)}
	if o.bead != "" {
		b := o.bead
		l.Bead = &b
	}
	if to != nil {
		s := recordTime(*to)
		l.To = &s
	}
	return l
}

// write appends one line to the month's file. A failure is logged once until
// a write succeeds again; nothing else changes, so the record never stops the
// TUI.
func (r *sessionBeadRecorder) write(l sessionBeadLine, now time.Time) {
	if month := now.UTC().Format("2006-01"); month != r.month {
		r.month = month
		r.prune(now)
	}
	b, err := json.Marshal(l)
	if err == nil {
		err = appendRecordLine(filepath.Join(r.dir, sessionBeadFile(now)), append(b, '\n'))
	}
	if err != nil {
		if !r.failing {
			LogWarn("session-beads", "cannot write the session-bead record; the TUI goes on without it", "dir", r.dir, "err", err)
		}
		r.failing = true
		return
	}
	r.failing = false
}

// appendRecordLine appends b to path in one write, creating the file (and its
// folder) when missing. O_APPEND keeps every line whole and in place.
func appendRecordLine(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// prune removes the month files whose month ended more than
// sessionBeadKeepDays before now. Only names the record itself writes are
// ever removed, so at least 90 days of the record stay on disk.
func (r *sessionBeadRecorder) prune(now time.Time) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := sessionBeadFileRe.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		if mo < 1 || mo > 12 {
			continue
		}
		end := time.Date(y, time.Month(mo)+1, 1, 0, 0, 0, 0, time.UTC)
		if now.Sub(end) > sessionBeadKeepDays*24*time.Hour {
			if err := os.Remove(filepath.Join(r.dir, e.Name())); err == nil {
				LogInfo("session-beads", "removed a month older than the keep", "file", e.Name())
			}
		}
	}
}

// noteSessionID takes the pane's Claude session id from entries the tailer
// just read: the newest one written since the process started. Entries from
// before the start (a transcript left from an earlier run) never name the
// pane, so a pane with no entry of its own yet has no session id.
func (p *Pane) noteSessionID(entries []JournalEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, e := range entries {
		if e.SessionID == "" || e.Timestamp.IsZero() || e.Timestamp.Before(p.startedAt) {
			continue
		}
		if e.SessionID != p.sessionID {
			p.sessionID, p.sessionSince = e.SessionID, e.Timestamp
		}
	}
}

// sessionWork is the pane as the session-bead record reads it.
func (p *Pane) sessionWork() paneWork {
	p.mu.Lock()
	defer p.mu.Unlock()
	w := paneWork{
		key:       p,
		role:      p.name,
		sessionID: p.sessionID,
		bead:      p.primaryBeadLocked(),
		source:    "bd",
		working:   p.alive && !p.suspended,
	}
	from := p.derivedFrom
	if p.overrideLocked() {
		w.source = "manual"
		from = p.overrideSince
	} else {
		// Back on the derived bead after a clear, the new interval starts at
		// the clear: reconcile opens it where the override's interval closed.
		w.frozen = p.beadStale
	}
	for _, t := range []time.Time{p.startedAt, p.sessionSince} {
		if t.After(from) {
			from = t
		}
	}
	w.from = from
	return w
}

// localPaneWork is the record's view of the given panes: local ones only. A
// window 2 pane is recorded by the process that runs its agent.
func localPaneWork(panes []PaneView) []paneWork {
	var works []paneWork
	for _, pv := range panes {
		if p, ok := pv.(*Pane); ok {
			works = append(works, p.sessionWork())
		}
	}
	return works
}

// sessionBeadEvery is how often the record is brought in line with the panes.
// The times it writes come from the panes (the claim, the override, the
// first entry), not from this tick.
const sessionBeadEvery = time.Second

// recordSessionBeads reconciles the record with window 1's local panes, at
// most once a second. Nothing in a window that does not record.
func (t *TUI) recordSessionBeads(now time.Time) {
	if t.sessionBeads == nil || now.Sub(t.lastSessionBeads) < sessionBeadEvery {
		return
	}
	t.lastSessionBeads = now
	t.sessionBeads.reconcile(localPaneWork(t.panes), now)
}

// sessionBeadRecorderFor is the TUI's recorder: window 1 with a project root
// records; a second window or a viewer records nothing (window 1, or the
// daemon running the agents, writes their rig's file).
func (t *TUI) sessionBeadRecorderFor(viewer bool) *sessionBeadRecorder {
	if !t.isFleetAuthority() || viewer || t.projectRoot == "" {
		return nil
	}
	return newSessionBeadRecorder(t.projectRoot, t.projectName)
}
