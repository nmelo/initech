// Package appserve is the Mac-side service the initech iPhone and iPad app
// talks to (initech app-serve).
package appserve

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/progress"
)

// The blockers list: what waits on Nelson and which panes are stuck, per
// running rig. Blockers is a pure function of a Snapshot; the service fills
// the Snapshot from data it already holds (the status poll, the transcript
// tail, the progress run) and serves the result at GET /v1/blockers and as
// the blockers feed event.

// Snapshot is everything Blockers reads, as of Now.
type Snapshot struct {
	Now  time.Time
	Rigs []RigSnapshot // the running rigs, in the order the list shows them
}

// RigSnapshot is one running rig.
type RigSnapshot struct {
	Rig   string
	Panes []PaneSnapshot
	Beads []BeadSnapshot // from the latest progress run; none when none ran
}

// PaneSnapshot is one pane. State and Banner are exactly what initech status
// --json prints for it ("" for a null state or banner).
type PaneSnapshot struct {
	Name    string
	State   string  // running, idle, waiting, background, pending, stopped, suspended
	Banner  string  // none, permission_prompt, trust_prompt, picker, login, unknown
	Replies []Reply // the super pane only: its agent replies, oldest first
}

// Reply is one agent reply in a transcript: its text and its footer lines
// (the lines starting with the ▶ marker).
type Reply struct {
	Text   string
	Footer []string
}

// BeadSnapshot is one bead of the rig as the progress run read it. LastMove
// is the bead's last move as initech progress reads it; zero when unknown.
type BeadSnapshot struct {
	ID       string
	Status   string
	Assignee string
	LastMove time.Time
}

// RigBlockers is one rig's row in the blockers list. Quiet is true when the
// rig has nothing in either list.
type RigBlockers struct {
	Rig     string        `json:"rig"`
	Waiting []WaitingItem `json:"waiting"`
	Stuck   []StuckPane   `json:"stuck"`
	Quiet   bool          `json:"quiet"`
}

// WaitingItem is one item waiting on Nelson: a ▶ line of a super's footer.
// Recommendation is the reply's Recommendation line, on the reply's first
// item only (the open question), and "" when the reply has none.
type WaitingItem struct {
	Pane           string `json:"pane"`
	Text           string `json:"text"`
	Recommendation string `json:"recommendation"`
}

// StuckPane is a pane that cannot act, with the reason in plain words.
type StuckPane struct {
	Pane   string `json:"pane"`
	Reason string `json:"reason"`
}

// superPaneName is the pane whose footer lists what waits on Nelson.
const superPaneName = "super"

// bannerReasons are the stuck reasons for the banners status --json names;
// any other banner but none reads as a dialog.
var bannerReasons = map[string]string{
	"permission_prompt": "permission prompt open",
	"trust_prompt":      "trust prompt open",
	"picker":            "picker open",
	"login":             "login screen open",
}

// stateReasons are the stuck reasons for pane states that cannot act.
var stateReasons = map[string]string{
	"waiting": "waiting at a prompt",
	"pending": "text left unsent in the input box",
}

// Blockers is the blockers list for s: one row per rig, in order.
func Blockers(s Snapshot) []RigBlockers {
	out := make([]RigBlockers, 0, len(s.Rigs))
	for _, rig := range s.Rigs {
		row := RigBlockers{Rig: rig.Rig, Waiting: []WaitingItem{}, Stuck: []StuckPane{}}
		for _, p := range rig.Panes {
			if p.Name == superPaneName {
				row.Waiting = append(row.Waiting, waitingItems(p)...)
			}
			for _, reason := range stuckReasons(p, rig.Beads, s.Now) {
				row.Stuck = append(row.Stuck, StuckPane{Pane: p.Name, Reason: reason})
			}
		}
		row.Quiet = len(row.Waiting) == 0 && len(row.Stuck) == 0
		out = append(out, row)
	}
	return out
}

// waitingItems are the items of p's latest reply that carries a footer. A
// reply with no footer clears nothing; a footer of "▶ nothing" has no items.
func waitingItems(p PaneSnapshot) []WaitingItem {
	for i := len(p.Replies) - 1; i >= 0; i-- {
		r := p.Replies[i]
		if len(r.Footer) == 0 {
			continue
		}
		var items []WaitingItem
		for _, line := range r.Footer {
			if text := footerItem(line); text != "" {
				items = append(items, WaitingItem{Pane: p.Name, Text: text})
			}
		}
		if len(items) > 0 {
			items[0].Recommendation = recommendationOf(r.Text)
		}
		return items
	}
	return nil
}

// footerItem is a footer line's item text with its "- ▶" marker stripped, or
// "" for a line that is not an item (no marker, empty, or "nothing").
func footerItem(line string) string {
	s := strings.TrimSpace(line)
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "*"))
	if !strings.HasPrefix(s, "▶") {
		return ""
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "▶"))
	if strings.EqualFold(strings.TrimSuffix(s, "."), "nothing") {
		return ""
	}
	return s
}

// recommendationLine matches a line that gives a recommendation, in the
// shapes the supers write it: "Recommendation: X", "**Recommendation:** X",
// "3. **Recommendation.** X", "My recommendation: X".
var recommendationLine = regexp.MustCompile(`(?i)^\s*(?:[-*]\s+|\d+[.)]\s+)?[*_]*\s*(?:my\s+)?recommendation\b[*_]*\s*[:.\-—–]?[*_]*\s*(.*)$`)

// recommendationOf is the first recommendation a reply's text gives, or "".
func recommendationOf(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if m := recommendationLine.FindStringSubmatch(line); m != nil {
			if rec := strings.TrimSpace(m[1]); rec != "" {
				return rec
			}
		}
	}
	return ""
}

// stuckReasons are the reasons p cannot act: an open banner or a state that
// cannot act, a bead of its quiet an hour while it is idle, and, for a QA
// pane, idle while work waits for QA.
func stuckReasons(p PaneSnapshot, beads []BeadSnapshot, now time.Time) []string {
	var reasons []string
	switch {
	case p.Banner != "" && p.Banner != "none":
		reason, ok := bannerReasons[p.Banner]
		if !ok {
			reason = "a dialog is open"
		}
		reasons = append(reasons, reason)
	case stateReasons[p.State] != "":
		reasons = append(reasons, stateReasons[p.State])
	}
	if p.State != "idle" {
		return reasons
	}
	for _, b := range beads {
		if b.Assignee != p.Name || b.Status == "open" || b.Status == "closed" || b.LastMove.IsZero() {
			continue
		}
		if quiet := now.Sub(b.LastMove); quiet >= progress.QuietAfter {
			reasons = append(reasons, fmt.Sprintf("%s quiet %s, its owner idle", b.ID, hoursMinutes(quiet)))
		}
	}
	if strings.HasPrefix(p.Name, "qa") {
		for _, b := range beads {
			if b.Status == "ready_for_qa" || b.Status == "in_qa" && (b.Assignee == "" || b.Assignee == p.Name) {
				reasons = append(reasons, "idle while "+b.ID+" waits for QA")
				break
			}
		}
	}
	return reasons
}

// hoursMinutes prints d as "1h30m".
func hoursMinutes(d time.Duration) string {
	m := int(d / time.Minute)
	return fmt.Sprintf("%dh%02dm", m/60, m%60)
}
