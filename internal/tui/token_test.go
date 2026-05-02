package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if len(tok) < 40 {
		t.Errorf("token too short: %q (%d chars)", tok, len(tok))
	}

	tok2, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken (2nd): %v", err)
	}
	if tok == tok2 {
		t.Error("two generated tokens should differ")
	}
}

func TestReadOrCreateToken_Creates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".initech")

	tok, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("ReadOrCreateToken: %v", err)
	}
	if tok == "" {
		t.Fatal("token should not be empty")
	}

	info, err := os.Stat(filepath.Join(dir, "token"))
	if err != nil {
		t.Fatalf("token file not created: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("token file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestReadOrCreateToken_ReusesExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".initech")

	tok1, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	tok2, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if tok1 != tok2 {
		t.Errorf("token should be stable: got %q then %q", tok1, tok2)
	}
}

func TestReadOrCreateToken_RegeneratesEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".initech")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "token"), []byte(""), 0600)

	tok, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("ReadOrCreateToken: %v", err)
	}
	if tok == "" {
		t.Fatal("should regenerate when token file is empty")
	}
}

func TestReadOrCreateToken_RegeneratesWhitespace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".initech")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "token"), []byte("  \n  \n"), 0600)

	tok, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("ReadOrCreateToken: %v", err)
	}
	if tok == "" {
		t.Fatal("should regenerate when token file is only whitespace")
	}
}

func TestReadOrCreateToken_DirPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".initech")

	_, err := ReadOrCreateToken(dir)
	if err != nil {
		t.Fatalf("ReadOrCreateToken: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("dir permissions = %o, want 0700", info.Mode().Perm())
	}
}
