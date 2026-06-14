// layout_presets.go makes the Alt/Option 1–5 layout shortcuts config-driven.
//
// initech.yaml carries raw `layout_presets` strings (slot "1".."5" ->
// "CxR"/focus/live/main). ResolvePresets parses and validates them once at
// startup, filling every missing or invalid slot with its built-in default so
// a typo never blocks launch. applyLayoutPreset then maps a parsed preset onto
// LayoutState, replicating the exact GridExplicit / live-toggle behavior the
// hardcoded handler had before this feature (ini-lkww).

package tui

import (
	"fmt"
	"strconv"
	"strings"
)

// presetKind identifies the family of layout a preset slot applies.
type presetKind int

const (
	presetGrid  presetKind = iota // Fixed CxR grid (GridExplicit).
	presetFocus                   // LayoutFocus: single focused pane, peers dimmed.
	presetMain                    // Layout2Col: main pane + vertical stack.
	presetLive                    // LayoutLive: conviction-scored auto grid (toggle).
)

// presetSlots is the number of remappable layout shortcut slots (Alt+1..Alt+5).
const presetSlots = 5

// maxPresetDim caps grid columns and rows for a preset. Larger shapes are
// rejected as typos (the spec's sane ceiling; revisit if real layouts exceed it).
const maxPresetDim = 8

// LayoutPreset is a parsed, validated layout preset for one Alt+N slot.
// Cols/Rows are meaningful only when Kind == presetGrid. Spec is the canonical
// source string, kept for warning/log messages.
type LayoutPreset struct {
	Kind presetKind
	Cols int
	Rows int
	Spec string
}

// defaultLayoutPresets returns the built-in Alt+1–5 bindings used when a slot
// is absent or invalid: 2x1 / 3x1 / 4x1 / 3x2 / live. These are the new shipped
// defaults (ini-lkww), softened by full remappability via initech.yaml.
func defaultLayoutPresets() [presetSlots]LayoutPreset {
	return [presetSlots]LayoutPreset{
		{Kind: presetGrid, Cols: 2, Rows: 1, Spec: "2x1"},
		{Kind: presetGrid, Cols: 3, Rows: 1, Spec: "3x1"},
		{Kind: presetGrid, Cols: 4, Rows: 1, Spec: "4x1"},
		{Kind: presetGrid, Cols: 3, Rows: 2, Spec: "3x2"},
		{Kind: presetLive, Spec: "live"},
	}
}

// ParseLayoutPreset parses a single preset spec into a LayoutPreset.
//
// Valid forms:
//   - grid "CxR": columns × rows, each in [1, maxPresetDim] (e.g. "4x1", "3x2").
//     "1x1" is a one-cell grid, distinct from focus.
//   - keyword "focus", "live", or "main".
//
// Input is lowercased first, so "3X2" is accepted. A bare column count ("3")
// is intentionally rejected here even though :grid accepts it — presets require
// an explicit CxR. Returns ok=false for anything else.
func ParseLayoutPreset(spec string) (LayoutPreset, bool) {
	s := strings.ToLower(strings.TrimSpace(spec))
	switch s {
	case "focus":
		return LayoutPreset{Kind: presetFocus, Spec: s}, true
	case "live":
		return LayoutPreset{Kind: presetLive, Spec: s}, true
	case "main":
		return LayoutPreset{Kind: presetMain, Spec: s}, true
	}

	// Grid "CxR": exactly one 'x', a positive integer on each side, both in range.
	parts := strings.Split(s, "x")
	if len(parts) != 2 {
		return LayoutPreset{}, false
	}
	cols, err1 := strconv.Atoi(parts[0])
	rows, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return LayoutPreset{}, false
	}
	if cols < 1 || cols > maxPresetDim || rows < 1 || rows > maxPresetDim {
		return LayoutPreset{}, false
	}
	return LayoutPreset{Kind: presetGrid, Cols: cols, Rows: rows, Spec: fmt.Sprintf("%dx%d", cols, rows)}, true
}

// ResolvePresets builds the five Alt+1–5 slots from raw initech.yaml config,
// filling each missing or invalid slot with its built-in default. It never
// fails: an unparseable value or out-of-range key falls back to the default and
// is reported in the returned warnings (which the caller logs). raw may be nil.
func ResolvePresets(raw map[string]string) (presets [presetSlots]LayoutPreset, warnings []string) {
	presets = defaultLayoutPresets()
	for key, val := range raw {
		slot, err := strconv.Atoi(key)
		if err != nil || slot < 1 || slot > presetSlots {
			warnings = append(warnings, fmt.Sprintf("layout_presets[%q] ignored: slot must be \"1\"..\"5\"", key))
			continue
		}
		p, ok := ParseLayoutPreset(val)
		if !ok {
			def := presets[slot-1]
			warnings = append(warnings, fmt.Sprintf("layout_presets[%q]=%q invalid, using default %s", key, val, def.Spec))
			continue
		}
		presets[slot-1] = p
	}
	return presets, warnings
}

// applyLayoutPreset applies the preset bound to the given zero-based slot
// (0 = Alt+1 ... 4 = Alt+5). Grid presets pin GridExplicit so recalcGrid won't
// auto-resize over the operator's choice; the live slot preserves the
// toggle-off-to-grid behavior of the old hardcoded handler. Out-of-range slots
// are a no-op. Mirrors the apply + persist tail every layout shortcut runs.
func (t *TUI) applyLayoutPreset(slot int) {
	if slot < 0 || slot >= len(t.layoutPresets) {
		return
	}
	p := t.layoutPresets[slot]
	switch p.Kind {
	case presetGrid:
		t.layoutState.Mode = LayoutGrid
		t.layoutState.GridCols = p.Cols
		t.layoutState.GridRows = p.Rows
		t.layoutState.GridExplicit = true
		t.layoutState.Zoomed = false
	case presetFocus:
		t.layoutState.Mode = LayoutFocus
		t.layoutState.GridExplicit = false
		t.layoutState.Zoomed = false
	case presetMain:
		t.layoutState.Mode = Layout2Col
		t.layoutState.GridExplicit = false
		t.layoutState.Zoomed = false
	case presetLive:
		if t.layoutState.Mode == LayoutLive {
			// Toggle off: switch back to grid.
			t.layoutState.Mode = LayoutGrid
			t.liveEngine = nil
		} else {
			// Toggle on: enter live auto mode (default).
			t.layoutState.Mode = LayoutLive
			t.layoutState.LiveAuto = true
			t.layoutState.GridExplicit = false
			t.layoutState.Zoomed = false
			if t.layoutState.LivePinned == nil {
				t.layoutState.LivePinned = make(map[string]int)
			}
			t.initLiveEngine(0)
			t.trackLiveModeActivated()
		}
	}
	t.applyLayout()
	t.saveLayoutIfConfigured()
}

// applyLayoutPresetLive applies the preset bound to the given zero-based slot
// in LIVE mode (Shift+Alt+1..5, ini-era4). It reads the SAME layout_presets map
// as applyLayoutPreset and only changes the mode:
//   - grid preset (CxR) -> LayoutLive at fixed dims (LiveAuto=false). GridExplicit
//     is pinned so recalcGrid won't auto-resize the live viewport on hot-add.
//   - keyword preset (focus/main/live) -> LayoutLive auto-grid (LiveAuto=true,
//     dims via autoGrid).
//
// Entry runs the same engine-init path as the Alt+5 live toggle (LivePinned
// init, initLiveEngine, trackLiveModeActivated) so pinning/eviction/activation
// telemetry behave identically. It is a direct set, not a toggle; leave live by
// pressing any static Alt+M. Out-of-range slots are a no-op.
func (t *TUI) applyLayoutPresetLive(slot int) {
	if slot < 0 || slot >= len(t.layoutPresets) {
		return
	}
	p := t.layoutPresets[slot]
	t.layoutState.Mode = LayoutLive
	t.layoutState.Zoomed = false
	if p.Kind == presetGrid {
		// Fixed-dimension live grid: the LiveEngine assigns agents to slots
		// within exactly C×R cells.
		t.layoutState.LiveAuto = false
		t.layoutState.GridCols = p.Cols
		t.layoutState.GridRows = p.Rows
		t.layoutState.GridExplicit = true
	} else {
		// Keyword preset -> live auto-grid sized from the active agent count.
		t.layoutState.LiveAuto = true
		t.layoutState.GridExplicit = false
	}
	if t.layoutState.LivePinned == nil {
		t.layoutState.LivePinned = make(map[string]int)
	}
	t.initLiveEngine(0)
	t.trackLiveModeActivated()
	t.applyLayout()
	t.saveLayoutIfConfigured()
}
