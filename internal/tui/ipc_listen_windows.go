//go:build windows

package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func socketPath(projectRoot, projectName string) string {
	name := projectName
	if name == "" {
		name = "default"
	}
	name = strings.ReplaceAll(name, " ", "_")
	if projectRoot != "" {
		return filepath.Join(projectRoot, ".initech", "initech-"+name+".port")
	}
	var b [4]byte
	rand.Read(b[:])
	return filepath.Join(os.TempDir(), "initech-"+name+"-"+hex.EncodeToString(b[:])+".port")
}

func listenIPC(portFile string) (net.Listener, error) {
	if data, err := os.ReadFile(portFile); err == nil {
		addr := strings.TrimSpace(string(data))
		conn, dialErr := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			return nil, fmt.Errorf("session already running (%s is active). Use 'initech down' to stop it first", addr)
		}
		os.Remove(portFile)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen tcp: %w", err)
	}

	os.MkdirAll(filepath.Dir(portFile), 0700)
	os.WriteFile(portFile, []byte(ln.Addr().String()), 0600)

	return ln, nil
}

// DialIPC connects to the IPC endpoint via the port file.
func DialIPC(portFile string) (net.Conn, error) {
	data, err := os.ReadFile(portFile)
	if err != nil {
		return nil, fmt.Errorf("read port file %s: %w", portFile, err)
	}
	addr := strings.TrimSpace(string(data))
	return net.DialTimeout("tcp", addr, 500*time.Millisecond)
}
