package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCrashLogWritesReport(t *testing.T) {
	dir := t.TempDir()
	report := crashLog(dir, "v0.1.0-test", "nil pointer dereference")

	// Verify report content.
	if !strings.Contains(report, "INITECH CRASH") {
		t.Error("report missing header")
	}
	if !strings.Contains(report, "v0.1.0-test") {
		t.Error("report missing version")
	}
	if !strings.Contains(report, "nil pointer dereference") {
		t.Error("report missing panic value")
	}
	if !strings.Contains(report, "goroutine") {
		t.Error("report missing stack trace")
	}

	// Verify file was written.
	path := filepath.Join(dir, ".initech", "crash.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("crash.log not written: %v", err)
	}
	if string(data) != report {
		t.Error("file content doesn't match returned report")
	}
}

func TestCrashLogAppends(t *testing.T) {
	dir := t.TempDir()
	crashLog(dir, "v1", "first panic")
	crashLog(dir, "v1", "second panic")

	path := filepath.Join(dir, ".initech", "crash.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := strings.Count(string(data), "INITECH CRASH")
	if count != 2 {
		t.Errorf("expected 2 crash entries, got %d", count)
	}
}

func TestCrashLogNoProjectRoot(t *testing.T) {
	// Empty projectRoot: should still return a report, just not write a file.
	report := crashLog("", "v1", "panic")
	if !strings.Contains(report, "panic") {
		t.Error("report should still be generated without projectRoot")
	}
}

func TestSafeGo_CatchesPanicAndWritesCrashLog(t *testing.T) {
	dir, err := os.MkdirTemp("", "initech-crash-*")
	if err != nil {
		t.Fatal(err)
	}

	quitCh := make(chan struct{})
	tui := &TUI{
		projectRoot: dir,
		version:     "test-v1",
		quitCh:      quitCh,
	}

	// Track goroutine completion with a WaitGroup so we can block until
	// the entire defer chain (crashLog + close(quitCh)) has finished
	// before attempting cleanup. This is the structural fix for the CI
	// race where t.TempDir cleanup ran while the goroutine was still
	// in its defer stack.
	var wg sync.WaitGroup
	wg.Add(1)
	tui.safeGo(func() {
		defer wg.Done()
		panic("test goroutine panic")
	})

	select {
	case <-quitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("quitCh was not closed after goroutine panic")
	}

	// Wait for goroutine to fully exit (defer chain complete).
	wg.Wait()
	defer os.RemoveAll(dir) // Clean up only after goroutine is done.

	path := filepath.Join(dir, ".initech", "crash.log")
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("crash.log not written: %v", readErr)
	}
	content := string(data)
	if !strings.Contains(content, "test goroutine panic") {
		t.Error("crash.log missing panic value")
	}
	if !strings.Contains(content, "test-v1") {
		t.Error("crash.log missing version")
	}
}

func TestSafeGo_NormalFunctionRunsCleanly(t *testing.T) {
	tui := &TUI{
		quitCh: make(chan struct{}),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	ran := false
	tui.safeGo(func() {
		defer wg.Done()
		ran = true
	})
	wg.Wait()

	if !ran {
		t.Error("safeGo should execute the function normally")
	}
	// quitCh should NOT be closed since no panic occurred.
	select {
	case <-tui.quitCh:
		t.Error("quitCh should not be closed when no panic occurs")
	default:
		// Good.
	}
}

func TestSafeGo_DoubleCloseQuitChSafe(t *testing.T) {
	dir, err := os.MkdirTemp("", "initech-crash-*")
	if err != nil {
		t.Fatal(err)
	}

	quitCh := make(chan struct{})
	tui := &TUI{
		projectRoot: dir,
		version:     "test",
		quitCh:      quitCh,
	}

	close(quitCh)
	var wg sync.WaitGroup
	wg.Add(1)
	tui.safeGo(func() {
		defer wg.Done()
		panic("after quitCh already closed")
	})

	// Wait for the goroutine to fully exit before cleanup.
	wg.Wait()
	defer os.RemoveAll(dir)
}

func TestPaneStart_UsesSafeGo(t *testing.T) {
	calls := 0
	var mu sync.Mutex
	// Don't actually launch goroutines - just count invocations.
	fakeSafeGo := func(fn func()) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	p := &Pane{
		safeGo:   fakeSafeGo,
		jsonlDir: "/tmp/nonexistent", // triggers watchJSONL launch
	}
	p.Start()

	// readLoop + responseLoop + watchJSONL = 3 goroutine launches.
	mu.Lock()
	defer mu.Unlock()
	if calls != 3 {
		t.Errorf("safeGo called %d times, want 3 (readLoop + responseLoop + watchJSONL)", calls)
	}
}

func TestPaneStart_WithoutJsonlDir(t *testing.T) {
	calls := 0
	fakeSafeGo := func(fn func()) { calls++ }

	p := &Pane{
		safeGo:   fakeSafeGo,
		jsonlDir: "", // no JSONL dir
	}
	p.Start()

	// readLoop + responseLoop only = 2 goroutine launches.
	if calls != 2 {
		t.Errorf("safeGo called %d times, want 2 (no watchJSONL without jsonlDir)", calls)
	}
}
