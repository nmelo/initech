package roles

import "strings"

// roleSummaries is the one-line "what it owns" per role family, for the
// root CLAUDE.md's Roles list.
var roleSummaries = map[string]string{
	"super":   "Supervisor: dispatches beads, monitors agents, is the operator's one point of contact",
	"pm":      "Product manager: PRDs, specs, and the acceptance criteria on every bead",
	"pmm":     "Product marketing: positioning, messaging, go-to-market",
	"arch":    "Architect: system design, API contracts, cross-package interfaces",
	"sec":     "Security reviewer: vulnerabilities, supply chain, security architecture",
	"shipper": "Release manager: cuts releases, changelogs, publishing",
	"writer":  "Technical writer: docs, READMEs, user guides",
	"ops":     "Operations and UX tester: UX flows, accessibility, operational readiness",
	"growth":  "Growth engineer: analytics, onboarding funnels, conversion",
	"intern":  "Intern: research and small, well-scoped tasks",
	"eng":     "Engineer: implements beads with tests",
	"qa":      "QA: verifies delivered work against acceptance criteria by running it",
}

// RoleSummary returns the one-line description of what a role owns, used in
// the root CLAUDE.md's Roles list. Numbered variants share their family's
// line (eng2 -> Engineer); a role outside the catalog reads "Custom role".
func RoleSummary(name string) string {
	if s, ok := roleSummaries[name]; ok {
		return s
	}
	for _, fam := range []string{"eng", "qa"} {
		if rest, ok := strings.CutPrefix(name, fam); ok && rest != "" && strings.Trim(rest, "0123456789") == "" {
			return roleSummaries[fam]
		}
	}
	return "Custom role"
}
