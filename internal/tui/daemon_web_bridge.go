// daemon_web_bridge.go adapts the headless Daemon to the web.* interfaces
// so the web companion can run alongside initech serve.
package tui

import (
	"github.com/nmelo/initech/internal/web"
)

// daemonPaneLister implements web.PaneLister for the Daemon.
type daemonPaneLister struct {
	d *Daemon
}

func (l *daemonPaneLister) AllPanes() ([]web.PaneInfo, bool) {
	panes := make([]web.PaneInfo, len(l.d.panes))
	for i, p := range l.d.panes {
		panes[i] = web.PaneInfo{
			Name:     p.Name(),
			Activity: p.Activity().String(),
			Alive:    p.IsAlive(),
			Visible:  true,
		}
	}
	return panes, true
}

// daemonPaneSubscriber implements web.PaneSubscriber for the Daemon,
// delegating to each Pane's built-in Subscribe/Unsubscribe fan-out.
type daemonPaneSubscriber struct {
	d *Daemon
}

func (s *daemonPaneSubscriber) SubscribePane(paneName, subscriberID string) (chan []byte, bool) {
	for _, p := range s.d.panes {
		if p.Name() == paneName {
			return p.Subscribe(subscriberID), true
		}
	}
	return nil, false
}

func (s *daemonPaneSubscriber) UnsubscribePane(paneName, subscriberID string) {
	for _, p := range s.d.panes {
		if p.Name() == paneName {
			p.Unsubscribe(subscriberID)
			return
		}
	}
}

// daemonStateProvider implements web.StateProvider for the Daemon.
// It returns a synthetic layout based on pane count (no TUI layout state).
type daemonStateProvider struct {
	d *Daemon
}

func (sp *daemonStateProvider) CurrentState() (web.StateSnapshot, bool) {
	n := len(sp.d.panes)
	cols, rows := autoGrid(n)

	panes := make([]web.PaneState, n)
	for i, p := range sp.d.panes {
		panes[i] = web.PaneState{
			Name:     p.Name(),
			Activity: p.Activity().String(),
			Alive:    p.IsAlive(),
			Visible:  true,
			BeadID:   p.BeadID(),
			Order:    i,
		}
	}

	return web.StateSnapshot{
		Layout: web.LayoutInfo{
			Mode: "grid",
			Cols: cols,
			Rows: rows,
		},
		Panes: panes,
	}, true
}

// daemonPaneWriter implements web.PaneWriter for the Daemon.
type daemonPaneWriter struct {
	d *Daemon
}

func (w *daemonPaneWriter) WriteToPTY(paneName string, data []byte) error {
	for _, p := range w.d.panes {
		if p.Name() == paneName {
			p.sendMu.Lock()
			defer p.sendMu.Unlock()
			if p.ptmx != nil {
				_, err := p.ptmx.Write(data)
				return err
			}
			return nil
		}
	}
	return nil
}
