//go:build windows

package tui

// Process liveness on Windows: ask the OS about the handle (ini-uop2).
//
// WHY THIS FILE EXISTS. The portable code probed liveness with
// Process.Signal(syscall.Signal(0)). On Windows that is not a weaker check --
// it is not a check at all. Go's os.(*Process).signal returns
// syscall.EWINDOWS for every signal except Kill, UNCONDITIONALLY, without
// consulting the process:
//
//	// TODO(rsc): Handle Interrupt too?
//	return syscall.Errno(syscall.EWINDOWS)
//
// So the probe answered "dead" for every living process, on every resume. The
// caller treats dead as "the wake failed", re-queues the messages and marks
// the pane suspended again -- so suspend/resume was a ONE-WAY DOOR for Windows
// operators, and it shipped that way in v2.9.0 (ab299e5). No Windows test
// exercised resume until the suspend/stagger tests landed, which is CI doing
// the job commit-time guards structurally cannot: vet cross-compiles, but
// cross-compiled tests cannot RUN.
//
// THE TRADE, chosen deliberately and stated because the bead asked for it.
// The two failure directions are not symmetric:
//
//   - False DEAD (what we had) makes wake impossible: the operator's agent
//     never comes back and the product says it died.
//   - False ALIVE moves the failure to queue-drain into a corpse: messages are
//     injected at a pane whose process is gone, and the pane's own EOF path
//     then reports it.
//
// Trusting IsAlive alone -- the bead's stated minimum -- buys false ALIVE,
// because IsAlive flips only when readLoop sees EOF and therefore LAGS. The
// handle check below is not meaningfully more expensive and is honest in both
// directions, so it is what this uses; IsAlive remains the first gate at the
// call site, unchanged.

import (
	"os"
	"syscall"
)

// childProcessAlive reports whether a child we spawned is still running.
func childProcessAlive(p *os.Process) bool {
	if p == nil {
		return false
	}
	return pidExists(p.Pid)
}

// pidExists reports whether a process with this pid is still running.
//
// WaitForSingleObject rather than GetExitCodeProcess, on purpose: a process
// that exits with code 259 is indistinguishable from STILL_ACTIVE, and a
// liveness probe that a program can defeat by choosing an exit code is the
// kind of subtlety that surfaces years later. Waiting zero milliseconds asks
// the scheduler directly -- WAIT_TIMEOUT means the handle has not been
// signalled, i.e. the process is still running.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	const access = syscall.PROCESS_QUERY_INFORMATION | syscall.SYNCHRONIZE
	h, err := syscall.OpenProcess(access, false, uint32(pid))
	if err != nil {
		// The pid is gone, or belongs to a process this session may not open.
		// Both read as "not ours to talk to"; for the resume probe the pid is
		// always our own child, where the former is the only real case.
		return false
	}
	defer syscall.CloseHandle(h)

	ev, err := syscall.WaitForSingleObject(h, 0)
	if err != nil {
		return false
	}
	return ev == uint32(syscall.WAIT_TIMEOUT)
}

// errorIsPermission mirrors the unix helper so both platforms compile the same
// call sites. Windows liveness never routes through errno permission checks.
func errorIsPermission(err error) bool {
	return os.IsPermission(err)
}
