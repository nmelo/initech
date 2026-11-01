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
	"regexp"
	"sort"
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

// QuietAfter is how long a bead goes without a move before its wait is read.
const QuietAfter = time.Hour

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

// Render is the report as initech progress prints it: the header, the table
// of beads past open, the effort lines, the wait of every quiet bead, then
// the notes.
func Render(in Input) string {
	var w strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&w, format+"\n", a...) }

	var beads []Bead
	for _, b := range in.Beads {
		if strings.HasPrefix(b.ID, in.Prefix) {
			beads = append(beads, b)
		}
	}

	line("%s %s* at %s ET; commits counted over the last %d h\n", in.Rig, in.Prefix, hhmm(in.Now), in.Hours)
	line("%s %s %s %s %s  %s %s title", left("bead", 15), left("stage", 11), left("owner", 7),
		right("AC", 5), right("commits", 7), left("last move", 10), left("by", 8))

	var active []Bead
	for _, b := range beads {
		if b.Status != "open" && b.Status != "closed" && !strings.HasPrefix(b.Title, "Step ") {
			active = append(active, b)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].Status < active[j].Status })
	type quiet struct {
		b    Bead
		wait string
	}
	var quiets []quiet
	for _, b := range active {
		cs := in.Comments[b.ID]
		commits := "-"
		if in.CommitsKnown {
			commits = fmt.Sprint(len(beadCommits(b, in.Commits)))
		}
		last, by := LastMove(b, cs, in.Commits)
		owner := b.Assignee
		if owner == "" {
			owner = "-"
		}
		line("%s %s %s %s %s  %s %s %s", left(b.ID, 15), left(b.Status, 11), left(owner, 7),
			right(ACCell(b, cs), 5), right(commits, 7), left(clock(last), 10), left(cut(by, 8), 8), cut(b.Title, 60))
		if !last.IsZero() && in.Now.Sub(last) >= QuietAfter {
			quiets = append(quiets, quiet{b, Wait(b, cs, in)})
		}
	}

	var closed []Bead
	build := 0
	stages := map[string]int{}
	for _, b := range beads {
		stages[b.Status]++
		if b.Status != "closed" {
			continue
		}
		closed = append(closed, b)
		for _, c := range in.Comments[b.ID] {
			if isEngineer(c.Author) {
				build++
				break
			}
		}
	}
	line("\nclosed %d of %d: worked by engineers %d, writing/review only %d", len(closed), len(beads), build, len(closed)-build)
	var names []string
	for k := range stages {
		names = append(names, k)
	}
	sort.Strings(names)
	var parts []string
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s %d", k, stages[k]))
	}
	line("stages: %s", strings.Join(parts, ", "))

	var recent []Bead
	for _, b := range closed {
		if !b.ClosedAt.IsZero() && in.Now.Sub(b.ClosedAt) < time.Duration(in.Hours)*time.Hour {
			recent = append(recent, b)
		}
	}
	sort.SliceStable(recent, func(i, j int) bool { return recent[i].ClosedAt.Before(recent[j].ClosedAt) })
	fmt.Fprintf(&w, "closed in the last %d h: %d", in.Hours, len(recent))
	shown := recent
	if len(shown) > 8 {
		shown = shown[len(shown)-8:]
	}
	for _, b := range shown {
		fmt.Fprintf(&w, "\n  %s %s %s", clock(b.ClosedAt), b.ID, cut(b.Title, 70))
	}
	w.WriteString("\n")

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
	var crit []string
	for _, b := range path {
		crit = append(crit, fmt.Sprintf("%s (%d wait, %s) %s", b.ID, b.DependentCount, b.Status, cut(b.Title, 50)))
	}
	critLine := strings.Join(crit, "; ")
	if critLine == "" {
		critLine = "none"
	}
	line("critical path (open, most waiting on it): %s", critLine)

	engSet := map[string]bool{}
	for _, b := range active {
		if strings.HasPrefix(b.Assignee, "eng") {
			engSet[b.Assignee] = true
		}
	}
	var engs []string
	for e := range engSet {
		engs = append(engs, e)
	}
	sort.Strings(engs)
	line("engineers on it: %d (%s)", len(engs), strings.Join(engs, " "))

	if len(quiets) == 0 {
		line("\nquiet an hour or more: none")
	} else {
		line("\nquiet an hour or more:")
		for _, q := range quiets {
			owner := q.b.Assignee
			if owner == "" {
				owner = "-"
			}
			line("  %s %s: %s", q.b.ID, owner, q.wait)
		}
	}
	for _, n := range in.Notes {
		line("note: %s", n)
	}
	return w.String()
}
