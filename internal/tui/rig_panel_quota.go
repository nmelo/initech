package tui

// rig_panel_quota.go fills the bottom bar's fleet quota (ini-kb4c): a
// fleetquota.Poller on cla's account usage file, read on the machine
// ceiling's minute tick, shown as a whole percent and four cells.

import (
	"fmt"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/fleetquota"
)

// quotaPath is the usage file the quota reads. A package variable so the
// package's tests never read the operator's.
var quotaPath = fleetquota.DefaultPath

// startPanelQuota makes the poller, reads it once, and sets it behind the
// bar. Main loop, before the loop starts; every window shows the bar.
func (t *TUI) startPanelQuota(now time.Time) {
	if t.ceiling.quota != nil {
		return
	}
	path, err := quotaPath()
	if err != nil {
		LogWarn("panel", "fleet quota: no home folder; the bar shows a dash", "err", err)
		return
	}
	p := fleetquota.NewPoller(path, func(line string) { LogInfo("panel", line) })
	p.Poll(now)
	t.ceiling.quota = p
	t.panelQuota = &panelQuotaSource{p: p}
}

// panelQuotaSource is the bar's quotaSource: the poller's last reading.
type panelQuotaSource struct{ p *fleetquota.Poller }

// QuotaText is the bar's value, "58% ▓▓░░", and false when the quota is a
// dash.
func (q *panelQuotaSource) QuotaText() (string, bool) {
	return quotaText(q.p.Last())
}

func quotaText(q fleetquota.Quota) (string, bool) {
	if !q.Known {
		return "", false
	}
	c := q.Cells()
	return fmt.Sprintf("%d%% %s%s", q.Percent, strings.Repeat("▓", c), strings.Repeat("░", 4-c)), true
}
