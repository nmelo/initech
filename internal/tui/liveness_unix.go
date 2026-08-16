//go:build !windows

package tui

// Process liveness on unix: signal 0 (ini-uop2).
//
// UNIX SEMANTICS ARE UNCHANGED BY THIS SPLIT, deliberately. The probe exists
// because ini-g7fl measured that an exec-failed child still produces OUTPUT
// (the shell's error text), so waitForInit returning success proves nothing
// about health -- signal 0 does. Any error here still means dead, exactly as
// before; the split was made to give WINDOWS an honest answer, not to soften
// this one.

import (
	"os"
	"syscall"
)

// childProcessAlive reports whether a child we spawned is still running.
//
// Any error means dead, including EPERM: a process we spawned ourselves cannot
// legitimately refuse us, so an error here is the exec-failure g7fl exists to
// catch rather than a permissions edge.
func childProcessAlive(p *os.Process) bool {
	if p == nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// pidExists reports whether ANY process holds this pid -- a weaker question
// than childProcessAlive, and asked by a different caller for a different
// reason (the PID file's stale-instance check).
//
// EPERM counts as EXISTS here, and that asymmetry with childProcessAlive is
// the point: the pid file may name a process owned by someone else, and
// "exists but not mine" must not be read as "gone", which would delete a live
// instance's pid file.
func pidExists(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errorIsPermission(err)
}

// errorIsPermission keeps the EPERM test in one place so both platforms read
// the same way at the call site.
func errorIsPermission(err error) bool {
	return err == syscall.EPERM || os.IsPermission(err)
}
