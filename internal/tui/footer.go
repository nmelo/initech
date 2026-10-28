package tui

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// Footer is what the rows under Claude Code's input box say about an agent,
// for initech status --json (ini-l6om). A nil field was not read: status
// reports it as null, never a guess.
//
// MEASURED on Claude Code 2.1.310 (Haiku). Under the input box's bottom rule:
//
//	Haiku 5.5 │ Personal │ 8% │ 75% ▓▓▓┃▓▓▓▓░░ → 06:50 PM │ 20% ▓▓┃░░░░░░░
//	⏸ manual mode on · 1 shell, 1 monitor
//	⏺ main
//	◯ general-purpose  Run countdown loop, report last number    1m 4s
//
// The first row is not Claude Code's: it is the operator's status line
// (cla statusline): model, account name, context used, then the five-hour
// and weekly use, which are not read. The second is Claude's mode line with
// its background counts; the rows under "⏺ main" are background agents.
// While a dialog is open these rows are gone, and so is every value.
type Footer struct {
	ContextPercent *int
	Shells         *int
	Monitors       *int
	Tasks          *int
	Model          *string
	User           *string
}

// parseFooter reads the footer rows of a screen width columns wide. Only rows
// below the lowest input-box frame are read, so a footer quoted in the
// transcript never parses; no frame (a dialog, a shell, an empty pane) reads
// nothing.
func parseFooter(rows []string, width int) Footer {
	var f Footer
	bottom, ok := footerFrameBottom(rows, width)
	if !ok {
		return f
	}
	below := rows[bottom+1:]
	i := 0
	for i < len(below) && strings.TrimSpace(below[i]) == "" {
		i++
	}
	if i == len(below) {
		return f
	}
	if readStatusLine(below[i], &f) {
		i++
	}
	for ; i < len(below); i++ {
		if readModeLine(below[i], width, &f) {
			f.Tasks = backgroundAgents(below[i+1:])
			break
		}
	}
	return f
}

// footerFrameBottom finds the bottom rule of the lowest input-box frame, as
// lowestComposerFrame does, but a rule need only span the pane: in a pane
// narrower than composerRuleMinRunes the box's rules are shorter than that,
// and the footer under them is still Claude's (measured at 34 and 30
// columns, ini-67k7). lowestComposerFrame keeps its floor for the send path.
//
// The TOP rule may end in a label: Claude Code draws the session name there
// after /rename ("──── pa ─"). MEASURED on 2.1.310 (theguy's pa pane, and
// Haiku panes renamed pa and accountant at 94 and 40 columns): one short
// label, then " ─", at the rule's end; the bottom rule is never labelled.
// Without this every field of a renamed agent read null (ini-2tec).
func footerFrameBottom(rows []string, width int) (int, bool) {
	_, bottom, ok := composerFrame(rows, width)
	return bottom, ok
}

// labelledRule is a top rule ending in a label: the rule, one space, a short
// label with no rule or bar characters, then " ─".
var labelledRule = regexp.MustCompile(`^(\x{2500}+) [^\x{2500}\x{2502}]{1,40} \x{2500}$`)

// statusSep separates the status line's segments.
const statusSep = " │ "

// contextSegment is the context-used segment: a whole percent, 0 to 100.
var contextSegment = regexp.MustCompile(`^(\d{1,3})%$`)

// cutContextSegment is the line's last segment when Claude cut the line
// right after the percent: "0% │…", "0% …", "0%…", or the line ending at it.
// MEASURED on 2.1.310 at 39, 38 and 37 columns (ini-gdym). The % sign is on
// screen, so the number before it is whole; "8…", cut inside, is not this.
var cutContextSegment = regexp.MustCompile(`^(\d{1,3})%( │…| …|…)?$`)

// modelSegment is a segment that names a model: "Haiku 5.5", "Opus 5.5",
// "Claude Sonnet 4.6".
var modelSegment = regexp.MustCompile(`^(Claude )?(Haiku|Sonnet|Opus|Fable|Claude)( [A-Za-z]+)? \d+(\.\d+)*\b`)

// readStatusLine reads model, account name and context percent from the
// operator's status line BY SHAPE, never by position (ini-67k7): the line
// leads with a "⎇ branch" segment when the agent's folder is a git repo,
// and position shifted every field. The context is the first segment that
// is exactly a whole percent; the account name is the segment right before
// it; the model is the nearest earlier segment shaped like a model name.
// Anything else is skipped. A segment counts only when the separator after
// it is on screen, so one cut at the pane's edge or with "…" is not read
// ("8…" may be 80). It reports whether row had the status line's shape.
func readStatusLine(row string, f *Footer) bool {
	segs := strings.Split(strings.TrimSpace(row), statusSep)
	if len(segs) < 2 {
		return false
	}
	closed := segs[:len(segs)-1]
	for _, s := range closed {
		if s == "" || strings.Contains(s, "…") {
			return false
		}
	}
	pct := -1
	sawPercent := false
	for i, s := range closed {
		if m := contextSegment.FindStringSubmatch(s); m != nil {
			sawPercent = true
			if n, err := strconv.Atoi(m[1]); err == nil && n <= 100 {
				pct = i
				f.ContextPercent = &n
			}
			break
		}
	}
	// The first percent-shaped segment decides: an earlier one, even out of
	// range, leaves the cut last segment unread.
	if !sawPercent {
		if m := cutContextSegment.FindStringSubmatch(segs[len(segs)-1]); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n <= 100 {
				pct = len(closed)
				f.ContextPercent = &n
			}
		}
	}
	limit := len(closed)
	if pct >= 0 {
		limit = pct
		if pct > 0 {
			f.User = &closed[pct-1]
		}
	}
	for i := limit - 1; i >= 0; i-- {
		if modelSegment.MatchString(closed[i]) {
			f.Model = &closed[i]
			break
		}
	}
	return f.Model != nil || f.User != nil || f.ContextPercent != nil
}

// footerRightMargin is the columns Claude Code leaves free at the right of
// its footer rows. MEASURED on 2.1.310 at 40 columns: the cut mode line
// ("... 1 shell, 1 monito") and the status line cut with "…" both end at
// column 38. A row that reaches it is read as cut.
const footerRightMargin = 2

// modeLineHead is the start of the mode line: "⏸ manual mode on",
// "⏵⏵ accept edits on", then optionally the hint, whole or cut:
// "⏵⏵ auto mode on (shift+tab to cycle)", "⏵⏵ auto mode on (shift+tab to".
var modeLineHead = regexp.MustCompile(`^(⏸|⏵⏵) [a-z ]+ on( \(|$)`)

// MEASURED on 2.1.310 in all four modes (ini-67k7): the "(shift+tab to
// cycle)" hint is drawn only when there are no background words; with a
// shell, every mode drops it. The line never wraps in a narrow pane: it is
// cut, often inside the hint.
const modeLineHint = " ("

// backgroundCount is one whole count on the mode line: "1 shell", "2 monitors".
var backgroundCount = regexp.MustCompile(`^(\d+) (shell|monitor)s?$`)

// readModeLine reads the background shell and monitor counts from Claude's
// mode line, reporting whether row is one. Counts are whole words only. A
// mode line with no count says there is none: 0. A row that runs to the
// pane's edge may hide more, so a kind it does not show is not read.
func readModeLine(row string, width int, f *Footer) bool {
	row = strings.TrimRight(row, " ")
	segs := strings.Split(strings.TrimSpace(row), " · ")
	if !modeLineHead.MatchString(segs[0]) {
		return false
	}
	if strings.Contains(segs[0], modeLineHint) {
		zero := 0
		f.Shells, f.Monitors = &zero, &zero
		return true
	}
	cut := runewidth.StringWidth(row) >= width-footerRightMargin
	var shells, monitors *int
	for _, seg := range segs[1:] {
		for _, item := range strings.Split(seg, ", ") {
			m := backgroundCount.FindStringSubmatch(item)
			if m == nil {
				continue
			}
			n, err := strconv.Atoi(m[1])
			if err != nil {
				continue
			}
			if m[2] == "shell" {
				shells = &n
			} else {
				monitors = &n
			}
		}
	}
	if !cut {
		zero := 0
		if shells == nil {
			shells = &zero
		}
		if monitors == nil {
			monitors = &zero
		}
	}
	f.Shells, f.Monitors = shells, monitors
	return true
}

// backgroundAgents counts the background agents listed under the mode line:
// the "◯" rows under "⏺ main". No list is none.
func backgroundAgents(rows []string) *int {
	n := 0
	inList := false
	for _, r := range rows {
		t := strings.TrimSpace(r)
		switch {
		case t == "⏺ main":
			inList = true
		case inList && strings.HasPrefix(t, "◯ "):
			n++
		}
	}
	return &n
}

// Banner kinds reported by status --json. limit (the usage-limit banner) has
// no recogniser: no specimen could be produced on demand, so a limit banner
// that latches reads unknown.
const (
	bannerNone       = "none"
	bannerPermission = "permission_prompt"
	bannerTrust      = "trust_prompt"
	bannerPicker     = "picker"
	bannerLogin      = "login"
	bannerUnknown    = "unknown"
)

// bannerKind names the dialog open on screen, read where a live dialog can be
// drawn (liveDialogRows), so dialog words in the transcript above a resting
// input box are not a dialog. A latched dialog no recogniser knows is
// unknown; nothing open is none.
func bannerKind(rows []string, latched bool) string {
	text := strings.Join(liveDialogRows(rows), "\n")
	low := strings.ToLower(text)
	switch {
	// MEASURED: Claude Code 2.1.310 with an empty config folder, after the
	// theme step: "Select login method:" over "❯ 1. Claude account with
	// subscription ...".
	case strings.Contains(low, "select login method:") && screenOffersAnswer(text):
		return bannerLogin
	// MEASURED: 2.1.310 and earlier, a new folder: "❯ No, exit" / "Yes, I
	// trust this folder" over "Enter to confirm · Esc to cancel".
	case strings.Contains(low, "yes, i trust this folder") && strings.Contains(low, "enter to confirm"):
		return bannerTrust
	case permissionPromptShape(text):
		return bannerPermission
	// MEASURED: 2.1.310 AskUserQuestion: "Enter to select · ↑/↓ to navigate
	// · Esc to cancel" under numbered options.
	case strings.Contains(low, "to navigate") && screenOffersAnswer(text):
		return bannerPicker
	case latched:
		return bannerUnknown
	}
	return bannerNone
}

// backgroundWork is a pane's background work and since when (ini-b4l0).
// Zero when none. seenFor marks an age that is how long the footer has shown
// work, not a process's start (ini-d2sx): the tree could not be read, or the
// only work is background agents, which run inside claude.
type backgroundWork struct {
	shells, monitors, tasks int
	since                   time.Time
	seenFor                 bool
}

// busy reports whether any background work is recorded.
func (w backgroundWork) busy() bool { return w.shells+w.monitors+w.tasks > 0 }

// observeBackground is observeBackgroundTree with no process tree: the
// footer's reading, its age labelled as seen for.
func (p *Pane) observeBackground(now time.Time, alive bool, f Footer) {
	p.observeBackgroundTree(now, alive, f, nil, false)
}

// observeBackgroundTree records what the footer read this tick says about
// background work, checked against the pane's running background shells
// (starts, read from the process tree when treeOK).
//
// The footer names the kinds. A kind it did not show readably keeps its last
// value, so a dialog over the footer or a cut row changes nothing; the
// no-background shape (every count read as 0) clears it, and so does the
// process ending.
//
// The tree says what runs (ini-d2sx): Claude Code keeps a finished shell
// listed until it is read, so shells and monitors shown are the footer's
// capped at the shells running, and the age is the oldest running one's
// start. Listed but none running, and no background agents: no work, and the
// pane reads idle. Background agents run inside claude and have no process;
// they, and a pane whose tree was not read, keep the footer's age, labelled
// seen for.
func (p *Pane) observeBackgroundTree(now time.Time, alive bool, f Footer, starts []time.Time, treeOK bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !alive {
		p.bgWork, p.bgFooter = backgroundWork{}, backgroundWork{}
		return
	}
	raw := p.bgFooter
	if f.Shells != nil {
		raw.shells = *f.Shells
	}
	if f.Monitors != nil {
		raw.monitors = *f.Monitors
	}
	if f.Tasks != nil {
		raw.tasks = *f.Tasks
	}
	switch {
	case !raw.busy():
		raw = backgroundWork{}
	case raw.since.IsZero():
		raw.since = now
	}
	p.bgFooter = raw

	w := backgroundWork{shells: raw.shells, monitors: raw.monitors, tasks: raw.tasks, since: raw.since, seenFor: true}
	if treeOK {
		w.shells = min(raw.shells, len(starts))
		w.monitors = min(raw.monitors, len(starts)-w.shells)
		if w.shells+w.monitors > 0 {
			w.since, w.seenFor = starts[0], false
			for _, s := range starts[1:] {
				if s.Before(w.since) {
					w.since = s
				}
			}
		}
	}
	if !w.busy() {
		w = backgroundWork{}
	}
	p.bgWork = w
}

// footerBusy reports whether the pane's footer last showed background work,
// which is when its process tree is worth reading.
func (p *Pane) footerBusy() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bgFooter.busy()
}

// backgroundWorkNow returns the recorded background work and whether there is
// any.
func (p *Pane) backgroundWorkNow() (backgroundWork, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bgWork, p.bgWork.busy()
}

// backgroundAge is how long a pane's background work has been seen, for the
// overlay row; ok is false when it has none. Local panes read the tick's
// record, a second window's panes the record window 1 sent (ini-pi8p).
func backgroundAge(pv PaneView, now time.Time) (time.Duration, bool) {
	var w backgroundWork
	var busy bool
	switch p := pv.(type) {
	case *Pane:
		w, busy = p.backgroundWorkNow()
	case *RemotePane:
		w, busy = p.backgroundWorkNow()
	}
	if !busy {
		return 0, false
	}
	return now.Sub(w.since), true
}

// backgroundSeenFor reports whether a local pane's background age is how
// long the footer has shown the work rather than a process's age. A second
// window's record carries no such mark and prints as window 1 sent it.
func backgroundSeenFor(pv PaneView) bool {
	p, ok := pv.(*Pane)
	if !ok {
		return false
	}
	w, busy := p.backgroundWorkNow()
	return busy && w.seenFor
}

// shortAge is an age as the overlay prints it: 12s, 4m, 2h.
func shortAge(d time.Duration) string {
	switch s := int(d.Seconds()); {
	case s >= 3600:
		return strconv.Itoa(s/3600) + "h"
	case s >= 60:
		return strconv.Itoa(s/60) + "m"
	default:
		return strconv.Itoa(max(s, 0)) + "s"
	}
}

// paneBusy is a pane's background work for the list and patrol replies; nil
// when there is none or the pane is not a local one.
func paneBusy(pv PaneView, now time.Time) *PaneBusy {
	var w backgroundWork
	var busy bool
	switch p := pv.(type) {
	case *Pane:
		w, busy = p.backgroundWorkNow()
	case *RemotePane:
		w, busy = p.backgroundWorkNow()
	}
	if !busy {
		return nil
	}
	return &PaneBusy{Shells: w.shells, Monitors: w.monitors, Tasks: w.tasks, SinceSec: int(now.Sub(w.since).Seconds()), SeenFor: w.seenFor}
}
