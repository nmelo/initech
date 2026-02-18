package exec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FakeRunner is defined in fake.go (same package, non-test file)
// so downstream packages can import it for their tests.

func TestDefaultRunner_Run(t *testing.T) {
	r := &DefaultRunner{}

	out, err := r.Run("echo", "hello", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q, want %q", out, "hello world")
	}
}

func TestDefaultRunner_RunTrimsOutput(t *testing.T) {
	r := &DefaultRunner{}

	// printf with trailing newline should be trimmed
	out, err := r.Run("printf", "  padded  \n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "padded" {
		t.Errorf("got %q, want %q", out, "padded")
	}
}

func TestDefaultRunner_RunInDir(t *testing.T) {
	r := &DefaultRunner{}
	dir := t.TempDir()

	// Create a file so we can verify pwd
	testFile := "exec_test_marker"
	if err := os.WriteFile(filepath.Join(dir, testFile), []byte("ok"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	out, err := r.RunInDir(dir, "ls")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, testFile) {
		t.Errorf("ls output %q does not contain %q", out, testFile)
	}
}

func TestDefaultRunner_RunInDir_EmptyDir(t *testing.T) {
	r := &DefaultRunner{}

	// Empty dir should use caller's working directory (same as Run)
	out, err := r.RunInDir("", "echo", "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "test" {
		t.Errorf("got %q, want %q", out, "test")
	}
}

func TestDefaultRunner_RunError(t *testing.T) {
	r := &DefaultRunner{}

	_, err := r.Run("this-binary-does-not-exist-9f8a7b")
	if err == nil {
		t.Fatal("expected error for nonexistent binary, got nil")
	}
	if !strings.Contains(err.Error(), "this-binary-does-not-exist-9f8a7b") {
		t.Errorf("error %q should contain the command name", err.Error())
	}
}

func TestDefaultRunner_RunNonZeroExit(t *testing.T) {
	r := &DefaultRunner{}

	out, err := r.Run("ls", "/nonexistent-path-9f8a7b")
	if err == nil {
		t.Fatal("expected error for ls on nonexistent path, got nil")
	}
	// Error should contain the command name
	if !strings.Contains(err.Error(), "ls") {
		t.Errorf("error %q should contain 'ls'", err.Error())
	}
	// Output should still be captured (stderr from ls)
	_ = out // may or may not be empty depending on OS, just verify no panic
}

func TestFakeRunner(t *testing.T) {
	f := &FakeRunner{Output: "fake output"}

	out, err := f.Run("git", "status")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "fake output" {
		t.Errorf("got %q, want %q", out, "fake output")
	}
	if len(f.Calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(f.Calls))
	}
	if f.Calls[0] != "|git status" {
		t.Errorf("got call %q, want %q", f.Calls[0], "|git status")
	}

	// RunInDir
	out, err = f.RunInDir("/tmp", "ls", "-la")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if f.Calls[1] != "/tmp|ls -la" {
		t.Errorf("got call %q, want %q", f.Calls[1], "/tmp|ls -la")
	}
}
