package scaffold

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// TestRun_EveryCatalogRoleRendersWithoutPlaceholders is the ini-rg12
// regression. Before the fix this failed with eight placeholder lines across
// the eng and qa templates ({{tech_stack}}, {{build_cmd}}, {{test_cmd}}),
// because nothing but a hand-written role_overrides block ever supplied those
// three variables — so a plain project, which is what `initech init` and
// `initech hire` both produce, wrote literal placeholders into every agent's
// instructions.
//
// The config here has NO role_overrides and no project-level keys on purpose:
// that is the shape of a default project, and it was the unconfigured path that
// shipped broken. A fixture that configured the variables would pass on the
// unfixed code and prove nothing.
func TestRun_EveryCatalogRoleRendersWithoutPlaceholders(t *testing.T) {
	root := t.TempDir()
	names := make([]string, 0, len(roles.Catalog))
	for name := range roles.Catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatal("roles.Catalog is empty; the fixture would assert nothing")
	}
	// Numbered family members take a different TemplateForRole branch than the
	// bare catalog names, and both eng and qa templates carry the variables.
	names = append(names, "eng1", "qa1", "marketing")

	p := &config.Project{Name: "placeholder-project", Root: root, Roles: names}
	if _, err := Run(p, Options{}); err != nil {
		t.Fatalf("scaffold: %v", err)
	}

	var offenders []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		for _, left := range roles.UnrenderedPlaceholders(string(raw)) {
			offenders = append(offenders, rel+": "+left)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("scaffold wrote %d unrendered placeholder(s):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}

	// Guard against the test passing because nothing was written: the walk
	// above reports nothing for an empty tree just as it does for a clean one.
	engMD, readErr := os.ReadFile(filepath.Join(root, "eng1", "CLAUDE.md"))
	if readErr != nil {
		t.Fatalf("eng1/CLAUDE.md not written: %v", readErr)
	}
	if !strings.Contains(string(engMD), "## Tech Stack") {
		t.Error("eng1/CLAUDE.md has no Tech Stack section; the fixture is not exercising the variables")
	}
}

// TestRenderVarsFor_PrecedenceAndUnsetText covers all three levels of the
// resolver in one table: a role override wins over the project key, the project
// key applies to roles without an override, and an unconfigured variable
// resolves to text that says so. The last row is the load-bearing one — it is
// the only level that existed nowhere before ini-rg12.
func TestRenderVarsFor_PrecedenceAndUnsetText(t *testing.T) {
	p := &config.Project{
		Name:      "proj",
		Root:      "/root",
		Roles:     []string{"eng1", "eng2", "qa1"},
		TechStack: "Go 1.25",
		BuildCmd:  "make build",
		TestCmd:   "make test",
		RoleOverrides: map[string]config.RoleOverride{
			"eng2": {TechStack: "Rust 1.80", BuildCmd: "cargo build"},
		},
	}

	projectLevel := RenderVarsFor(p, p.Root, "eng1")
	if projectLevel.TechStack != "Go 1.25" || projectLevel.BuildCmd != "make build" || projectLevel.TestCmd != "make test" {
		t.Errorf("eng1 took project keys = %+v", projectLevel)
	}

	overridden := RenderVarsFor(p, p.Root, "eng2")
	if overridden.TechStack != "Rust 1.80" {
		t.Errorf("eng2 TechStack = %q, want the role override to win", overridden.TechStack)
	}
	if overridden.BuildCmd != "cargo build" {
		t.Errorf("eng2 BuildCmd = %q, want the role override to win", overridden.BuildCmd)
	}
	// A PARTIAL override must not blank the fields it does not mention.
	if overridden.TestCmd != "make test" {
		t.Errorf("eng2 TestCmd = %q, want the project key to survive a partial override", overridden.TestCmd)
	}

	bare := &config.Project{Name: "proj", Root: "/root", Roles: []string{"eng1"}}
	unset := RenderVarsFor(bare, bare.Root, "eng1")
	if unset.BuildCmd != UnsetBuildCmd || unset.TestCmd != UnsetTestCmd {
		t.Errorf("unconfigured commands = %q / %q, want the unset text", unset.BuildCmd, unset.TestCmd)
	}
	if !strings.Contains(unset.TechStack, "initech.yaml") {
		t.Errorf("unset TechStack = %q, want it to name the file to edit", unset.TechStack)
	}
	if !strings.Contains(unset.TechStack, "role_overrides.eng1.tech_stack") {
		t.Errorf("unset TechStack = %q, want it to name this role's override key", unset.TechStack)
	}
	// Never guess: the unset text must not read as a usable stack or command.
	if strings.Contains(unset.TechStack, "Go") {
		t.Errorf("unset TechStack = %q, want no invented stack", unset.TechStack)
	}

	// Every field non-empty is the property the write guard rests on: Render
	// leaves a placeholder intact for any empty value.
	for name, got := range map[string]string{
		"ProjectName": unset.ProjectName, "ProjectRoot": unset.ProjectRoot,
		"TechStack": unset.TechStack, "BuildCmd": unset.BuildCmd, "TestCmd": unset.TestCmd,
	} {
		if got == "" {
			t.Errorf("%s resolved empty; Render would leave its placeholder on disk", name)
		}
	}
}

// TestWriteFile_RefusesUnrenderedPlaceholder pins the AC's "failure, not a
// warning": the call errors AND no file is left behind. A guard that reported
// the problem but still wrote the file would satisfy a naive assertion on the
// error alone.
func TestWriteFile_RefusesUnrenderedPlaceholder(t *testing.T) {
	dir := t.TempDir()
	path, err := writeFile(dir, "CLAUDE.md", "# Stack\n\n{{tech_stack}}\n", false)
	if err == nil {
		t.Fatal("writeFile accepted content with an unrendered placeholder")
	}
	if path != "" {
		t.Errorf("path = %q, want empty on refusal", path)
	}
	if !strings.Contains(err.Error(), "tech_stack") {
		t.Errorf("error = %q, want it to name the offending variable", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(dir, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Error("refused content was written to disk anyway")
	}

	// The same call with the variable resolved must still write, or the guard
	// would be indistinguishable from writeFile being broken outright.
	if _, err := writeFile(dir, "CLAUDE.md", "# Stack\n\nGo 1.25\n", false); err != nil {
		t.Fatalf("clean content rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
		t.Errorf("clean content not written: %v", err)
	}
}

// TestWriteFile_RefusesMalformedPlaceholder covers the reason the guard scans
// for the literal "{{" instead of reusing the renderer's varPattern: a
// placeholder the renderer cannot even recognise is the one most likely to
// reach disk, since no substitution attempt will ever touch it.
func TestWriteFile_RefusesMalformedPlaceholder(t *testing.T) {
	for _, content := range []string{"Build: {{ build_cmd }}\n", "Build: {{build-cmd}}\n", "Build: {{}}\n"} {
		dir := t.TempDir()
		if _, err := writeFile(dir, "CLAUDE.md", content, false); err == nil {
			t.Errorf("writeFile accepted %q; varPattern does not match it but it is just as broken on disk", content)
		}
	}
}
