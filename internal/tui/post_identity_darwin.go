//go:build darwin

package tui

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func postPeerPID(conn net.Conn) (int, error) {
	c, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("connection is not a local Unix socket")
	}
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var socketErr error
	err = raw.Control(func(fd uintptr) { pid, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID) })
	if err != nil {
		return 0, err
	}
	return pid, socketErr
}

func postParentPID(pid int) (int, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	return int(info.Eproc.Ppid), nil
}
