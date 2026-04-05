package config

import (
	"reflect"
	"strings"
	"testing"
)

// TestRegistryCoversAllConfigFields uses reflection to walk every yaml-tagged
// field in the Project struct (and nested types) and verifies that each leaf
// field has a corresponding registry entry. This prevents drift: adding a new
// config field without a registry entry causes this test to fail.
func TestRegistryCoversAllConfigFields(t *testing.T) {
	// Build a set of registry keys for fast lookup.
	registered := make(map[string]bool, len(Registry))
	for _, f := range Registry {
		registered[f.Key] = true
	}

	var missing []string
	collectYAMLPaths(reflect.TypeOf(Project{}), "", &missing, registered)

	if len(missing) > 0 {
		t.Errorf("config fields missing from Registry:\n  %s\n\nAdd a FieldMeta entry for each in registry.go",
			strings.Join(missing, "\n  "))
	}
}

// collectYAMLPaths recursively walks a struct type and collects dot-notation
// yaml paths. For map[string]Struct values, it uses the template placeholder
// from the registry (e.g., "<name>"). For slice-of-struct values, it uses "[]".
func collectYAMLPaths(t reflect.Type, prefix string, missing *[]string, registered map[string]bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("yaml")
		if tag == "" || tag == "-" {
			continue
		}
		// Strip ",omitempty" and other options.
		yamlKey := strings.Split(tag, ",")[0]
		if yamlKey == "" {
			continue
		}

		fullKey := yamlKey
		if prefix != "" {
			fullKey = prefix + "." + yamlKey
		}

		ft := field.Type
		// Unwrap pointer types.
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}

		switch ft.Kind() {
		case reflect.Struct:
			// Recurse into nested structs.
			collectYAMLPaths(ft, fullKey, missing, registered)
		case reflect.Map:
			// Map values: use template placeholder. Determine the placeholder
			// by finding a registry key that starts with fullKey + ".".
			placeholder := findTemplatePlaceholder(fullKey, registered)
			if placeholder == "" {
				// No template entries found at all; report the map key itself.
				*missing = append(*missing, fullKey+".<???>.*")
				continue
			}
			elemType := ft.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
			if elemType.Kind() == reflect.Struct {
				collectYAMLPaths(elemType, fullKey+"."+placeholder, missing, registered)
			}
		case reflect.Slice:
			elemType := ft.Elem()
			if elemType.Kind() == reflect.Ptr {
				elemType = elemType.Elem()
			}
			if elemType.Kind() == reflect.Struct {
				// Slice of structs: use "[]" bracket notation.
				collectYAMLPaths(elemType, fullKey+"[]", missing, registered)
			} else {
				// Slice of scalars (e.g., []string): treat as leaf.
				if !registered[fullKey] {
					*missing = append(*missing, fullKey)
				}
			}
		default:
			// Scalar leaf field.
			if !registered[fullKey] {
				*missing = append(*missing, fullKey)
			}
		}
	}
}

// findTemplatePlaceholder looks for registry keys that start with prefix+"."
// and extracts the template placeholder segment (e.g., "<name>").
func findTemplatePlaceholder(prefix string, registered map[string]bool) string {
	search := prefix + "."
	for key := range registered {
		if strings.HasPrefix(key, search) {
			rest := key[len(search):]
			seg := strings.Split(rest, ".")[0]
			if len(seg) > 2 && seg[0] == '<' && seg[len(seg)-1] == '>' {
				return seg
			}
		}
	}
	return ""
}

func TestLookupField_ExactMatch(t *testing.T) {
	f, ok := LookupField("mcp_port")
	if !ok {
		t.Fatal("expected to find mcp_port")
	}
	if f.Type != "int" {
		t.Errorf("type = %q, want %q", f.Type, "int")
	}
	if f.Default != "9200" {
		t.Errorf("default = %q, want %q", f.Default, "9200")
	}
}

func TestLookupField_NestedKey(t *testing.T) {
	f, ok := LookupField("beads.prefix")
	if !ok {
		t.Fatal("expected to find beads.prefix")
	}
	if f.Type != "string" {
		t.Errorf("type = %q, want %q", f.Type, "string")
	}
}

func TestLookupField_TemplateMatch(t *testing.T) {
	f, ok := LookupField("remotes.workbench.addr")
	if !ok {
		t.Fatal("expected template match for remotes.workbench.addr")
	}
	if f.Key != "remotes.<name>.addr" {
		t.Errorf("key = %q, want %q", f.Key, "remotes.<name>.addr")
	}
}

func TestLookupField_RoleOverrideTemplate(t *testing.T) {
	f, ok := LookupField("role_overrides.eng1.agent_type")
	if !ok {
		t.Fatal("expected template match for role_overrides.eng1.agent_type")
	}
	if f.Key != "role_overrides.<role>.agent_type" {
		t.Errorf("key = %q, want %q", f.Key, "role_overrides.<role>.agent_type")
	}
}

func TestLookupField_NotFound(t *testing.T) {
	_, ok := LookupField("nonexistent.field")
	if ok {
		t.Error("expected not found for nonexistent.field")
	}
}

func TestIsSecret(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"mcp_token", true},
		{"token", true},
		{"slack.app_token", true},
		{"slack.bot_token", true},
		{"remotes.workbench.token", true},
		{"mcp_port", false},
		{"project", false},
		{"nonexistent", false},
	}
	for _, tt := range tests {
		if got := IsSecret(tt.key); got != tt.want {
			t.Errorf("IsSecret(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestAllFields_NonEmpty(t *testing.T) {
	fields := AllFields()
	if len(fields) == 0 {
		t.Fatal("AllFields() returned empty slice")
	}
	// Verify no duplicate keys.
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if seen[f.Key] {
			t.Errorf("duplicate registry key: %q", f.Key)
		}
		seen[f.Key] = true
	}
}

func TestAllFields_AllHaveDescriptions(t *testing.T) {
	for _, f := range AllFields() {
		if f.Description == "" {
			t.Errorf("registry entry %q has empty description", f.Key)
		}
		if f.Type == "" {
			t.Errorf("registry entry %q has empty type", f.Key)
		}
	}
}
