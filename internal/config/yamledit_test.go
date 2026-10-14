package config

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const editSecret = "ghp_FAKEtoken0123456789abcdefABCDEF"

// annotated writes testdata/annotated.yaml into a temp dir at mode and
// returns its path and bytes.
func annotated(t *testing.T, mode os.FileMode) (string, string) {
	t.Helper()
	dir := t.TempDir()
	raw, err := os.ReadFile(filepath.Join("testdata", "annotated.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(raw), "__ROOT__", dir)
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(text), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path, text
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustUpdate(t *testing.T, path string, fn func(*Project)) {
	t.Helper()
	if err := Update(path, func(p *Project) error { fn(p); return nil }); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// A changed scalar changes only its value text: the end-of-line comment and
// every other line stay.
func TestEdit_ChangedScalarKeepsItsLineComment(t *testing.T) {
	path, orig := annotated(t, 0o600)
	mustUpdate(t, path, func(p *Project) { p.Name = "demo2" })
	want := strings.Replace(orig, "project: demo   #", "project: demo2   #", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// New keys go at the end of their own mapping, in the file's indent; the
// operator's key order is kept.
func TestEdit_NewKeysAppendAtTheEndOfTheirMapping(t *testing.T) {
	path, orig := annotated(t, 0o600)
	mustUpdate(t, path, func(p *Project) {
		p.RoleOverrides["eng1"] = RoleOverride{ClaudeArgs: []string{"--model", "opus"}, TestCmd: "make test"}
		p.RunningPaneTint = "#1e2022"
	})
	want := strings.Replace(orig, "    claude_args: [--model, opus]\n", "    claude_args: [--model, opus]\n    test_cmd: make test\n", 1)
	want = strings.Replace(want, "my_unknown_key: keep me\n", "my_unknown_key: keep me\nrunning_pane_tint: '#1e2022'\n", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// A key initech does not know and a root spelled with ~ are never touched,
// and writing the same values changes nothing at all.
func TestEdit_UnchangedValuesWriteNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "# mine\nproject: x\nroot: ~/x   # home\nroles: [eng1]\nzzz: 1\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != orig {
		t.Errorf("an unchanged Project rewrote the file:\n%s", got)
	}
}

// A file that does not parse is refused and left exactly as it was.
func TestEdit_UnparseableFileIsRefusedAndUntouched(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "project: x\nroles: [eng1\ntoken: " + editSecret + "\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Write(path, &Project{Name: "x", Root: dir, Roles: []string{"eng1", "eng2"}})
	if err == nil {
		t.Fatal("an unparseable file was overwritten")
	}
	if strings.Contains(err.Error(), editSecret) {
		t.Error("the refusal carried the token")
	}
	if got := readFile(t, path); got != orig {
		t.Errorf("refused write still changed the file:\n%s", got)
	}
}

// A mistyped field fails to decode; the error names no value.
func TestEdit_DecodeErrorNeverCarriesTheValue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "project: x\nroles: [eng1]\nidle_with_bead_threshold: " + editSecret + "\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Update(path, func(p *Project) error { return nil })
	if err == nil {
		t.Fatal("a mistyped file was accepted")
	}
	// yaml.v3 quotes a long value as its first 7 characters plus "...", so
	// a leak shows up as the prefix, not the whole token.
	if strings.Contains(err.Error(), editSecret[:7]) {
		t.Errorf("error carried (part of) the value: %v", err)
	}
	if got := readFile(t, path); got != orig {
		t.Error("the file changed")
	}
}

// A top level that is not a mapping is refused.
func TestEdit_NonMappingTopLevelIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte("- a\n- b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, &Project{Name: "x", Root: dir, Roles: []string{"a"}}); err == nil {
		t.Fatal("a list file was overwritten")
	}
	if got := readFile(t, path); got != "- a\n- b\n" {
		t.Error("the file changed")
	}
}

// The guard: when the splice cannot produce what the caller asked for (here
// a value that comes from a YAML merge key, which has no line of its own to
// delete), the write is refused with the key and line, never the value, and
// the file is untouched.
func TestEdit_GuardRefusesAnEditThatWouldNotDecodeToTheCallersConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "project: x\nroot: " + dir + "\nroles: [eng1]\ntoken: " + editSecret + "\n" +
		"defaults: &d\n  tech_stack: " + editSecret + "\nrole_overrides:\n  eng1:\n    <<: *d\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Update(path, func(p *Project) error {
		p.RoleOverrides["eng1"] = RoleOverride{}
		return nil
	})
	if err == nil {
		t.Fatal("an edit the splice could not make was written")
	}
	msg := err.Error()
	if !strings.Contains(msg, "role_overrides.eng1") || !strings.Contains(msg, "line 8") {
		t.Errorf("refusal does not name the key and line: %v", msg)
	}
	if strings.Contains(msg, editSecret) {
		t.Error("the refusal carried the value")
	}
	if got := readFile(t, path); got != orig {
		t.Errorf("refused write still changed the file:\n%s", got)
	}
}

// A flow-style list re-renders only its own entry; the rest is kept.
func TestEdit_FlowListRerendersOnlyItsEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	orig := "# top\nproject: x\nroot: " + dir + "\nroles: [eng1, qa1]\n\n# tail note\nzzz: 1   # unknown\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, path, func(p *Project) { p.Roles = append(p.Roles, "eng2") })
	want := strings.Replace(orig, "roles: [eng1, qa1]\n", "roles:\n    - eng1\n    - qa1\n    - eng2\n", 1)
	if got := readFile(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Feed it nothing: an empty file and a comments-only file get the keys
// appended (the comments stay first); Update on a missing file fails and
// creates nothing.
func TestEdit_EmptyCommentsOnlyAndMissingFiles(t *testing.T) {
	for name, orig := range map[string]string{"empty": "", "comments-only": "# just notes\n\n# more\n"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "initech.yaml")
		if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
			t.Fatal(err)
		}
		want := &Project{Name: "x", Root: dir, Roles: []string{"eng1"}}
		if err := Write(path, want); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := readFile(t, path)
		if !strings.HasPrefix(got, orig) {
			t.Errorf("%s: the original text is not kept first:\n%s", name, got)
		}
		p, err := Load(path)
		if err != nil || p.Name != "x" || len(p.Roles) != 1 {
			t.Errorf("%s: result does not load as the written config: %v %+v", name, err, p)
		}
	}
	missing := filepath.Join(t.TempDir(), "initech.yaml")
	if err := Update(missing, func(*Project) error { return nil }); err == nil {
		t.Error("Update on a missing file succeeded")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Error("Update on a missing file created it")
	}
}

// The mode is kept (0600 and 0400 stay); a symlinked config is edited at its
// target and stays a symlink.
func TestEdit_ModeAndSymlinkAreKept(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o400} {
		path, _ := annotated(t, mode)
		mustUpdate(t, path, func(p *Project) { p.Name = "demo2" })
		if fi, _ := os.Stat(path); fi.Mode().Perm() != mode {
			t.Errorf("mode %#o became %#o", mode, fi.Mode().Perm())
		}
	}
	path, _ := annotated(t, 0o600)
	link := filepath.Join(t.TempDir(), "initech.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("no symlinks here")
	}
	mustUpdate(t, link, func(p *Project) { p.Name = "demo3" })
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a file")
	}
	if !strings.Contains(readFile(t, path), "project: demo3") {
		t.Error("the link's target was not edited")
	}
}

// The lock lives beside the config, never in a shared temp dir another
// user could fill first; and it is gone once the write is done.
func TestEdit_LockLivesBesideTheConfigAndIsRemoved(t *testing.T) {
	path, _ := annotated(t, 0o600)
	lock := configLockPath(path)
	if filepath.Dir(lock) != filepath.Dir(path) {
		t.Fatalf("lock %s is not beside the config %s", lock, path)
	}
	var held bool
	updateReadHook = func() { _, err := os.Stat(lock); held = err == nil }
	t.Cleanup(func() { updateReadHook = nil })
	mustUpdate(t, path, func(p *Project) { p.Name = "demo2" })
	if !held {
		t.Error("the write ran without its lock file beside the config")
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("the lock file was left behind")
	}
}

// Two writers at once (hire from a shell while the TUI records the hooks
// answer) both land: each re-reads under the lock.
func TestEdit_ConcurrentWritersBothLand(t *testing.T) {
	path, _ := annotated(t, 0o600)
	updateReadHook = func() { time.Sleep(100 * time.Millisecond) }
	t.Cleanup(func() { updateReadHook = nil })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, f := range []func(*Project){
		func(p *Project) { p.Roles = append(p.Roles, "arch") },
		func(p *Project) { yes := true; p.Attention.Hooks = &yes },
	} {
		wg.Add(1)
		go func(i int, f func(*Project)) {
			defer wg.Done()
			errs[i] = Update(path, func(p *Project) error { f(p); return nil })
		}(i, f)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Attention.HooksGranted() || p.Roles[len(p.Roles)-1] != "arch" {
		t.Errorf("a concurrent write was lost: roles=%v hooks=%v", p.Roles, p.Attention.Hooks)
	}
}
