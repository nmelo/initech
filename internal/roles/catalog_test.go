package roles

import "testing"

func TestCatalogCompleteness(t *testing.T) {
	expected := []string{
		"super", "eng1", "eng2", "eng3", "qa1", "qa2", "shipper",
		"pm", "pmm", "arch", "sec", "writer", "ops", "growth",
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

	autonomous := []string{"super", "eng1", "eng2", "eng3", "qa1", "qa2", "shipper", "pm", "pmm", "arch", "sec", "writer", "ops", "growth"}
	for _, name := range autonomous {
		def := Catalog[name]
		if def.Permission != Autonomous {
			t.Errorf("%s should be Autonomous, got %d", name, def.Permission)
		}
	}
}

func TestCatalogNeedsSrc(t *testing.T) {
	needsSrc := []string{"eng1", "eng2", "eng3", "qa1", "qa2", "shipper", "growth"}
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
