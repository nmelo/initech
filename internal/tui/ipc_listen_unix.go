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
			return nil, fmt.Errorf("session already running (socket %s is active). Use 'initech down' to stop it first", socketPath)
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
