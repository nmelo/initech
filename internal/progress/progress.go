// Package progress computes how a multi-bead effort is moving: per bead the
// acceptance items proven, the commits landed and the last move; across the
// effort closed of total, beads per stage, the critical path and the
// engineers on it; and what a bead quiet for an hour waits on. It is the
// model behind initech progress. It does no I/O: callers read bd, git and the
// panes and pass the answers in. It never produces a finish time, and it
// never reads or prints an estimate or an actual.
package progress

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Bead is one bead as bd list --json gives it.
type Bead struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Status         string    `json:"status"`
	Assignee       string    `json:"assignee"`
	Description    string    `json:"description"`
	UpdatedAt      time.Time `json:"updated_at"`
	ClosedAt       time.Time `json:"closed_at"`
	DependentCount int       `json:"dependent_count"`

	// Moved is the bead's last move where the caller knows more than bd's
	// updated_at (a comment or a commit since); zero when it does not. Stuck
	// reads the newer of the two. Never read from bd's JSON: initech progress
	// sets it from LastMove, the TUI from its bead poll.
	Moved time.Time `json:"-"`
}

// Comment is one bead comment as bd comments --json gives it.
type Comment struct {
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

// Commit is one commit on main: its committer time and subject line.
type Commit struct {
	When    time.Time
	Subject string
}

// Input is everything one report reads.
type Input struct {
	Rig    string // the project name, printed in the header
	Prefix string // the bead-id prefix of the effort; "" is the whole rig
	Hours  int    // the window for commits and closes
	Now    time.Time
	Rule   Rule // what this rig counts stuck by

	Beads    []Bead
	Comments map[string][]Comment // by bead id; a bead missing here has none

	// Commits on main inside the window. CommitsKnown false (no repos to
	// read) prints the commits column as "-".
	Commits      []Commit
	CommitsKnown bool

	// Panes maps a role to its pane's activity ("running", "background",
	// "idle", ...). PanesKnown false (no initech answering) means no owner
	// reads as busy.
	Panes      map[string]string
	PanesKnown bool

	// Notes are printed last, one per line: what could not be read.
	Notes []string
}

// Rule is what one rig counts stuck by: how long a bead may sit, and the
// statuses in its working chain (open and side states are not). Every surface
// that counts stuck takes one, so a slow rig and a fast one each read true.
// The zero Rule counts nothing.
type Rule struct {
	After time.Duration
	Chain map[string]bool
}

// NewRule is the rule for after and a rig's working chain.
func NewRule(after time.Duration, working []string) Rule {
	chain := make(map[string]bool, len(working))
	for _, s := range working {
		chain[s] = true
	}
	return Rule{After: after, Chain: chain}
}

// AfterText is the rule's value as it is printed: "20m", "1h", "1h30m".
func (r Rule) AfterText() string {
	s := r.After.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

// Wait labels for a quiet bead.
const (
	WaitWorkingUnrecorded = "working, unrecorded"
	WaitStalled           = "stalled"
)

// et is the time zone the report prints in.
var et = func() *time.Location {
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		return loc
	}
	return time.FixedZone("ET", -4*3600)
}()

// hhmm is a time as "Sat 18:00" in ET, "-" for none.
func hhmm(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(et).Format("Mon 15:04")
}

// clock is hhmm without the day: "18:00".
func clock(t time.Time) string {
	s := hhmm(t)
	if r := []rune(s); len(r) > 4 {
		return string(r[4:])
	}
	return ""
}

// cut is s to at most n runes.
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// left pads s with spaces to n runes; a longer s is kept whole.
func left(s string, n int) string {
	if k := len([]rune(s)); k < n {
		return s + strings.Repeat(" ", n-k)
	}
	return s
}

// right pads s on the left to n runes.
func right(s string, n int) string {
	if k := len([]rune(s)); k < n {
		return strings.Repeat(" ", n-k) + s
	}
	return s
}

var (
	// acHeading is the acceptance-criteria heading as a line of its own,
	// "### Acceptance Criteria" or "Acceptance Criteria:"; never the words
	// quoted inside a sentence.
	acHeading = regexp.MustCompile(`(?m)^[ \t]*(?:#+[ \t]*)?Acceptance Criteria:?[ \t]*$`)
	acEnd     = regexp.MustCompile(`\n#+ |\nDocs:`)
	acBullet  = regexp.MustCompile(`(?m)^\s*(?:[-*•]|\d+[.)])\s+`)
	acProven  = regexp.MustCompile(`(?i)^\s*AC\s*(\d+)\s+proven`)
)

// ACItems counts the bullets under a description's acceptance-criteria
// heading; ok is false when it has no such heading. The block ends at the
// next heading, a "Docs:" line, or a blank line followed by a line that is
// not a bullet.
func ACItems(desc string) (n int, ok bool) {
	loc := acHeading.FindStringIndex(desc)
	if loc == nil {
		return 0, false
	}
	block := desc[loc[1]:]
	if m := acEnd.FindStringIndex(block); m != nil {
		block = block[:m[0]]
	}
	lines := strings.Split(block, "\n")
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i-1]) == "" && strings.TrimSpace(lines[i]) != "" &&
			!acBullet.MatchString(lines[i]) && n > 0 {
			lines = lines[:i]
			break
		}
		if acBullet.MatchString(lines[i]) {
			n++
		}
	}
	return n, true
}

// ACProven is the set of item numbers the comments mark proven, each comment
// starting "AC <n> proven".
func ACProven(comments []Comment) map[string]bool {
	proven := map[string]bool{}
	for _, c := range comments {
		if m := acProven.FindStringSubmatch(c.Text); m != nil {
			proven[m[1]] = true
		}
	}
	return proven
}

// ACCell is the table's AC cell: "n/m", or "-" for a bead with no criteria
// section.
func ACCell(b Bead, comments []Comment) string {
	items, ok := ACItems(b.Description)
	if !ok || items == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", len(ACProven(comments)), items)
}

// NamesBead reports whether a commit subject names id as a word.
func NamesBead(subject, id string) bool {
	re := regexp.MustCompile(`(?:^|[^\w-])` + regexp.QuoteMeta(id) + `\b`)
	return re.MatchString(subject)
}

// beadCommits are the commits whose subject names b.
func beadCommits(b Bead, commits []Commit) []Commit {
	var out []Commit
	for _, c := range commits {
		if NamesBead(c.Subject, b.ID) {
			out = append(out, c)
		}
	}
	return out
}

// LastMove is the newest of the bead's status change (updated_at), its
// comments and its commits, and who made it: "status", the comment's author
// ("note" when it has none), or "commit". A tie keeps the earlier kind in
// that order.
func LastMove(b Bead, comments []Comment, commits []Commit) (time.Time, string) {
	last, by := b.UpdatedAt, "status"
	consider := func(t time.Time, who string) {
		if !t.IsZero() && (last.IsZero() || t.After(last)) {
			last, by = t, who
		}
	}
	for _, c := range comments {
		who := c.Author
		if who == "" {
			who = "note"
		}
		consider(c.CreatedAt, who)
	}
	for _, c := range beadCommits(b, commits) {
		consider(c.When, "commit")
	}
	return last, by
}

// waitPhrase finds the thing a comment says the bead waits on.
var waitPhrase = regexp.MustCompile(`(?i)\b(?:wait(?:s|ing)?\s+(?:on|for)|blocked\s+(?:on|by)|held\s+(?:on|by|for))\s+([^.;!?\n]+)`)

// Wait is what a quiet bead waits on: "working, unrecorded" when its owner's
// pane is running or background, `waiting on "<thing>"` when its last
// comment names one, else "stalled".
func Wait(b Bead, comments []Comment, in Input) string {
	if in.PanesKnown {
		switch in.Panes[b.Assignee] {
		case "running", "background":
			return WaitWorkingUnrecorded
		}
	}
	var lastC *Comment
	for i := range comments {
		if lastC == nil || comments[i].CreatedAt.After(lastC.CreatedAt) {
			lastC = &comments[i]
		}
	}
	if lastC != nil {
		if m := waitPhrase.FindStringSubmatch(lastC.Text); m != nil {
			if thing := strings.TrimSpace(m[1]); thing != "" {
				return fmt.Sprintf("waiting on %q", cut(thing, 60))
			}
		}
	}
	return WaitStalled
}

// isEngineer reports whether a comment author is an engineer, which marks a
// closed bead as built rather than writing or review only.
func isEngineer(author string) bool {
	return strings.HasPrefix(author, "eng") || strings.HasPrefix(author, "video")
}

// Report is one initech progress report as data: what the table and the
// effort lines show, in the order they show it. --json prints it; Render
// prints the same Report as text, so the two never differ.
type Report struct {
	// Rule is what the report counted stuck by, for a reader in this process
	// (the app's blockers count by the same one); StuckAfter is its value.
	Rule       Rule   `json:"-"`
	StuckAfter string `json:"stuck_after"`
	// StuckIDs are the beads the Stuck count counted.
	StuckIDs []string `json:"stuck_ids"`

	Rig    string    `json:"rig"`
	Prefix string    `json:"prefix"`
	At     time.Time `json:"at"`
	Hours  int       `json:"hours"`

	// Beads are the table rows: the beads past open and not closed, step
	// beads left out, in stage order.
	Beads []Row `json:"beads"`

	Closed      int            `json:"closed"`
	Total       int            `json:"total"`
	Stuck       int            `json:"stuck"`
	Blocked     int            `json:"blocked"`
	ByEngineers int            `json:"worked_by_engineers"`
	WritingOnly int            `json:"writing_review_only"`
	Stages      map[string]int `json:"stages"`

	// ClosedRecent are the closes in the window, oldest first; the table
	// lists the last eight of them.
	ClosedRecent []Closed `json:"closed_recent"`

	// CriticalPath is up to five open beads with the most dependents.
	CriticalPath []PathBead `json:"critical_path"`
	Engineers    []string   `json:"engineers"`
	Quiet        []QuietRow `json:"quiet"`
	Notes        []string   `json:"notes"`
}

// Row is one bead in the table. AC is the table's cell ("n/m" or "-");
// ACProven and ACItems are its numbers, null when the bead has no criteria
// section. Commits is null when no repo was read. LastMove is zero when the
// bead has none.
type Row struct {
	ID       string    `json:"id"`
	Stage    string    `json:"stage"`
	Owner    string    `json:"owner"`
	AC       string    `json:"ac"`
	ACProven *int      `json:"ac_proven"`
	ACItems  *int      `json:"ac_items"`
	Commits  *int      `json:"commits"`
	LastMove time.Time `json:"last_move"`
	By       string    `json:"by"`
	Title    string    `json:"title"`
}

// Closed is one close in the window.
type Closed struct {
	ID       string    `json:"id"`
	Title    string    `json:"title"`
	ClosedAt time.Time `json:"closed_at"`
}

// PathBead is one bead on the critical path: how many wait on it.
type PathBead struct {
	ID      string `json:"id"`
	Waiting int    `json:"waiting"`
	Stage   string `json:"stage"`
	Title   string `json:"title"`
}

// QuietRow is a bead with no move for the rule's value or more, and what it waits
// on (WaitWorkingUnrecorded, WaitStalled or "waiting on ...").
type QuietRow struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Wait  string `json:"wait"`
}

// Build is the report for in, as data.
func Build(in Input) Report {
	r := Report{Rig: in.Rig, Prefix: in.Prefix, At: in.Now, Hours: in.Hours, Rule: in.Rule, StuckAfter: in.Rule.AfterText(), StuckIDs: []string{},
		Beads: []Row{}, Stages: map[string]int{}, ClosedRecent: []Closed{},
		CriticalPath: []PathBead{}, Engineers: []string{}, Quiet: []QuietRow{}, Notes: []string{}}

	var beads []Bead
	for _, b := range in.Beads {
		if strings.HasPrefix(b.ID, in.Prefix) && !Deferred(b) {
			beads = append(beads, b)
		}
	}

	var active []Bead
	for _, b := range beads {
		if b.Status != "open" && b.Status != "closed" && !strings.HasPrefix(b.Title, "Step ") {
			active = append(active, b)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].Status < active[j].Status })
	for _, b := range active {
		cs := in.Comments[b.ID]
		row := Row{ID: b.ID, Stage: b.Status, Owner: b.Assignee, AC: ACCell(b, cs), Title: b.Title}
		if row.Owner == "" {
			row.Owner = "-"
		}
		if items, ok := ACItems(b.Description); ok && items > 0 {
			proven := len(ACProven(cs))
			row.ACProven, row.ACItems = &proven, &items
		}
		if in.CommitsKnown {
			n := len(beadCommits(b, in.Commits))
			row.Commits = &n
		}
		row.LastMove, row.By = LastMove(b, cs, in.Commits)
		r.Beads = append(r.Beads, row)
		if !row.LastMove.IsZero() && in.Now.Sub(row.LastMove) >= in.Rule.After {
			r.Quiet = append(r.Quiet, QuietRow{ID: b.ID, Owner: row.Owner, Wait: Wait(b, cs, in)})
		}
	}

	var closed []Bead
	for _, b := range beads {
		r.Stages[b.Status]++
		if b.Status != "closed" {
			continue
		}
		closed = append(closed, b)
		for _, c := range in.Comments[b.ID] {
			if isEngineer(c.Author) {
				r.ByEngineers++
				break
			}
		}
	}
	r.Closed, r.Total = len(closed), len(beads)
	// The pace's last move is the quiet list's: LastMove over the comments
	// and commits this report read.
	for i, b := range beads {
		beads[i].Moved, _ = LastMove(b, in.Comments[b.ID], in.Commits)
	}
	var panes map[string]string
	if in.PanesKnown {
		panes = in.Panes
	}
	pace := PaceOver(beads, strings.TrimSuffix(in.Prefix, "."), panes, in.Rule, in.Now, time.Duration(in.Hours)*time.Hour)
	r.Stuck, r.Blocked = pace.Stuck, pace.Blocked
	r.StuckIDs = append(r.StuckIDs, pace.StuckIDs...)
	r.WritingOnly = r.Closed - r.ByEngineers

	var recent []Bead
	for _, b := range closed {
		if !b.ClosedAt.IsZero() && in.Now.Sub(b.ClosedAt) < time.Duration(in.Hours)*time.Hour {
			recent = append(recent, b)
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].ClosedAt.Before(recent[j].ClosedAt) })
	for _, b := range recent {
		r.ClosedRecent = append(r.ClosedRecent, Closed{ID: b.ID, Title: b.Title, ClosedAt: b.ClosedAt})
	}

	var path []Bead
	for _, b := range beads {
		if b.Status != "closed" && b.DependentCount > 0 {
			path = append(path, b)
		}
	}
	sort.SliceStable(path, func(i, j int) bool { return path[i].DependentCount > path[j].DependentCount })
	if len(path) > 5 {
		path = path[:5]
	}
	for _, b := range path {
		r.CriticalPath = append(r.CriticalPath, PathBead{ID: b.ID, Waiting: b.DependentCount, Stage: b.Status, Title: b.Title})
	}

	engSet := map[string]bool{}
	for _, b := range active {
		if strings.HasPrefix(b.Assignee, "eng") {
			engSet[b.Assignee] = true
		}
	}
	for e := range engSet {
		r.Engineers = append(r.Engineers, e)
	}
	sort.Strings(r.Engineers)
	r.Notes = append(r.Notes, in.Notes...)
	return r
}

// stageWord is a row's stage as the table prints it: the bd status, except a
// bead waiting on Nelson, which reads "blocked (Nelson)".
func stageWord(status string) string {
	if status == "waiting_on_nelson" {
		return "blocked (Nelson)"
	}
	return status
}

// Render is the report as initech progress prints it: the header, the table
// of beads past open, the effort lines, the wait of every quiet bead, then
// the notes.
func Render(in Input) string { return RenderReport(Build(in)) }

// RenderReport prints r as the initech progress table.
func RenderReport(r Report) string {
	var w strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&w, format+"\n", a...) }

	line("%s %s* at %s ET; commits counted over the last %d h; stuck after %s\n", r.Rig, r.Prefix, hhmm(r.At), r.Hours, r.StuckAfter)
	stageW := 11
	for _, b := range r.Beads {
		stageW = max(stageW, len([]rune(stageWord(b.Stage))))
	}
	if len(r.Beads) == 0 {
		line("no bead in flight")
	} else {
		line("%s %s %s %s %s  %s %s title", left("bead", 15), left("stage", stageW), left("owner", 7),
			right("AC", 5), right("commits", 7), left("last move", 10), left("by", 8))
	}
	for _, b := range r.Beads {
		commits := "-"
		if b.Commits != nil {
			commits = fmt.Sprint(*b.Commits)
		}
		line("%s %s %s %s %s  %s %s %s", left(b.ID, 15), left(stageWord(b.Stage), stageW), left(b.Owner, 7),
			right(b.AC, 5), right(commits, 7), left(clock(b.LastMove), 10), left(cut(b.By, 8), 8), cut(b.Title, 60))
	}

	line("\nclosed %d of %d: worked by engineers %d, writing/review only %d; stuck %d, blocked %d",
		r.Closed, r.Total, r.ByEngineers, r.WritingOnly, r.Stuck, r.Blocked)
	var names []string
	for k := range r.Stages {
		names = append(names, k)
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s %d", k, r.Stages[k]))
	}
	if len(r.StuckIDs) > 0 {
		line("stuck: %s", strings.Join(r.StuckIDs, " "))
	}
	line("stages: %s", strings.Join(parts, ", "))

	fmt.Fprintf(&w, "closed in the last %d h: %d", r.Hours, len(r.ClosedRecent))
	shown := r.ClosedRecent
	if len(shown) > 8 {
		shown = shown[len(shown)-8:]
	}
	for _, b := range shown {
		fmt.Fprintf(&w, "\n  %s %s %s", clock(b.ClosedAt), b.ID, cut(b.Title, 70))
	}
	w.WriteString("\n")

	var crit []string
	for _, b := range r.CriticalPath {
		crit = append(crit, fmt.Sprintf("%s (%d wait, %s) %s", b.ID, b.Waiting, b.Stage, cut(b.Title, 50)))
	}
	critLine := strings.Join(crit, "; ")
	if critLine == "" {
		critLine = "none"
	}
	line("critical path (open, most waiting on it): %s", critLine)
	line("engineers on it: %d (%s)", len(r.Engineers), strings.Join(r.Engineers, " "))

	if len(r.Quiet) == 0 {
		line("\nquiet %s or more: none", r.StuckAfter)
	} else {
		line("\nquiet %s or more:", r.StuckAfter)
		for _, q := range r.Quiet {
			line("  %s %s: %s", q.ID, q.Owner, q.Wait)
		}
	}
	for _, n := range r.Notes {
		line("note: %s", n)
	}
	return w.String()
}

// HeaderWindow is the window the pane header's pace is counted over.
const HeaderWindow = 6 * time.Hour

// Effort is the effort a bead belongs to: its id up to and including the
// last ".", so ini-wra.2 is in ini-wra. with its siblings. A bead with no "."
// is an effort of its own. label is the effort as the header names it,
// without the dot.
func Effort(beadID string) (prefix, label string) {
	if i := strings.LastIndexByte(beadID, '.'); i > 0 {
		return beadID[:i+1], beadID[:i]
	}
	return beadID, beadID
}

// Pace is how one effort is moving, as a pane header shows it.
type Pace struct {
	Label   string
	Closed  int
	Total   int
	PerHour float64 // beads closed per hour over the window
	Stuck   int
	Blocked int
	// StuckIDs are the stuck beads, in the order they came.
	StuckIDs []string
}

// blockedStates are the bd statuses of a bead waiting on something named: it
// is counted blocked, never stuck.
var blockedStates = map[string]bool{"blocked": true, "waiting_on_nelson": true}

// Deferred reports whether a bead is parked on purpose: it is left out of
// every count and row.
func Deferred(b Bead) bool { return b.Status == "deferred" }

// stuckStates are the owner pane states that leave a quiet bead stuck: the
// owner is not working on it. A running or background owner is working, and
// an owner with no pane here is not known to be idle.
var stuckStates = map[string]bool{"idle": true, "waiting": true, "pending": true}

// Stuck is the one definition of a stuck bead: in the rig's working chain
// (rule.Chain, so a side state or a status added later never is), its last
// move (LastMoved) rule.After old or more, and its owner's pane (ownerState,
// the pane state by name; "" when the owner has no pane here) idle, waiting or
// pending.
func Stuck(b Bead, ownerState string, rule Rule, now time.Time) bool {
	if !rule.Chain[b.Status] {
		return false
	}
	return now.Sub(b.LastMoved()) >= rule.After && stuckStates[ownerState]
}

// LastMoved is the bead's last move as the pace counts it: the newer of bd's
// updated_at and Moved.
func (b Bead) LastMoved() time.Time {
	if b.Moved.After(b.UpdatedAt) {
		return b.Moved
	}
	return b.UpdatedAt
}

// PaceOf is the pace of the effort beadID belongs to: PaceOver on that
// effort's beads.
func PaceOf(beads []Bead, beadID string, panes map[string]string, rule Rule, now time.Time, window time.Duration) Pace {
	prefix, label := Effort(beadID)
	var mine []Bead
	for _, b := range beads {
		if b.ID == beadID || (strings.HasSuffix(prefix, ".") && strings.HasPrefix(b.ID, prefix)) {
			mine = append(mine, b)
		}
	}
	return PaceOver(mine, label, panes, rule, now, window)
}

// PaceOver is the one count the pane header, the overlay's effort rows and
// initech progress share: of beads, leaving out the deferred ones, the closed
// of the total, the closes per hour over window, the stuck beads (Stuck, with
// the owner's pane state from panes, by role) and the blocked ones (blocked
// or waiting on Nelson). A bead's last move is Bead.LastMoved: initech
// progress sets Moved from LastMove (comments and commits), so its pace and
// its quiet list agree; the TUI sets it from its bead poll.
func PaceOver(beads []Bead, label string, panes map[string]string, rule Rule, now time.Time, window time.Duration) Pace {
	p := Pace{Label: label}
	recent := 0
	for _, b := range beads {
		if Deferred(b) {
			continue
		}
		p.Total++
		switch {
		case b.Status == "closed":
			p.Closed++
			if !b.ClosedAt.IsZero() && now.Sub(b.ClosedAt) < window {
				recent++
			}
		case blockedStates[b.Status]:
			p.Blocked++
		case Stuck(b, panes[b.Assignee], rule, now):
			p.Stuck++
			p.StuckIDs = append(p.StuckIDs, b.ID)
		}
	}
	if window > 0 {
		p.PerHour = float64(recent) / window.Hours()
	}
	return p
}

// Line is the pace as the pane header prints it: "ini-wra 2/4 · 1/h · 0
// stuck", or "ini-abc 0/1 · 0/h" for an effort of one bead, followed by
// " · N blocked" when N is above zero; "" when bd
// listed none of it (a bead bd does not know). It never holds a time, an
// estimate or an actual.
func (p Pace) Line() string {
	if p.Total == 0 {
		return ""
	}
	rate := strconv.FormatFloat(math.Round(p.PerHour*10)/10, 'f', -1, 64)
	s := fmt.Sprintf("%s %d/%d · %s/h", p.Label, p.Closed, p.Total, rate)
	if p.Total > 1 {
		s += fmt.Sprintf(" · %d stuck", p.Stuck)
	}
	if p.Blocked > 0 {
		s += fmt.Sprintf(" · %d blocked", p.Blocked)
	}
	return s
}
