package progress

// waits.go is what a waiting bead waits on and whether that wait is over. The
// target is bd's own where bd has one (a dependency on another bead), else the
// one metadata key wait_on: run:<github run id>, tag:<name> or
// date:<YYYY-MM-DD>. A waiting bead with neither waits on "unknown". This file
// does no I/O: a caller that can ask git or gh passes an External.

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
	DependsOn string `json:"depends_on_id"`
	Type      string `json:"type"`
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

// TargetOf is what b waits on: its first "blocks" dependency, else its
// wait_on metadata, else unknown. A wait_on that does not parse is unknown,
// naming the value.
func TargetOf(b Bead) WaitTarget {
	for _, d := range b.Dependencies {
		if d.Type == "blocks" && d.DependsOn != "" {
			return WaitTarget{Kind: WaitOnBead, Name: d.DependsOn}
		}
	}
	if v := b.metaString("wait_on"); v != "" {
		if t, ok := ParseWaitOn(v); ok {
			return t
		}
		return WaitTarget{Kind: WaitOnUnknown, Name: "wait_on " + cut(v, 40)}
	}
	return WaitTarget{Kind: WaitOnUnknown}
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
	if b.Status != "waiting" {
		return WaitState{}
	}
	t := TargetOf(b)
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
	return w
}

// JudgeAll sets Wait on every waiting bead of beads, by Judge over the same
// list, and returns beads.
func JudgeAll(beads []Bead, now time.Time, ext External) []Bead {
	status := make(map[string]string, len(beads))
	for _, b := range beads {
		status[b.ID] = b.Status
	}
	for i := range beads {
		beads[i].Wait = Judge(beads[i], status, now, ext).cleaned()
	}
	return beads
}
