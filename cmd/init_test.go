package cmd

import (
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
