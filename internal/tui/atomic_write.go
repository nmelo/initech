package tui

import (
	"fmt"
	"os"
	"path/filepath"
)

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
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
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
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
