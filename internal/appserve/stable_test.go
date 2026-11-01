package appserve

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stableRig is a temp HOME with a Homebrew-shaped install: the release
// binary at <root>/Cellar/initech/<version>/bin/initech and the link
// <root>/bin/initech to it, the way a command is run. Its env counts every
// file look it makes through the seams.
type stableRig struct {
	home, link, src string
	stats, lstats   atomic.Int32
	resolves, signs atomic.Int32
	codesign        Codesign
}

const relVersion = "2.55.2"

// copyPathSuffix ends the copy's path (and only the copy's: a temp copy
// beside it is named .initech-*).
var copyPathSuffix = filepath.Join("app-serve", "initech")

func newStableRig(t *testing.T) *stableRig {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &stableRig{home: filepath.Join(root, "home"), link: filepath.Join(root, "bin", "initech"),
		src: filepath.Join(root, "Cellar", "initech", relVersion, "bin", "initech")}
	os.MkdirAll(r.home, 0o755)
	os.MkdirAll(filepath.Dir(r.src), 0o755)
	os.MkdirAll(filepath.Dir(r.link), 0o755)
	os.WriteFile(r.src, []byte("#!/bin/sh\necho initech "+relVersion+"\n"), 0o755)
	if err := os.Symlink("../Cellar/initech/"+relVersion+"/bin/initech", r.link); err != nil {
		t.Fatal(err)
	}
	r.codesign = func(args ...string) (string, error) {
		return "code object is not signed at all", errors.New("exit status 1")
	}
	return r
}

func (r *stableRig) env() RefreshEnv {
	return RefreshEnv{
		Home: r.home, Version: relVersion, GOOS: "darwin",
		Executable: func() (string, error) { return r.link, nil },
		Stat:       func(p string) (os.FileInfo, error) { r.stats.Add(1); return os.Stat(p) },
		Lstat:      func(p string) (os.FileInfo, error) { r.lstats.Add(1); return os.Lstat(p) },
		Resolve:    func(p string) (string, error) { r.resolves.Add(1); return filepath.EvalSymlinks(p) },
		Codesign:   func(args ...string) (string, error) { r.signs.Add(1); return r.codesign(args...) },
		Now:        func() time.Time { return time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC) },
	}
}

func (r *stableRig) copyPath() string { return CopyPath(r.home) }
func (r *stableRig) dir() string      { return filepath.Dir(r.copyPath()) }
func (r *stableRig) stamp(v string) string {
	return filepath.Join(r.dir(), StampPrefix+v)
}

// oldCopy puts a previous build at the copy path with its stamp.
func (r *stableRig) oldCopy(t *testing.T) []byte {
	t.Helper()
	old := []byte("#!/bin/sh\necho initech 2.55.1\n")
	os.MkdirAll(r.dir(), 0o700)
	os.WriteFile(r.copyPath(), old, 0o755)
	os.WriteFile(r.stamp("2.55.1"), nil, 0o600)
	return old
}

func (r *stableRig) logLines() int {
	b, _ := os.ReadFile(filepath.Join(r.dir(), RefreshLog))
	return strings.Count(string(b), "\n")
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// quiet runs fn with stdout and stderr captured, and answers what reached
// them: a refresh never writes to a command's output.
func quiet(t *testing.T, fn func()) string {
	t.Helper()
	rOut, wOut, _ := os.Pipe()
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = wOut, wOut
	done := make(chan string)
	go func() { b, _ := io.ReadAll(rOut); done <- string(b) }()
	fn()
	os.Stdout, os.Stderr = oldOut, oldErr
	wOut.Close()
	return <-done
}

// ini-e7y AC1: the first run of a new release build through the Cellar
// link, with the stamp naming the old version, refreshes the copy:
// byte-identical to the build, 0755 in a 0700 folder, the new stamp written
// and the old one gone. A Mac with no copy yet gets one. Red at base: there
// was no refresh.
func TestRefreshStableCopy_ANewReleaseRefreshesTheCopy(t *testing.T) {
	for _, had := range []bool{true, false} {
		r := newStableRig(t)
		if had {
			r.oldCopy(t)
		}
		if out := quiet(t, func() { RefreshStableCopy(r.env()) }); out != "" {
			t.Errorf("the refresh wrote %q to the command's output", out)
		}
		if !SameBytes(r.src, r.copyPath()) {
			t.Fatalf("had a copy %v: the copy is not the new build", had)
		}
		if fi, _ := os.Stat(r.copyPath()); fi.Mode().Perm() != 0o755 {
			t.Errorf("copy mode %o", fi.Mode().Perm())
		}
		if fi, _ := os.Stat(r.dir()); fi.Mode().Perm() != 0o700 {
			t.Errorf("folder mode %o", fi.Mode().Perm())
		}
		if !exists(r.stamp(relVersion)) || exists(r.stamp("2.55.1")) {
			t.Errorf("stamps: new %v, old %v", exists(r.stamp(relVersion)), exists(r.stamp("2.55.1")))
		}
		if r.logLines() != 0 {
			t.Error("a refresh that worked logged a failure")
		}
	}
}

// ini-e7y AC2, AC4: with the stamp current a command makes one stat, of the
// stamp, and nothing else: no read of either binary, no codesign, no write.
func TestRefreshStableCopy_ACurrentStampIsOneStat(t *testing.T) {
	r := newStableRig(t)
	RefreshStableCopy(r.env())
	before, _ := os.Stat(r.copyPath())
	r.stats.Store(0)
	r.lstats.Store(0)
	r.resolves.Store(0)
	r.signs.Store(0)
	RefreshStableCopy(r.env())
	if r.stats.Load() != 1 || r.lstats.Load() != 0 || r.resolves.Load() != 0 || r.signs.Load() != 0 {
		t.Errorf("stat %d, lstat %d, resolve %d, codesign %d: want one stat and nothing else", r.stats.Load(), r.lstats.Load(), r.resolves.Load(), r.signs.Load())
	}
	after, _ := os.Stat(r.copyPath())
	if !os.SameFile(before, after) {
		t.Error("a current stamp rewrote the copy")
	}
}

// ini-e7y AC2: runs that are not the first run of a release build through
// its Cellar link do nothing at all: the copy and its stamp as they were.
func TestRefreshStableCopy_NothingButAReleaseRunThroughItsLink(t *testing.T) {
	for name, change := range map[string]func(r *stableRig, e *RefreshEnv){
		"run from the copy itself": func(r *stableRig, e *RefreshEnv) {
			e.Executable = func() (string, error) { return r.copyPath(), nil }
		},
		"a dev build":         func(r *stableRig, e *RefreshEnv) { e.Version = "dev" },
		"a build past a tag":  func(r *stableRig, e *RefreshEnv) { e.Version = "2.55.2-3-gabc1234" },
		"a v-prefixed tag":    func(r *stableRig, e *RefreshEnv) { e.Version = "v2.55.2" },
		"the keg by its path": func(r *stableRig, e *RefreshEnv) { e.Executable = func() (string, error) { return r.src, nil } },
		"started before the upgrade": func(r *stableRig, e *RefreshEnv) {
			e.Version = "2.55.0" // the link already points at 2.55.2's keg
		},
		"HOME unset": func(r *stableRig, e *RefreshEnv) { e.Home = "" },
		"not macOS":  func(r *stableRig, e *RefreshEnv) { e.GOOS = "linux" },
		"own path fails": func(r *stableRig, e *RefreshEnv) {
			e.Executable = func() (string, error) { return "", errors.New("no") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			// From a temp folder: with HOME unset the copy's path would be
			// relative, and a broken check must not write into the source
			// tree.
			t.Chdir(t.TempDir())
			r := newStableRig(t)
			old := r.oldCopy(t)
			e := r.env()
			change(r, &e)
			RefreshStableCopy(e)
			got, _ := os.ReadFile(r.copyPath())
			if !bytes.Equal(got, old) || !exists(r.stamp("2.55.1")) || exists(r.stamp(e.Version)) && e.Version != "2.55.1" || r.logLines() != 0 {
				t.Errorf("the copy or its stamp changed, or a line was logged (copy now %q)", got)
			}
			if _, err := os.Stat("Library"); err == nil {
				t.Error("a copy was written relative to the working folder")
			}
		})
	}
}

// ini-e7y AC2 feed-it-nothing: a build whose copy does not verify, or whose
// identifier or team differ from the signed copy already there, is refused:
// the previous copy stays, the stamp stays old, one line goes to the log
// beside the copy, nothing to the command's output.
func TestRefreshStableCopy_ARefusedBuildKeepsThePreviousCopy(t *testing.T) {
	for name, codesign := range map[string]Codesign{
		"does not verify": func(args ...string) (string, error) {
			if args[0] == "--verify" {
				return "invalid signature", errors.New("exit status 1")
			}
			return "Identifier=com.neblihq.initech\nTeamIdentifier=K7Z8YKVA8G\n", nil
		},
		"another team": func(args ...string) (string, error) {
			if args[0] == "--verify" {
				return "", nil
			}
			if strings.HasSuffix(args[len(args)-1], copyPathSuffix) {
				return "Identifier=com.neblihq.initech\nTeamIdentifier=K7Z8YKVA8G\n", nil
			}
			return "Identifier=com.neblihq.initech\nTeamIdentifier=OTHERTEAM1\n", nil
		},
		"another identifier": func(args ...string) (string, error) {
			if args[0] == "--verify" {
				return "", nil
			}
			if strings.HasSuffix(args[len(args)-1], copyPathSuffix) {
				return "Identifier=com.neblihq.initech\nTeamIdentifier=K7Z8YKVA8G\n", nil
			}
			return "Identifier=com.example.other\nTeamIdentifier=K7Z8YKVA8G\n", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			// The identity follows the bytes: the build and any copy made
			// of it read the build's, and only the copy already there reads
			// the granted one, so it is the identity check that refuses.
			r := newStableRig(t)
			old := r.oldCopy(t)
			r.codesign = codesign
			for range 2 {
				if out := quiet(t, func() { RefreshStableCopy(r.env()) }); out != "" {
					t.Errorf("the refusal reached the command's output: %q", out)
				}
			}
			got, _ := os.ReadFile(r.copyPath())
			if !bytes.Equal(got, old) || exists(r.stamp(relVersion)) || !exists(r.stamp("2.55.1")) {
				t.Errorf("the previous copy or stamp did not stay (copy %q)", got)
			}
			if n := r.logLines(); n != 2 {
				t.Errorf("%d log lines over two refused runs, want one each", n)
			}
			b, _ := os.ReadFile(filepath.Join(r.dir(), RefreshLog))
			if !strings.Contains(string(b), relVersion) || !strings.Contains(string(b), "keeps the build it has") {
				t.Errorf("log %q", b)
			}
			if fi, _ := os.Stat(filepath.Join(r.dir(), RefreshLog)); fi.Mode().Perm() != 0o600 {
				t.Errorf("log mode %o, want 600", fi.Mode().Perm())
			}
		})
	}
}

// ini-e7y AC5: two refreshes at once, and a refresh racing the app
// service's watcher (InstallCopy), leave one whole file: a reader looking
// throughout reads the old build or the new, never neither; no temp file is
// left. A writer holding the copy lock makes a refresh step aside silently,
// and the next run does it.
func TestRefreshStableCopy_TwoWritersLeaveOneWholeFile(t *testing.T) {
	r := newStableRig(t)
	old := r.oldCopy(t)
	big := bytes.Repeat([]byte("x"), 4<<20)
	os.WriteFile(r.src, big, 0o755)
	var bad atomic.Int32
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := os.ReadFile(r.copyPath())
			if err != nil || (!bytes.Equal(got, old) && !bytes.Equal(got, big)) {
				bad.Add(1)
			}
		}
	}()
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 2 {
				InstallCopy(r.codesign, r.src, r.copyPath()) //nolint:errcheck // the watcher's path; checked by the reader
				return
			}
			RefreshStableCopy(r.env())
		}()
	}
	wg.Wait()
	close(stop)
	<-done
	if bad.Load() != 0 {
		t.Errorf("a reader saw the copy missing or partial %d times", bad.Load())
	}
	if !SameBytes(r.src, r.copyPath()) {
		t.Error("the new build did not land")
	}
	entries, _ := os.ReadDir(r.dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".initech-") {
			t.Errorf("a temp file was left: %s", e.Name())
		}
	}

	// A held lock: the refresh steps aside, logs nothing, and the next run
	// refreshes.
	q := newStableRig(t)
	q.oldCopy(t)
	unlock, err := lockCopyDir(q.dir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	RefreshStableCopy(q.env())
	if SameBytes(q.src, q.copyPath()) || exists(q.stamp(relVersion)) || q.logLines() != 0 {
		t.Error("a refresh under another writer's lock did not step aside quietly")
	}
	unlock()
	RefreshStableCopy(q.env())
	if !SameBytes(q.src, q.copyPath()) || !exists(q.stamp(relVersion)) {
		t.Error("the next run after the lock was released did not refresh")
	}
}

// ini-e7y: a copy the app service's watcher already refreshed to this build
// gets the stamp alone, with nothing copied.
func TestRefreshStableCopy_ACopyAlreadyCurrentGetsTheStampAlone(t *testing.T) {
	r := newStableRig(t)
	r.oldCopy(t)
	if err := InstallCopy(r.codesign, r.src, r.copyPath()); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(r.copyPath())
	r.signs.Store(0)
	RefreshStableCopy(r.env())
	after, _ := os.Stat(r.copyPath())
	if !os.SameFile(before, after) || !exists(r.stamp(relVersion)) || r.signs.Load() != 0 {
		t.Errorf("rewritten %v, stamp %v, codesign calls %d", !os.SameFile(before, after), exists(r.stamp(relVersion)), r.signs.Load())
	}
}

// ini-e7y AC3: with real codesign over signed binaries, a build is copied
// with its signature kept (strict verify, same identifier and team), and a
// build signed with another identity than the signed copy already there is
// refused, that copy kept. Apple's platform binaries stand in for the
// release identity (shipper checks the release binary in AC7).
func TestRefreshStableCopy_RealSignaturesKeptAndAnotherIdentityRefused(t *testing.T) {
	if goos != "darwin" {
		t.Skip("code signatures are read with macOS codesign")
	}
	put := func(r *stableRig, from string) {
		b, err := os.ReadFile(from)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(r.src, b, 0o755)
	}

	// No copy yet: the signed build lands with its signature intact.
	r := newStableRig(t)
	r.codesign = SystemCodesign
	put(r, "/usr/bin/true")
	RefreshStableCopy(r.env())
	want, _ := ReadSignature(SystemCodesign, "/usr/bin/true")
	got, err := ReadSignature(SystemCodesign, r.copyPath())
	if err != nil || got != want || !want.Signed {
		t.Fatalf("the copy is signed %+v (%v), want %+v", got, err, want)
	}
	if out, err := SystemCodesign("--verify", "--strict", r.copyPath()); err != nil {
		t.Errorf("the copy does not verify: %v %s", err, out)
	}

	// A signed copy of another identity there: refused, kept, logged.
	q := newStableRig(t)
	q.codesign = SystemCodesign
	os.MkdirAll(q.dir(), 0o700)
	if err := InstallCopy(SystemCodesign, "/usr/bin/false", q.copyPath()); err != nil {
		t.Fatal(err)
	}
	put(q, "/usr/bin/true")
	RefreshStableCopy(q.env())
	if !SameBytes(q.copyPath(), "/usr/bin/false") || exists(q.stamp(relVersion)) || q.logLines() != 1 {
		t.Errorf("another identity: copy kept %v, stamp %v, log lines %d", SameBytes(q.copyPath(), "/usr/bin/false"), exists(q.stamp(relVersion)), q.logLines())
	}
}
