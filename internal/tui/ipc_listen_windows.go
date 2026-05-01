//go:build windows

package tui

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func socketPath(projectRoot, projectName string) string {
	name := projectName
	if name == "" {
		name = "default"
	}
	name = strings.ReplaceAll(name, " ", "_")
	return `\\.\pipe\initech-` + name
}

func listenIPC(pipePath string) (net.Listener, error) {
	conn, dialErr := net.DialTimeout("unix", pipePath, 500*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		return nil, fmt.Errorf("session already running (pipe %s is active). Use 'initech down' to stop it first", pipePath)
	}
	ln, err := net.Listen("unix", pipePath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", pipePath, err)
	}
	return ln, nil
}

// DialIPC connects to the IPC endpoint at the given path.
func DialIPC(pipePath string) (net.Conn, error) {
	return net.DialTimeout("unix", pipePath, 500*time.Millisecond)
}
