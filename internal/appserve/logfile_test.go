package appserve

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

// ini-r0q.21 AC1, AC4: the log is created 0600 in a 0700 folder made if
// missing; an existing log with any group or world bit is narrowed to 0600 and
// that is said once; a 0600 log is left alone, with nothing said (AC3 twin).
func TestEnsurePrivateLog_CreatedAndKept0600(t *testing.T) {
	home := t.TempDir()
	path := LogPath(home)
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("fixture: the folder exists")
	}
	narrowed, err := EnsurePrivateLog(path)
	if err != nil || narrowed {
		t.Fatalf("a new log: narrowed %v, %v", narrowed, err)
	}
	if got := mode(t, path); got != 0o600 {
		t.Errorf("new log mode %o, want 600", got)
	}
	if got := mode(t, filepath.Dir(path)); got != 0o700 {
		t.Errorf("new folder mode %o, want 700", got)
	}

	os.WriteFile(path, []byte("kept\n"), 0o600)
	os.Chmod(path, 0o644)
	narrowed, err = EnsurePrivateLog(path)
	if err != nil || !narrowed || mode(t, path) != 0o600 {
		t.Errorf("a 0644 log: narrowed %v, mode %o, %v; want narrowed to 600", narrowed, mode(t, path), err)
	}
	if b, _ := os.ReadFile(path); string(b) != "kept\n" {
		t.Errorf("the log's content changed: %q", b)
	}
	narrowed, err = EnsurePrivateLog(path)
	if err != nil || narrowed || mode(t, path) != 0o600 {
		t.Errorf("a 0600 log: narrowed %v, mode %o, %v; want left alone", narrowed, mode(t, path), err)
	}
}

// ini-r0q.21 AC4: a log that cannot be opened for writing is an error the
// caller reports; the file is not replaced.
func TestEnsurePrivateLog_NotWritableIsAnError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root writes a 0400 file")
	}
	path := LogPath(t.TempDir())
	os.MkdirAll(filepath.Dir(path), 0o700)
	os.WriteFile(path, []byte("x\n"), 0o400)
	if _, err := EnsurePrivateLog(path); err == nil {
		t.Error("a log it cannot write: no error")
	}
	if mode(t, path) != 0o400 {
		t.Errorf("mode changed to %o", mode(t, path))
	}
}

// ini-r0q.21 AC1: the running service narrows its own log: stderr that is a
// regular file wider than 0600 becomes 0600, with one line; a 0600 file and a
// pipe are left alone and nothing is said.
func TestNarrowLog_TheServiceNarrowsItsOwnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app-serve.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	os.Chmod(path, 0o644)
	var log bytes.Buffer
	NarrowLog(f, &log)
	if got := mode(t, path); got != 0o600 {
		t.Errorf("mode %o after start, want 600", got)
	}
	if strings.Count(log.String(), "\n") != 1 || !strings.Contains(log.String(), "0600") {
		t.Errorf("log = %q, want one line saying it is now 0600", log.String())
	}
	log.Reset()
	NarrowLog(f, &log)
	if log.Len() != 0 {
		t.Errorf("a 0600 file: said %q", log.String())
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	NarrowLog(w, &log)
	if log.Len() != 0 {
		t.Errorf("a pipe: said %q", log.String())
	}
}
