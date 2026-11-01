//go:build linux || darwin

package tui

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// slaveRawMode reads the slave's termios (read only, through SyscallConn so
// the file's blocking mode is left alone) and reports raw: ICANON and IXON
// both clear.
func slaveRawMode(f *os.File) (bool, error) {
	if f == nil {
		return false, errors.New("pty has no slave")
	}
	rc, err := f.SyscallConn()
	if err != nil {
		return false, err
	}
	var t *unix.Termios
	var ioErr error
	if err := rc.Control(func(fd uintptr) {
		t, ioErr = unix.IoctlGetTermios(int(fd), ioctlReadTermios)
	}); err != nil {
		return false, err
	}
	if ioErr != nil {
		return false, ioErr
	}
	return t.Lflag&unix.ICANON == 0 && t.Iflag&unix.IXON == 0, nil
}

// platformDefaultRawMode answers for a pty that exposes neither a mode nor a
// slave: on unix that mode cannot be read.
func platformDefaultRawMode() (bool, error) {
	return false, errors.New("pty exposes no terminal to read")
}
