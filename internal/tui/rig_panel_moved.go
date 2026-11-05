package tui

// rig_panel_moved.go is the rig panel's moved 30m box (ini-9z0b): what moved
// in the last half hour, by effort. An effort is an in-progress or waiting
// bead with children, by a parent-child link or a dotted id. Each effort is
// one row (its beads closed over all, its recent closes per hour, its
// flags), then the beads under it that moved, newest first; nested efforts
// follow their parent's beads; beads under no effort hang under a last row
// named for the rig. Closed and archived beads never show. The rules are
// pure over movedBead; the draw only lays out the rows.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	// movedWindow is how far back a bead's last move counts.
	movedWindow = 30 * time.Minute
	// movedRateWindow is the hours an effort's closes per hour average over.
	movedRateWindow = 3 * time.Hour
	// movedTitleCut is the most a bead row's title shows before its ….
	movedTitleCut = 56
)

// movedBead is one bead as the box reads it.
type movedBead struct {
	ID, Title, Status string
	Parent            string // Its parent's id, "" for a root.
	Priority          int
	Archived          bool
	LastMoved         time.Time
	ClosedAt          time.Time
	Kind              string // The kind of its last change; "" when not known.
}

// movedRow is one row of the box: an effort, a bead, the rig's last row, or
// a "+n more" tail.
type movedRow struct {
	Depth  int    // Nesting under efforts, for the indent.
	Effort bool   // An effort row (or the rig's last row).
	ID     string // "" for the rig's last row and a tail.
	Status string
	Title  string
	// Effort rows.
	Done     string // "closed/total"
	Rate     string // "0.5/h", or "0/h" (dim) or "—" with no beads to close
	RateZero bool
	Flags    []movedFlag
	// Bead rows.
	Kind string
	Ago  string
	// A tail: "+n more" under the effort that lost rows.
	More int
}

// movedFlag is one effort flag: "3 stale", "1 blocked", "1 on Nelson",
// "1 waiting". Waiting is the yellow one; the rest are red.
type movedFlag struct {
	Text    string
	Waiting bool
}

// beadKinds is where the kind of a bead's last change comes from; ☾
// ini-u2xf fills it from the beads journal. nil uses kindFromStatus.
type beadKinds interface {
	Kind(id string) (kind string, ok bool)
}

// kindFromStatus is the kind the bead poll alone can tell: from the status,
// and filed for an open bead filed inside the window. It never says comment
// or commit, which it cannot tell from a status.
func kindFromStatus(b movedBead, created time.Time, now time.Time) string {
	switch b.Status {
	case "waiting_on_nelson":
		return "nelson"
	case "blocked":
		return "blocked"
	case "waiting":
		return "waiting"
	case "ready_for_qa", "in_qa", "qa_passed":
		return "qa"
	case "in_progress":
		return "claimed"
	case "open":
		if !created.IsZero() && now.Sub(created) <= movedWindow {
			return "filed"
		}
	}
	return ""
}

// isEffort reports whether b is an effort, given which ids have children.
func isEffort(b movedBead, hasChildren map[string]bool) bool {
	return hasChildren[b.ID] && (b.Status == "in_progress" || b.Status == "waiting") && !b.Archived
}

// movedRows lays out the box for beads at now, in at most rows rows (a tail
// counts as a row); rig names the last row.
func movedRows(beads []movedBead, now time.Time, rows int, rig string) []movedRow {
	byID := make(map[string]movedBead, len(beads))
	children := map[string][]string{}
	for _, b := range beads {
		byID[b.ID] = b
	}
	for _, b := range beads {
		if b.Parent != "" {
			if _, ok := byID[b.Parent]; ok {
				children[b.Parent] = append(children[b.Parent], b.ID)
			}
		}
	}
	hasChildren := map[string]bool{}
	for id, c := range children {
		hasChildren[id] = len(c) > 0
	}
	// nearestEffort is the effort a bead hangs under, "" for none.
	nearestEffort := func(b movedBead) string {
		seen := map[string]bool{}
		for p := b.Parent; p != "" && !seen[p]; p = byID[p].Parent {
			seen[p] = true
			if pb, ok := byID[p]; ok && isEffort(pb, hasChildren) {
				return p
			}
		}
		return ""
	}
	// Every bead under an effort, nested efforts' included, for its numbers.
	var under func(id string, seen map[string]bool) []movedBead
	under = func(id string, seen map[string]bool) []movedBead {
		var out []movedBead
		for _, c := range children[id] {
			if seen[c] {
				continue
			}
			seen[c] = true
			cb := byID[c]
			if !cb.Archived {
				out = append(out, cb)
			}
			out = append(out, under(c, seen)...)
		}
		return out
	}
	moved := func(b movedBead) bool {
		return b.Status != "closed" && !b.Archived && !b.LastMoved.IsZero() && now.Sub(b.LastMoved) <= movedWindow
	}

	// The efforts in tree order: roots by priority then id, each followed
	// by its nested efforts.
	var efforts []movedBead
	for _, b := range beads {
		if isEffort(b, hasChildren) {
			efforts = append(efforts, b)
		}
	}
	sort.Slice(efforts, func(i, j int) bool {
		if efforts[i].Priority != efforts[j].Priority {
			return efforts[i].Priority < efforts[j].Priority
		}
		return efforts[i].ID < efforts[j].ID
	})
	parentEffort := map[string]string{}
	for _, e := range efforts {
		parentEffort[e.ID] = nearestEffort(e)
	}
	type group struct {
		effort *movedBead
		depth  int
		beads  []movedBead // Moved beads directly under it, newest first.
	}
	var groups []group
	var add func(e movedBead, depth int)
	add = func(e movedBead, depth int) {
		eb := e
		groups = append(groups, group{effort: &eb, depth: depth})
		for _, n := range efforts {
			if parentEffort[n.ID] == e.ID {
				add(n, depth+1)
			}
		}
	}
	for _, e := range efforts {
		if parentEffort[e.ID] == "" {
			add(e, 0)
		}
	}
	at := map[string]int{}
	for i, g := range groups {
		at[g.effort.ID] = i
	}
	var loose []movedBead
	for _, b := range beads {
		if !moved(b) || isEffort(b, hasChildren) {
			continue
		}
		if e := nearestEffort(b); e != "" {
			if i, ok := at[e]; ok {
				groups[i].beads = append(groups[i].beads, b)
				continue
			}
		}
		loose = append(loose, b)
	}
	newest := func(l []movedBead) {
		sort.SliceStable(l, func(i, j int) bool { return l[i].LastMoved.After(l[j].LastMoved) })
	}
	for i := range groups {
		newest(groups[i].beads)
	}
	newest(loose)
	if len(loose) > 0 {
		groups = append(groups, group{beads: loose})
	}

	// The budget: every effort row first, then the newest bead rows across
	// the box, a "+n more" tail under each effort that lost rows.
	budget := rows - len(groups)
	if budget < 0 {
		budget = 0
	}
	type pick struct{ g, i int }
	var all []pick
	for gi, g := range groups {
		for bi := range g.beads {
			all = append(all, pick{gi, bi})
		}
	}
	sort.SliceStable(all, func(a, b int) bool {
		return groups[all[a].g].beads[all[a].i].LastMoved.After(groups[all[b].g].beads[all[b].i].LastMoved)
	})
	shown := map[pick]bool{}
	// A tail takes a row, so shown rows plus the tails stay within budget.
	for n := len(all); n >= 0; n-- {
		cut := map[int]bool{}
		for _, p := range all[n:] {
			cut[p.g] = true
		}
		if n+len(cut) <= budget || n == 0 {
			for _, p := range all[:n] {
				shown[p] = true
			}
			break
		}
	}

	var out []movedRow
	for gi, g := range groups {
		if g.effort == nil {
			out = append(out, movedRow{Effort: true, Title: rig})
		} else {
			e := *g.effort
			out = append(out, effortRow(e, g.depth, under(e.ID, map[string]bool{}), now))
		}
		lost := 0
		for bi, b := range g.beads {
			if !shown[pick{gi, bi}] {
				lost++
				continue
			}
			out = append(out, movedRow{Depth: g.depth + 1, ID: b.ID, Status: b.Status, Title: b.Title,
				Kind: b.Kind, Ago: panelAge(b.LastMoved, now)})
		}
		if lost > 0 {
			out = append(out, movedRow{Depth: g.depth + 1, More: lost})
		}
	}
	return out
}

// effortRow is an effort's row from the beads under it.
func effortRow(e movedBead, depth int, kids []movedBead, now time.Time) movedRow {
	r := movedRow{Depth: depth, Effort: true, ID: e.ID, Status: e.Status, Title: e.Title}
	closed, stale, blocked, nelson, waiting, recent := 0, 0, 0, 0, 0, 0
	for _, k := range kids {
		switch k.Status {
		case "closed":
			closed++
			if !k.ClosedAt.IsZero() && now.Sub(k.ClosedAt) <= movedRateWindow {
				recent++
			}
		case "blocked":
			blocked++
		case "waiting_on_nelson":
			nelson++
		case "waiting":
			waiting++
		case "in_progress":
			if !k.LastMoved.IsZero() && now.Sub(k.LastMoved) >= panelAttentionAfter {
				stale++
			}
		}
	}
	r.Done = fmt.Sprintf("%d/%d", closed, len(kids))
	if len(kids) == 0 {
		r.Rate = "—"
	} else {
		perHour := float64(recent) / movedRateWindow.Hours()
		r.Rate = strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", perHour), "0"), ".") + "/h"
		r.RateZero = recent == 0
	}
	for _, f := range []struct {
		n       int
		word    string
		waiting bool
	}{{stale, "stale", false}, {blocked, "blocked", false}, {nelson, "on Nelson", false}, {waiting, "waiting", true}} {
		if f.n > 0 {
			r.Flags = append(r.Flags, movedFlag{Text: fmt.Sprintf("%d %s", f.n, f.word), Waiting: f.waiting})
		}
	}
	return r
}
