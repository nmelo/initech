package tui

import (
	"fmt"
	"sync"

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
		case LayoutLive:
			mode = "live"
		}
		snap.Layout = web.LayoutInfo{
			Mode:    mode,
			Cols:    ls.GridCols,
			Rows:    ls.GridRows,
			Focused: ls.Focused,
		}

		// Build pinned lookup for live mode.
		livePinned := ls.LivePinned // may be nil

		// Pane states.
		snap.Panes = make([]web.PaneState, len(s.t.panes))
		for i, p := range s.t.panes {
			emu := p.Emulator()
			_, isPinned := livePinned[paneKey(p)]
			snap.Panes[i] = web.PaneState{
				Name:     p.Name(),
				Activity: p.Activity().String(),
				Alive:    p.IsAlive(),
				Visible:  !ls.Hidden[paneKey(p)],
				Pinned:   ls.Mode == LayoutLive && isPinned,
				BeadID:   p.BeadID(),
				Order:    i,
				Cols:     emu.Width(),
				Rows:     emu.Height(),
			}
		}

		// Live mode info.
		if ls.Mode == LayoutLive {
			snap.Live = &web.LiveInfo{
				Pinned: livePinned,
				Slots:  ls.LiveSlots,
			}
		}
	})
	return snap, ok
}

// tuiEventProvider adapts the TUI's agent event system to the web.EventProvider
// interface. Subscribers receive events when the TUI calls BroadcastWebEvent.
type tuiEventProvider struct {
	t   *TUI
	mu  sync.Mutex
	subs map[string]chan web.AgentEventInfo
}

var _ web.EventProvider = (*tuiEventProvider)(nil)

func (p *tuiEventProvider) SubscribeEvents(id string) chan web.AgentEventInfo {
	ch := make(chan web.AgentEventInfo, 64)
	p.mu.Lock()
	if p.subs == nil {
		p.subs = make(map[string]chan web.AgentEventInfo)
	}
	p.subs[id] = ch
	p.mu.Unlock()
	return ch
}

func (p *tuiEventProvider) UnsubscribeEvents(id string) {
	p.mu.Lock()
	ch, ok := p.subs[id]
	if ok {
		delete(p.subs, id)
	}
	p.mu.Unlock()
	if ok {
		close(ch)
	}
}

// BroadcastWebEvent sends an agent event to all web subscribers. Called from
// the TUI's handleAgentEvent path. Non-blocking: drops events for slow consumers.
func (p *tuiEventProvider) BroadcastWebEvent(ev AgentEvent) {
	info := web.AgentEventInfo{
		Kind:   ev.Type.String(),
		Pane:   ev.Pane,
		BeadID: ev.BeadID,
		Detail: ev.Detail,
		Time:   ev.Time.Format("2006-01-02T15:04:05Z07:00"),
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, ch := range p.subs {
		select {
		case ch <- info:
		default:
			// Slow consumer: drop event.
		}
	}
}

// tuiPaneWriter adapts the TUI to the web.PaneWriter interface. It writes raw
// bytes to a pane's PTY master, serialized with sendMu to prevent interleaving
// with IPC sends.
type tuiPaneWriter struct {
	t *TUI
}

var _ web.PaneWriter = (*tuiPaneWriter)(nil)

// WriteToPTY writes raw bytes to the named pane's PTY. Uses sendMu for
// serialization with concurrent IPC sends. Returns an error if the pane is not
// found or not a local pane.
func (w *tuiPaneWriter) WriteToPTY(paneName string, data []byte) error {
	var pane *Pane
	ok := w.t.runOnMain(func() {
		pv := w.t.findPaneByName(paneName)
		if pv == nil {
			return
		}
		if lp, isLocal := pv.(*Pane); isLocal {
			pane = lp
		}
	})
	if !ok {
		return fmt.Errorf("tui shutting down")
	}
	if pane == nil {
		return fmt.Errorf("pane %q not found", paneName)
	}

	pane.sendMu.Lock()
	defer pane.sendMu.Unlock()
	if pane.ptmx == nil {
		return fmt.Errorf("pane %q PTY closed", paneName)
	}
	_, err := pane.ptmx.Write(data)
	return err
}

// tuiPinToggler adapts the TUI to the web.PinToggler interface.
// Toggling a pin in live mode either pins the agent to its current slot
// or removes its pin.
type tuiPinToggler struct {
	t *TUI
}

var _ web.PinToggler = (*tuiPinToggler)(nil)

// TogglePin toggles the live-mode pin for the named pane. If the pane is
// already pinned, it is unpinned. If not pinned, it is pinned to its current
// slot in LiveSlots. Returns the new pinned state and true, or false if the
// pane is not found or live mode is not active.
func (p *tuiPinToggler) TogglePin(paneName string) (bool, bool) {
	var pinned bool
	var found bool
	ok := p.t.runOnMain(func() {
		if p.t.layoutState.Mode != LayoutLive {
			return
		}
		pv := p.t.findPaneByName(paneName)
		if pv == nil {
			return
		}
		key := paneKey(pv)

		// Check if already pinned.
		if _, isPinned := p.t.layoutState.LivePinned[key]; isPinned {
			// Unpin.
			delete(p.t.layoutState.LivePinned, key)
			if p.t.liveEngine != nil {
				p.t.liveEngine.Pinned = p.t.layoutState.LivePinned
			}
			pinned = false
			found = true
		} else {
			// Find current slot for this agent.
			slot := -1
			for i, s := range p.t.layoutState.LiveSlots {
				if s == key {
					slot = i
					break
				}
			}
			if slot < 0 {
				return
			}
			if p.t.layoutState.LivePinned == nil {
				p.t.layoutState.LivePinned = make(map[string]int)
			}
			// Remove any existing pin on this slot.
			for k, v := range p.t.layoutState.LivePinned {
				if v == slot {
					delete(p.t.layoutState.LivePinned, k)
				}
			}
			p.t.layoutState.LivePinned[key] = slot
			if p.t.liveEngine != nil {
				p.t.liveEngine.Pinned = p.t.layoutState.LivePinned
			}
			pinned = true
			found = true
		}
		p.t.applyLayout()
		p.t.saveLayoutIfConfigured()
	})
	if !ok || !found {
		return false, false
	}
	return pinned, true
}
