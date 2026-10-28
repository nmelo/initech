package tui

import "sync/atomic"

// The "changed since the last frame" fact (ini-ysr.1), the base the redraw
// work stands on: a pane needs drawing only when its content changed or the
// way it is drawn changed. Two halves make it.
//
// What the pane alone knows is a flag on the pane, set where it happens:
// bytes reached a local pane's emulator, a window 2 pane's stream delivered a
// frame, or the pane was scrolled. Any goroutine may set it; the frame loop
// takes it (reads and clears) once per drawn pane.
//
// What the frame loop knows -- focus, dimming, the pane's number, its region
// (a resize or a layout change) and the selection -- is compared with what the
// pane was last drawn with. One place, so a focus change made by any of the
// paths that move focus is seen without a hook in each.
//
// Nothing acts on the answer yet (step 2 of the epic does).

// frameChange is the pane's half of the fact.
type frameChange struct{ dirty atomic.Bool }

// markFrameChanged records that the pane's content changed since the last
// frame was drawn.
func (f *frameChange) markFrameChanged() { f.dirty.Store(true) }

// takeFrameChanged reports whether the content changed since the last call,
// and clears the fact. Called by the frame loop when it draws the pane.
func (f *frameChange) takeFrameChanged() bool { return f.dirty.Swap(false) }

// paneFrameState is how the frame loop last drew a pane.
type paneFrameState struct {
	focused, dimmed bool
	index           int
	region          Region
	sel             Selection
}

// frameChangeTaker is the pane half's reader, met by Pane and RemotePane.
type frameChangeTaker interface{ takeFrameChanged() bool }

// paneChangedForFrame is the whole fact for one pane in this frame: its own
// flag (taken, so cleared), or a difference from how it was last drawn, or
// never drawn before. It records this frame's state in next, which becomes
// the last-drawn record once the frame is done, so a pane that leaves the
// plan is forgotten.
func paneChangedForFrame(last, next map[PaneView]paneFrameState, pr PaneRender, sel Selection) bool {
	changed := false
	if f, ok := pr.Pane.(frameChangeTaker); ok {
		changed = f.takeFrameChanged()
	}
	st := paneFrameState{focused: pr.Focused, dimmed: pr.Dimmed, index: pr.Index, region: pr.Region, sel: sel}
	if prev, ok := last[pr.Pane]; !ok || prev != st {
		changed = true
	}
	next[pr.Pane] = st
	return changed
}
