package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/roles"
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
		"super":   "supervised",
		"shipper": "supervised",
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
