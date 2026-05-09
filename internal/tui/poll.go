// Package tui polling functions. These gather data on the render tick
// (tip rotation) or in background goroutines (battery).
// Separated from render.go so that file stays pure drawing.
package tui

import (
	"math/rand"
	"sync"
	"time"
)

var (
	readBatteryFn    = readBattery
	newBatteryTicker = time.NewTicker
)

// statusTips are progressive hints shown in the status bar. They cycle
// every tipRotationInterval, teaching one feature at a time.
// Built lazily so modKey (Opt on macOS, Alt elsewhere) is resolved.
var statusTips []string
var statusTipsOnce sync.Once

func getStatusTips() []string {
	statusTipsOnce.Do(func() {
		m := modKey
		statusTips = []string{
			"Press backtick to open the command bar",
			"Press ` then ? for the full command reference",
			"Try " + m + "+z to zoom the focused pane",
			"Use " + m + "+Left/Right to switch panes",
			m + "+s toggles the agent status overlay",
			m + "+1 enters focus mode (one pane)",
			"Try " + m + "+2 for a 2x2 grid layout",
			"Type grid 3x2 for a custom layout",
			"Use main for a split layout",
			"Drag to select text, auto-copies",
			"Use `agents` or " + m + "+a to manage visibility and order",
			"Tab completes agent names in commands",
			"Use `patrol` to peek all agents at once",
			"Try `top` to see memory and PID per agent",
			"Use `log` to see recent event history",
			"Green dot = working, gray = idle",
			"Red dot = stuck or error loop detected",
			"Use `add`/`remove` to change the roster live",
		}
	})
	return statusTips
}

// tipRotationInterval is how long each tip is displayed before cycling.
const tipRotationInterval = 2 * time.Minute

// rotateTip advances to the next tip if the rotation interval has elapsed.
// Called from the render tick.
func (t *TUI) rotateTip() {
	if time.Now().After(t.tipRotateAt) {
		t.tipIndex = rand.Intn(len(getStatusTips()))
		t.tipRotateAt = time.Now().Add(tipRotationInterval)
	}
}

// startBatteryPoller launches a goroutine that polls battery state every 60s.
// If the first poll finds no battery, the goroutine exits immediately and
// batteryPercent stays at -1 (nothing rendered in the status bar).
//
// All writes to t.batteryPercent and t.batteryCharging happen under t.mu so
// the renderer (which reads on every paint) and tests (which read after the
// goroutine starts) cannot race with the poller goroutine.
func (t *TUI) startBatteryPoller() {
	pct, charging, hasBattery := readBatteryFn()
	if !hasBattery {
		return // Desktop or VM, no battery to monitor.
	}
	t.mu.Lock()
	t.batteryPercent = pct
	t.batteryCharging = charging
	t.mu.Unlock()

	t.safeGo(func() {
		ticker := newBatteryTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pct, charging, has := readBatteryFn()
				if has {
					t.mu.Lock()
					t.batteryPercent = pct
					t.batteryCharging = charging
					t.mu.Unlock()
				}
			case <-t.quitCh:
				return
			}
		}
	})
}

// batteryStatus returns the current battery percentage and charging state
// under t.mu. Renderer paints, tests, and any future readers must use this
// helper instead of reading the fields directly so the locking discipline
// stays in one place.
func (t *TUI) batteryStatus() (pct int, charging bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.batteryPercent, t.batteryCharging
}
