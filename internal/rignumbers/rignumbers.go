// Package rignumbers computes a rig's health numbers for the last hour, one
// line of them, and the threshold crossings that message super.
//
// The numbers: merges per hour on the product's main; the push CI runs on
// main in the hour, green and red, with their median minutes; the landing
// line's depth (beads at qa_passed that no recent main commit names); beads
// filed and closed in the hour; in_progress beads with no move for 20
// minutes; and idle engineers. A number whose source could not be read is
// unknown and prints "?"; a rig with no repo prints its repo numbers as "-".
//
// The package reads nothing itself: callers gather the inputs (gh, git, bd,
// pane states) and pass them in, so every number is computed from fixtures in
// tests and the cost of gathering stays with the caller.
package rignumbers

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Window is the period every per-hour number counts over.
const Window = time.Hour

// NoMoveAfter is how long an in_progress bead sits before it counts as not
// moving.
const NoMoveAfter = 20 * time.Minute

// Commit is one commit on the product's main.
type Commit struct {
	When    time.Time
	Subject string
}

// Run is one push CI run on main. Conclusion is "" while it runs.
type Run struct {
	Conclusion string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Bead is one bead with the times the numbers read. LastMoved is the newest
// sign of work on it (bd's updated_at, a comment or a commit, as the caller
// knows it).
type Bead struct {
	ID        string
	Status    string
	Assignee  string
	CreatedAt time.Time
	ClosedAt  time.Time
	LastMoved time.Time
}

// Input is everything one computation reads. A false Known flag means that
// source could not be read, so its numbers are unknown rather than zero.
type Input struct {
	Now          time.Time
	HasRepo      bool
	Commits      []Commit // the latest commits on main, newest first or not
	CommitsKnown bool
	Runs         []Run
	RunsKnown    bool
	Beads        []Bead
	BeadsKnown   bool
	Panes        map[string]string // pane name to its state ("idle", "running", ...); nil when unknown
}

// Numbers is one computation. A nil field is unknown.
type Numbers struct {
	At                time.Time `json:"at"`
	Repo              bool      `json:"repo"`
	MergesPerHour     *int      `json:"merges_per_hour"`
	GateGreen         *int      `json:"gate_green"`
	GateRed           *int      `json:"gate_red"`
	GateMedianMinutes *float64  `json:"gate_median_minutes"`
	LandingDepth      *int      `json:"landing_depth"`
	Filed             *int      `json:"filed"`
	Closed            *int      `json:"closed"`
	NoMove20m         *int      `json:"no_move_20m"`
	IdleEngineers     *int      `json:"idle_engineers"`
	// Today's push runs on main since local midnight (ini-9z0b): passes,
	// fails, whether the latest finished one failed, and their median
	// minutes; nil while the runs are unread.
	MainPass          *int     `json:"main_pass_today"`
	MainFail          *int     `json:"main_fail_today"`
	MainLatestRed     bool     `json:"main_latest_red"`
	MainMedianMinutes *float64 `json:"main_median_minutes_today"`
	// ClosedToday is beads closed since local midnight; nil while unread.
	ClosedToday *int `json:"closed_today"`
	// Fleet is the machine's numbers, filled by whoever has them (window 1's
	// gate, or status reading the machine itself); absent when nobody does.
	Fleet *Fleet `json:"fleet,omitempty"`
}

// Fleet is the machine ceiling's numbers (ini-49q): the awake Claude
// sessions of every rig on the machine, free memory and the 5-minute load,
// beside the ceiling they are read against. A nil number was unreadable.
type Fleet struct {
	Awake     *int         `json:"awake"`
	FreeGB    *float64     `json:"free_gb"`
	Load      *float64     `json:"load"`
	SwapGB    *float64     `json:"swap_gb"`
	Ceiling   FleetCeiling `json:"ceiling"`
	AtCeiling bool         `json:"at_ceiling"`
	Line      string       `json:"line"`
}

// FleetCeiling is the ceiling the fleet numbers were read against.
type FleetCeiling struct {
	Sessions     int     `json:"sessions"`
	FreeMemoryGB float64 `json:"free_memory_gb"`
	FreeSource   string  `json:"free_memory_source"`
	Load         float64 `json:"load"`
}

func ptr[T any](v T) *T { return &v }

// engineer matches an engineer's pane name.
var engineer = regexp.MustCompile(`^eng[0-9]+$`)

// Compute returns the numbers for in.
func Compute(in Input) Numbers {
	since := in.Now.Add(-Window)
	n := Numbers{At: in.Now, Repo: in.HasRepo}
	if in.HasRepo && in.CommitsKnown {
		c := 0
		for _, m := range in.Commits {
			if !m.When.Before(since) {
				c++
			}
		}
		n.MergesPerHour = ptr(c)
	}
	if in.HasRepo && in.RunsKnown {
		green, red := 0, 0
		var mins []float64
		for _, r := range in.Runs {
			if r.CreatedAt.Before(since) {
				continue
			}
			switch r.Conclusion {
			case "success":
				green++
			case "failure":
				red++
			default:
				continue
			}
			mins = append(mins, r.UpdatedAt.Sub(r.CreatedAt).Minutes())
		}
		n.GateGreen, n.GateRed = ptr(green), ptr(red)
		if len(mins) > 0 {
			sort.Float64s(mins)
			n.GateMedianMinutes = ptr(mins[len(mins)/2])
		}
	}
	midnight := localMidnight(in.Now)
	if in.HasRepo && in.RunsKnown {
		pass, fail := 0, 0
		var mins []float64
		var latest time.Time
		latestRed := false
		for _, r := range in.Runs {
			if r.CreatedAt.Before(midnight) {
				continue
			}
			switch r.Conclusion {
			case "success":
				pass++
			case "failure":
				fail++
			default:
				continue // Still running, or cancelled: not a verdict.
			}
			mins = append(mins, r.UpdatedAt.Sub(r.CreatedAt).Minutes())
			if r.CreatedAt.After(latest) {
				latest, latestRed = r.CreatedAt, r.Conclusion == "failure"
			}
		}
		n.MainPass, n.MainFail, n.MainLatestRed = ptr(pass), ptr(fail), latestRed
		if len(mins) > 0 {
			sort.Float64s(mins)
			n.MainMedianMinutes = ptr(mins[len(mins)/2])
		}
	}
	if in.BeadsKnown {
		today := 0
		for _, b := range in.Beads {
			if !b.ClosedAt.IsZero() && !b.ClosedAt.Before(midnight) {
				today++
			}
		}
		n.ClosedToday = ptr(today)
	}
	if in.BeadsKnown {
		filed, closed, still := 0, 0, 0
		for _, b := range in.Beads {
			if !b.CreatedAt.IsZero() && !b.CreatedAt.Before(since) {
				filed++
			}
			if !b.ClosedAt.IsZero() && !b.ClosedAt.Before(since) {
				closed++
			}
			if b.Status == "in_progress" && in.Now.Sub(b.LastMoved) >= NoMoveAfter {
				still++
			}
		}
		n.Filed, n.Closed, n.NoMove20m = ptr(filed), ptr(closed), ptr(still)
		if in.HasRepo && in.CommitsKnown {
			depth := 0
			for _, b := range in.Beads {
				if b.Status == "qa_passed" && !onMain(b.ID, in.Commits) {
					depth++
				}
			}
			n.LandingDepth = ptr(depth)
		}
	}
	if in.Panes != nil {
		idle := 0
		for name, state := range in.Panes {
			if engineer.MatchString(name) && state == "idle" {
				idle++
			}
		}
		n.IdleEngineers = ptr(idle)
	}
	return n
}

// onMain reports whether a commit subject names id as a word.
func onMain(id string, commits []Commit) bool {
	re := regexp.MustCompile(`(?:^|[^\w-])` + regexp.QuoteMeta(id) + `\b`)
	for _, c := range commits {
		if re.MatchString(c.Subject) {
			return true
		}
	}
	return false
}

// num prints a number: "?" unknown, "-" for a repo number on a rig with none.
func num(v *int, repoNumber, hasRepo bool) string {
	if repoNumber && !hasRepo {
		return "-"
	}
	if v == nil {
		return "?"
	}
	return fmt.Sprint(*v)
}

// Line is the one line status and the board print.
func (n Numbers) Line() string {
	gate := "?"
	switch {
	case !n.Repo:
		gate = "-"
	case n.GateGreen != nil && n.GateRed != nil:
		med := "-"
		if n.GateMedianMinutes != nil {
			med = fmt.Sprintf("%.0fm", *n.GateMedianMinutes)
		}
		gate = fmt.Sprintf("%dg/%dr med %s", *n.GateGreen, *n.GateRed, med)
	}
	filed := "?"
	if n.Filed != nil && n.Closed != nil {
		filed = fmt.Sprintf("+%d/-%d", *n.Filed, *n.Closed)
	}
	return fmt.Sprintf("merges/h %s · gate %s · landing %s · filed %s · no move 20m %s · idle %s",
		num(n.MergesPerHour, true, n.Repo), gate, num(n.LandingDepth, true, n.Repo), filed,
		num(n.NoMove20m, false, n.Repo), num(n.IdleEngineers, false, n.Repo))
}

// names are the numbers a threshold can name, with the words its message uses.
var names = map[string]string{
	"merges_per_hour":     "merges per hour",
	"gate_green":          "gate green",
	"gate_red":            "gate red",
	"gate_median_minutes": "gate median minutes",
	"landing_depth":       "landing depth",
	"filed":               "filed",
	"closed":              "closed",
	"no_move_20m":         "no move 20m",
	"idle_engineers":      "idle engineers",
}

// value returns a number by its threshold name, and whether it is known.
func (n Numbers) value(name string) (float64, bool) {
	var p *int
	switch name {
	case "merges_per_hour":
		p = n.MergesPerHour
	case "gate_green":
		p = n.GateGreen
	case "gate_red":
		p = n.GateRed
	case "gate_median_minutes":
		if n.GateMedianMinutes == nil {
			return 0, false
		}
		return *n.GateMedianMinutes, true
	case "landing_depth":
		p = n.LandingDepth
	case "filed":
		p = n.Filed
	case "closed":
		p = n.Closed
	case "no_move_20m":
		p = n.NoMove20m
	case "idle_engineers":
		p = n.IdleEngineers
	}
	if p == nil {
		return 0, false
	}
	return float64(*p), true
}

// splitKey returns a threshold key's number and direction ("min" or "max").
func splitKey(key string) (name, dir string, ok bool) {
	i := strings.LastIndex(key, "_")
	if i < 0 {
		return "", "", false
	}
	name, dir = key[:i], key[i+1:]
	if _, known := names[name]; !known || (dir != "min" && dir != "max") {
		return "", "", false
	}
	return name, dir, true
}

// ValidKey reports whether key is a threshold initech.yaml may set:
// <number>_min or <number>_max for one of the numbers.
func ValidKey(key string) error {
	if _, _, ok := splitKey(key); !ok {
		return fmt.Errorf("%q is not a number's _min or _max", key)
	}
	return nil
}

// Thresholds maps a key (gate_red_max, idle_engineers_min, ...) to its bound.
// A number past a max, or under a min, is outside.
type Thresholds map[string]float64

// Alarm remembers which thresholds are crossed, so a crossing sends once.
type Alarm struct {
	crossed map[string]bool
}

// Check returns one message for each threshold n crosses from inside to
// outside, and nothing for one that stays crossed. A threshold re-arms when
// its number comes back inside. An unknown number changes nothing.
func (a *Alarm) Check(n Numbers, th Thresholds) []string {
	if a.crossed == nil {
		a.crossed = map[string]bool{}
	}
	keys := make([]string, 0, len(th))
	for k := range th {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		name, dir, ok := splitKey(k)
		if !ok {
			continue
		}
		v, known := n.value(name)
		if !known {
			continue
		}
		bound := th[k]
		outside := (dir == "max" && v > bound) || (dir == "min" && v < bound)
		if outside && !a.crossed[k] {
			word := "over its max"
			if dir == "min" {
				word = "under its min"
			}
			out = append(out, fmt.Sprintf("rig numbers: %s is %s, %s of %s", names[name], trim(v), word, trim(bound)))
		}
		a.crossed[k] = outside
	}
	return out
}

func trim(v float64) string { return strings.TrimSuffix(fmt.Sprintf("%.1f", v), ".0") }

// localMidnight is the start of now's day in now's location.
func localMidnight(now time.Time) time.Time {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
}

// Span marks a rune range of a bar line that is drawn red.
type Span struct{ From, To int }

// BarLeft is the bottom bar's left part, as the rig panel and initech status
// print it (ini-9z0b): "merges/h N · main ✓N ✗N · median Nm · closed today
// N". The spans are what is red: the fail count when there are any, and the
// whole main item when the latest finished run failed. ? is a number initech
// could not read, - a rig with no repo, — a median with no run today.
func (n Numbers) BarLeft() (string, []Span) {
	var b strings.Builder
	var spans []Span
	at := 0
	write := func(s string, red bool) {
		if red {
			spans = append(spans, Span{at, at + len([]rune(s))})
		}
		b.WriteString(s)
		at += len([]rune(s))
	}
	write("merges/h "+num(n.MergesPerHour, true, n.Repo), false)
	write(" · ", false)
	switch {
	case !n.Repo:
		write("main -", false)
	case n.MainPass == nil || n.MainFail == nil:
		write("main ?", false)
	default:
		pass, fail := fmt.Sprintf("main ✓%d ", *n.MainPass), fmt.Sprintf("✗%d", *n.MainFail)
		if n.MainLatestRed {
			write(pass+fail, true)
		} else {
			write(pass, false)
			write(fail, *n.MainFail > 0)
		}
	}
	write(" · ", false)
	med := "—"
	switch {
	case !n.Repo:
		med = "-"
	case n.MainPass == nil:
		med = "?"
	case n.MainMedianMinutes != nil:
		med = fmt.Sprintf("%.0fm", *n.MainMedianMinutes)
	}
	write("median "+med, false)
	write(" · ", false)
	write("closed today "+num(n.ClosedToday, false, n.Repo), false)
	return b.String(), spans
}
