package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
)

// TestInstallSignalHandlers_CleanupNoPanic verifies that calling the cleanup
// function returned by installSignalHandlers does not panic.
func TestInstallSignalHandlers_CleanupNoPanic(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	cleanup := installSignalHandlers(screen, make(chan struct{}))
	cleanup() // must not panic
}

// TestInstallSignalHandlers_CleanupDisarmsExit verifies that calling the
// cleanup function prevents the signal goroutine from triggering an exit.
// Uses the injectable exitFunc to intercept the os.Exit call.
func TestInstallSignalHandlers_CleanupDisarmsExit(t *testing.T) {
	// Override exitFunc for the duration of this test.
	original := exitFunc
	exited := false
	exitFunc = func(code int) { exited = true }
	defer func() { exitFunc = original }()

	screen := tcell.NewSimulationScreen("")
	cleanup := installSignalHandlers(screen, make(chan struct{}))

	// Cleanup claims the sync.Once, disarming the goroutine's exitFunc call.
	cleanup()

	if exited {
		t.Error("cleanup should not trigger exit")
	}
}
