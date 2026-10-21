package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// ini-0h7u: init --force rewrites exactly what the guide promises, the root
// CLAUDE.md and every agent's CLAUDE.md. Before the fix the same flag reached
// every templated file, so the documented way to refresh charters replaced
// the operator's PRD, spec, design doc, roadmap, AGENTS.md and .gitignore
// with templates. Temp dirs only.

func forceProject(t *testing.T) *config.Project {
	t.Helper()
	return &config.Project{Name: "demo", Root: t.TempDir(), Roles: []string{"super", "eng1"}}
}

func readAt(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func writeAt(t *testing.T, root, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// AC 1: --force leaves the operator's docs, AGENTS.md and .gitignore byte
// for byte, and still rewrites the charters (the documented half, kept).
func TestRun_ForceKeepsTheOperatorsDocsAndRewritesOnlyCharters(t *testing.T) {
	p := forceProject(t)
	if _, err := Run(p, Options{}); err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	mine := map[string]string{
		"docs/prd.md":          "# Our real PRD\n\nmonths of work\n",
		"docs/spec.md":         "# Our real spec\n",
		"docs/systemdesign.md": "# Our real design\n",
		"docs/roadmap.md":      "# Our real roadmap\n",
		"AGENTS.md":            "# our agents notes\n",
		".gitignore":           "node_modules/\nsecrets.env\n",
	}
	for rel, c := range mine {
		writeAt(t, p.Root, rel, c)
	}
	charters := []string{"CLAUDE.md", "super/CLAUDE.md", "eng1/CLAUDE.md"}
	for _, rel := range charters {
		writeAt(t, p.Root, rel, "stale charter\n")
	}

	if _, err := Run(p, Options{Force: true}); err != nil {
		t.Fatalf("scaffold --force: %v", err)
	}

	for rel, want := range mine {
		if got := readAt(t, p.Root, rel); got != want {
			t.Errorf("init --force replaced %s with a template:\n%s", rel, got)
		}
	}
	for _, rel := range charters {
		if readAt(t, p.Root, rel) == "stale charter\n" {
			t.Errorf("init --force did not rewrite %s; the guide promises it does", rel)
		}
	}
}

// AC 2: a fresh project gets every file, with or without --force.
func TestRun_AFreshProjectGetsEveryFileWithOrWithoutForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		p := forceProject(t)
		if _, err := Run(p, Options{Force: force}); err != nil {
			t.Fatalf("scaffold (force=%v): %v", force, err)
		}
		for _, rel := range []string{".gitignore", "CLAUDE.md", "AGENTS.md", "docs/prd.md", "docs/spec.md",
			"docs/systemdesign.md", "docs/roadmap.md", "super/CLAUDE.md", "eng1/CLAUDE.md"} {
			if fi, err := os.Stat(filepath.Join(p.Root, rel)); err != nil || fi.Size() == 0 {
				t.Errorf("force=%v: %s missing or empty (err=%v)", force, rel, err)
			}
		}
	}
}

// AC 3: a missing doc is created on a plain init or a forced one, and the
// other docs are left alone.
func TestRun_AMissingDocIsCreatedWithoutTouchingTheOthers(t *testing.T) {
	for _, force := range []bool{false, true} {
		p := forceProject(t)
		if _, err := Run(p, Options{}); err != nil {
			t.Fatal(err)
		}
		writeAt(t, p.Root, "docs/prd.md", "# kept\n")
		if err := os.Remove(filepath.Join(p.Root, "docs", "spec.md")); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(p, Options{Force: force}); err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(filepath.Join(p.Root, "docs", "spec.md")); err != nil || fi.Size() == 0 {
			t.Errorf("force=%v: the missing docs/spec.md was not created (err=%v)", force, err)
		}
		if got := readAt(t, p.Root, "docs/prd.md"); got != "# kept\n" {
			t.Errorf("force=%v: creating spec.md touched prd.md: %q", force, got)
		}
	}
}

// Feed it nothing: an empty docs dir is filled; a zero-byte doc the operator
// left is theirs, and stays zero bytes.
func TestRun_ForceLeavesAZeroByteDocAndFillsAnEmptyDocsDir(t *testing.T) {
	p := forceProject(t)
	if err := os.MkdirAll(filepath.Join(p.Root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(p, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(p.Root, "docs", "prd.md")); err != nil || fi.Size() == 0 {
		t.Fatalf("an empty docs/ was not filled (err=%v)", err)
	}
	writeAt(t, p.Root, "docs/roadmap.md", "")
	if _, err := Run(p, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	if got := readAt(t, p.Root, "docs/roadmap.md"); got != "" {
		t.Errorf("a zero-byte docs/roadmap.md was replaced with a template (%d bytes)", len(got))
	}
}
