package cmd

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBdStore simulates bd's status field for one bead behind a mutex, so
// the test harness itself doesn't race even though it drives real
// concurrency through compareAndSetBeadStatus. It also counts writes so
// tests can assert exactly one status update landed.
type fakeBdStore struct {
	mu         sync.Mutex
	status     string
	writeCount atomic.Int32
	readDelay  time.Duration
}

func newFakeBdStore(initial string) *fakeBdStore {
	return &fakeBdStore{status: initial}
}

func (s *fakeBdStore) show(id string) (string, string, string, error) {
	if s.readDelay > 0 {
		time.Sleep(s.readDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return "title", "assignee", s.status, nil
}

func (s *fakeBdStore) update(id, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.writeCount.Add(1)
	return nil
}

// wireFakeBdStore points bdShowBeadFn/bdUpdateStatusFn at store and restores
// the originals on test cleanup.
func wireFakeBdStore(t *testing.T, store *fakeBdStore) {
	t.Helper()
	origShow, origUpdate := bdShowBeadFn, bdUpdateStatusFn
	t.Cleanup(func() {
		bdShowBeadFn = origShow
		bdUpdateStatusFn = origUpdate
	})
	bdShowBeadFn = store.show
	bdUpdateStatusFn = store.update
}

// isolateDeliverLockDir points deliverLockDir at a fresh temp directory for
// the test's lifetime, so concurrent test runs (or a stray real deliver on
// the host) can never share a lock file.
func isolateDeliverLockDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	orig := deliverLockDir
	deliverLockDir = func() string { return dir }
	t.Cleanup(func() { deliverLockDir = orig })
}

// TestCompareAndSetBeadStatus_ConcurrentDelivery_ExactlyOneAdvances is the
// named regression for ini-khjh: two deliveries racing on the same bead,
// both having observed the same starting status, must result in exactly one
// status advance. The loser gets a clear conflict error, not a silent
// second write. Genuine concurrency: two goroutines, launched together and
// released off the same barrier, contend for the real per-bead lock — this
// is not two sequential calls.
func TestCompareAndSetBeadStatus_ConcurrentDelivery_ExactlyOneAdvances(t *testing.T) {
	isolateDeliverLockDir(t)
	store := newFakeBdStore("ready_for_qa")
	store.readDelay = 15 * time.Millisecond // widen the window so both racers are genuinely in flight together
	wireFakeBdStore(t, store)

	const beadID = "ini-race"
	const expected = "ready_for_qa"
	const target = "in_qa"

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // both goroutines release together
			results <- compareAndSetBeadStatus(beadID, expected, target)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var successes, conflicts int
	var conflictErr error
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		conflicts++
		conflictErr = err
	}

	if successes != 1 {
		t.Errorf("successes = %d, want exactly 1", successes)
	}
	if conflicts != 1 {
		t.Errorf("conflicts = %d, want exactly 1", conflicts)
	}
	if conflictErr == nil {
		t.Fatalf("loser error = nil, want a conflict error mentioning concurrent delivery")
	}
	if !strings.Contains(conflictErr.Error(), "concurrent delivery") {
		t.Errorf("loser error = %v, want it to mention concurrent delivery", conflictErr)
	}
	if !strings.Contains(conflictErr.Error(), expected) || !strings.Contains(conflictErr.Error(), target) {
		t.Errorf("loser error = %q, want it to name both the expected (%q) and actual (%q) status", conflictErr.Error(), expected, target)
	}
	if got := store.writeCount.Load(); got != 1 {
		t.Errorf("writeCount = %d, want exactly 1 (no double-advance)", got)
	}
	if store.status != target {
		t.Errorf("final status = %q, want %q", store.status, target)
	}
}

// TestCompareAndSetBeadStatus_SingleCaller_NoRegression pins the "no
// behavior change for the normal single-deliver path" AC: one caller, no
// contention, succeeds exactly as bdUpdateStatusFn alone used to.
func TestCompareAndSetBeadStatus_SingleCaller_NoRegression(t *testing.T) {
	isolateDeliverLockDir(t)
	store := newFakeBdStore("in_progress")
	wireFakeBdStore(t, store)

	if err := compareAndSetBeadStatus("ini-solo", "in_progress", "ready_for_qa"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.status != "ready_for_qa" {
		t.Errorf("status = %q, want ready_for_qa", store.status)
	}
	if got := store.writeCount.Load(); got != 1 {
		t.Errorf("writeCount = %d, want 1", got)
	}
}

// TestCompareAndSetBeadStatus_MismatchDetected is the deterministic
// (non-concurrent) companion: if the bead's current status doesn't match
// what the caller expected — for any reason, not just a race — the write
// must be refused, not silently redirected.
func TestCompareAndSetBeadStatus_MismatchDetected(t *testing.T) {
	isolateDeliverLockDir(t)
	store := newFakeBdStore("in_qa") // caller expected ready_for_qa; someone already moved it
	wireFakeBdStore(t, store)

	err := compareAndSetBeadStatus("ini-stale", "ready_for_qa", "in_qa")
	if err == nil {
		t.Fatal("expected a conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "ready_for_qa") || !strings.Contains(err.Error(), "in_qa") {
		t.Errorf("error = %q, want it to name both statuses", err.Error())
	}
	if got := store.writeCount.Load(); got != 0 {
		t.Errorf("writeCount = %d, want 0 (mismatch must not write)", got)
	}
	if store.status != "in_qa" {
		t.Errorf("status = %q, want unchanged in_qa", store.status)
	}
}

// TestAcquireDeliverLock_StaleLockSelfHeals verifies a lock file left behind
// by a crashed holder (older than staleLockTTL) is cleared rather than
// blocking delivery forever.
func TestAcquireDeliverLock_StaleLockSelfHeals(t *testing.T) {
	isolateDeliverLockDir(t)

	// Plant an abandoned lock file, backdated past staleLockTTL.
	if err := os.MkdirAll(deliverLockDir(), 0700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	path := deliverLockPath("ini-abandoned")
	if err := os.WriteFile(path, []byte("99999\n"), 0600); err != nil {
		t.Fatalf("plant stale lock: %v", err)
	}
	old := time.Now().Add(-2 * staleLockTTL)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("backdate stale lock: %v", err)
	}

	release, err := acquireDeliverLock("ini-abandoned")
	if err != nil {
		t.Fatalf("expected stale lock to self-heal, got error: %v", err)
	}
	release()
}

// TestAcquireDeliverLock_TimesOutOnLiveContention verifies a genuinely held
// (fresh) lock is respected — a second acquirer waits and eventually times
// out with a clear error, rather than barging in.
func TestAcquireDeliverLock_TimesOutOnLiveContention(t *testing.T) {
	isolateDeliverLockDir(t)
	origTimeout := lockWaitTimeout
	lockWaitTimeout = 100 * time.Millisecond
	t.Cleanup(func() { lockWaitTimeout = origTimeout })

	release, err := acquireDeliverLock("ini-held")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	_, err = acquireDeliverLock("ini-held")
	if err == nil {
		t.Fatal("expected timeout error for contended lock, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want 'timed out'", err.Error())
	}
}
