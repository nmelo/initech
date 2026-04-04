package tui

import (
	"github.com/nmelo/initech/internal/web"
)

// tuiPaneLister adapts the TUI to the web.PaneLister interface by converting
// tui.PaneInfo to web.PaneInfo. The web package does not import tui, so a
// thin adapter is needed even though the structs are identical.
type tuiPaneLister struct {
	t *TUI
}

var _ web.PaneLister = (*tuiPaneLister)(nil)

// AllPanes returns the current pane set. The bool is false when the TUI is
// shutting down and data is unavailable.
func (l *tuiPaneLister) AllPanes() ([]web.PaneInfo, bool) {
	internal, ok := l.t.AllPanes()
	if !ok {
		return nil, false
	}
	out := make([]web.PaneInfo, len(internal))
	for i, p := range internal {
		out[i] = web.PaneInfo{
			Name:     p.Name,
			Host:     p.Host,
			Activity: p.Activity,
			Alive:    p.Alive,
			Visible:  p.Visible,
		}
	}
	return out, true
}

// tuiPaneSubscriber adapts the TUI's pane registry to the web.PaneSubscriber
// interface. It uses runOnMain for pane lookup (required for thread safety on
// t.panes), then calls Subscribe/Unsubscribe directly on the Pane (which has
// its own subscriberMu).
type tuiPaneSubscriber struct {
	t *TUI
}

var _ web.PaneSubscriber = (*tuiPaneSubscriber)(nil)

// SubscribePane looks up the named pane and registers a PTY byte subscriber.
// Returns the channel and true on success, or nil and false if the pane does
// not exist or the TUI is shutting down.
func (s *tuiPaneSubscriber) SubscribePane(paneName, subscriberID string) (chan []byte, bool) {
	var pane *Pane
	ok := s.t.runOnMain(func() {
		pv := s.t.findPaneByName(paneName)
		if pv == nil {
			return
		}
		if lp, isLocal := pv.(*Pane); isLocal {
			pane = lp
		}
	})
	if !ok || pane == nil {
		return nil, false
	}
	return pane.Subscribe(subscriberID), true
}

// UnsubscribePane looks up the named pane and removes a PTY byte subscriber.
// Safe to call if the pane or subscriber does not exist.
func (s *tuiPaneSubscriber) UnsubscribePane(paneName, subscriberID string) {
	var pane *Pane
	s.t.runOnMain(func() {
		pv := s.t.findPaneByName(paneName)
		if pv == nil {
			return
		}
		if lp, isLocal := pv.(*Pane); isLocal {
			pane = lp
		}
	})
	if pane != nil {
		pane.Unsubscribe(subscriberID)
	}
}

// tuiStateProvider adapts the TUI to the web.StateProvider interface.
type tuiStateProvider struct {
	t *TUI
}

var _ web.StateProvider = (*tuiStateProvider)(nil)

func (s *tuiStateProvider) CurrentState() (web.StateSnapshot, bool) {
	var snap web.StateSnapshot
	ok := s.t.runOnMain(func() {
		// Layout info.
		ls := s.t.layoutState
		mode := "grid"
		switch ls.Mode {
		case LayoutFocus:
			mode = "focus"
		case Layout2Col:
			mode = "2col"
		}
		snap.Layout = web.LayoutInfo{
			Mode:    mode,
			Cols:    ls.GridCols,
			Rows:    ls.GridRows,
			Focused: ls.Focused,
		}

		// Pane states.
		snap.Panes = make([]web.PaneState, len(s.t.panes))
		for i, p := range s.t.panes {
			snap.Panes[i] = web.PaneState{
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !ls.Hidden[paneKey(p)],
				BeadID:   p.BeadID(),
				Order:    i,
			}
		}
	})
	return snap, ok
}
