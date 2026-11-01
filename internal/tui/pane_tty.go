package tui

import (
	"os"

	"github.com/charmbracelet/x/xpty"
)

// THE PARENT LETS GO OF THE SLAVE ONCE THE CHILD HAS IT (ini-dyv).
//
// xpty opens a pty pair and keeps both ends; Start hands the slave to the
// child as its stdin, stdout and stderr. If the parent then keeps its own copy
// of the slave open, the master never hangs up when the child exits: on Linux
// a master reports end of file only once EVERY slave descriptor is closed, so
// readLoop never ended, the pane never read as dead, and everything that waits
// on a dead pane (a failed resume keeping its mail, dead-pane marking, the
// restart path) never fired. creack/pty's own Start closes the parent's tty
// for the same reason.
//
// Every parent-side read of the terminal goes through the master instead: the
// stash-mode read (stash_mode.go) and resize (xpty's setWinsize).

// slaveHolder is the part of xpty.UnixPty that exposes the slave end.
type slaveHolder interface {
	Slave() *os.File
}

// releaseParentSlave closes the parent's copy of pty's slave. It is called
// once, right after the child has started. A pty with no slave (Windows
// ConPTY) has nothing to release.
func releaseParentSlave(pty xpty.Pty, pane string) {
	s, ok := pty.(slaveHolder)
	if !ok {
		return
	}
	f := s.Slave()
	if f == nil {
		return
	}
	if err := f.Close(); err != nil {
		LogWarn("pane", "could not close the parent's copy of the pty slave; this pane's exit may go unseen",
			"pane", pane, "err", err)
	}
}
