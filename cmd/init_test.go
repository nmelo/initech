package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/scaffold"
	"github.com/spf13/cobra"
)

func TestBuildSelectorItems_Count(t *testing.T) {
	items := buildSelectorItems()
	if len(items) != len(selectorOrder) {
		t.Errorf("buildSelectorItems() returned %d items, want %d", len(items), len(selectorOrder))
	}
}

func TestBuildSelectorItems_StandardPreset(t *testing.T) {
	items := buildSelectorItems()
	wantChecked := map[string]bool{
		"super": true, "pm": true,
		"eng1": true, "eng2": true,
		"qa1": true, "qa2": true,
		"shipper": true,
	}
	checkedCount := 0
	for _, item := range items {
		if item.Checked {
			checkedCount++
			if !wantChecked[item.Name] {
				t.Errorf("item %q is checked but not in standard preset", item.Name)
			}
		} else if wantChecked[item.Name] {
			t.Errorf("item %q should be in standard preset but is unchecked", item.Name)
		}
	}
	if checkedCount != len(wantChecked) {
		t.Errorf("standard preset has %d checked items, want %d", checkedCount, len(wantChecked))
	}
}

func TestBuildSelectorItems_Groups(t *testing.T) {
	items := buildSelectorItems()
	wantGroup := map[string]string{
		"super":   "COORDINATORS",
		"pm":      "PRODUCT",
		"pmm":     "PRODUCT",
		"arch":    "PRODUCT",
		"eng1":    "ENGINEERS",
		"eng2":    "ENGINEERS",
		"eng3":    "ENGINEERS",
		"qa1":     "QA",
		"qa2":     "QA",
		"shipper": "OPERATIONS",
		"sec":     "OPERATIONS",
		"ops":     "OPERATIONS",
		"writer":  "OPERATIONS",
		"growth":  "OPERATIONS",
	}
	for _, item := range items {
		want, ok := wantGroup[item.Name]
		if !ok {
			t.Errorf("unexpected item %q in selector", item.Name)
			continue
		}
		if item.Group != want {
			t.Errorf("item %q group = %q, want %q", item.Name, item.Group, want)
		}
	}
}

func TestBuildSelectorItems_Tags(t *testing.T) {
	items := buildSelectorItems()
	wantTag := map[string]string{
		"super":   "",
		"shipper": "needs src",
		"eng1":    "needs src",
		"eng2":    "needs src",
		"eng3":    "needs src",
		"qa1":     "needs src",
		"qa2":     "needs src",
		"growth":  "needs src",
		"pm":      "",
		"pmm":     "",
		"arch":    "",
		"sec":     "",
		"ops":     "",
		"writer":  "",
	}
	for _, item := range items {
		want, ok := wantTag[item.Name]
		if !ok {
			t.Errorf("no expected tag entry for item %q", item.Name)
			continue
		}
		if item.Tag != want {
			t.Errorf("item %q tag = %q, want %q", item.Name, item.Tag, want)
		}
	}
}

func TestBuildSelectorItems_Descriptions(t *testing.T) {
	items := buildSelectorItems()
	for _, item := range items {
		if item.Description == "" {
			t.Errorf("item %q has empty description", item.Name)
		}
	}
}

func TestBuildSelectorItems_Names(t *testing.T) {
	items := buildSelectorItems()
	for i, item := range items {
		if item.Name != selectorOrder[i].name {
			t.Errorf("items[%d].Name = %q, want %q", i, item.Name, selectorOrder[i].name)
		}
	}
}

func TestBuildSelectorItems_SelectorItemType(t *testing.T) {
	// Verify the function returns the correct type understood by RunSelector.
	items := buildSelectorItems()
	var _ []roles.SelectorItem = items // compile-time type check
	if len(items) == 0 {
		t.Error("buildSelectorItems returned empty slice")
	}
}

// ── detectWorkspaces ─────────────────────────────────────────────────

func TestDetectWorkspaces_FindsExisting(t *testing.T) {
	root := t.TempDir()
	// Create super/ and eng1/ with CLAUDE.md, qa1/ without.
	for _, name := range []string{"super", "eng1"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# "+name), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "qa1"), 0755); err != nil {
		t.Fatal(err)
	}

	got := detectWorkspaces(root)
	if len(got) != 2 {
		t.Fatalf("detectWorkspaces: got %v, want [eng1 super]", got)
	}
	if got[0] != "eng1" || got[1] != "super" {
		t.Errorf("detectWorkspaces: got %v, want [eng1 super] (sorted)", got)
	}
}

func TestDetectWorkspaces_SkipsHiddenAndKnown(t *testing.T) {
	root := t.TempDir()
	// Hidden dir and skip-listed dirs with CLAUDE.md should not appear.
	for _, name := range []string{".beads", ".git", "docs", "dist", "node_modules"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# "+name), 0644)
	}
	// A real agent dir should appear.
	agentDir := filepath.Join(root, "super")
	os.MkdirAll(agentDir, 0755)
	os.WriteFile(filepath.Join(agentDir, "CLAUDE.md"), []byte("# super"), 0644)

	got := detectWorkspaces(root)
	if len(got) != 1 || got[0] != "super" {
		t.Errorf("detectWorkspaces: got %v, want [super]", got)
	}
}

func TestDetectWorkspaces_EmptyDir(t *testing.T) {
	root := t.TempDir()
	got := detectWorkspaces(root)
	if len(got) != 0 {
		t.Errorf("detectWorkspaces empty dir: got %v, want []", got)
	}
}

func TestDetectWorkspaces_InvalidRoot(t *testing.T) {
	got := detectWorkspaces("/nonexistent/path/xyz")
	if got != nil {
		t.Errorf("detectWorkspaces bad root: got %v, want nil", got)
	}
}

// ── describeWorkspace ────────────────────────────────────────────────

func TestDescribeWorkspace_CLAUDE_only(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# agent"), 0644)

	got := describeWorkspace(root, "agent")
	if got != "(CLAUDE.md)" {
		t.Errorf("describeWorkspace: got %q, want \"(CLAUDE.md)\"", got)
	}
}

func TestDescribeWorkspace_WithSrc(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "eng1")
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# eng1"), 0644)

	got := describeWorkspace(root, "eng1")
	if got != "(CLAUDE.md, src/)" {
		t.Errorf("describeWorkspace: got %q, want \"(CLAUDE.md, src/)\"", got)
	}
}

func TestDescribeWorkspace_WithClaude(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "super")
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# super"), 0644)

	got := describeWorkspace(root, "super")
	if got != "(CLAUDE.md, .claude/)" {
		t.Errorf("describeWorkspace: got %q, want \"(CLAUDE.md, .claude/)\"", got)
	}
}

func TestDescribeWorkspace_All(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "eng2")
	os.MkdirAll(filepath.Join(dir, "src"), 0755)
	os.MkdirAll(filepath.Join(dir, ".claude"), 0755)
	os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# eng2"), 0644)

	got := describeWorkspace(root, "eng2")
	if got != "(CLAUDE.md, src/, .claude/)" {
		t.Errorf("describeWorkspace: got %q, want \"(CLAUDE.md, src/, .claude/)\"", got)
	}
}

// ── buildSelectorItemsFromDetected ───────────────────────────────────

func TestBuildSelectorItemsFromDetected_CatalogRoles(t *testing.T) {
	detected := []string{"super", "eng1", "qa1"}
	items := buildSelectorItemsFromDetected(detected)

	// Should have at least as many items as selectorOrder (no extra CUSTOM).
	if len(items) != len(selectorOrder) {
		t.Errorf("len(items) = %d, want %d (all detected are catalog roles)", len(items), len(selectorOrder))
	}

	checkedNames := map[string]bool{}
	for _, item := range items {
		if item.Checked {
			checkedNames[item.Name] = true
		}
	}
	for _, d := range detected {
		if !checkedNames[d] {
			t.Errorf("detected role %q should be checked, but isn't", d)
		}
	}
	// Non-detected catalog roles should be unchecked.
	for _, item := range items {
		if !checkedNames[item.Name] {
			continue
		}
		found := false
		for _, d := range detected {
			if d == item.Name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("item %q is checked but not in detected list", item.Name)
		}
	}
}

func TestBuildSelectorItemsFromDetected_CustomRoles(t *testing.T) {
	detected := []string{"designer", "dba", "eng1"}
	items := buildSelectorItemsFromDetected(detected)

	// Should have selectorOrder items + 2 custom (designer, dba).
	want := len(selectorOrder) + 2
	if len(items) != want {
		t.Errorf("len(items) = %d, want %d (selectorOrder + 2 custom)", len(items), want)
	}

	// Custom items should be in CUSTOM group, checked, with "(detected)" description.
	customFound := map[string]bool{}
	for _, item := range items {
		if item.Group == "CUSTOM" {
			customFound[item.Name] = true
			if !item.Checked {
				t.Errorf("custom detected item %q should be checked", item.Name)
			}
			if item.Description != "(detected)" {
				t.Errorf("custom item %q description = %q, want \"(detected)\"", item.Name, item.Description)
			}
		}
	}
	if !customFound["designer"] || !customFound["dba"] {
		t.Errorf("expected designer and dba in CUSTOM group, got %v", customFound)
	}
	// eng1 is a catalog role, should NOT be in CUSTOM.
	if customFound["eng1"] {
		t.Error("eng1 is a catalog role and should not appear in CUSTOM group")
	}
}

func TestBuildSelectorItemsFromDetected_NoDetected(t *testing.T) {
	items := buildSelectorItemsFromDetected(nil)
	// With no detected roles, should behave like buildSelectorItems but all unchecked.
	if len(items) != len(selectorOrder) {
		t.Errorf("len(items) = %d, want %d", len(items), len(selectorOrder))
	}
	for _, item := range items {
		if item.Checked {
			t.Errorf("item %q should not be checked when no roles detected", item.Name)
		}
	}
}

// ── catalogContains ──────────────────────────────────────────────────

func TestCatalogContains(t *testing.T) {
	if !catalogContains("super") {
		t.Error("catalogContains(\"super\") should be true")
	}
	if !catalogContains("eng1") {
		t.Error("catalogContains(\"eng1\") should be true")
	}
	if catalogContains("designer") {
		t.Error("catalogContains(\"designer\") should be false")
	}
	if catalogContains("") {
		t.Error("catalogContains(\"\") should be false")
	}
}

func TestPrompt_BlankReturnsDefault(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	if got := prompt(reader, "Project name", "demo"); got != "demo" {
		t.Fatalf("prompt blank = %q, want default demo", got)
	}
}

func TestPrompt_ReturnsEnteredValue(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("custom\n"))
	if got := prompt(reader, "Project name", "demo"); got != "custom" {
		t.Fatalf("prompt entered = %q, want custom", got)
	}
}

func TestInteractiveSetup_UsesDetectedWorkspacesAndRepo(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "eng1", "CLAUDE.md"), "# eng1")
	mustWriteFile(t, filepath.Join(root, "designer", "CLAUDE.md"), "# designer")

	restoreStdin := withStdin(t, "\n"+root+"\nhttps://github.com/acme/widget.git\nY\ny\nwid\n")
	defer restoreStdin()

	restoreSelector := stubRoleSelector(t, func(title string, items []roles.SelectorItem, help ...string) ([]string, error) {
		if !strings.Contains(title, "Select agents for "+filepath.Base(root)) {
			t.Fatalf("selector title = %q", title)
		}

		checked := map[string]bool{}
		group := map[string]string{}
		for _, item := range items {
			if item.Checked {
				checked[item.Name] = true
			}
			group[item.Name] = item.Group
		}
		if !checked["eng1"] || !checked["designer"] {
			t.Fatalf("detected roles should be prechecked, got checked=%v", checked)
		}
		if group["designer"] != "CUSTOM" {
			t.Fatalf("designer group = %q, want CUSTOM", group["designer"])
		}
		return []string{"eng1", "designer"}, nil
	})
	defer restoreSelector()

	p, err := interactiveSetup(root)
	if err != nil {
		t.Fatalf("interactiveSetup: %v", err)
	}

	if p.Name != filepath.Base(root) {
		t.Fatalf("project name = %q, want %q", p.Name, filepath.Base(root))
	}
	if p.Root != root {
		t.Fatalf("project root = %q, want %q", p.Root, root)
	}
	if len(p.Roles) != 2 || p.Roles[0] != "eng1" || p.Roles[1] != "designer" {
		t.Fatalf("roles = %v, want [eng1 designer]", p.Roles)
	}
	if p.Beads.Prefix != "wid" {
		t.Fatalf("beads prefix = %q, want wid", p.Beads.Prefix)
	}
	if len(p.Repos) != 1 || p.Repos[0].URL != "https://github.com/acme/widget.git" || p.Repos[0].Name != "widget" {
		t.Fatalf("repos = %#v, want widget repo", p.Repos)
	}
}

func TestInteractiveSetup_CancelledSelector(t *testing.T) {
	root := t.TempDir()
	restoreStdin := withStdin(t, "demo\n"+root+"\n\nabc\n")
	defer restoreStdin()

	restoreSelector := stubRoleSelector(t, func(title string, items []roles.SelectorItem, help ...string) ([]string, error) {
		return nil, fmt.Errorf("cancelled")
	})
	defer restoreSelector()

	_, err := interactiveSetup(root)
	if err == nil || err.Error() != "role selection cancelled" {
		t.Fatalf("interactiveSetup cancel error = %v, want role selection cancelled", err)
	}
}

func TestRunInit_LoadsExistingConfigAndUsesStubs(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Project{
		Name:  "demo",
		Root:  dir,
		Roles: []string{"pm"},
		Beads: config.BeadsConfig{Prefix: "dem"},
	}
	if err := config.Write(filepath.Join(dir, "initech.yaml"), cfg); err != nil {
		t.Fatalf("config.Write: %v", err)
	}

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	restoreRunner := stubInitRunner(t, &fakeMultiRunner{
		responses: []fakeResponse{
			{output: "", err: fmt.Errorf("which bd: not found")},
		},
	})
	defer restoreRunner()

	restoreScaffold := stubScaffoldRun(t, func(p *config.Project, opts scaffold.Options) ([]string, error) {
		if p.Name != "demo" {
			t.Fatalf("scaffold project = %q, want demo", p.Name)
		}
		if opts.Force != initForce {
			t.Fatalf("scaffold force = %v, want %v", opts.Force, initForce)
		}
		if opts.Progress != nil {
			opts.Progress("Creating docs/prd.md")
		}
		return []string{"docs/prd.md", "pm/CLAUDE.md"}, nil
	})
	defer restoreScaffold()

	restoreGit := stubInitGit(t)
	defer restoreGit()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	if err := runInit(cmd, nil); err != nil {
		t.Fatalf("runInit: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"Loaded existing initech.yaml",
		"Scaffolding project...",
		"docs/prd.md",
		"Initializing git repository",
		"Skipping beads (bd not found)",
		"Initial commit",
		"2 files created",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runInit output missing %q:\n%s", want, got)
		}
	}
}

func TestRunInit_InvalidExistingConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte("project: demo\nroles: ["), 0o644); err != nil {
		t.Fatalf("WriteFile initech.yaml: %v", err)
	}

	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	err := runInit(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("runInit should fail when existing config is invalid")
	}
}

func withStdin(t *testing.T, input string) func() {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatalf("WriteString stdin: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	return func() {
		os.Stdin = orig
		_ = r.Close()
	}
}

func stubRoleSelector(t *testing.T, fn func(string, []roles.SelectorItem, ...string) ([]string, error)) func() {
	t.Helper()
	orig := runRoleSelector
	runRoleSelector = fn
	return func() { runRoleSelector = orig }
}

func stubInitRunner(t *testing.T, runner iexec.Runner) func() {
	t.Helper()
	orig := newInitRunner
	newInitRunner = func() iexec.Runner { return runner }
	return func() { newInitRunner = orig }
}

func stubScaffoldRun(t *testing.T, fn func(*config.Project, scaffold.Options) ([]string, error)) func() {
	t.Helper()
	orig := scaffoldRun
	scaffoldRun = fn
	return func() { scaffoldRun = orig }
}

func stubInitGit(t *testing.T) func() {
	t.Helper()
	origInit := gitInit
	origAddSubmodule := gitAddSubmodule
	origCommitAll := gitCommitAll
	gitInit = func(r iexec.Runner, root string) error { return nil }
	gitAddSubmodule = func(r iexec.Runner, root, repoURL, subPath string) error { return nil }
	gitCommitAll = func(r iexec.Runner, root, message string) error { return nil }
	return func() {
		gitInit = origInit
		gitAddSubmodule = origAddSubmodule
		gitCommitAll = origCommitAll
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}
