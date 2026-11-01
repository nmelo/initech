package appserve

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The stable initech path for launchd jobs is the copy at CopyPath: one path
// macOS keys a privacy grant by, kept current across upgrades. The app
// service's watcher refreshes it when the service sees a new build; every
// initech command also refreshes it on its first run as a new release build,
// so a Mac without the app service still has a current copy.

// StampPrefix names the version stamp beside the copy: version-<X.Y.Z>, its
// name alone saying which release the copy was last refreshed to, so a
// current stamp is one stat and no read.
const StampPrefix = "version-"

// RefreshLog is the log beside the copy where a failed refresh says why;
// nothing of the refresh reaches a command's own output.
const RefreshLog = "refresh.log"

// releaseVersion is a release build's version: X.Y.Z, nothing else.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// RefreshEnv is what RefreshStableCopy reads: the running build and the
// file system, as seams.
type RefreshEnv struct {
	Home       string // "" is HOME unset: nothing happens
	Version    string // the running build's version
	GOOS       string
	Executable func() (string, error) // the path as exec'd, unresolved (os.Executable on darwin)
	Stat       func(string) (os.FileInfo, error)
	Lstat      func(string) (os.FileInfo, error)
	Resolve    func(string) (string, error)
	Codesign   Codesign
	Now        func() time.Time
}

// SystemRefreshEnv is the running process's environment.
func SystemRefreshEnv(version string) RefreshEnv {
	home, _ := os.UserHomeDir()
	return RefreshEnv{Home: home, Version: version, GOOS: goos, Executable: os.Executable, Stat: os.Stat,
		Lstat: os.Lstat, Resolve: filepath.EvalSymlinks, Codesign: SystemCodesign, Now: time.Now}
}

// RefreshStableCopy refreshes the copy at CopyPath when this run is the
// first of a new release build: the stamp beside the copy names another
// version, the build's version is X.Y.Z, and the build was started through a
// link that resolves to that version's own Cellar binary (not the copy
// itself, not a dev build, not a keg not yet linked). It copies the build
// beside the copy, verifies it, renames it over, and writes the stamp. A copy
// whose bytes already match (the watcher got there first) gets the stamp
// alone. With the stamp current it is one stat. A failure leaves the old copy
// and writes one line to RefreshLog; a copy another process is writing at
// that moment is left to it, and the next run tries again.
func RefreshStableCopy(env RefreshEnv) {
	if env.GOOS != "darwin" || env.Home == "" || !releaseVersion.MatchString(env.Version) {
		return
	}
	copyPath := CopyPath(env.Home)
	dir := filepath.Dir(copyPath)
	stamp := filepath.Join(dir, StampPrefix+env.Version)
	if _, err := env.Stat(stamp); err == nil {
		return
	}
	exe, err := env.Executable()
	if err != nil || exe == copyPath {
		return
	}
	if fi, err := env.Lstat(exe); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return
	}
	src, err := env.Resolve(exe)
	if err != nil || !strings.HasSuffix(src, filepath.Join("Cellar", "initech", env.Version, "bin", "initech")) || src == copyPath {
		return
	}
	if err := refreshCopy(env, src, copyPath, stamp); err != nil {
		logRefreshFailure(env, dir, fmt.Sprintf("the copy at %s was not refreshed to %s from %s: %v; it keeps the build it has", copyPath, env.Version, src, err))
	}
}

// refreshCopy is the refresh past the checks.
func refreshCopy(env RefreshEnv, src, copyPath, stamp string) error {
	if !SameBytes(src, copyPath) {
		if _, err := env.Stat(copyPath); err == nil {
			// The copy holds a grant for one identity: another one is refused.
			had, err := ReadSignature(env.Codesign, copyPath)
			if err != nil {
				return err
			}
			want, err := ReadSignature(env.Codesign, src)
			if err != nil {
				return err
			}
			if had.Signed && had != want {
				return fmt.Errorf("it is signed %s (team %s), the copy %s (team %s)", want.Identifier, want.Team, had.Identifier, had.Team)
			}
		}
		if err := installCopy(env.Codesign, src, copyPath, 0); err != nil {
			if strings.Contains(err.Error(), errCopyBusy.Error()) {
				return nil // another initech is putting a copy there now
			}
			return err
		}
	}
	return writeStamp(filepath.Dir(copyPath), stamp)
}

// writeStamp writes stamp and removes the other version stamps in dir.
func writeStamp(dir, stamp string) error {
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		return fmt.Errorf("write the stamp: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if p := filepath.Join(dir, e.Name()); strings.HasPrefix(e.Name(), StampPrefix) && p != stamp {
			os.Remove(p) //nolint:errcheck // an old stamp left behind only costs a stat
		}
	}
	return nil
}

// logRefreshFailure appends line to the refresh log beside the copy.
func logRefreshFailure(env RefreshEnv, dir, line string) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, RefreshLog), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", env.Now().UTC().Format(time.RFC3339), line)
}
