package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// product_repo (ini-61by): a top-level path to a local clone of the rig's
// product repo; the rig numbers read that clone's main instead of the first
// repo's.

func captureConfigWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := configWarnings
	configWarnings = &buf
	t.Cleanup(func() { configWarnings = prev })
	return &buf
}

// a clone at dir: a folder holding .git.
func fakeClone(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestProductRepo_ARelativePathResolvesAgainstTheRoot(t *testing.T) {
	root := t.TempDir()
	fakeClone(t, filepath.Join(root, "eng5", "src"))
	warn := captureConfigWarnings(t)
	p, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\nproduct_repo: eng5/src\n"))
	if err != nil {
		t.Fatal(err)
	}
	dir, ok := p.ProductRepoDir()
	if !ok || dir != filepath.Join(root, "eng5", "src") {
		t.Fatalf("ProductRepoDir = %q, %v", dir, ok)
	}
	if warn.Len() != 0 {
		t.Fatalf("a good clone warned: %q", warn.String())
	}
}

func TestProductRepo_AnAbsolutePathIsTakenAsWritten(t *testing.T) {
	root, clone := t.TempDir(), t.TempDir()
	fakeClone(t, clone)
	p, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\nproduct_repo: "+clone+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if dir, ok := p.ProductRepoDir(); !ok || dir != clone {
		t.Fatalf("ProductRepoDir = %q, %v", dir, ok)
	}
}

// A worktree's .git is a file; it is a clone too.
func TestProductRepo_AWorktreeCounts(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, "wt")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: elsewhere\n"), 0o644)
	p, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\nproduct_repo: wt\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.ProductRepoDir(); !ok {
		t.Fatal("a worktree was not taken as a clone")
	}
}

// AC2 load check: not a folder, or not a clone, warns once naming the key and
// the path; the rest loads, and the repo numbers have no clone to read.
func TestProductRepo_ABadPathWarnsAndTheRestLoads(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "plain"), 0o755)
	os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0o644)
	for _, path := range []string{"missing", "plain", "afile"} {
		warn := captureConfigWarnings(t)
		p, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\nproduct_repo: "+path+"\n"))
		if err != nil {
			t.Fatalf("%s: a bad product_repo failed the load: %v", path, err)
		}
		if p.Name != "x" || len(p.Roles) != 1 {
			t.Fatalf("%s: the rest did not load: %+v", path, p)
		}
		w := warn.String()
		if strings.Count(w, "\n") != 1 || !strings.Contains(w, "product_repo") || !strings.Contains(w, filepath.Join(root, path)) {
			t.Fatalf("%s: warning %q, want one line naming product_repo and the path", path, w)
		}
		if p.ProductRepo == "" {
			t.Fatalf("%s: the key was dropped", path)
		}
		if _, ok := p.ProductRepoDir(); ok {
			t.Fatalf("%s: a bad path is readable", path)
		}
	}
}

// Feed it nothing: absent and empty both read as today, with no warning.
func TestProductRepo_AbsentOrEmptyIsToday(t *testing.T) {
	root := t.TempDir()
	fakeClone(t, root) // the rig root is itself a clone: empty must not mean "the root"
	for _, extra := range []string{"", "product_repo: \"\"\n", "product_repo:\n"} {
		warn := captureConfigWarnings(t)
		p, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\n"+extra))
		if err != nil {
			t.Fatalf("%q: %v", extra, err)
		}
		if _, ok := p.ProductRepoDir(); ok || p.ProductRepo != "" || warn.Len() != 0 {
			t.Fatalf("%q: ProductRepo %q, warn %q", extra, p.ProductRepo, warn.String())
		}
	}
}

// Rule 6: the v2.60.0 loader shape (Project without the field) loads a file
// carrying product_repo, because the top level is not strict.
func TestProductRepo_AnOlderLoaderIgnoresTheKey(t *testing.T) {
	root := t.TempDir()
	data := []byte("project: x\nroot: " + root + "\nroles: [eng1]\nproduct_repo: src\nnumbers:\n  gate_red_max: 1\n")
	pt := reflect.TypeOf(Project{})
	var fields []reflect.StructField
	found := false
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		if f.Name == "ProductRepo" {
			found = true
			continue
		}
		if !f.IsExported() {
			continue
		}
		fields = append(fields, f)
	}
	if !found {
		t.Fatal("Project has no ProductRepo field to leave out")
	}
	older := reflect.New(reflect.StructOf(fields))
	if err := yaml.Unmarshal(data, older.Interface()); err != nil {
		t.Fatalf("the older shape refused product_repo: %v", err)
	}
	if got := older.Elem().FieldByName("Name").String(); got != "x" {
		t.Fatalf("the older shape read project %q", got)
	}
}

// A product_repo under numbers: by mistake fails with the numbers parser's
// message: the block parser is untouched.
func TestProductRepo_UnderNumbersFailsWithTheParsersMessage(t *testing.T) {
	root := t.TempDir()
	_, err := Load(writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\nnumbers:\n  product_repo: src\n"))
	if err == nil || !strings.Contains(err.Error(), `numbers.product_repo: "product_repo" is not a number's _min or _max`) {
		t.Fatalf("err = %v, want the numbers parser's refusal", err)
	}
}

// Rule 4: a write keeps the key as the file has it, comment and all.
func TestProductRepo_AWriteKeepsTheLine(t *testing.T) {
	root := t.TempDir()
	fakeClone(t, filepath.Join(root, "src"))
	line := "product_repo: src   # the product, not the scaffolding\n"
	path := writeConfig(t, root, "project: x\nroot: "+root+"\nroles: [eng1]\n"+line)
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p.Roles = append(p.Roles, "eng2")
	if err := Write(path, p); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), line) || !strings.Contains(string(got), "eng2") {
		t.Fatalf("after a write:\n%s", got)
	}
}

// Rule 4: a fresh file never gains the key.
func TestProductRepo_AFreshFileNeverGainsTheKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "initech.yaml")
	if err := Write(path, &Project{Name: "x", Root: root, Roles: []string{"eng1"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "product_repo") {
		t.Fatalf("a fresh file carries product_repo:\n%s", got)
	}
}
