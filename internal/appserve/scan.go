package appserve

import (
	"sync"
	"time"
)

// DefaultFolderBound is how long a folder's read has to answer.
const DefaultFolderBound = 5 * time.Second

// FolderScan reads rig folders with a bound, so a folder whose read does not
// return (macOS holding an open under ~/Desktop for a launchd job) does not
// stall the others: scans move on without it, its one read is left going
// rather than started again, and the folder is named once through OnHeld.
// Scans running at once share a read and its bound. A held read that finally
// returns clears the folder, for the next scan to read afresh and to name
// again if it is held again.
type FolderScan struct {
	Read   func(dir string) []Rig // reads one folder; may never return
	Bound  time.Duration          // DefaultFolderBound when zero
	OnHeld func(dir string)       // told once per hold; may be nil

	mu       sync.Mutex
	inFlight map[string]*folderRead
}

// folderRead is one read of a folder: closed done carries rigs.
type folderRead struct {
	deadline time.Time
	done     chan struct{}
	rigs     []Rig
	named    bool
}

// Scan is the rigs in dirs, in order, from every folder that answered within
// its bound. A folder already past its bound adds no wait.
func (f *FolderScan) Scan(dirs []string) []Rig {
	bound := f.Bound
	if bound <= 0 {
		bound = DefaultFolderBound
	}
	reads := make([]*folderRead, len(dirs))
	f.mu.Lock()
	if f.inFlight == nil {
		f.inFlight = map[string]*folderRead{}
	}
	for i, dir := range dirs {
		rd := f.inFlight[dir]
		if rd == nil {
			rd = &folderRead{deadline: time.Now().Add(bound), done: make(chan struct{})}
			f.inFlight[dir] = rd
			go func(dir string, rd *folderRead) {
				rigs := f.Read(dir)
				f.mu.Lock()
				rd.rigs = rigs
				delete(f.inFlight, dir)
				f.mu.Unlock()
				close(rd.done)
			}(dir, rd)
		}
		reads[i] = rd
	}
	f.mu.Unlock()

	var out []Rig
	var held []string
	seen := map[string]bool{}
	for i, rd := range reads {
		wait := time.NewTimer(time.Until(rd.deadline))
		select {
		case <-rd.done:
		case <-wait.C:
		}
		wait.Stop()
		select {
		case <-rd.done:
			f.mu.Lock()
			rigs := rd.rigs
			f.mu.Unlock()
			for _, rg := range rigs {
				if !seen[rg.Root] {
					seen[rg.Root] = true
					out = append(out, rg)
				}
			}
		default:
			f.mu.Lock()
			if !rd.named {
				rd.named = true
				held = append(held, dirs[i])
			}
			f.mu.Unlock()
		}
	}
	if f.OnHeld != nil {
		for _, dir := range held {
			f.OnHeld(dir)
		}
	}
	return out
}
