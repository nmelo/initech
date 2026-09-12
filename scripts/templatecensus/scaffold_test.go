package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
)

func TestScaffoldOutput_WalksAdditionalFilesAndReportsVerbs(t *testing.T) {
	dir := t.TempDir()
	// No extension/path allowlist, including files outside docs and CLAUDE.md.
	for path, text := range map[string]string{
		"CLAUDE.md":                      "Run initech status",
		"docs/onboarding.md":             "Welcome\nRun initech notarealverb",
		"new-writer/nested/instructions": "initech anothermissingverb",
	} {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := readScaffoldOutput(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("read %d files, want all 3", len(files))
	}
	count, err := checkOutputVerbs(files, map[string]bool{"status": true})
	if count != 3 || err == nil {
		t.Fatalf("count=%d err=%v", count, err)
	}
	for _, want := range []string{"docs/onboarding.md:2: initech notarealverb", "new-writer/nested/instructions:1: initech anothermissingverb"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v does not name %s", err, want)
		}
	}
	count, err = checkOutputVerbs(files, map[string]bool{"status": true, "notarealverb": true, "anothermissingverb": true})
	if count != 3 || err != nil {
		t.Fatalf("registered output: count=%d err=%v", count, err)
	}
}

func TestScaffoldOutput_RefusesEmptyOrUnreadableSubjects(t *testing.T) {
	if _, err := readScaffoldOutput(t.TempDir()); err == nil {
		t.Fatal("empty output passed")
	}
	if _, err := readScaffoldOutput(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing root passed")
	}
	if _, err := checkOutputVerbs(map[string]string{"CLAUDE.md": "no commands"}, nil); err == nil {
		t.Fatal("zero mentions passed")
	}
}

func TestTemplateInventory_ResolvesConstantsAndNamesUnreachedTemplates(t *testing.T) {
	dir := t.TempDir()
	src := "package roles\nconst fragment = `initech status`\nconst FirstTemplate = `# CLAUDE.md\n` + fragment\nconst ForgottenTemplate = `never rendered`\n"
	if err := os.WriteFile(filepath.Join(dir, "new_templates.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	templates, err := templateConstants(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 2 || templates["FirstTemplate"] != "# CLAUDE.md\ninitech status" {
		t.Fatalf("inventory=%v", templates)
	}
	p := &config.Project{Roles: []string{"first"}}
	files := map[string]string{"first/CLAUDE.md": templates["FirstTemplate"]}
	if err := checkTemplateCoverage(templates, files, p); err == nil || !strings.Contains(err.Error(), "ForgottenTemplate") {
		t.Fatalf("missing template: %v", err)
	}
	files["docs/new.md"] = templates["ForgottenTemplate"]
	if err := checkTemplateCoverage(templates, files, p); err != nil {
		t.Fatal(err)
	}
	delete(files, "first/CLAUDE.md")
	files["docs/wrong-place.md"] = templates["FirstTemplate"]
	if err := checkTemplateCoverage(templates, files, p); err == nil || !strings.Contains(err.Error(), "FirstTemplate") {
		t.Fatalf("role template never handed to role: %v", err)
	}
}

func TestTemplateInventory_RefusesEmptyAndInvalidPackages(t *testing.T) {
	for _, src := range []string{"package roles", "package roles\nconst BadTemplate = missing", "package roles\nconst BadTemplate = 3"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "templates.go"), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := templateConstants(dir); err == nil {
			t.Fatalf("accepted %s", src)
		}
	}
}

func TestTemplateCoverage_CatalogRolesReachEveryRealTemplate(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	templates, err := templateConstants(filepath.Join(root, scanDir))
	if err != nil {
		t.Fatal(err)
	}
	p := &config.Project{Name: "coverage", Root: t.TempDir()}
	for name := range roles.Catalog {
		p.Roles = append(p.Roles, name)
	}
	if _, err := scaffold.Run(p, scaffold.Options{}); err != nil {
		t.Fatal(err)
	}
	files, err := readScaffoldOutput(p.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTemplateCoverage(templates, files, p); err != nil {
		t.Fatal(err)
	}
	// Remove each catalog output in turn. Shared engineer/QA templates still
	// reach other roles; templates with a single role must fail by constant name.
	for _, role := range p.Roles {
		tmpl := scaffold.TemplateForRole(role)
		unique := true
		for _, other := range p.Roles {
			if other != role && scaffold.TemplateForRole(other) == tmpl {
				unique = false
			}
		}
		if !unique {
			continue
		}
		path := role + "/CLAUDE.md"
		saved := files[path]
		delete(files, path)
		err := checkTemplateCoverage(templates, files, p)
		files[path] = saved
		for name, text := range templates {
			if text == tmpl && (err == nil || !strings.Contains(err.Error(), name)) {
				t.Errorf("removed %s: missing %s was not reported: %v", path, name, err)
			}
		}
	}
	t.Logf("catalog=%d templates=%d files=%d", len(p.Roles), len(templates), len(files))
}
