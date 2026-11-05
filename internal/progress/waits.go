package progress

// waits.go is what a waiting bead waits on and whether that wait is over. The
// bead's stated wait is its newest comment starting WAIT, an optional
// parenthetical and a colon ("WAIT: ini-abc", "WAIT (super): …"), whose
// target is the first typed token in it: a bead id of the bead's own prefix,
// a release tag vN.N.N, a GitHub run (run:<id> or an Actions run URL) or
// date:YYYY-MM-DD. Its blocks dependencies made after that comment are
// judged with it, older ones not; without a stated wait every blocks
// dependency is judged; with neither, the metadata key wait_on. The wait is
// over when every target is. This file does no I/O: a caller that can ask
// git or gh passes an External, and the caller reads the comments.

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Wait target kinds.
const (
	WaitOnBead    = "bead"
	WaitOnTag     = "tag"
	WaitOnRun     = "run"
	WaitOnDate    = "date"
	WaitOnUnknown = "unknown"
)

// WaitTarget is what one waiting bead waits on.
type WaitTarget struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
}

// String is the target as a reader sees it: "ini-abc", "tag v2.58.0",
// "run 41858021524", "2026-11-05", "unknown".
func (w WaitTarget) String() string {
	w.Name = clean(w.Name)
	switch w.Kind {
	case WaitOnTag, WaitOnRun:
		return w.Kind + " " + w.Name
	case WaitOnBead, WaitOnDate:
		return w.Name
	}
	if w.Name != "" {
		return "unknown (" + w.Name + ")"
	}
	return WaitOnUnknown
}

// WaitState is a waiting bead's target and, once it is over, the event that
// ended it: "ini-abc closed", "tag v2.58.0 exists", "run 4185 completed",
// "2026-11-05 passed".
type WaitState struct {
	Target WaitTarget
	Over   bool
	Event  string
}

// Dependency is one of a bead's dependencies as bd list --json gives it. A
// "blocks" dependency names the bead this one waits on.
type Dependency struct {
	DependsOn string    `json:"depends_on_id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

var (
	runID = regexp.MustCompile(`^[0-9]+$`)
	// tagName is the characters a git tag name may use here.
	tagName = regexp.MustCompile(`^[A-Za-z0-9._/+-]+$`)
)

// ParseWaitOn reads a wait_on value. ok is false for any other shape.
func ParseWaitOn(v string) (WaitTarget, bool) {
	kind, name, found := strings.Cut(strings.TrimSpace(v), ":")
	name = strings.TrimSpace(name)
	if !found || name == "" {
		return WaitTarget{}, false
	}
	switch kind {
	case WaitOnRun:
		if runID.MatchString(name) {
			return WaitTarget{Kind: WaitOnRun, Name: name}, true
		}
	case WaitOnTag:
		if tagName.MatchString(name) {
			return WaitTarget{Kind: WaitOnTag, Name: name}, true
		}
	case WaitOnDate:
		if _, err := time.Parse("2006-01-02", name); err == nil {
			return WaitTarget{Kind: WaitOnDate, Name: name}, true
		}
	}
	return WaitTarget{}, false
}

// waitOnTarget is b's wait_on metadata as a target, unknown when absent, and
// unknown naming the value when it does not parse.
func waitOnTarget(b Bead) WaitTarget {
	if v := b.metaString("wait_on"); v != "" {
		if t, ok := ParseWaitOn(v); ok {
			return t
		}
		return WaitTarget{Kind: WaitOnUnknown, Name: "wait_on " + cut(v, 40)}
	}
	return WaitTarget{Kind: WaitOnUnknown}
}

// statedPrefix is how a stated wait starts, after leading spaces: WAIT, an
// optional parenthetical, a colon. Upper case only; WAIT OVER:, WAIT added:,
// WAITING and prose do not match.
var statedPrefix = regexp.MustCompile(`^WAIT(\s*\([^)]*\))?:`)

var (
	tokenTag    = regexp.MustCompile(`\bv[0-9]+\.[0-9]+\.[0-9]+\b`)
	tokenRun    = regexp.MustCompile(`\brun:([0-9]+)\b`)
	tokenRunURL = regexp.MustCompile(`https?://github\.com/[^/\s]+/[^/\s]+/actions/runs/([0-9]+)`)
	tokenDate   = regexp.MustCompile(`\bdate:([0-9]{4}-[0-9]{2}-[0-9]{2})\b`)
)

// StatedWait is the target of the newest comment that is a stated wait, and
// that comment's time; ok false when there is none. A stated wait with no
// typed token has a zero target: a wait initech cannot judge.
func StatedWait(id string, comments []Comment) (WaitTarget, time.Time, bool) {
	var newest *Comment
	for i := range comments {
		c := &comments[i]
		if !statedPrefix.MatchString(strings.TrimLeft(c.Text, " \t")) {
			continue
		}
		if newest == nil || c.CreatedAt.After(newest.CreatedAt) {
			newest = c
		}
	}
	if newest == nil {
		return WaitTarget{}, time.Time{}, false
	}
	return firstToken(id, newest.Text), newest.CreatedAt, true
}

// firstToken is the first typed token in text, left to right, skipping the
// bead's own id. A bead id is one of the bead's own prefix ("ini-…", dotted
// children included). A bare hex word is never read as anything.
func firstToken(id, text string) WaitTarget {
	type hit struct {
		at int
		t  WaitTarget
	}
	var hits []hit
	if i := strings.Index(id, "-"); i > 0 {
		beadRe := regexp.MustCompile(`(^|[^A-Za-z0-9._-])(` + regexp.QuoteMeta(id[:i+1]) + `[a-z0-9]+(?:\.[0-9]+)*)\b`)
		for _, m := range beadRe.FindAllStringSubmatchIndex(text, -1) {
			if name := text[m[4]:m[5]]; name != id {
				hits = append(hits, hit{m[4], WaitTarget{Kind: WaitOnBead, Name: name}})
			}
		}
	}
	for _, m := range tokenTag.FindAllStringIndex(text, -1) {
		hits = append(hits, hit{m[0], WaitTarget{Kind: WaitOnTag, Name: text[m[0]:m[1]]}})
	}
	for _, re := range []*regexp.Regexp{tokenRun, tokenRunURL} {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			hits = append(hits, hit{m[0], WaitTarget{Kind: WaitOnRun, Name: text[m[2]:m[3]]}})
		}
	}
	for _, m := range tokenDate.FindAllStringSubmatchIndex(text, -1) {
		if _, err := time.Parse("2006-01-02", text[m[2]:m[3]]); err == nil {
			hits = append(hits, hit{m[0], WaitTarget{Kind: WaitOnDate, Name: text[m[2]:m[3]]}})
		}
	}
	best := -1
	for i, h := range hits {
		if best < 0 || h.at < hits[best].at {
			best = i
		}
	}
	if best < 0 {
		return WaitTarget{}
	}
	return hits[best].t
}

// metaString is one string key of bd's metadata object; "" when absent.
func (b Bead) metaString(key string) string {
	if len(b.Metadata) == 0 {
		return ""
	}
	raw := []byte(b.Metadata)
	var asText string
	if json.Unmarshal(raw, &asText) == nil {
		raw = []byte(asText) // bd may carry the object as a JSON string
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

// External answers what only the world knows: whether a tag exists in the
// rig's repo and whether a GitHub run has completed. exists is false for a run
// id GitHub does not know. An error leaves the wait open.
type External interface {
	TagExists(name string) (bool, error)
	RunCompleted(id string) (done, exists bool, err error)
}

// Judge is b's wait as of now: its target, and whether it is over. status maps
// every bead id the caller listed to its status. ext may be nil, and then a
// tag or a run wait stays open. A bead not in waiting has no wait.
func Judge(b Bead, status map[string]string, now time.Time, ext External) WaitState {
	return JudgeIndexed(b, Index{Status: status}, now, ext)
}

// Index is what the caller knows of every bead it listed: its status, and
// when a closed one closed (for naming the last target to end).
type Index struct {
	Status   map[string]string
	ClosedAt map[string]time.Time
}

// JudgeIndexed is Judge with closing times, so a wait with several targets
// names the one that ended last.
func JudgeIndexed(b Bead, idx Index, now time.Time, ext External) WaitState {
	return judge(b, idx, now, ext).cleaned()
}

// Clean removes control characters from text that came from bd, git or gh,
// for a caller that prints it or types it into a pane.
func Clean(s string) string { return clean(s) }

// untypedWait is the target of a stated wait initech cannot judge.
var untypedWait = WaitTarget{Kind: WaitOnUnknown, Name: "stated wait"}

func judge(b Bead, idx Index, now time.Time, ext External) WaitState {
	if b.Status != "waiting" {
		return WaitState{}
	}
	stated, at, hasStated := StatedWait(b.ID, b.WaitComments)
	var targets []WaitTarget
	if hasStated {
		if stated.Kind == "" {
			// A stated wait on something initech cannot judge: waiting,
			// never over, whatever its edges do.
			return WaitState{Target: untypedWait}
		}
		targets = append(targets, stated)
	}
	for _, d := range b.Dependencies {
		if d.Type != "blocks" || d.DependsOn == "" {
			continue
		}
		if hasStated && !d.CreatedAt.After(at) {
			continue // an edge from an earlier wait
		}
		targets = append(targets, WaitTarget{Kind: WaitOnBead, Name: d.DependsOn})
	}
	if len(targets) == 0 {
		targets = append(targets, waitOnTarget(b))
	}
	var ended []WaitState
	for _, t := range targets {
		w := judgeOne(t, idx.Status, now, ext)
		if !w.Over {
			return w // the first target still open is the wait
		}
		ended = append(ended, w)
	}
	// Every target is over: name the one that ended last. A bead's end is
	// its closing time; a stated tag, run or date has none known here, and
	// is named over the edges as the wait the bead stated.
	last := ended[0]
	for _, w := range ended[1:] {
		if last.Target.Kind != WaitOnBead {
			break
		}
		if w.Target.Kind == WaitOnBead && idx.ClosedAt[w.Target.Name].After(idx.ClosedAt[last.Target.Name]) {
			last = w
		}
	}
	return last
}

// judgeOne is one target as of now.
func judgeOne(t WaitTarget, status map[string]string, now time.Time, ext External) WaitState {
	w := WaitState{Target: t}
	switch t.Kind {
	case WaitOnBead:
		if status[t.Name] == "closed" {
			w.Over, w.Event = true, t.Name+" closed"
		}
	case WaitOnDate:
		if day, err := time.ParseInLocation("2006-01-02", t.Name, now.Location()); err == nil && !now.Before(day.AddDate(0, 0, 1)) {
			w.Over, w.Event = true, t.Name+" passed"
		}
	case WaitOnTag:
		if ext != nil {
			if ok, err := ext.TagExists(t.Name); err == nil && ok {
				w.Over, w.Event = true, "tag "+t.Name+" exists"
			}
		}
	case WaitOnRun:
		if ext != nil {
			done, exists, err := ext.RunCompleted(t.Name)
			switch {
			case err != nil:
			case !exists:
				w.Target = WaitTarget{Kind: WaitOnUnknown, Name: "no run " + t.Name}
			case done:
				w.Over, w.Event = true, "run "+t.Name+" completed"
			}
		}
	}
	return w
}

// clean the event too: it names a target that came from bd.
func (w WaitState) cleaned() WaitState {
	w.Event = clean(w.Event)
	w.Target.Name = clean(w.Target.Name)
	return w
}

// JudgeAll sets Wait on every waiting bead of beads, by Judge over the same
// list, and returns beads.
func JudgeAll(beads []Bead, now time.Time, ext External) []Bead {
	idx := Index{Status: make(map[string]string, len(beads)), ClosedAt: make(map[string]time.Time, len(beads))}
	for _, b := range beads {
		idx.Status[b.ID] = b.Status
		if !b.ClosedAt.IsZero() {
			idx.ClosedAt[b.ID] = b.ClosedAt
		}
	}
	for i := range beads {
		beads[i].Wait = JudgeIndexed(beads[i], idx, now, ext)
	}
	return beads
}

// WaitOverSentKey is the bd metadata key that records, on a waiting bead, the
// target its over line was sent for, so a restart sends it no second time.
const WaitOverSentKey = "wait_over_sent"

// WaitOverSent is the target the bead's over line was last sent for, as its
// metadata records it; "" when none.
func WaitOverSent(b Bead) string { return clean(b.metaString(WaitOverSentKey)) }
