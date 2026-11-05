package tui

// rig_panel_bar.go is the rig panel's bottom bar (ini-9z0b): on the left
// whether the rig is moving (rignumbers.BarLeft), on the right whether the
// machine is fine, from the machine ceiling's own reading, before the mode,
// battery and clock the bar already shows.

import (
	"fmt"
	"runtime"

	"github.com/gdamore/tcell/v2"
	"github.com/nmelo/initech/internal/rignumbers"
)

// quotaSource is where the fleet quota comes from; ☾ ini-kb4c fills it from
// the account poller. Until then nothing does, and the bar shows a dash.
type quotaSource interface {
	// QuotaText is the bar's value, as "58% ▓▓░░", and whether it is known.
	QuotaText() (string, bool)
}

// panelCores is this machine's core count, the load's yardstick. A variable
// so a test can set it.
var panelCores = runtime.NumCPU

// loadStyle colours the load: dim below the cores, yellow to twice them, red
// above.
func loadStyle(load float64, cores int, bar tcell.Style) tcell.Style {
	switch {
	case load > 2*float64(cores):
		return bar.Foreground(tcell.ColorRed)
	case load >= float64(cores):
		return bar.Foreground(tcell.ColorYellow)
	}
	return bar.Foreground(tcell.ColorGray)
}

// barLeftItems is BarLeft's line as bar items, its red parts red.
func barLeftItems(n rignumbers.Numbers, bar tcell.Style) []statusBarItem {
	line, spans := n.BarLeft()
	r := []rune(line)
	var out []statusBarItem
	at := 0
	red := bar.Foreground(tcell.ColorRed)
	for _, s := range spans {
		if s.From > at {
			out = append(out, statusBarItem{string(r[at:s.From]), bar})
		}
		out = append(out, statusBarItem{string(r[s.From:s.To]), red})
		at = s.To
	}
	if at < len(r) {
		out = append(out, statusBarItem{string(r[at:]), bar})
	}
	return out
}

// barRightItems is the machine's part: memory free over the free-memory
// ceiling, awake sessions over the sessions ceiling, the fleet quota and the
// load over the cores. A number the reading lacks prints ?.
func barRightItems(f *rignumbers.Fleet, quota quotaSource, cores int, bar tcell.Style) []statusBarItem {
	if f == nil {
		return nil
	}
	mem := "?"
	if f.FreeGB != nil {
		mem = fmt.Sprintf("%.1f", *f.FreeGB)
	}
	sessions := "?"
	if f.Awake != nil {
		sessions = fmt.Sprint(*f.Awake)
	}
	q := "—"
	if quota != nil {
		if text, ok := quota.QuotaText(); ok {
			q = text
		}
	}
	items := []statusBarItem{
		{fmt.Sprintf("mem %s/%g GB", mem, f.Ceiling.FreeMemoryGB), bar},
		{fmt.Sprintf("sessions %s/%d", sessions, f.Ceiling.Sessions), bar},
		{"fleet quota " + q, bar},
	}
	load := statusBarItem{"load ?/" + fmt.Sprint(cores), bar}
	if f.Load != nil {
		load = statusBarItem{fmt.Sprintf("load %.1f/%d", *f.Load, cores), loadStyle(*f.Load, cores, bar)}
	}
	return append(items, load)
}
