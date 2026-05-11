package roles

import "testing"

func TestCatalogCompleteness(t *testing.T) {
	expected := []string{
		"super", "eng1", "eng2", "eng3", "qa1", "qa2", "shipper",
		"pm", "pmm", "arch", "sec", "writer", "ops", "growth", "intern",
	}

	for _, name := range expected {
		if _, ok := Catalog[name]; !ok {
			t.Errorf("missing catalog entry for %q", name)
		}
	}

	if len(Catalog) != len(expected) {
		t.Errorf("catalog has %d entries, expected %d", len(Catalog), len(expected))
	}
}

func TestCatalogPermissionTiers(t *testing.T) {
	supervised := []string{}
	for _, name := range supervised {
		def := Catalog[name]
		if def.Permission != Supervised {
			t.Errorf("%s should be Supervised, got %d", name, def.Permission)
		}
	}

	autonomous := []string{"super", "eng1", "eng2", "eng3", "qa1", "qa2", "shipper", "pm", "pmm", "arch", "sec", "writer", "ops", "growth", "intern"}
	for _, name := range autonomous {
		def := Catalog[name]
		if def.Permission != Autonomous {
			t.Errorf("%s should be Autonomous, got %d", name, def.Permission)
		}
	}
}

func TestCatalogNeedsSrc(t *testing.T) {
	needsSrc := []string{"eng1", "eng2", "eng3", "qa1", "qa2", "shipper", "growth", "intern"}
	for _, name := range needsSrc {
		def := Catalog[name]
		if !def.NeedsSrc {
			t.Errorf("%s should have NeedsSrc=true", name)
		}
	}

	noSrc := []string{"super", "pm", "pmm", "arch", "sec", "writer", "ops"}
	for _, name := range noSrc {
		def := Catalog[name]
		if def.NeedsSrc {
			t.Errorf("%s should have NeedsSrc=false", name)
		}
	}
}

func TestCatalogNeedsPlaybooks(t *testing.T) {
	needsPlaybooks := []string{"qa1", "qa2", "shipper", "ops"}
	for _, name := range needsPlaybooks {
		def := Catalog[name]
		if !def.NeedsPlaybooks {
			t.Errorf("%s should have NeedsPlaybooks=true", name)
		}
	}
}

func TestCatalogNames(t *testing.T) {
	for name, def := range Catalog {
		if def.Name != name {
			t.Errorf("catalog key %q has Name %q", name, def.Name)
		}
	}
}

func TestLookupRole_Known(t *testing.T) {
	def := LookupRole("super")
	if def.Name != "super" {
		t.Errorf("Name = %q, want %q", def.Name, "super")
	}
	if def.Permission != Autonomous {
		t.Errorf("super should be Autonomous")
	}
}

func TestLookupRole_Unknown(t *testing.T) {
	def := LookupRole("designer")
	if def.Name != "designer" {
		t.Errorf("Name = %q, want %q", def.Name, "designer")
	}
	if def.Permission != Autonomous {
		t.Errorf("unknown role should default to Autonomous")
	}
	if def.NeedsSrc {
		t.Error("unknown role should not need src")
	}
	if def.NeedsPlaybooks {
		t.Error("unknown role should not need playbooks")
	}
}

func TestIsValidRoleName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		// Exact catalog matches.
		{"super", true},
		{"shipper", true},
		{"eng1", true},
		{"qa2", true},
		{"intern", true},
		{"growth", true},

		// Numbered family extensions (the new behavior).
		{"qa3", true},
		{"qa10", true},
		{"qa007", true},
		{"qa99999", true},
		{"qa0", true}, // no zero special-case per spec
		{"eng4", true},
		{"eng7", true},
		{"eng99", true},

		// Typos and near-misses must still reject.
		{"qaa1", false},
		{"enginer", false},
		{"engineer", false},
		{"qa1.5", false},
		{"qa-1", false},
		{"qa_1", false},
		{"q1", false},
		{"engX", false},
		{"eng", false},
		{"qa", false},
		{"eng99extra", false},
		{"prefixqa1", false}, // anchored: qa must start at position 0
		{"qa1suffix", false}, // anchored: qa1 must end at the right anchor

		// Case-sensitive: uppercase is rejected.
		{"Q1", false},
		{"QA1", false},
		{"ENG1", false},
		{"Qa1", false},

		// Custom names that are not in catalog and not numbered families.
		{"designer", false},
		{"dba", false},
		{"random-custom-role", false},

		// Empty rejects.
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidRoleName(tt.name)
			if got != tt.want {
				t.Errorf("IsValidRoleName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestLookupRole_NumberedFamily(t *testing.T) {
	t.Run("qa10 inherits qa1/qa2 defaults", func(t *testing.T) {
		def := LookupRole("qa10")
		if def.Name != "qa10" {
			t.Errorf("Name = %q, want %q", def.Name, "qa10")
		}
		if def.Permission != Autonomous {
			t.Error("qa10 should be Autonomous")
		}
		if !def.NeedsSrc {
			t.Error("qa10 must inherit NeedsSrc=true from qa1/qa2; otherwise scaffold skips qa10/src/")
		}
		if !def.NeedsPlaybooks {
			t.Error("qa10 must inherit NeedsPlaybooks=true from qa1/qa2; otherwise scaffold skips qa10/playbooks/")
		}
	})

	t.Run("eng7 inherits eng1/eng2/eng3 defaults", func(t *testing.T) {
		def := LookupRole("eng7")
		if def.Name != "eng7" {
			t.Errorf("Name = %q, want %q", def.Name, "eng7")
		}
		if def.Permission != Autonomous {
			t.Error("eng7 should be Autonomous")
		}
		if !def.NeedsSrc {
			t.Error("eng7 must inherit NeedsSrc=true from eng1/eng2/eng3")
		}
		if def.NeedsPlaybooks {
			t.Error("eng7 must NOT have NeedsPlaybooks=true (eng family does not use playbooks)")
		}
	})

	t.Run("typo qaa1 falls through to bare default", func(t *testing.T) {
		// Regression guard for the open-set contract: names that fail the
		// numbered-family regex must still receive the bare default RoleDef
		// rather than being silently treated as qa-family.
		def := LookupRole("qaa1")
		if def.Name != "qaa1" {
			t.Errorf("Name = %q, want %q", def.Name, "qaa1")
		}
		if def.NeedsSrc {
			t.Error("qaa1 must NOT have NeedsSrc=true (typo, not a real qa)")
		}
		if def.NeedsPlaybooks {
			t.Error("qaa1 must NOT have NeedsPlaybooks=true (typo, not a real qa)")
		}
	})
}

// TestRoleFamilyOfWithRoster covers the ini-98n tier extension: roster-aware
// classification for custom roles defined in initech.yaml. The roster is the
// third-tier source of truth, behind prefix matches (tier 2/3) and catalog
// exact matches (tier 4), so the function's existing semantics are preserved
// for prefix and catalog roles regardless of roster contents.
func TestRoleFamilyOfWithRoster(t *testing.T) {
	tests := []struct {
		desc   string
		name   string
		roster []string
		want   RoleFamily
	}{
		// ini-98n core: custom role from initech.yaml roster.
		{"practitioner in roster -> Other", "practitioner", []string{"super", "practitioner", "analyst"}, FamilyOther},
		{"analyst in roster -> Other", "analyst", []string{"super", "practitioner", "analyst"}, FamilyOther},

		// Typo protection survives: arbitrary string not in roster -> Unknown.
		{"wronk not in roster -> Unknown", "wronk", []string{"super", "practitioner"}, FamilyUnknown},
		{"random-typo not in roster -> Unknown", "random-typo", []string{"practitioner"}, FamilyUnknown},

		// Empty roster matches today's RoleFamilyOf for every input.
		{"empty roster: eng1 prefix -> Eng", "eng1", nil, FamilyEng},
		{"empty roster: qa1 prefix -> QA", "qa1", nil, FamilyQA},
		{"empty roster: pm catalog -> Other", "pm", nil, FamilyOther},
		{"empty roster: custom unknown -> Unknown", "practitioner", nil, FamilyUnknown},
		{"empty roster: empty agent -> Unknown", "", nil, FamilyUnknown},

		// Prefix wins before roster lookup. A roster entry that LOOKS like an
		// eng/qa name must not reclassify it from Eng/QA to Other.
		{"prefix wins over roster: engineer (eng prefix) stays Eng even if in roster",
			"engineer", []string{"engineer"}, FamilyEng},
		{"prefix wins over roster: qaThing (qa prefix) stays QA even if in roster",
			"qaThing", []string{"qaThing"}, FamilyQA},

		// Catalog wins before roster lookup. A roster that includes a catalog
		// name still gets the catalog classification (which is FamilyOther
		// today, but the SOURCE of the classification matters — preserves the
		// invariant that catalog roles never depend on roster contents).
		{"catalog wins over roster: shipper stays Other (catalog, not roster)",
			"shipper", []string{"shipper"}, FamilyOther},
		{"catalog wins over roster: pm stays Other (catalog, not roster)",
			"pm", []string{"pm", "practitioner"}, FamilyOther},

		// Empty name short-circuits regardless of roster.
		{"empty name with non-empty roster still Unknown", "", []string{"practitioner"}, FamilyUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got := RoleFamilyOfWithRoster(tt.name, tt.roster)
			if got != tt.want {
				t.Errorf("RoleFamilyOfWithRoster(%q, %v) = %q, want %q", tt.name, tt.roster, got, tt.want)
			}
		})
	}
}

func TestRoleFamilyOf(t *testing.T) {
	tests := []struct {
		name string
		want RoleFamily
	}{
		{"", FamilyUnknown},
		{"eng1", FamilyEng},
		{"eng2", FamilyEng},
		{"eng3", FamilyEng},
		{"engineer", FamilyEng},
		{"qa1", FamilyQA},
		{"qa2", FamilyQA},
		{"qa3", FamilyQA},
		{"qaWhatever", FamilyQA},
		{"super", FamilyOther},
		{"shipper", FamilyOther},
		{"pm", FamilyOther},
		{"pmm", FamilyOther},
		{"growth", FamilyOther},
		{"writer", FamilyOther},
		{"ops", FamilyOther},
		{"arch", FamilyOther},
		{"sec", FamilyOther},
		{"intern", FamilyOther},
		{"designer", FamilyUnknown},
		{"random-custom-role", FamilyUnknown},
		{"dba", FamilyUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RoleFamilyOf(tt.name)
			if got != tt.want {
				t.Errorf("RoleFamilyOf(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestResolveClaudeArgs(t *testing.T) {
	tests := []struct {
		name       string
		role       string
		globalArgs []string
		roleArgs   []string
		want       []string
	}{
		{
			name: "autonomous role with no config uses skip-permissions",
			role: "eng1",
			want: []string{"--dangerously-skip-permissions"},
		},
		{
			name: "shipper (autonomous) gets skip-permissions",
			role: "shipper",
			want: []string{"--dangerously-skip-permissions"},
		},
		{
			name:       "global args override catalog default",
			role:       "eng1",
			globalArgs: []string{"--model", "opus"},
			want:       []string{"--model", "opus"},
		},
		{
			name:       "global args apply to all roles",
			role:       "shipper",
			globalArgs: []string{"--verbose"},
			want:       []string{"--verbose"},
		},
		{
			name:       "per-role args override global args",
			role:       "eng1",
			globalArgs: []string{"--model", "opus"},
			roleArgs:   []string{"--model", "sonnet", "--dangerously-skip-permissions"},
			want:       []string{"--model", "sonnet", "--dangerously-skip-permissions"},
		},
		{
			name:     "per-role empty slice overrides catalog default",
			role:     "eng1",
			roleArgs: []string{},
			want:     []string{},
		},
		{
			name: "unknown role defaults to autonomous",
			role: "custom-role",
			want: []string{"--dangerously-skip-permissions"},
		},
		{
			name:       "per-role nil falls through to global",
			role:       "eng1",
			globalArgs: []string{"--continue"},
			roleArgs:   nil,
			want:       []string{"--continue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveClaudeArgs(tt.role, tt.globalArgs, tt.roleArgs)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("arg[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
