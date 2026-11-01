package tui

// session_beads.go writes the session-bead record (ini-wra.2): which Claude
// session was on which role and bead, and when, so the cost ledger (cla)
// charges spend to the bead that was worked instead of guessing it. The
// format was agreed with the claude-accounts side and is not ours to extend:
// a new field never changes the meaning of an old one, and no field is added
// without that agreement.
//
// One file per rig and month, ~/.claude-accounts/records/<rig>-YYYY-MM.jsonl,
// where the cla ledger reads it (ini-5jh: a launchd job cannot open a rig
// under ~/Desktop), one JSON object per line, appended and never rewritten.
// <rig>.owner beside the files names the rig root that owns the name; a
// second root with the same name writes <rig>-<six hex of its root>-....
// A dev build writes the same files under <rig>/.initech/records instead, and
// never under ~/.claude-accounts: the ledger reads every *.jsonl there, so a
// test rig's session would reach the real spend figures.
// One line is one interval:
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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// sessionBeadKeepDays is how long a month's file stays after the month ends.
const sessionBeadKeepDays = 90

// sessionRecordRetry is how often a recorder whose folder cannot be written
// looks again: one cheap probe, never more often.
const sessionRecordRetry = time.Minute

// sessionRecordPendingMax bounds the lines held while the folder cannot be
// written; past it the oldest go, counted in one log line.
const sessionRecordPendingMax = 10000

// sessionRecordsDir is the folder the record is written to and cla reads:
// ~/.claude-accounts/records, "" when there is no home folder.
func sessionRecordsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".claude-accounts", "records")
}

// sessionRecordsDirFor is the folder a build of this version records into: a
// release build's is the account folder, and any other build's is its own rig
// folder, whatever HOME says. "" when there is no folder to name (no home
// folder for a release build, no rig root for a dev build), never a relative
// path.
func sessionRecordsDirFor(version, root string) string {
	if IsReleaseVersion(version) {
		return sessionRecordsDir()
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, ".initech", "records")
}

// recordRigName is the rig's name in the record's file names: the project
// name, else the rig root's folder name, with anything outside
// [A-Za-z0-9._-] made a dash.
func recordRigName(name, root string) string {
	if name == "" {
		name = filepath.Base(filepath.Clean(root))
	}
	b := []byte(name)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			b[i] = '-'
		}
	}
	if s := strings.Trim(string(b), "."); s != "" {
		return s
	}
	return "rig"
}

// rootSuffix is six hex of a rig root path: the clash suffix.
func rootSuffix(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:6]
}

// fileFor is this recorder's file for t's UTC month.
func (r *sessionBeadRecorder) fileFor(t time.Time) string {
	return r.name + "-" + t.UTC().Format("2006-01") + ".jsonl"
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
	derived   string // the pane's derived bead: the writer guard's stand-in for a non-id bead
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
	root           string // the rig root, which the owner marker names
	name           string // the files' prefix: the rig name, or it with the clash suffix
	open           map[any]*openInterval
	month          string          // the UTC month of the last write, for the keep sweep
	failing        bool            // the folder could not be written, and that was logged
	upgraded       bool            // the first reconcile closed the open non-id intervals on disk
	rejected       map[string]bool // non-id bead values already logged by the guard

	ready   bool              // the folder is there, writable, and the name resolved
	nextTry time.Time         // the next probe while not ready
	probes  int               // probes made, for the never-blocks test
	pending []sessionBeadLine // lines held while the folder could not be written
	dropped int               // pending lines dropped past the bound
}

// newSessionBeadRecorder records the rig at root, named rig, into dir (the
// records folder). Nothing touches the file system until the first write.
func newSessionBeadRecorder(dir, root, rig string) *sessionBeadRecorder {
	host, _ := os.Hostname()
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	if rig == "" {
		rig = filepath.Base(filepath.Clean(root))
	}
	return &sessionBeadRecorder{
		dir:  dir,
		rig:  rig,
		root: root,
		host: host,
		open: map[any]*openInterval{},
	}
}

// ensure reports whether the folder can take a write, probing it at most once
// a minute while it cannot: it creates the folder (0700) when missing,
// resolves the file name through the owner marker, and then writes what was
// held. It never sleeps, and costs nothing between probes.
func (r *sessionBeadRecorder) ensure(now time.Time) bool {
	if r.ready {
		return true
	}
	if !r.nextTry.IsZero() && now.Before(r.nextTry) {
		return false
	}
	r.nextTry = now.Add(sessionRecordRetry)
	r.probes++
	if err := r.openFolder(); err != nil {
		r.fail(err)
		return false
	}
	r.ready = true
	r.flush(now)
	return r.ready
}

// fail logs the folder's failure once per failing streak.
func (r *sessionBeadRecorder) fail(err error) {
	if !r.failing {
		LogWarn("session-beads", "cannot write the session-bead record; the TUI goes on without it and retries each minute", "dir", r.dir, "err", err)
	}
	r.failing = true
	r.ready = false
}

// openFolder makes the records folder ready: created 0700 when missing, a
// folder and not a file, and the rig's file name resolved.
func (r *sessionBeadRecorder) openFolder() error {
	if r.dir == "" {
		return errors.New("no home folder for ~/.claude-accounts/records")
	}
	st, err := os.Stat(r.dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(r.dir, 0o700); err != nil {
			return err
		}
	case err != nil:
		return err
	case !st.IsDir():
		return fmt.Errorf("%s is not a folder", r.dir)
	}
	return r.resolveName()
}

// resolveName picks the files' prefix from the owner marker <rig>.owner: a
// missing marker is written with this root, and the plain rig name is
// this root's; a marker naming another root means two rigs share the name,
// and this one writes under <rig>-<six hex of its root>, with one log line.
func (r *sessionBeadRecorder) resolveName() error {
	base := recordRigName(r.rig, r.root)
	marker := filepath.Join(r.dir, base+".owner")
	b, err := os.ReadFile(marker)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.WriteFile(marker, []byte(r.root+"\n"), 0o600); err != nil {
			return err
		}
		r.name = base
	case err != nil:
		return err
	case strings.TrimSpace(string(b)) == r.root:
		r.name = base
	default:
		r.name = base + "-" + rootSuffix(r.root)
		LogInfo("session-beads", "another rig root owns this rig name; this rig records under a suffixed name", "rig", base, "owner", strings.TrimSpace(string(b)), "root", r.root, "file_prefix", r.name)
	}
	return nil
}

// peekName is the files' prefix the owner marker gives this rig, read
// without writing anything: "" when there is no marker (no file here is this
// rig's) or the folder cannot be read.
func (r *sessionBeadRecorder) peekName() string {
	if r.dir == "" {
		return ""
	}
	base := recordRigName(r.rig, r.root)
	b, err := os.ReadFile(filepath.Join(r.dir, base+".owner"))
	if err != nil {
		return ""
	}
	if strings.TrimSpace(string(b)) == r.root {
		return base
	}
	return base + "-" + rootSuffix(r.root)
}

// flush writes the held lines in order; it stops at a failure and keeps the
// rest.
func (r *sessionBeadRecorder) flush(now time.Time) {
	if r.dropped > 0 {
		LogWarn("session-beads", "lines held while the folder could not be written were dropped past the bound", "dropped", r.dropped)
		r.dropped = 0
	}
	for len(r.pending) > 0 && r.ready {
		if !r.append(r.pending[0], now) {
			return
		}
		r.pending = r.pending[1:]
	}
}

// hold keeps l for when the folder can be written, within the bound.
func (r *sessionBeadRecorder) hold(l sessionBeadLine) {
	if len(r.pending) >= sessionRecordPendingMax {
		r.pending = r.pending[1:]
		r.dropped++
	}
	r.pending = append(r.pending, l)
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
	if len(r.pending) > 0 {
		// Lines are held: look at the folder again (at most once a minute);
		// when it can be written, they go out with their original times.
		r.ensure(now)
	}
	if !r.upgraded {
		r.upgraded = true
		r.closeOpenNonIDs(now)
	}
	seen := make(map[any]bool, len(works))
	for _, w := range works {
		w = r.guard(w)
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

// guard keeps a bead that is not an id out of the record (ini-5qf): the
// interval carries the pane's derived bead instead, with source bd, or no
// bead when there is none, and the rejected value is logged once.
func (r *sessionBeadRecorder) guard(w paneWork) paneWork {
	if w.bead == "" || IsBeadID(w.bead) {
		return w
	}
	if r.rejected == nil {
		r.rejected = map[string]bool{}
	}
	if !r.rejected[w.bead] {
		r.rejected[w.bead] = true
		LogWarn("session-beads", "a bead that is not an id was not recorded; the derived bead stands in", "role", w.role, "value", w.bead, "derived", w.derived)
	}
	w.bead, w.source = "", "bd"
	if IsBeadID(w.derived) {
		w.bead = w.derived
	}
	return w
}

// closeOpenNonIDs closes, once, every interval this month's or last month's
// file holds open with a bead that is not an id: lines a run before the fix
// left open (a crash, or a rig that never stopped). It appends one closing
// line each, to now; nothing already written is changed. An open interval on
// an id is left as the next start always leaves it.
func (r *sessionBeadRecorder) closeOpenNonIDs(now time.Time) {
	name := r.name
	if name == "" {
		// Read the owner marker without writing it: with no marker, nothing in
		// the folder is this rig's, and the folder is left untouched.
		name = r.peekName()
		if name == "" {
			return
		}
	}
	type key struct{ session, role, bead, from string }
	latest := map[key]sessionBeadLine{}
	var order []key
	for _, month := range []time.Time{now.AddDate(0, -1, 0), now} {
		b, err := os.ReadFile(filepath.Join(r.dir, name+"-"+month.UTC().Format("2006-01")+".jsonl"))
		if err != nil {
			continue
		}
		for _, ln := range strings.Split(string(b), "\n") {
			var l sessionBeadLine
			if strings.TrimSpace(ln) == "" || json.Unmarshal([]byte(ln), &l) != nil || l.Bead == nil {
				continue
			}
			k := key{l.SessionID, l.Role, *l.Bead, l.From}
			if _, ok := latest[k]; !ok {
				order = append(order, k)
			}
			latest[k] = l
		}
	}
	for _, k := range order {
		l := latest[k]
		if l.To != nil || IsBeadID(k.bead) {
			continue
		}
		to := recordTime(now)
		l.To = &to
		LogWarn("session-beads", "closed an open interval whose bead is not an id", "role", l.Role, "value", k.bead)
		r.write(l, now)
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

// write appends one line to the month's file, or holds it while the folder
// cannot be written (probed once a minute). A failure is logged once per
// streak; nothing else changes, so the record never stops or slows the TUI.
func (r *sessionBeadRecorder) write(l sessionBeadLine, now time.Time) {
	if !r.ensure(now) {
		r.hold(l)
		return
	}
	if len(r.pending) > 0 {
		r.hold(l)
		r.flush(now)
		return
	}
	if !r.append(l, now) {
		r.hold(l)
	}
}

// append writes l to the month's file; false (logged, and the recorder back
// to probing) when it could not.
func (r *sessionBeadRecorder) append(l sessionBeadLine, now time.Time) bool {
	if month := now.UTC().Format("2006-01"); month != r.month {
		r.month = month
		r.prune(now)
	}
	b, err := json.Marshal(l)
	if err == nil {
		err = appendRecordLine(filepath.Join(r.dir, r.fileFor(now)), append(b, '\n'))
	}
	if err != nil {
		r.fail(err)
		r.nextTry = now.Add(sessionRecordRetry)
		return false
	}
	r.failing = false
	return true
}

// appendRecordLine appends b to path in one write, creating the file when
// missing (the folder is the recorder's to make). O_APPEND keeps every line
// whole and in place.
func appendRecordLine(path string, b []byte) error {
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
	if r.name == "" {
		return
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	// Only this rig's own month files: <name>-YYYY-MM.jsonl, exactly. Other
	// rigs' files, markers and anything else in the folder are never touched.
	own := regexp.MustCompile(`^` + regexp.QuoteMeta(r.name) + `-(\d{4})-(\d{2})\.jsonl$`)
	for _, e := range entries {
		m := own.FindStringSubmatch(e.Name())
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
		derived:   p.derivedBead,
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
	return newSessionBeadRecorder(sessionRecordsDirFor(t.version, t.projectRoot), t.projectRoot, t.projectName)
}

// reportSessionRecordStart writes SessionRecordStartLines to w, one per line.
func reportSessionRecordStart(w io.Writer, version, root string) {
	for _, l := range SessionRecordStartLines(version, root) {
		fmt.Fprintln(w, l)
	}
}

// SessionRecordStartLines is what a rig's start says about the session-bead
// record: for a dev build, the line naming the folder it records into, and for
// any build, one line when that folder cannot be written. It makes the folder
// (0700) when missing, as the writer would, and only that build's folder.
// Called before the screen takes the terminal, so the lines are readable; the
// writer itself retries each minute and never blocks. A rig with no root
// records nothing and says nothing.
func SessionRecordStartLines(version, root string) []string {
	if root == "" {
		return nil
	}
	dir := sessionRecordsDirFor(version, root)
	var lines []string
	if !IsReleaseVersion(version) {
		lines = append(lines, fmt.Sprintf("dev build: session records go to %s, not the account ledger", dir))
	}
	if w := sessionRecordFolderWarning(dir); w != "" {
		lines = append(lines, w)
	}
	return lines
}

// sessionRecordFolderWarning is the one line saying dir cannot be written, ""
// when it can.
func sessionRecordFolderWarning(dir string) string {
	err := func() error {
		if dir == "" {
			return errors.New("no home folder")
		}
		st, err := os.Stat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return os.MkdirAll(dir, 0o700)
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("%s is not a folder", dir)
		}
		f, err := os.CreateTemp(dir, ".initech-probe-*")
		if err != nil {
			return err
		}
		name := f.Name()
		f.Close()
		return os.Remove(name)
	}()
	if err == nil {
		return ""
	}
	return fmt.Sprintf("initech: the session-bead record cannot be written to %s (%v); the TUI runs without it and retries each minute", dir, err)
}
