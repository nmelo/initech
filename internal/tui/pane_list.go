package tui

// The pane list's only writers (ini-813u). Window 1's window server serves
// pane OBJECTS, so a pane that leaves or enters t.panes without the server
// hearing of it is a bug every second window pays for: after a restart the
// server kept serving the closed object, so a second window's keys were
// dropped while the reply said OK and its copy of the agent froze; a hot-added
// agent never reached a second window at all. Every change to t.panes goes
// through one of these functions, which update the list and the server in the
// same step. TestPaneList_OnlyTheseFunctionsWriteThePaneList enforces it.
//
// Main goroutine only, like every other access to t.panes. A nil window
// server (a single-window session) is a no-op on the server side.

// replacePane puts np in slot i: a restart, start or wake successor. A local
// successor is handed to the window server, which serves it and rebinds the
// existing stream, so attached windows keep streaming and typing into it.
func (t *TUI) replacePane(i int, np PaneView) {
	t.panes[i] = np
	if lp, ok := np.(*Pane); ok {
		t.windowSrv.ReplacePane(lp)
	}
}

// appendPane adds p to the end of the list. A local pane is served to every
// attached window, which lists and streams it.
func (t *TUI) appendPane(p PaneView) {
	t.panes = append(t.panes, p)
	if lp, ok := p.(*Pane); ok {
		t.windowSrv.AddPane(lp)
	}
}

// removePaneAt removes slot i. A local pane stops being served, and every
// attached window drops it.
func (t *TUI) removePaneAt(i int) {
	p := t.panes[i]
	t.panes = append(t.panes[:i], t.panes[i+1:]...)
	if lp, ok := p.(*Pane); ok {
		t.windowSrv.RemovePane(lp.Name())
	}
}

// swapPanes exchanges two slots: the same set, in a new order, so the server
// is not involved.
func (t *TUI) swapPanes(i, j int) {
	t.panes[i], t.panes[j] = t.panes[j], t.panes[i]
}

// movePane moves the pane at from so it sits at index to of the list without
// it (an Agents-panel drag). The same set, so the server is not involved.
func (t *TUI) movePane(from, to int) {
	p := t.panes[from]
	t.panes = append(t.panes[:from], t.panes[from+1:]...)
	if to > len(t.panes) {
		to = len(t.panes)
	}
	t.panes = append(t.panes[:to], append([]PaneView{p}, t.panes[to:]...)...)
}

// setRemotePanes replaces the list after a peer update, which changes only
// other machines' panes. The window server serves local panes alone, so a
// change to the local set here would skip it: such a call is refused and
// logged rather than applied.
func (t *TUI) setRemotePanes(ps []PaneView) {
	if !sameLocalPanes(t.panes, ps) {
		LogWarn("pane-list", "refused a remote-pane update that changes local panes",
			"before", len(localPanes(t.panes)), "after", len(localPanes(ps)))
		return
	}
	t.panes = ps
}

// sameLocalPanes reports whether a and b hold the same local pane objects.
func sameLocalPanes(a, b []PaneView) bool {
	la, lb := localPanes(a), localPanes(b)
	if len(la) != len(lb) {
		return false
	}
	set := make(map[*Pane]bool, len(la))
	for _, p := range la {
		set[p] = true
	}
	for _, p := range lb {
		if !set[p] {
			return false
		}
	}
	return true
}
