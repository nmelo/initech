package roles

import (
	"fmt"
	"regexp"
	"strings"
)

var varPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// RenderVars holds the substitution values for template rendering.
// Any field left empty is skipped, leaving the {{variable}} placeholder intact.
type RenderVars struct {
	ProjectName string
	ProjectRoot string
	TechStack   string
	BuildCmd    string
	TestCmd     string
}

// Render substitutes {{variable}} placeholders in a template string.
// Known variables with non-empty values are replaced. Unknown variables and
// empty values are left as-is. No runtime errors are possible.
//
// This is intentionally simple: regex replacement, no conditionals, no loops.
// If a template needs branching, the template is doing too much.
func Render(tmpl string, vars RenderVars) string {
	lookup := map[string]string{
		"project_name": vars.ProjectName,
		"project_root": vars.ProjectRoot,
		"tech_stack":   vars.TechStack,
		"build_cmd":    vars.BuildCmd,
		"test_cmd":     vars.TestCmd,
	}

	return varPattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := varPattern.FindStringSubmatch(match)[1]
		if val, ok := lookup[key]; ok && val != "" {
			return val
		}
		return match
	})
}

// RenderString is a convenience for rendering a single variable by name.
// Useful when a template uses custom variables not in RenderVars.
func RenderString(tmpl string, key, value string) string {
	return strings.ReplaceAll(tmpl, "{{"+key+"}}", value)
}

// UnrenderedPlaceholders returns one short excerpt per remaining "{{" site in
// text, as "line N: snippet". Empty result means the text is safe to write.
//
// The scan is for the LITERAL "{{", deliberately not for varPattern. Render is
// lenient by contract — an empty value leaves its placeholder intact — so the
// thing that reaches disk is precisely the placeholder the renderer did NOT
// substitute, and a malformed one ("{{ tech_stack }}", "{{build-cmd}}") is
// exactly as broken in an agent's instructions as a well-formed one while
// matching no variable pattern at all. A guard that sees only what the renderer
// sees cannot catch what the renderer missed (ini-rg12).
func UnrenderedPlaceholders(text string) []string {
	var found []string
	for i, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "{{") {
			continue
		}
		snippet := strings.TrimSpace(line)
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		found = append(found, fmt.Sprintf("line %d: %s", i+1, snippet))
	}
	return found
}
