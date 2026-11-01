package appserve

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// heldFolders is a Read in which a folder listed in hold blocks until
// released, as an open macOS is holding does.
type heldFolders struct {
	mu    sync.Mutex
	hold  map[string]chan struct{}
	reads map[string]int
}

func (h *heldFolders) read(dir string) []Rig {
	h.mu.Lock()
	h.reads[dir]++
	ch := h.hold[dir]
	h.mu.Unlock()
	if ch != nil {
		<-ch
	}
	return []Rig{{Name: dir, Root: "/" + dir}}
}

func rigNames(rigs []Rig) string {
	var n []string
	for _, r := range rigs {
		n = append(n, r.Name)
	}
	return strings.Join(n, ",")
}

// AC12: one held folder does not stall the others: they are listed within the
// bound, the held one is named once however many scans pass, its read is not
// started again, and later scans add no wait for it. When the hold lifts it
// is read and listed; held again, it is named again.
func TestFolderScan_AHeldFolderDoesNotStallTheOthers(t *testing.T) {
	release := make(chan struct{})
	h := &heldFolders{hold: map[string]chan struct{}{"held": release}, reads: map[string]int{}}
	var named []string
	var mu sync.Mutex
	f := &FolderScan{Read: h.read, Bound: 50 * time.Millisecond, OnHeld: func(d string) { mu.Lock(); named = append(named, d); mu.Unlock() }}
	dirs := []string{"a", "held", "b"}

	done := make(chan string, 1)
	go func() { done <- rigNames(f.Scan(dirs)) }()
	select {
	case got := <-done:
		if got != "a,b" {
			t.Errorf("first scan = %q, want a,b without the held one", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a held folder stalled the scan past its bound")
	}
	start := time.Now()
	for i := 0; i < 3; i++ {
		if got := rigNames(f.Scan(dirs)); got != "a,b" {
			t.Errorf("scan %d = %q", i, got)
		}
	}
	if d := time.Since(start); d > 40*time.Millisecond {
		t.Errorf("three scans took %s: a folder already held added a wait", d)
	}
	h.mu.Lock()
	heldReads := h.reads["held"]
	h.mu.Unlock()
	if heldReads != 1 {
		t.Errorf("the held folder was read %d times, want its one read left going", heldReads)
	}
	mu.Lock()
	if strings.Join(named, ",") != "held" {
		t.Errorf("named %v, want the held folder once", named)
	}
	mu.Unlock()

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for rigNames(f.Scan(dirs)) != "a,held,b" {
		if time.Now().After(deadline) {
			t.Fatalf("after the hold lifted: %q", rigNames(f.Scan(dirs)))
		}
		time.Sleep(5 * time.Millisecond)
	}

	again := make(chan struct{})
	defer close(again)
	h.mu.Lock()
	h.hold["held"] = again
	h.mu.Unlock()
	if got := rigNames(f.Scan(dirs)); got != "a,b" {
		t.Errorf("held again: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(named) != 2 {
		t.Errorf("held again: named %v, want it named a second time", named)
	}
}

// AC6 feed-it-nothing, AC12: no folders is no rigs; every folder held is no
// rigs within the bound; a rig in two folders is listed once.
func TestFolderScan_NoFoldersAllHeldAndDuplicates(t *testing.T) {
	h := &heldFolders{hold: map[string]chan struct{}{}, reads: map[string]int{}}
	f := &FolderScan{Read: h.read, Bound: 20 * time.Millisecond}
	if got := f.Scan(nil); len(got) != 0 {
		t.Errorf("no folders: %v", got)
	}
	block := make(chan struct{})
	defer close(block)
	h.hold["x"], h.hold["y"] = block, block
	if got := f.Scan([]string{"x", "y"}); len(got) != 0 {
		t.Errorf("all held: %v", got)
	}
	same := &FolderScan{Read: func(string) []Rig { return []Rig{{Name: "r", Root: "/r"}} }, Bound: time.Second}
	if got := same.Scan([]string{"a", "b"}); len(got) != 1 {
		t.Errorf("a rig in two folders: %v", got)
	}
}
