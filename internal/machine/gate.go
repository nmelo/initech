package machine

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

// ClearAfter is how long all three numbers must stay under the ceiling
// before it clears, so a dip under for one reading does not let a start
// through only to engage again.
const ClearAfter = time.Minute

// Reading is one look at the machine. A number whose OK is false could not
// be read; it never engages the ceiling. FreeGB is reclaimable memory: what
// the system can hand out now, not the bare free pages. SwapGB, swap in use,
// is information for the fleet line and never engages the ceiling.
type Reading struct {
	Awake   int
	AwakeOK bool
	FreeGB  float64
	FreeOK  bool
	Load    float64
	LoadOK  bool
	SwapGB  float64
	SwapOK  bool
	At      time.Time
}

// Over reports whether r is at or above c on any number it could read.
// Free memory is "at" the ceiling at or below its value.
func Over(r Reading, c Ceiling) bool {
	return (r.AwakeOK && r.Awake >= c.Sessions) ||
		(r.FreeOK && r.FreeGB <= c.FreeMemoryGB) ||
		(r.LoadOK && r.Load >= c.Load)
}

// Gate holds whether the ceiling is engaged. It engages on the first reading
// at or above the ceiling and clears only once every reading for ClearAfter
// has been under it. Safe for use from any goroutine.
type Gate struct {
	mu         sync.Mutex
	engaged    bool
	underSince time.Time
	last       Reading
	ceiling    Ceiling
	seen       bool
	// admitted is how many starts and wakes Admit let through since the last
	// reading. They are awake before the next reading can count them, so
	// Admit counts them itself: a burst of wakes inside one minute cannot
	// carry the machine past the ceiling between two readings.
	admitted int
}

// Step records one reading against c at now and returns whether the gate is
// engaged after it, and whether this step cleared it.
func (g *Gate) Step(r Reading, c Ceiling, now time.Time) (engaged, cleared bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.At.IsZero() {
		r.At = now
	}
	g.last, g.ceiling, g.seen = r, c, true
	g.admitted = 0
	if Over(r, c) {
		g.engaged = true
		g.underSince = time.Time{}
		return true, false
	}
	if !g.engaged {
		return false, false
	}
	if g.underSince.IsZero() {
		g.underSince = now
	}
	if now.Sub(g.underSince) >= ClearAfter {
		g.engaged = false
		g.underSince = time.Time{}
		return false, true
	}
	return true, false
}

// Admit asks to start or wake one agent. It refuses, with the refusal's
// words, when the gate is engaged or when the last reading's awake count
// plus the agents admitted since would be at the ceiling; otherwise it
// counts the agent and lets it through. A gate with no reading admits:
// the ceiling never engages on numbers it has not read.
func (g *Gate) Admit() (ok bool, words string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.seen {
		return true, ""
	}
	r := g.last
	if r.AwakeOK {
		r.Awake += g.admitted
	}
	if g.engaged || Over(r, g.ceiling) {
		return false, Refusal(r, g.ceiling)
	}
	if r.AwakeOK {
		g.admitted++
	}
	return true, ""
}

// Engaged reports whether the gate is engaged now. A gate that has never
// had a reading is not engaged.
func (g *Gate) Engaged() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.engaged
}

// Snapshot is the gate's last reading, its ceiling, whether it is engaged,
// and whether it has had a reading at all.
func (g *Gate) Snapshot() (r Reading, c Ceiling, engaged, seen bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last, g.ceiling, g.engaged, g.seen
}

// Refusal is the line a refused start or wake shows in the pane and in
// status: "not started: the Mac is at its ceiling: 31 awake sessions,
// 6.1 GB free, load 34; ceiling 30 / 8 GB / 30".
func Refusal(r Reading, c Ceiling) string {
	return "not started: the Mac is at its ceiling: " + figures(r) + "; ceiling " + ceilingFigures(c)
}

// Deferred is what a sender hears when its send to a suspended agent is held
// at the ceiling, after "deferred to <agent> — ".
const Deferred = "the Mac is at its ceiling; will wake and deliver when it is under"

// StatusLine is status's fleet line: "fleet 31 awake · 6.1 GB free · swap 2.3
// GB · load 34 · ceiling 30 / 8 GB (default) / 30". It names where the free
// memory value came from; the refusal gives the value alone.
func StatusLine(r Reading, c Ceiling) string {
	swap := "?"
	if r.SwapOK {
		swap = strconv.FormatFloat(r.SwapGB, 'f', 1, 64)
	}
	free := num(c.FreeMemoryGB) + " GB"
	if c.FreeSource != "" {
		free += " (" + c.FreeSource + ")"
	}
	return fmt.Sprintf("fleet %s awake · %s GB free · swap %s GB · load %s · ceiling %d / %s / %s",
		awakeText(r), freeText(r), swap, loadText(r), c.Sessions, free, num(c.Load))
}

func figures(r Reading) string {
	return fmt.Sprintf("%s awake sessions, %s GB free, load %s", awakeText(r), freeText(r), loadText(r))
}

func ceilingFigures(c Ceiling) string {
	return fmt.Sprintf("%d / %s GB / %s", c.Sessions, num(c.FreeMemoryGB), num(c.Load))
}

func awakeText(r Reading) string {
	if !r.AwakeOK {
		return "?"
	}
	return strconv.Itoa(r.Awake)
}

func freeText(r Reading) string {
	if !r.FreeOK {
		return "?"
	}
	return strconv.FormatFloat(r.FreeGB, 'f', 1, 64)
}

func loadText(r Reading) string {
	if !r.LoadOK {
		return "?"
	}
	return strconv.FormatFloat(r.Load, 'f', 0, 64)
}

// num prints a ceiling value without a trailing ".0": 8, 7.5.
func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
