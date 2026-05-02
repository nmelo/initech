package tui

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GenerateToken returns a cryptographically random 32-byte token
// encoded as base64.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// ReadOrCreateToken reads a persisted token from dir/token, or generates
// a new one and writes it. The dir is created with 0700 if it doesn't exist.
// The token file has 0600 permissions.
func ReadOrCreateToken(dir string) (string, error) {
	tok, _, err := ReadOrCreateTokenStatus(dir)
	return tok, err
}

// ReadOrCreateTokenStatus is like ReadOrCreateToken but also reports whether
// the token was newly generated (true) or read from an existing file (false).
// Used by 'initech serve' to decide whether to print the connection snippet.
func ReadOrCreateTokenStatus(dir string) (token string, created bool, err error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", false, fmt.Errorf("create token dir: %w", err)
	}

	tokenPath := filepath.Join(dir, "token")
	data, readErr := os.ReadFile(tokenPath)
	if readErr == nil {
		tok := strings.TrimSpace(string(data))
		if tok != "" {
			return tok, false, nil
		}
	}

	tok, err := GenerateToken()
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(tokenPath, []byte(tok+"\n"), 0600); err != nil {
		return "", false, fmt.Errorf("write token: %w", err)
	}
	return tok, true, nil
}
