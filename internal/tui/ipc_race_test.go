package tui

import (
	"sync"
	"testing"
	"time"
)

// TestRunOnMain_ExecutesOnMainLoop verifies that runOnMain dispatches fn to
// a goroutine simulating the main event loop and blocks until it completes.
func TestRunOnMain_ExecutesOnMainLoop(t *testing.T) {
	quitCh := make(chan struct{})
	ipcCh := make(chan ipcAction, 32)
	tui := &TUI{quitCh: quitCh, ipcCh: ipcCh}

	var executed bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		tui.runOnMain(func() { executed = true })
	}()

	// Simulate main loop processing one op.
	select {
	case op := <-ipcCh:
		op.fn()
		close(op.done)
	case <-time.After(time.Second):
		t.Fatal("runOnMain did not send to ipcCh within 1s")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runOnMain did not return after op completed")
	}
	if !executed {
		t.Error("runOnMain fn was not executed")
	}
}

// TestRunOnMain_ReturnsFalseOnShutdown verifies that runOnMain returns false
// immediately when quitCh is already closed.
func TestRunOnMain_ReturnsFalseOnShutdown(t *testing.T) {
	quitCh := make(chan struct{})
	close(quitCh) // Already shut down.
	tui := &TUI{
		quitCh: quitCh,
		ipcCh:  make(chan ipcAction, 32),
	}

	var called bool
	result := tui.runOnMain(func() { called = true })
	if result {
		t.Error("runOnMain should return false when TUI is shutting down")
	}
	if called {
		t.Error("fn should not execute when shutting down")
	}
}

// TestRunOnMain_NilChannelFallback verifies that runOnMain executes fn
// directly when ipcCh is nil (test/no-event-loop contexts).
func TestRunOnMain_NilChannelFallback(t *testing.T) {
	tui := &TUI{
		quitCh: make(chan struct{}),
		ipcCh:  nil,
	}

	var executed bool
	result := tui.runOnMain(func() { executed = true })
	if !result {
		t.Error("runOnMain should return true when ipcCh is nil")
	}
	if !executed {
		t.Error("fn should execute directly when ipcCh is nil")
	}
}

// TestRunOnMain_SerializesAccess verifies that concurrent goroutines accessing
// t.panes via runOnMain are serialised — the race detector will flag any
// unsynchronised access if this pattern is broken.
func TestRunOnMain_SerializesAccess(t *testing.T) {
	quitCh := make(chan struct{})
	ipcCh := make(chan ipcAction, 32)
	tui := &TUI{
		quitCh: quitCh,
		ipcCh:  ipcCh,
		panes:  toPaneViews([]*Pane{{name: "eng1"}, {name: "eng2"}}),
	}

	// Simulate main loop.
	go func() {
		for {
			select {
			case op := <-ipcCh:
				op.fn()
				close(op.done)
			case <-quitCh:
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var found *Pane
			tui.runOnMain(func() {
				found = tui.findPane("eng1")
				_ = len(tui.panes) // Read slice header; race detector catches unprotected access.
			})
			_ = found
		}()
	}
	wg.Wait()
	close(quitCh)
}

// TestRunOnMain_ExecutesDirectlyWhenCalledFromMainGoroutine is the ini-jesh
// regression test at the runOnMain primitive itself. It reproduces the exact
// deadlock shape: a goroutine identifies itself as the TUI's main goroutine
// (exactly as Run() does once at construction), then calls runOnMain
// synchronously with nobody else free to drain ipcCh -- because that
// goroutine IS the one that would drain it, and it's blocked calling into
// itself. Timeout-guarded so a regression shows up as a clean test failure,
// not a hung suite.
func TestRunOnMain_ExecutesDirectlyWhenCalledFromMainGoroutine(t *testing.T) {
	tui := &TUI{
		quitCh: make(chan struct{}),
		ipcCh:  make(chan ipcAction), // unbuffered: nothing ever drains this
	}

	done := make(chan bool, 1)
	go func() {
		tui.mainGoroutineID = currentGoroutineID()
		var executed bool
		tui.runOnMain(func() { executed = true })
		done <- executed
	}()

	select {
	case executed := <-done:
		if !executed {
			t.Error("runOnMain returned without executing fn")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runOnMain deadlocked when called from the goroutine it identifies as main (ini-jesh)")
	}
}
