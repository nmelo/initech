// deliver_lock.go implements the compare-and-set guard that makes deliver's
// read-status/compute-next/write-status sequence safe under concurrent
// delivery of the same bead (ini-khjh).
//
// bd's CLI has no atomic "update status only if it still equals X" primitive,
// so the compare-and-set is implemented client-side: an exclusive per-bead
// file lock brackets a fresh status re-read and the write, closing the race
// window between deliver's initial read and its status write. The lock is
// purely a mechanism for atomicity; the semantics are compare-and-set, not
// serialization — a caller whose observed status no longer matches at write
// time gets a clear conflict error instead of silently advancing from
// whatever the other caller left behind.
package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// staleLockTTL bounds how long a delivery lock file may persist before a
// stuck or crashed holder's lock is treated as abandoned and cleared.
// deliver's critical section is one or two bd subprocess calls — well under
// a second normally — so this leaves generous headroom while still
// self-healing after a crash without needing platform-specific flock code.
const staleLockTTL = 30 * time.Second

// lockRetryInterval is how often a contended caller polls for the lock.
const lockRetryInterval = 20 * time.Millisecond

// lockWaitTimeout bounds how long a caller waits for a contended lock before
// giving up with a clear error, rather than hanging deliver indefinitely.
// Overridable for testing so a contention test doesn't need to wait 5s.
var lockWaitTimeout = 5 * time.Second

// deliverLockDir is overridable for testing.
var deliverLockDir = defaultDeliverLockDir

func defaultDeliverLockDir() string {
	return filepath.Join(os.TempDir(), "initech-deliver-locks")
}

// deliverLockPath returns the lock file path for a bead ID. Bead IDs never
// contain path separators (see beadIDRe in the TUI package), so a direct
// join is safe.
func deliverLockPath(beadID string) string {
	return filepath.Join(deliverLockDir(), beadID+".lock")
}

// acquireDeliverLock creates an exclusive, per-bead lock file, blocking
// (via polling) until acquired or lockWaitTimeout elapses. The returned
// release func removes the lock file; callers must defer it.
func acquireDeliverLock(beadID string) (release func(), err error) {
	dir := deliverLockDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create delivery lock dir: %w", err)
	}
	path := deliverLockPath(beadID)
	deadline := time.Now().Add(lockWaitTimeout)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("create delivery lock file: %w", err)
		}
		// Someone else holds the lock. An abandoned lock from a crashed
		// holder self-heals once it's older than staleLockTTL.
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > staleLockTTL {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for delivery lock on %s (another delivery may be in progress)", beadID)
		}
		time.Sleep(lockRetryInterval)
	}
}

// compareAndSetBeadStatus writes newStatus only if the bead's current status
// (re-read fresh, under the lock) still equals expectedStatus — the value
// the caller observed when it decided what newStatus should be. A mismatch
// means another delivery advanced the bead in between; that caller gets a
// clear error instead of silently computing its own advance from stale
// information.
//
// The lock spans the re-read, comparison, and write as one atomic critical
// section: without it, a second caller could pass its own re-read/compare
// before the first caller's write lands, reopening the exact race this
// guards against.
func compareAndSetBeadStatus(beadID, expectedStatus, newStatus string) error {
	release, err := acquireDeliverLock(beadID)
	if err != nil {
		return err
	}
	defer release()

	_, _, current, err := bdShowBeadFn(beadID)
	if err != nil {
		return fmt.Errorf("re-read bead status before write: %w", err)
	}
	if current != expectedStatus {
		return fmt.Errorf("bead %s status changed from %q to %q since it was read — concurrent delivery? re-run deliver to act on the current state", beadID, expectedStatus, current)
	}
	return bdUpdateStatusFn(beadID, newStatus)
}
