package tui

// viewerSize is the size one viewer last asked for on one agent, and when
// relative to every other request, so "the most recent" has one answer.
type viewerSize struct {
	rows, cols int
	seq        uint64
}

// resizeForViewer applies a viewer's size to the pane and remembers it. A
// headless peer keeps one size per pane, so a second viewer's request replaces
// the first's; remembering each viewer's lets viewerLeft give the size back
// when the second one goes. It holds viewerSizeMu across the resize, so a
// re-apply cannot overwrite a newer request. Only a headless daemon with a
// named viewer records: a window server's panes are local, and a viewer with no
// name cannot be seen to leave.
func (d *Daemon) resizeForViewer(p *Pane, peer string, rows, cols int) {
	d.viewerSizeMu.Lock()
	defer d.viewerSizeMu.Unlock()
	if d.headless {
		p.ResizeExact(rows, cols)
	} else {
		p.Resize(rows, cols)
	}
	if d.headless && peer != "" {
		if d.viewerSizes == nil {
			d.viewerSizes = make(map[string]map[string]viewerSize)
		}
		if d.viewerSizes[p.Name()] == nil {
			d.viewerSizes[p.Name()] = make(map[string]viewerSize)
		}
		d.viewerSeq++
		d.viewerSizes[p.Name()][peer] = viewerSize{rows: rows, cols: cols, seq: d.viewerSeq}
	}
	d.noteLastSize(p.Name(), rows, cols)
}

// noteLastSize records the size in effect, which a restart spawns the new
// process at.
func (d *Daemon) noteLastSize(agent string, rows, cols int) {
	d.panesMu.Lock()
	defer d.panesMu.Unlock()
	if d.lastSizes == nil {
		d.lastSizes = make(map[string][2]int)
	}
	d.lastSizes[agent] = [2]int{rows, cols}
}

// viewerLeft forgets a viewer that has gone. For each agent whose size it was
// the last to set, the most recent size from a viewer still connected goes back
// on the pane, since nothing else will send it: a viewer sends a size only when
// its own tile changes. With no viewer left the size stays, and a remaining
// size equal to the one in effect causes no resize, so no needless repaint.
func (d *Daemon) viewerLeft(peer string) {
	d.viewerSizeMu.Lock()
	defer d.viewerSizeMu.Unlock()
	for agent, viewers := range d.viewerSizes {
		left, had := viewers[peer]
		if !had {
			continue
		}
		delete(viewers, peer)
		var back viewerSize
		backPeer := ""
		for name, v := range viewers {
			if v.seq > back.seq {
				back, backPeer = v, name
			}
		}
		// The departed viewer's size was not the one in effect, or nobody
		// remains to take it back: nothing to do.
		if backPeer == "" || back.seq > left.seq || (back.rows == left.rows && back.cols == left.cols) {
			continue
		}
		p := d.findPane(agent)
		if p == nil {
			continue
		}
		p.ResizeExact(back.rows, back.cols)
		d.noteLastSize(agent, back.rows, back.cols)
		LogInfo("daemon", "resize restored", "agent", agent, "left", peer, "viewer", backPeer,
			"rows", back.rows, "cols", back.cols)
	}
}
