package tui

import (
	"os/exec"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/gdamore/tcell/v2"
)

func (t *TUI) handleMouse(ev *tcell.EventMouse) {
	if t.cmd.active {
		return
	}
	mx, my := ev.Position()

	switch {
	case ev.Buttons()&tcell.Button1 != 0 && !t.sel.active:
		// Button1 press: focus resolution works on all PaneViews (local + remote).
		// Text selection and mouse forwarding only apply to local *Pane.
		for i, pv := range t.panes {
			if t.layoutState.Hidden[pv.Name()] {
				continue
			}
			r := pv.GetRegion()
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				if t.layoutState.Focused != pv.Name() {
					t.layoutState.Focused = pv.Name()
					t.applyLayout()
				}
				// Text selection + mouse forwarding: local panes only.
				if p, ok := pv.(*Pane); ok {
					lx := mx - r.X
					ly := my - r.Y
					if ly < 0 {
						ly = 0
					}
					t.forwardMouseEvent(p, lx, ly, uv.MouseLeft, false, false, ev.Modifiers())
					sr, ro := p.contentOffset()
					t.sel.active = true
					t.sel.pane = i
					t.sel.startX = lx
					t.sel.startY = ly
					t.sel.endX = lx
					t.sel.endY = ly
					t.sel.startRow = sr
					t.sel.renderOffset = ro
				}
				return
			}
		}

	case ev.Buttons()&tcell.Button1 != 0 && t.sel.active:
		// Drag: update selection end and forward motion.
		if t.sel.pane < len(t.panes) {
			p, _ := t.panes[t.sel.pane].(*Pane)
			if p == nil { break }
			r := p.region
			lx := mx - r.X
			ly := my - r.Y
			cols, rows := r.InnerSize()
			if lx < 0 {
				lx = 0
			}
			if lx >= cols {
				lx = cols - 1
			}
			if ly < 0 {
				ly = 0
			}
			if ly >= rows {
				ly = rows - 1
			}
			t.forwardMouseEvent(p, lx, ly, uv.MouseLeft, true, false, ev.Modifiers())
			t.sel.endX = lx
			t.sel.endY = ly
		}

	case ev.Buttons() == tcell.ButtonNone && t.sel.active:
		// Release: forward to pane, copy selection, and clear.
		if t.sel.pane < len(t.panes) {
			p, _ := t.panes[t.sel.pane].(*Pane)
			if p == nil { break }
			r := p.region
			lx := mx - r.X
			ly := my - r.Y
			if ly < 0 {
				ly = 0
			}
			t.forwardMouseEvent(p, lx, ly, uv.MouseNone, false, true, ev.Modifiers())
		}
		t.copySelection()
		t.sel.active = false

	case ev.Buttons()&tcell.Button2 != 0:
		// Middle click: forward to focused pane only.
		t.forwardMouseToFocused(mx, my, uv.MouseMiddle, false, false, ev.Modifiers())

	case ev.Buttons()&tcell.Button3 != 0:
		// Right click: forward to focused pane only.
		t.forwardMouseToFocused(mx, my, uv.MouseRight, false, false, ev.Modifiers())

	case ev.Buttons()&tcell.WheelUp != 0:
		// Scroll back into history for the pane under cursor.
		// Focus works on all PaneViews; scroll only on local *Pane.
		for _, pv := range t.panes {
			if t.layoutState.Hidden[pv.Name()] {
				continue
			}
			r := pv.GetRegion()
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.layoutState.Focused = pv.Name()
				if p, ok := pv.(*Pane); ok {
					p.ScrollUp(3)
				}
				return
			}
		}

	case ev.Buttons()&tcell.WheelDown != 0:
		// Scroll toward live view for the pane under cursor.
		for _, pv := range t.panes {
			if t.layoutState.Hidden[pv.Name()] {
				continue
			}
			r := pv.GetRegion()
			if mx >= r.X && mx < r.X+r.W && my >= r.Y && my < r.Y+r.H {
				t.layoutState.Focused = pv.Name()
				if p, ok := pv.(*Pane); ok {
					p.ScrollDown(3)
				}
				return
			}
		}
	}
}

// forwardMouseEvent translates pane-local content coordinates to emulator
// coordinates and sends the mouse event. The emulator silently drops the
// event if the child hasn't enabled mouse reporting.
func (t *TUI) forwardMouseEvent(p *Pane, lx, ly int, button uv.MouseButton, isMotion, isRelease bool, mods tcell.ModMask) {
	startRow, renderOffset := p.contentOffset()
	emuY := startRow + (ly - renderOffset)
	emuX := lx
	if emuY < 0 {
		emuY = 0
	}
	if emuX < 0 {
		emuX = 0
	}

	var mod uv.KeyMod
	if mods&tcell.ModShift != 0 {
		mod |= uv.ModShift
	}
	if mods&tcell.ModAlt != 0 {
		mod |= uv.ModAlt
	}
	if mods&tcell.ModCtrl != 0 {
		mod |= uv.ModCtrl
	}

	m := uv.Mouse{X: emuX, Y: emuY, Button: button, Mod: mod}
	switch {
	case isRelease:
		p.ForwardMouse(uv.MouseReleaseEvent(m))
	case isMotion:
		p.ForwardMouse(uv.MouseMotionEvent(m))
	default:
		p.ForwardMouse(uv.MouseClickEvent(m))
	}
}

// forwardMouseToFocused forwards a mouse event to the focused pane if the
// click is within its region.
func (t *TUI) forwardMouseToFocused(mx, my int, button uv.MouseButton, isMotion, isRelease bool, mods tcell.ModMask) {
	fp := t.focusedPane()
	if fp == nil {
		return
	}
	p, ok := fp.(*Pane)
	if !ok {
		return
	}
	r := p.region
	if mx < r.X || mx >= r.X+r.W || my < r.Y || my >= r.Y+r.H {
		return
	}
	lx := mx - r.X
	ly := my - r.Y
	if ly < 0 {
		ly = 0
	}
	t.forwardMouseEvent(p, lx, ly, button, isMotion, isRelease, mods)
}

// copySelection extracts selected text from the pane's emulator and copies to clipboard.
func (t *TUI) copySelection() {
	if t.sel.pane >= len(t.panes) {
		return
	}
	// Skip zero-width selections (plain clicks with no drag). Without this
	// guard, every pane-focus click overwrites the system clipboard with the
	// single character under the cursor (ini-o0j).
	if t.sel.startX == t.sel.endX && t.sel.startY == t.sel.endY {
		return
	}
	pv := t.panes[t.sel.pane]
	p, ok := pv.(*Pane)
	if !ok {
		return
	}

	// Normalize selection bounds (start may be after end).
	r0, c0, r1, c1 := t.sel.startY, t.sel.startX, t.sel.endY, t.sel.endX
	if r0 > r1 || (r0 == r1 && c0 > c1) {
		r0, c0, r1, c1 = r1, c1, r0, c0
	}

	cols, rows := p.region.InnerSize()
	if r1 >= rows {
		r1 = rows - 1
	}

	startRow := t.sel.startRow
	renderOffset := t.sel.renderOffset
	emu := p.Emulator()
	emuRows := emu.Height()

	var buf strings.Builder
	for row := r0; row <= r1; row++ {
		emuRow := startRow + (row - renderOffset)
		if emuRow < 0 || emuRow >= emuRows {
			continue
		}

		startCol := 0
		endCol := cols
		if row == r0 {
			startCol = c0
		}
		if row == r1 {
			endCol = c1 + 1
		}
		if endCol > cols {
			endCol = cols
		}

		var line strings.Builder
		for col := startCol; col < endCol; col++ {
			cell := emu.CellAt(col, emuRow)
			if cell != nil && cell.Content != "" {
				line.WriteString(cell.Content)
			} else {
				line.WriteByte(' ')
			}
		}

		// Trim trailing spaces per line.
		text := strings.TrimRight(line.String(), " ")
		buf.WriteString(text)
		if row < r1 {
			buf.WriteByte('\n')
		}
	}

	text := buf.String()
	if text == "" {
		return
	}

	// Copy to macOS clipboard via pbcopy.
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	cmd.Run()
}
