package appserve

import (
	"sync"
	"time"
)

// DefaultFolderBound is how long a rig scan waits on its folders together.
const DefaultFolderBound = 5 * time.Second

// FolderScan reads rig folders with a bound, so a folder whose read does not
// return (macOS holding an open under ~/Desktop for a launchd job) does not
// stall the others: the scan moves on without it, leaves that one read going
// rather than starting another, and names the folder once through OnHeld. A
// held read that finally returns clears the folder, for the next scan to read
// afresh and to name again if it is held again.
type FolderScan struct {
	Read   func(dir string) []Rig // reads one folder; may never return
	Bound  time.Duration          // DefaultFolderBound when zero
	OnHeld func(dir string)       // told once per hold; may be nil

	mu       sync.Mutex
	inFlight map[string]chan []Rig // folders with a read going
	named    map[string]bool       // folders OnHeld was told about, while held
}

// Scan is the rigs in dirs, in order, from every folder that answered within
// the bound. A folder still held from an earlier scan adds no wait.
func (f *FolderScan) Scan(dirs []string) []Rig {
	bound := f.Bound
	if bound <= 0 {
		bound = DefaultFolderBound
	}
	type read struct {
		dir string
		ch  chan []Rig
	}
	var reads []read
	f.mu.Lock()
	if f.inFlight == nil {
		f.inFlight, f.named = map[string]chan []Rig{}, map[string]bool{}
	}
	for _, dir := range dirs {
		if f.inFlight[dir] != nil {
			continue // held from an earlier scan
		}
		ch := make(chan []Rig, 1)
		f.inFlight[dir] = ch
		reads = append(reads, read{dir, ch})
		go func(dir string, ch chan []Rig) {
			rigs := f.Read(dir)
			f.mu.Lock()
			delete(f.inFlight, dir)
			delete(f.named, dir)
			f.mu.Unlock()
			ch <- rigs
		}(dir, ch)
	}
	f.mu.Unlock()

	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	found := map[string][]Rig{}
	expired := false
	for _, r := range reads {
		if expired {
			select {
			case rigs := <-r.ch:
				found[r.dir] = rigs
			default:
			}
			continue
		}
		select {
		case rigs := <-r.ch:
			found[r.dir] = rigs
		case <-deadline.C:
			expired = true
			select {
			case rigs := <-r.ch:
				found[r.dir] = rigs
			default:
			}
		}
	}

	var out []Rig
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, rg := range found[dir] {
			if !seen[rg.Root] {
				seen[rg.Root] = true
				out = append(out, rg)
			}
		}
	}
	f.mu.Lock()
	var tell []string
	for _, dir := range dirs {
		if f.inFlight[dir] != nil && !f.named[dir] {
			f.named[dir] = true
			tell = append(tell, dir)
		}
	}
	f.mu.Unlock()
	if f.OnHeld != nil {
		for _, dir := range tell {
			f.OnHeld(dir)
		}
	}
	return out
}
