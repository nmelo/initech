package roles

import (
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
