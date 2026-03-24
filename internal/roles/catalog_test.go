package roles

import "testing"

func TestCatalogCompleteness(t *testing.T) {
	expected := []string{
		"super", "eng1", "eng2", "qa1", "qa2", "shipper",
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
	supervised := []string{"super", "shipper"}
	for _, name := range supervised {
		def := Catalog[name]
		if def.Permission != Supervised {
			t.Errorf("%s should be Supervised, got %d", name, def.Permission)
		}
	}

	autonomous := []string{"eng1", "eng2", "qa1", "qa2", "pm", "pmm", "arch", "sec", "writer", "ops", "growth"}
	for _, name := range autonomous {
		def := Catalog[name]
		if def.Permission != Autonomous {
			t.Errorf("%s should be Autonomous, got %d", name, def.Permission)
		}
	}
}

func TestCatalogNeedsSrc(t *testing.T) {
	needsSrc := []string{"eng1", "eng2", "qa1", "qa2", "shipper", "growth"}
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

func TestCatalogNeedsMakefile(t *testing.T) {
	needsMakefile := []string{"eng1", "eng2"}
	for _, name := range needsMakefile {
		def := Catalog[name]
		if !def.NeedsMakefile {
			t.Errorf("%s should have NeedsMakefile=true", name)
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
	if def.Permission != Supervised {
		t.Errorf("super should be Supervised")
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
	if def.NeedsMakefile {
		t.Error("unknown role should not need makefile")
	}
}
