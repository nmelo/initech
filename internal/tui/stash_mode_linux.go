//go:build linux

package tui

import "golang.org/x/sys/unix"

// ioctlReadTermios is the request that reads a terminal's termios on Linux.
const ioctlReadTermios = unix.TCGETS
