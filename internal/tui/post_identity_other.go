//go:build !darwin && !linux

package tui

import (
	"net"
	"runtime"
)

// Windows uses TCP, not named pipes. A per-pane token is the named future
// mechanism; this release refuses instead of pretending TCP supplies identity.
func postPeerPID(net.Conn) (int, error) { return 0, postPlatformError(runtime.GOOS) }
func postParentPID(int) (int, error)    { return 0, postPlatformError(runtime.GOOS) }
