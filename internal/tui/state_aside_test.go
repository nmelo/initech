package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// One rule for a damaged state file (ini-5xcb): in window 1, a file that does
// not parse is renamed <name>.bad-<time> with one log line, and the store
// starts empty and writable. A viewer leaves it in place and stays read-only,
// and so does window 1 when the file cannot be read or cannot be moved: a
// file is never overwritten while it is still in place.

var asideT0 = time.Date(2026, 11, 20, 1, 30, 0, 0, time.UTC)

const asideSuffix = ".bad-20261120T013000Z"

// stateStore is one shared-writer store as a test drives it.
type stateStore struct {
	name string
	path func(root string) string
	// open loads the store as tu would and reports whether it accepts a write.
	open func(tu *TUI) (writable bool, write func() error)
}

func stateStores() []stateStore {
	return []stateStore{
		{"fleet state", fleetStatePath, func(tu *TUI) (bool, func() error) {
			fs := tu.fleetState()
			return !fs.readOnly, func() error { return fs.SetHidden("eng1", true) }
		}},
		{"assignments", assignmentPath, func(tu *TUI) (bool, func() error) {
			a := tu.agentsAssignment()
			w, ok := a.Writer()
			return !a.readOnly && ok, func() error {
				if !ok {
					return ErrAssignmentNotAuthority
				}
				return w.MoveGroup("core", "window-2")
			}
		}},
		{"inbox", inboxPath, func(tu *TUI) (bool, func() error) {
			ib := tu.inboxState()
			return !ib.readOnly, func() error {
				_, err := ib.Post(InboxItem{Agent: "eng2", Body: "after"}, "")
				return err
			}
		}},
	}
}

func asideTUI(t *testing.T, root string, viewer bool) *TUI {
	t.Helper()
	tu := newTestTUI(testPane("eng1"))
	tu.projectRoot = root
	tu.agentEvents = make(chan AgentEvent, 8)
	if viewer {
		tu.viewerProcess, tu.windowID = true, "window-2"
	}
	orig := stateNow
	stateNow = func() time.Time { return asideT0 }
	t.Cleanup(func() { stateNow = orig })
	return tu
}

func writeBad(t *testing.T, path string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	bad := []byte("{{{ not: [valid")
	if err := os.WriteFile(path, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	return bad
}

func TestStateFiles_WindowOneSetsABadFileAside(t *testing.T) {
	for _, st := range stateStores() {
		t.Run(st.name, func(t *testing.T) {
			root := t.TempDir()
			path := st.path(root)
			bad := writeBad(t, path)
			tu := asideTUI(t, root, false)
			writable, write := st.open(tu)
			if !writable {
				t.Fatal("window 1 left the store read-only after setting its file aside")
			}
			if got, err := os.ReadFile(path + asideSuffix); err != nil || string(got) != string(bad) {
				t.Fatalf("the bad file was not set aside whole as %s: %v", filepath.Base(path)+asideSuffix, err)
			}
			if err := write(); err != nil {
				t.Fatalf("the fresh store refused a write: %v", err)
			}
			if got, _ := os.ReadFile(path); strings.Contains(string(got), "{{{") {
				t.Error("the new file holds the bad content: it was half-read")
			}
		})
	}
}

func TestStateFiles_AViewerLeavesABadFileInPlace(t *testing.T) {
	// A second window, with its window id, and while it starts, before its
	// window id is set (it then looks like window 1 but is still a viewer).
	for _, st := range stateStores() {
		for _, id := range []string{"window-2", ""} {
			t.Run(st.name+"/"+id, func(t *testing.T) {
				root := t.TempDir()
				path := st.path(root)
				bad := writeBad(t, path)
				tu := asideTUI(t, root, true)
				tu.windowID = id
				if writable, _ := st.open(tu); writable {
					t.Error("a viewer's store over a bad file is writable")
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != string(bad) {
					t.Errorf("a viewer moved or changed window 1's file: %v", err)
				}
				if _, err := os.Stat(path + asideSuffix); err == nil {
					t.Error("a viewer set window 1's file aside")
				}
			})
		}
	}
}

// Window 1 whose file cannot be moved (a read-only folder) or cannot be read
// at all (no permission, not a parse error) stays read-only, file in place.
func TestStateFiles_WindowOneKeepsReadOnlyWhenTheFileCannotBeSetAside(t *testing.T) {
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("folder and file permissions are not enforced here")
	}
	for _, st := range stateStores() {
		for _, how := range []string{"rename fails", "unreadable"} {
			t.Run(st.name+"/"+how, func(t *testing.T) {
				root := t.TempDir()
				path := st.path(root)
				bad := writeBad(t, path)
				if how == "rename fails" {
					_ = os.Chmod(filepath.Dir(path), 0o500)
				} else {
					_ = os.Chmod(path, 0o000)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o700); _ = os.Chmod(path, 0o600) })
				tu := asideTUI(t, root, false)
				if writable, _ := st.open(tu); writable {
					t.Error("the store is writable over a file still in place")
				}
				_ = os.Chmod(path, 0o600)
				if got, err := os.ReadFile(path); err != nil || string(got) != string(bad) {
					t.Errorf("the file was moved or changed: %v", err)
				}
				if _, err := os.Stat(path + asideSuffix); err == nil {
					t.Error("a file that could not be read was set aside")
				}
			})
		}
	}
}

// Feed it nothing: a missing file starts an empty, writable store, and no
// .bad file is made.
func TestStateFiles_AMissingFileStartsEmptyWithNoBadFile(t *testing.T) {
	for _, st := range stateStores() {
		t.Run(st.name, func(t *testing.T) {
			root := t.TempDir()
			tu := asideTUI(t, root, false)
			if writable, _ := st.open(tu); !writable {
				t.Error("a missing file left the store read-only")
			}
			if bads, _ := filepath.Glob(filepath.Join(root, ".initech", "*.bad-*")); len(bads) != 0 {
				t.Errorf("a missing file made %v", bads)
			}
		})
	}
}

// The strip book: a bad file is set aside and the book starts empty; one that
// cannot be moved makes the book refuse changes rather than overwrite it.
func TestStripBook_ABadFileIsSetAsideOrRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strips.yaml")
	bad := writeBad(t, path)
	b := loadStripBook(path, asideT0)
	if got, err := os.ReadFile(path + asideSuffix); err != nil || string(got) != string(bad) {
		t.Fatalf("the bad strip file was not set aside: %v", err)
	}
	if err := b.set("eng1", []string{"x"}); err != nil {
		t.Fatalf("the fresh book refused a change: %v", err)
	}
	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		return
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "strips.yaml")
	writeBad(t, path)
	_ = os.Chmod(dir, 0o500)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	b = loadStripBook(path, asideT0)
	// The folder is writable again: only the book's own refusal now stops a
	// write over the bad file, which is still in place.
	_ = os.Chmod(dir, 0o700)
	if err := b.set("eng1", []string{"x"}); err == nil {
		t.Error("a book over a file it could not set aside accepted a change")
	}
	if got, _ := os.ReadFile(path); !strings.Contains(string(got), "{{{") {
		t.Error("the bad strip file was overwritten in place")
	}
}

// Layout: a file that does not parse at all is set aside by window 1 and the
// window starts on defaults; a viewer leaves it; a file with one bad field
// still loads with that field repaired.
func TestLayout_AnUnparseableFileIsSetAsideByWindowOneOnly(t *testing.T) {
	orig := stateNow
	stateNow = func() time.Time { return asideT0 }
	t.Cleanup(func() { stateNow = orig })
	root := t.TempDir()
	path := layoutPath(root)
	bad := writeBad(t, path)
	if _, ok := LoadLayoutFor(root, []string{"eng1"}, false); ok {
		t.Fatal("an unparseable layout loaded")
	}
	if got, _ := os.ReadFile(path); string(got) != string(bad) {
		t.Error("a viewer moved or changed the layout file")
	}
	if _, ok := LoadLayoutFor(root, []string{"eng1"}, true); ok {
		t.Fatal("an unparseable layout loaded")
	}
	if got, err := os.ReadFile(path + asideSuffix); err != nil || string(got) != string(bad) {
		t.Fatalf("window 1 did not set the layout file aside whole: %v", err)
	}
	// One bad field: repaired, not set aside.
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Dir(layoutPath(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(layoutPath(root), []byte("mode: grid\ngrid: nonsense\ngroups:\n  - core\n"), 0o600)
	got, ok := LoadLayoutFor(root, []string{"eng1"}, true)
	if !ok {
		t.Fatal("a layout with one bad field did not load")
	}
	// Repaired, not set aside. That the rest of such a file is kept (groups,
	// membership, order, mode) is TestLoadLayout_AnUnreadableGridKeepsGroupsMembershipOrderAndMode.
	if got.GridCols < 1 || got.GridRows < 1 {
		t.Errorf("the bad grid was not repaired: %dx%d", got.GridCols, got.GridRows)
	}
	if bads, _ := filepath.Glob(layoutPath(root) + ".bad-*"); len(bads) != 0 {
		t.Errorf("a layout with one bad field was set aside: %v", bads)
	}
}
