//go:build !windows

package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

func socketPath(projectRoot, projectName string) string {
	if projectRoot == "" {
		var b [8]byte
		rand.Read(b[:])
		return fmt.Sprintf("/tmp/initech-%s-%s.sock", projectName, hex.EncodeToString(b[:]))
	}
	return filepath.Join(projectRoot, ".initech", "initech.sock")
}

func listenIPC(socketPath string) (net.Listener, error) {
	if _, statErr := os.Stat(socketPath); statErr == nil {
		conn, dialErr := net.DialTimeout("unix", socketPath, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			// Reachable ONLY from window 1's startup now: startIPC has a single
			// caller and it sits behind the not-a-secondary-window branch
			// (ini-civ). That matters for the wording -- "use initech down" was
			// catastrophic advice when a viewer could land here, because it told
			// the operator to kill the fleet he was trying to put on a second
			// monitor.
			//
			// It names --window N because that is what the operator who hits
			// this is usually reaching for: running plain `initech` a second
			// time in the same project is what someone does when they want a
			// second screen, and the old text sent them to tear the session down.
			return nil, fmt.Errorf("a session is already running in this project (socket %s is active).\n"+
				"To put another monitor on the SAME session, run 'initech --window 2' instead.\n"+
				"To stop the running session first, run 'initech down'", socketPath)
		}
		os.Remove(socketPath)
	}
	os.MkdirAll(filepath.Dir(socketPath), 0700)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	os.Chmod(socketPath, 0700)
	return ln, nil
}

// DialIPC connects to the IPC endpoint at the given path.
func DialIPC(socketPath string) (net.Conn, error) {
	return net.DialTimeout("unix", socketPath, 500*time.Millisecond)
}
