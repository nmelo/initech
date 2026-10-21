package tui

// The window token (ini-cxia). Window 1's listener authenticates every
// window that attaches. When initech.yaml configures a token, that token is
// used by every window, as before. When it does not, window 1 generates a
// random token for its session and writes it to .initech/window-token, mode
// 0600, where a secondary window on the same machine reads it on every
// connect attempt. The token is never logged, printed or passed to a
// process: it lives in this file, in memory, and in the hello.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// windowTokenFile is the session token's file name inside .initech.
const windowTokenFile = "window-token"

// errNoWindowToken means no usable token file: window 1 is not running on
// this machine, or has not written its token yet.
var errNoWindowToken = errors.New("window 1 is not running here: start it first (initech), and this window connects once it is up")

// errNoProjectRoot refuses a token path relative to the working directory:
// a project with no root would otherwise read or write .initech/window-token
// wherever the process happens to run.
var errNoProjectRoot = errors.New("window token: the project has no root directory")

func windowTokenPath(root string) string {
	return filepath.Join(root, ".initech", windowTokenFile)
}

// newWindowToken returns 32 random bytes, hex encoded.
func newWindowToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate window token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeWindowToken writes tok to .initech/window-token, mode 0600, through a
// temp file and a rename, so a reader never sees a partial token and a stale
// file from a crashed session is replaced whole.
func writeWindowToken(root, tok string) error {
	if root == "" {
		return errNoProjectRoot
	}
	dir := filepath.Join(root, ".initech")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("window token: %w", err)
	}
	f, err := os.CreateTemp(dir, ".window-token-*")
	if err != nil {
		return fmt.Errorf("window token: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("window token: %w", err)
	}
	if _, err := f.WriteString(tok); err != nil {
		f.Close()
		return fmt.Errorf("window token: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("window token: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("window token: %w", err)
	}
	if err := os.Rename(tmp, windowTokenPath(root)); err != nil {
		return fmt.Errorf("window token: %w", err)
	}
	return nil
}

// readWindowToken reads the session token. A missing file, or one holding
// only whitespace, is errNoWindowToken; other read errors are wrapped without
// the file's content.
func readWindowToken(root string) (string, error) {
	if root == "" {
		return "", errNoProjectRoot
	}
	b, err := os.ReadFile(windowTokenPath(root))
	if errors.Is(err, os.ErrNotExist) {
		return "", errNoWindowToken
	}
	if err != nil {
		return "", fmt.Errorf("read window token: %w", err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", errNoWindowToken
	}
	return tok, nil
}

// removeWindowToken deletes the session token file on window 1's exit.
func removeWindowToken(root string) {
	if root == "" {
		return
	}
	os.Remove(windowTokenPath(root))
}
