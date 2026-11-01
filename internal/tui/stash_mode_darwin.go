//go:build darwin

package tui

import "golang.org/x/sys/unix"

// ioctlReadTermios is the request that reads a terminal's termios on macOS.
const ioctlReadTermios = unix.TIOCGETA
