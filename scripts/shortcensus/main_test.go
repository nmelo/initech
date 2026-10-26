package main

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func auditSrc(t *testing.T, body string) result {
	t.Helper()
	src := "package p\n\nimport \"testing\"\n\n" + body
	r, err := audit(token.NewFileSet(), "x_test.go", src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return r
}

// Every shape the repo uses today is accepted: a bare guard, each skip
// call, and an || chain with the Short call on either side.
func TestAudit_AcceptsSkipGuards(t *testing.T) {
	for name, body := range map[string]string{
		"skip":      "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Skip(\"slow\")\n\t}\n}\n",
		"skipf":     "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Skipf(\"%s\", \"slow\")\n\t}\n}\n",
		"skipnow":   "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\tt.SkipNow()\n\t}\n}\n",
		"or right":  "func TestA(t *testing.T) {\n\tif x() || testing.Short() {\n\t\tt.Skip()\n\t}\n}\nfunc x() bool { return false }\n",
		"or left":   "func TestA(t *testing.T) {\n\tif (testing.Short() || x()) {\n\t\tt.Skip()\n\t}\n}\nfunc x() bool { return false }\n",
		"in helper": "func skipShort(tb testing.TB) {\n\tif testing.Short() {\n\t\ttb.Skip(\"slow\")\n\t}\n}\n",
	} {
		r := auditSrc(t, body)
		if len(r.problems) != 0 || len(r.accepted) != 1 {
			t.Errorf("%s: accepted %v, problems %v; want one accepted use", name, r.accepted, r.problems)
		}
	}
}

// The mutation table: each way a use could make -short change a test rather
// than only skip it is red, and named by line.
func TestAudit_RejectsEveryOtherUse(t *testing.T) {
	for name, body := range map[string]string{
		"lighter variant": "func TestA(t *testing.T) {\n\tn := 1000\n\tif testing.Short() {\n\t\tn = 10\n\t}\n\t_ = n\n}\n",
		"negated":         "func TestA(t *testing.T) {\n\tif !testing.Short() {\n\t\tt.Skip()\n\t}\n}\n",
		"and":             "func TestA(t *testing.T) {\n\tif x() && testing.Short() {\n\t\tt.Skip()\n\t}\n}\nfunc x() bool { return true }\n",
		"else":            "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Skip()\n\t} else {\n\t\tt.Log(\"long\")\n\t}\n}\n",
		"extra statement": "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\tt.Log(\"x\")\n\t\tt.Skip()\n\t}\n}\n",
		"not a skip":      "func TestA(t *testing.T) {\n\tif testing.Short() {\n\t\treturn\n\t}\n}\n",
		"init statement":  "func TestA(t *testing.T) {\n\tif n := 1; testing.Short() {\n\t\tt.Skip(n)\n\t}\n}\n",
		"outside an if":   "func TestA(t *testing.T) {\n\tshort := testing.Short()\n\t_ = short\n}\n",
		"as a value":      "var isShort = testing.Short\n",
	} {
		r := auditSrc(t, body)
		if len(r.problems) != 1 || len(r.accepted) != 0 || !strings.Contains(r.problems[0], "x_test.go:") {
			t.Errorf("%s: accepted %v, problems %v; want exactly one problem naming the file", name, r.accepted, r.problems)
		}
	}
}

// The import's own name is what is audited: an aliased import is still
// checked, and a dot import is red because it cannot be.
func TestAudit_FollowsTheImportName(t *testing.T) {
	src := "package p\n\nimport tt \"testing\"\n\nfunc TestA(t *tt.T) {\n\tn := 1\n\tif tt.Short() {\n\t\tn = 2\n\t}\n\t_ = n\n}\n"
	r, err := audit(token.NewFileSet(), "x_test.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.problems) != 1 {
		t.Errorf("aliased import: problems %v, want the lighter variant named", r.problems)
	}
	src = "package p\n\nimport . \"testing\"\n\nfunc TestA(t *T) {\n\tif Short() {\n\t\tt.Skip()\n\t}\n}\n"
	if r, _ = audit(token.NewFileSet(), "x_test.go", src); len(r.problems) != 1 || !strings.Contains(r.problems[0], "dot import") {
		t.Errorf("dot import: problems %v, want it refused", r.problems)
	}
	src = "package p\n\nfunc Short() bool { return true }\n\nfunc f() { if !Short() {} }\n"
	if r, _ = audit(token.NewFileSet(), "x.go", src); len(r.problems)+len(r.accepted) != 0 {
		t.Errorf("a file without the testing import: %v %v, want nothing read", r.accepted, r.problems)
	}
}

// Feed it nothing: a tree with no use at all is red, not an empty pass. The
// positive twin: the same walk over this repo finds uses, and testdata is
// skipped as the go tool skips it.
func TestCensus_EmptyTreeIsRedAndTheRepoIsRead(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "testdata"), 0o755)
	os.WriteFile(filepath.Join(dir, "testdata", "b_test.go"), []byte("package b\n\nimport \"testing\"\n\nvar s = testing.Short\n"), 0o644)
	r, err := census(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.problems) != 1 || !strings.Contains(r.problems[0], "reading nothing") {
		t.Errorf("empty tree: problems %v, want the reading-nothing failure (testdata is not scanned)", r.problems)
	}
	r, err = census("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.accepted) == 0 || len(r.problems) != 0 {
		t.Errorf("the repo: %d accepted, problems %v; want uses found and none red", len(r.accepted), r.problems)
	}
}
