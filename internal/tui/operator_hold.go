package tui

import (
	"sync"

	uv "github.com/charmbracelet/ultraviolet"
)

// THE OPERATOR'S INPUT WAITS FOR A MESSAGE'S ENTER (ini-q0fy).
//
// The typing hold decides once, before a send, that the operator has paused.
// The send then takes a moment to type: the stash key, the paste, the wait
// for the body to show in the box, the Enter and its confirmation. A key the
// operator pressed in that moment used to reach the child at once, after the
// body and before the Enter, and was submitted with the message: the CTO's
// "yes" went out as "...8.ye", then "s" alone (0.68 s between the hold's
// release and the submit, measured in that rig's log).
//
// So, from a send's first byte to its end (the confirmed Enter, a withheld
// submit, a failed or stuck write, a close), every piece of operator input
// to that pane is held in arrival order, and written in that order when the
// send ends. Only that pane's input is held. Typing is noted when the key
// arrives, not when it is written, so a held key still holds the next
// message.
//
// ONE FIFO FOR EVERY OPERATOR BYTE. Keys and mouse events reach the child
// through the emulator's input pipe (encoded there, relayed by
// responseLoop). Raw operator bytes (Shift+Enter, a paste, another window's
// stream) are written into that same pipe rather than straight onto the
// input queue, so they cannot overtake a key still crossing it: the order the
// child reads is the order the operator typed, held or not.

// operatorItem is one piece of operator input: a key, a mouse event, or raw
// bytes. Exactly one is set.
type operatorItem struct {
	key   uv.KeyEvent
	mouse uv.MouseEvent
	raw   []byte
}

// operatorHold is a pane's hold on operator input while a send is typing.
type operatorHold struct {
	mu   sync.Mutex
	on   bool
	held []operatorItem
}

// operatorInput writes one piece of operator input to the child, or holds it
// while a send is typing into this pane. It never waits on a send.
func (p *Pane) operatorInput(it operatorItem) {
	h := &p.opHold
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.on {
		if it.raw != nil {
			it.raw = append([]byte(nil), it.raw...) // the caller reuses its buffer
		}
		h.held = append(h.held, it)
		return
	}
	p.writeOperatorItem(it)
}

// writeOperatorItem hands one item to the emulator's input pipe, which
// responseLoop relays to the child in order. A pane with no emulator has no
// pipe to keep order against; its raw bytes go straight onto the input
// queue. Caller holds opHold.mu.
func (p *Pane) writeOperatorItem(it operatorItem) {
	switch {
	case p.emu == nil:
		p.writeInput(it.raw)
	case it.key != nil:
		p.emu.SendKey(it.key)
	case it.mouse != nil:
		p.emu.SendMouse(it.mouse)
	case len(it.raw) > 0:
		p.emu.SendText(string(it.raw))
	}
}

// holdOperatorInput holds this pane's operator input from now until the
// returned function runs: a send calls it before its first byte and defers
// the release, so every way the send ends releases the input.
func (p *Pane) holdOperatorInput() (release func()) {
	h := &p.opHold
	h.mu.Lock()
	h.on = true
	h.mu.Unlock()
	return p.releaseOperatorInput
}

// releaseOperatorInput writes the held input in arrival order and ends the
// hold, under the lock new input takes, so nothing passes what was held.
func (p *Pane) releaseOperatorInput() {
	h := &p.opHold
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, it := range h.held {
		p.writeOperatorItem(it)
	}
	h.held = nil
	h.on = false
}

// heldOperatorInput is how many pieces of operator input are held now.
func (p *Pane) heldOperatorInput() int {
	h := &p.opHold
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.held)
}
