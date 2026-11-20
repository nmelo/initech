package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// A save's ticket numbers it among the saves of one file, in the order their
// content was taken. The writer renames a save into place only when no newer
// save of that file has landed, so an older save never replaces a newer one:
// a save that comes too late is dropped, and the newer save on disk already
// carries its change, because every caller takes its ticket where it takes
// its content, from the same in-memory state.
type saveTicket struct {
	path string
	n    uint64
}

// fileSeq is one file's numbering: the last ticket handed out and the last
// that landed. Its lock also orders the renames of that file.
type fileSeq struct {
	mu            sync.Mutex
	taken, landed uint64
}

var fileSeqs sync.Map // absolute path -> *fileSeq

func seqFor(path string) *fileSeq {
	key := filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		key = abs
	}
	v, _ := fileSeqs.LoadOrStore(key, &fileSeq{})
	return v.(*fileSeq)
}

// takeSaveTicket numbers a save of path. Call it where the save's content is
// taken, before it is marshalled, inside whatever keeps that content
// consistent (the main goroutine, or the store's mutex).
func takeSaveTicket(path string) saveTicket {
	s := seqFor(path)
	s.mu.Lock()
	s.taken++
	n := s.taken
	s.mu.Unlock()
	return saveTicket{path: path, n: n}
}

// writeFileAtomic writes data to path via a UNIQUE staging file, then renames
// it into place. It is the single implementation of the write-temp-then-rename
// idiom, which lived in three copies before ini-g9x -- layout, fleet state, and
// assignment -- each with the same latent defect.
//
// THE DEFECT THE UNIQUE NAME FIXES. Those copies staged through a FIXED name,
// `final + ".tmp"`. That is safe against the hazard ini-9ka.3 was written for
// (two WINDOWS staging through one shared project-level temp, since the name is
// derived per-window), and unsafe against a second one nobody had separated
// from it: two writers of the SAME file share that one staging path. Writer A
// truncates and fills the temp while writer B is midway through its own write
// to the same temp, and whichever rename lands moves whatever the file happened
// to contain -- so the "atomic" write publishes a half-written document. The
// rename is atomic; what it publishes was never guaranteed to be whole.
//
// Unique staging names remove the sharing entirely: each writer owns its temp
// from creation to rename, so every rename publishes a COMPLETE document, and
// concurrent writers can only race over which complete document wins. That is
// the guarantee the concurrency tests have always asserted.
//
// WHY IT SURFACED ON WINDOWS FIRST (measured, ini-g9x): on POSIX the losing
// writer's rename usually still moves a whole file, so 200 looped runs pass on
// darwin under -race. On Windows a rename cannot proceed while another handle
// holds the source open, so the collision reports itself instead of being
// papered over -- the final file was left missing and the test's load returned
// false. Same race, different platform manners: one hides it, one tells you.
// ONE TRADE, stated rather than discovered later: a fixed staging name leaves
// at most ONE stale temp behind if a process is killed between write and
// rename, because the next save overwrites it. Unique names can leave one per
// crash. The files are tiny and .initech/ is not a user-facing directory, and
// the alternative -- scanning and sweeping the directory on every save -- costs
// a syscall per write to tidy a case that only follows a crash. If stale temps
// are ever seen accumulating in the wild, a sweep on startup is the cheaper
// place to add one.
//
// The ticket orders the saves of one file (see saveTicket): the rename runs
// under the file's lock and only when no newer ticket has landed; an older
// one's staging file is removed, one debug line says so, and the save reports
// success. Nothing here syncs to disk: the layout path's timing is unchanged.
func writeFileAtomic(t saveTicket, data []byte, perm os.FileMode) error {
	path := t.path
	dir := filepath.Dir(path)
	// CreateTemp gives every writer its own staging file. The pattern keeps the
	// temps recognisable and beside the target, so a rename never crosses a
	// filesystem boundary.
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmp := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := f.Chmod(perm); err != nil {
		// Not fatal on platforms where chmod is a no-op; the rename still
		// publishes correct content, and a wrong mode is a smaller failure
		// than refusing to save the operator's layout.
		LogWarn("atomic-write", "chmod temp file", "path", tmp, "err", err)
	}
	// Close BEFORE renaming: Windows refuses to move a file that still has an
	// open handle, which is the failure mode that made this bug visible.
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}
	s := seqFor(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.n <= s.landed {
		os.Remove(tmp)
		LogDebug("atomic-write", "older save dropped: a newer one has landed", "path", path, "save", t.n, "landed", s.landed)
		return nil
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename into place: %w", err)
	}
	s.landed = t.n
	return nil
}

// errStateUnparseable marks a state file that was read but does not parse (or
// has another version): the one case a store's file is set aside.
var errStateUnparseable = errors.New("does not parse")

// stateNow is the clock a set-aside file is named by; tests fix it.
var stateNow = time.Now

// setAsideStateFile renames a state file that does not parse to
// <path>.bad-<UTC time>, with one log line naming it, and reports whether it
// moved. Only window 1 calls it, and only for errStateUnparseable: a viewer,
// a file that cannot be read, or a rename that fails leaves the file where it
// is, and the store stays read-only, so a file is never overwritten in place.
func setAsideStateFile(store, path string, cause error) (string, bool) {
	return setAsideStateFileAt(store, path, cause, stateNow())
}

// setAsideStateFileAt is setAsideStateFile at a given time.
func setAsideStateFileAt(store, path string, cause error, now time.Time) (string, bool) {
	aside := path + ".bad-" + now.UTC().Format("20060102T150405Z")
	if err := os.Rename(path, aside); err != nil {
		LogWarn(store, "state file does not parse and could not be set aside; refusing changes until it is fixed",
			"file", path, "err", cause, "rename_err", err)
		return "", false
	}
	LogWarn(store, "state file did not parse; set aside, starting empty", "file", path, "aside", aside, "err", cause)
	return aside, true
}

// setAsideIfOwner sets a store's file aside when it did not parse and this
// process owns the rig's state (window 1, not a second window), and reports
// whether the store may start empty and writable. Any other failure, and any
// viewer, keeps the store's read-only fallback.
func (t *TUI) setAsideIfOwner(store, path string, err error) bool {
	if t.viewerProcess || !t.isFleetAuthority() || !errors.Is(err, errStateUnparseable) {
		return false
	}
	_, ok := setAsideStateFile(store, path, err)
	return ok
}
