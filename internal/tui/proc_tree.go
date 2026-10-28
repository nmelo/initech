package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The process tree under a pane (ini-d2sx): what a background job really is
// and how old it is. Claude Code's footer keeps a finished background shell
// listed until it is read, so a count read from the footer stays at one
// across many short jobs and its age grows to the first one's start. The
// tree says which jobs run now and when each started.
//
// Measured on Claude Code 2.1.312: a pane's claude process is the pane's
// child (or under an sh wrapper); a background Bash job is a shell that is a
// direct child of claude, with the job's command under it. claude's other
// children (MCP servers, caffeinate) are not shells. An exiting process is
// listed as "(zsh)".
//
// Only what each process IS is kept -- a shell, claude, or neither -- never
// its name: on macOS a name can carry a rewritten argv, and nothing here may
// print another process's command line.

// procEntry is one process in a sample.
type procEntry struct {
	pid, ppid int
	start     time.Time
	shell     bool // a shell (zsh, bash, sh, ...) that is not exiting
	claude    bool // Claude Code itself
}

// procTree is one sample of the machine's process table.
type procTree struct {
	taken    time.Time
	byPID    map[int]procEntry
	children map[int][]int
}

// shellNames are the shells a background job runs in.
var shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "tcsh": true}

// classifyComm reads what a process is from its comm field: a shell, claude,
// or neither. "-zsh" is a login shell; "(zsh)", one that is exiting, is
// neither, as its name matches no shell.
func classifyComm(comm string) (shell, claude bool) {
	base := strings.TrimPrefix(filepath.Base(strings.TrimSpace(comm)), "-")
	return shellNames[base], base == "claude"
}

// parseEtime reads ps's elapsed time, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	secs := 0
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, false
		}
		secs = secs*60 + n
	}
	return time.Duration(days*86400+secs) * time.Second, true
}

// parseProcTable reads "pid ppid etime comm" lines into a tree taken at
// taken. A line it cannot read is skipped.
func parseProcTable(out string, taken time.Time) *procTree {
	t := &procTree{taken: taken, byPID: map[int]procEntry{}, children: map[int][]int{}}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		age, ok := parseEtime(f[2])
		if err1 != nil || err2 != nil || !ok {
			continue
		}
		shell, claude := classifyComm(strings.Join(f[3:], " "))
		t.byPID[pid] = procEntry{pid: pid, ppid: ppid, start: taken.Add(-age), shell: shell, claude: claude}
		t.children[ppid] = append(t.children[ppid], pid)
	}
	return t
}

// runningShells is the start of every background shell running under the
// pane whose process is root: the shells that are direct children of the
// claude in root's subtree. ok is false when the tree does not hold root or
// has no claude under it, and the caller cannot tell from it.
func (t *procTree) runningShells(root int) (starts []time.Time, ok bool) {
	if t == nil || root <= 0 {
		return nil, false
	}
	claude := -1
	queue := []int{root}
	for len(queue) > 0 && claude < 0 {
		cur := queue[0]
		queue = queue[1:]
		if t.byPID[cur].claude {
			claude = cur
			break
		}
		queue = append(queue, t.children[cur]...)
	}
	if claude < 0 {
		return nil, false
	}
	for _, c := range t.children[claude] {
		if e := t.byPID[c]; e.shell {
			starts = append(starts, e.start)
		}
	}
	return starts, true
}

// treeSampler keeps the latest process-table sample, read off the caller's
// goroutine: ps takes tens of milliseconds and the TUI's tick runs on its
// main loop. One read at a time, at most once a second.
type treeSampler struct {
	read     func() (string, error) // the process table as text
	now      func() time.Time
	run      func(func())   // starts the read; a goroutine in production
	latest   atomic.Pointer[procTree]
	mu       sync.Mutex
	inFlight bool
	lastKick time.Time
}

// treeMaxAge is how old a sample may be and still describe the panes.
const treeMaxAge = 3 * time.Second

// sample returns a sample no older than treeMaxAge, or nil; when want is
// true (some pane reports background work) it starts a fresh read if none
// ran in the last second.
func (s *treeSampler) sample(want bool) *procTree {
	now := s.now()
	if want {
		s.mu.Lock()
		kick := !s.inFlight && now.Sub(s.lastKick) >= time.Second
		if kick {
			s.inFlight, s.lastKick = true, now
		}
		s.mu.Unlock()
		if kick {
			s.run(func() {
				defer func() {
					s.mu.Lock()
					s.inFlight = false
					s.mu.Unlock()
				}()
				out, err := s.read()
				if err != nil {
					return
				}
				s.latest.Store(parseProcTable(out, s.now()))
			})
		}
	}
	t := s.latest.Load()
	if t == nil || now.Sub(t.taken) > treeMaxAge {
		return nil
	}
	return t
}

// procSampler is this process's sampler: the TUI's and the serve daemon's
// ticks share it.
var procSampler = &treeSampler{
	read: readProcTable,
	now:  time.Now,
	run:  func(f func()) { go f() },
}
