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
	_, bottom, ok := lowestComposerFrame(rows)
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

// statusSep separates the status line's segments.
const statusSep = " │ "

// contextSegment is the context-used segment: a whole percent, 0 to 100.
var contextSegment = regexp.MustCompile(`^(\d{1,3})%$`)

// readStatusLine reads model, account name and context percent from the
// operator's status line. A segment counts only when the separator after it
// is on screen, so one cut at the pane's edge or with "…" is not read ("8…"
// may be 80). It reports whether row had the status line's shape.
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
	f.Model = &closed[0]
	if len(closed) > 1 {
		f.User = &closed[1]
	}
	if len(closed) > 2 {
		if m := contextSegment.FindStringSubmatch(closed[2]); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n <= 100 {
				f.ContextPercent = &n
			}
		}
	}
	return true
}

// footerRightMargin is the columns Claude Code leaves free at the right of
// its footer rows. MEASURED on 2.1.310 at 40 columns: the cut mode line
// ("... 1 shell, 1 monito") and the status line cut with "…" both end at
// column 38. A row that reaches it is read as cut.
const footerRightMargin = 2

// modeLineHead is the mode line's first segment: "⏸ manual mode on",
// "⏵⏵ accept edits on", "⏵⏵ auto mode on (shift+tab to cycle)".
var modeLineHead = regexp.MustCompile(`^(⏸|⏵⏵) [a-z ]+ on( \(.*\))?$`)

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

// backgroundWork is the background work a pane's footer last showed and when
// it was first seen (ini-b4l0). Zero when none.
type backgroundWork struct {
	shells, monitors, tasks int
	since                   time.Time
}

// busy reports whether any background work is recorded.
func (w backgroundWork) busy() bool { return w.shells+w.monitors+w.tasks > 0 }

// observeBackground records what the footer read this tick says about
// background work. A kind the footer did not show readably keeps its last
// value, so a dialog over the footer or a cut row changes nothing; the
// no-background shape (every count read as 0) clears it, and so does the
// process ending. since is when work was first seen, kept while any goes on.
func (p *Pane) observeBackground(now time.Time, alive bool, f Footer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !alive {
		p.bgWork = backgroundWork{}
		return
	}
	w := p.bgWork
	if f.Shells != nil {
		w.shells = *f.Shells
	}
	if f.Monitors != nil {
		w.monitors = *f.Monitors
	}
	if f.Tasks != nil {
		w.tasks = *f.Tasks
	}
	switch {
	case !w.busy():
		w = backgroundWork{}
	case w.since.IsZero():
		w.since = now
	}
	p.bgWork = w
}

// backgroundWorkNow returns the recorded background work and whether there is
// any.
func (p *Pane) backgroundWorkNow() (backgroundWork, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bgWork, p.bgWork.busy()
}

// paneBusy is a pane's background work for the list and patrol replies; nil
// when there is none or the pane is not a local one.
func paneBusy(pv PaneView, now time.Time) *PaneBusy {
	p, ok := pv.(*Pane)
	if !ok {
		return nil
	}
	w, busy := p.backgroundWorkNow()
	if !busy {
		return nil
	}
	return &PaneBusy{Shells: w.shells, Monitors: w.monitors, Tasks: w.tasks, SinceSec: int(now.Sub(w.since).Seconds())}
}
