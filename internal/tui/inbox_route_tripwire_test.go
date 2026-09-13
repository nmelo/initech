//go:build !windows

package tui

// The FIFO tripwire for ini-3wkl.7 AC 1: a child window never writes
// inbox.yaml, in the non-contact shape authority_negative_test.go uses.
//
// WHY A NAMED PIPE RATHER THAN A BYTE COMPARISON. A byte comparison catches
// only the write that changes content -- a write of identical bytes, or a
// write followed by a restore, passes it. A FIFO catches the ACT, in all three
// shapes the store could take:
//
//   - an atomic write (what save() does) renames a temp file over the path,
//     which REPLACES the pipe with a regular file: the mode assertion fails.
//   - an in-place write opens the path for writing, which BLOCKS forever on a
//     pipe with no reader: the deadline fires.
//   - even an OPEN FOR READING blocks on a pipe with no writer, so a child
//     that merely re-reads the file during a declined act is caught too.
//
// The deadline is therefore part of the instrument, not a flake guard: a
// blocked act is a detection, and the failure message says so.

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// armInboxTripwire replaces the store file with a named pipe and returns a
// check that fails the test if anything wrote (or opened) it.
func armInboxTripwire(t *testing.T, root string) func() {
	t.Helper()
	path := filepath.Join(root, ".initech", "inbox.yaml")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear store: %v", err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this filesystem does not support FIFOs, so the tripwire cannot arm: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	return func() {
		t.Helper()
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatalf(`THE STORE FILE IS GONE (%v).

A child window removed or replaced window 1's inbox. §291: a viewer requests
mutations through window 1 and never writes project-root state.`, err)
		}
		if fi.Mode()&os.ModeNamedPipe == 0 {
			t.Fatalf(`A CHILD WINDOW WROTE inbox.yaml (mode is now %v, the tripwire pipe is gone).

The store's atomic write renames a temp file over the path, which is exactly
what replaces the pipe. §291 verbatim: a viewer that cannot reach window 1
DECLINES the mutation -- IT NEVER IMPROVISES.`, fi.Mode())
		}
	}
}

// runWithDeadline runs act and fails if it blocks, which on an armed tripwire
// means it opened the pipe.
func runWithDeadline(t *testing.T, what string, act func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); act() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf(`%s BLOCKED on the tripwire pipe.

Opening the store -- for writing OR for reading -- blocks on a FIFO with no
peer. A declined act must not touch the file at all.`, what)
	}
}

func TestInboxTripwire_ADisconnectedChildNeverTouchesTheStore(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	item := routePost(t, w1, "eng2", "which schema?", "ship at 5pm")

	child := routeChild(t, root)
	child.panes = []PaneView{&mockPaneView{name: "eng1", host: "window1", alive: true}}
	child.inboxState() // The child's own load happens BEFORE the pipe is armed.
	child.wireInboxDelivery()

	check := armInboxTripwire(t, root)

	runWithDeadline(t, "a declined reply", func() {
		child.inbox.onReply(item.ID, "use the v2 schema")
	})
	runWithDeadline(t, "a declined accept", func() {
		child.inbox.onAccept(item.ID)
	})
	runWithDeadline(t, "a declined dismiss", func() {
		_ = child.inboxAct(inboxOpDismiss, item.ID, "")
	})
	runWithDeadline(t, "a declined mark-seen", func() {
		child.markInboxSeen(item.ID)
	})
	runWithDeadline(t, "a direct store write", func() {
		_ = child.inboxState().Transition(item.ID, InboxSeen, actorOperator)
	})

	check()
	if child.inbox.note == "" {
		t.Error("the child wrote nothing, which is right, and said nothing, which is not: " +
			"§291 is decline AND say so")
	}
}

// The tripwire has to be able to FAIL, or it proves nothing. This drives the
// write the rule forbids -- an authority store on the same root -- and asserts
// the instrument catches it.
//
// THE WRITER LOADS BEFORE THE PIPE IS ARMED. A first draft loaded after, and
// the load's own read blocked forever on the pipe -- which is the tripwire
// working, aimed at the wrong moment, with no deadline to turn the block into
// a verdict. The write is under the same deadline as every act above.
func TestInboxTripwire_CatchesAWriteWhenOneHappens(t *testing.T) {
	root := inboxRoot(t)
	w1 := routeWindowOne(t, root)
	routePost(t, w1, "eng2", "seed", "")

	// An AUTHORITY store on the same root: the write a child must never make.
	writer, err := LoadInbox(root, true)
	if err != nil {
		t.Fatalf("load writer: %v", err)
	}

	path := filepath.Join(root, ".initech", "inbox.yaml")
	if err := os.Remove(path); err != nil {
		t.Fatalf("clear store: %v", err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("this filesystem does not support FIFOs: %v", err)
	}
	defer os.Remove(path)

	runWithDeadline(t, "the control write", func() {
		if _, err := writer.Post(InboxItem{Agent: "eng2", Body: "a write"}, "run1"); err != nil {
			t.Errorf("setup: the control write failed, so it cannot test the instrument: %v", err)
		}
	})

	fi, statErr := os.Lstat(path)
	if statErr != nil {
		t.Fatalf("stat after the control write: %v", statErr)
	}
	if fi.Mode()&os.ModeNamedPipe != 0 {
		t.Fatal("the tripwire did not notice a real write to inbox.yaml. An instrument that " +
			"cannot produce the phenomenon it reports the absence of is not evidence")
	}
}
