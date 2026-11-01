//go:build linux

package tui

import "golang.org/x/sys/unix"

// ioctlWriteTermios sets a termios; tests only (production never writes one).
const ioctlWriteTermios = unix.TCSETS
