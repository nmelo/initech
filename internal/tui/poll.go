// Package tui polling functions. These gather data on the render tick
// (tip rotation, quota scraping) or in background goroutines (battery).
// Separated from render.go so that file stays pure drawing.
package tui

import (
	"math/rand"
	"time"
)

// statusTips are progressive hints shown in the status bar. They cycle
// every tipRotationInterval, teaching one feature at a time.
var statusTips = []string{
	"Press backtick to open the command bar",
	"Press ` then ? for the full command reference",
	"Try Alt+z to zoom the focused pane",
	"Use Alt+Left/Right to switch panes",
	"Alt+s toggles the agent status overlay",
	"Alt+1 enters focus mode (one pane)",
	"Try Alt+2 for a 2x2 grid layout",
	"Type grid 3x2 for a custom layout",
	"Use main for a split layout",
	"Drag to select text, auto-copies",
	"Use `show eng1, eng2` to reorder panes",
	"Tab completes agent names in commands",
	"Use `patrol` to peek all agents at once",
	"Try `top` to see memory and PID per agent",
	"Use `log` to see recent event history",
	"Green dot = working, gray = idle",
	"Yellow dot = idle with work waiting",
	"Red dot = stuck or error loop detected",
	"Use `add`/`remove` to change the roster live",
	"Use pin <name> to protect agents from auto-suspend",
}

// tipRotationInterval is how long each tip is displayed before cycling.
const tipRotationInterval = 2 * time.Minute

// rotateTip advances to the next tip if the rotation interval has elapsed.
// Called from the render tick.
func (t *TUI) rotateTip() {
	if time.Now().After(t.tipRotateAt) {
		t.tipIndex = rand.Intn(len(statusTips))
		t.tipRotateAt = time.Now().Add(tipRotationInterval)
	}
}

// quotaPollInterval is how often the TUI scrapes quota from a pane.
const quotaPollInterval = 30 * time.Second

// pollQuota reads the Claude Code quota percentage from the first available
// alive, non-suspended pane. Called on the render tick; only runs every 30s.
func (t *TUI) pollQuota() {
	if time.Now().Before(t.quotaPollAt) {
		return
	}
	t.quotaPollAt = time.Now().Add(quotaPollInterval)

	for _, p := range t.panes {
		if !p.IsAlive() || p.IsSuspended() {
			continue
		}
		lp, ok := p.(*Pane)
		if !ok {
			continue
		}
		if pct := lp.ScrapeQuota(); pct >= 0 {
			t.quotaPercent = pct
			return
		}
	}
	// No pane had quota data. Keep the last known value (stale > absent).
}

// startBatteryPoller launches a goroutine that polls battery state every 60s.
// If the first poll finds no battery, the goroutine exits immediately and
// batteryPercent stays at -1 (nothing rendered in the status bar).
func (t *TUI) startBatteryPoller() {
	pct, charging, hasBattery := readBattery()
	if !hasBattery {
		return // Desktop or VM, no battery to monitor.
	}
	t.batteryPercent = pct
	t.batteryCharging = charging

	t.safeGo(func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pct, charging, has := readBattery()
				if has {
					t.batteryPercent = pct
					t.batteryCharging = charging
				}
			case <-t.quitCh:
				return
			}
		}
	})
}
