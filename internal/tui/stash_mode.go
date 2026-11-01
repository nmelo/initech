package tui

import (
	"errors"
	"os"

	"github.com/charmbracelet/x/xpty"
)

// THE STASH KEY GOES ONLY INTO A TERMINAL IN RAW MODE (ini-933).
//
// Before a send that submits, sendPaneTextLocked types Ctrl+S, Claude Code's
// stash key, so a draft the operator was composing survives the paste
// (ini-gd0). That byte is a key only to a program that has put its terminal
// in raw mode, as Claude Code does. In a terminal still in cooked mode (a
// plain shell, an agent still booting, a test's echo child) the line
// discipline reads 0x13 as XOFF when IXON is set and STOPS ALL OUTPUT from the
// pane until an XON that never comes; with IXON clear it is still a stray
// control character typed into a shell line. Linux ptys start with IXON set,
// so a Linux pane in cooked mode froze at its first send.
//
// So the mode is READ from the pane's terminal, through the pty master, at the
// moment of the send, never inferred from the pane's name, agent type or
// state, and never written: initech does not touch the termios a program set
// for itself.

// termModeReader is implemented by a pty that can report its terminal's mode
// directly. Test ptys that are not terminals (a pipe) implement it to say
// which mode they model; production ptys are read through their master.
type termModeReader interface {
	TermRawMode() (bool, error)
}

// masterHolder is the part of xpty.UnixPty this file needs: the master end,
// the only end the parent keeps once the child has started (pane_tty.go). A
// master's termios request answers for the terminal the child sees.
type masterHolder interface {
	Master() *os.File
}

var errNoPTY = errors.New("pane has no pty")

// ptyRawMode reports whether the terminal behind pty is in raw mode now:
// canonical input off and output flow control off, the two termios bits
// that make 0x13 a key rather than an edit or an XOFF. An error means the
// mode could not be read; the caller then types no stash key.
func ptyRawMode(pty xpty.Pty) (bool, error) {
	if pty == nil {
		return false, errNoPTY
	}
	if r, ok := pty.(termModeReader); ok {
		return r.TermRawMode()
	}
	if m, ok := pty.(masterHolder); ok {
		return termiosRawMode(m.Master())
	}
	return platformDefaultRawMode()
}

// stashKeyAllowed reports whether this send may type the stash key: only when
// the pane's terminal reads raw. A terminal whose mode cannot be read gets no
// stash, and the first such send on each pane logs why, once.
func (p *Pane) stashKeyAllowed() bool {
	raw, err := ptyRawMode(p.ptmx)
	if err != nil {
		if p.stashModeLogged.CompareAndSwap(false, true) {
			LogWarn("inject", "no stash key: the terminal's mode could not be read",
				"pane", p.Name(), "err", err)
		}
		return false
	}
	if !raw {
		LogDebug("inject", "no stash key: the terminal is in cooked mode", "pane", p.Name())
	}
	return raw
}
