package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// ini-vby5.5: the root carries the evidence standard once (verification
// rules, what a DONE carries, evidence tiers); each charter carries only its
// role's delta of it.

// scaffoldEvidenceProject runs the init path into a temp dir and returns the
// files it wrote, keyed by "root" or role name: the checks read what init
// actually writes, not a renderer called on the side.
func scaffoldEvidenceProject(t *testing.T) map[string]string {
	t.Helper()
	p := &config.Project{Name: "acme", Root: t.TempDir(), Roles: []string{"super", "eng", "qa", "pm", "shipper"}}
	if _, err := Run(p, Options{}); err != nil {
		t.Fatalf("scaffold.Run: %v", err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(p.Root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	files := map[string]string{"root": read("CLAUDE.md")}
	for _, r := range p.Roles {
		files[r] = read(filepath.Join(r, "CLAUDE.md"))
	}
	return files
}

func TestRenderRootCLAUDE_CarriesTheEvidenceSectionsOnceInOrder(t *testing.T) {
	root := scaffoldEvidenceProject(t)["root"]
	last := strings.Index(root, "## Docs gate\n")
	for _, h := range []string{"## Evidence", "### Verification rules", "### Delivering", "### Evidence tiers", "## QA pairs"} {
		if n := strings.Count(root, h+"\n"); n != 1 {
			t.Fatalf("root carries %q %d times, want exactly once", h, n)
		}
		at := strings.Index(root, h+"\n")
		if at < last {
			t.Fatalf("%q is out of order in the root (Evidence sits between Docs gate and QA pairs)", h)
		}
		last = at
	}
	for _, rule := range []string{
		"git show origin/main:<path>",
		"Verify by effect",
		"A negative assertion needs a positive twin",
		"git merge-base --is-ancestor <sha> origin/main",
		"A fix on disk does not reach a running process",
		"audit every place that draws a conclusion from its absence",
		"**Fires-check.**",
		"**Reachability.**",
		"**Skip count.**",
		"**Code review.**",
		"**QA steps.**",
		"evidence-contradicts-claim",
		"**FULL** (default)",
		"**LIGHT**",
		"**NONE**",
		"is mis-tiered",
	} {
		if n := strings.Count(root, rule); n != 1 {
			t.Errorf("root carries %q %d times, want once", rule, n)
		}
	}
	// The placeholder checklist the evidence standard replaces must not survive beside it.
	for _, gone := range []string{"Run the build and confirm it exits 0", "\"Should pass\" and \"passed earlier\" are not evidence"} {
		if strings.Contains(root, gone) {
			t.Errorf("root still carries %q, which the evidence section replaces", gone)
		}
	}
}

func TestRenderCharter_EvidenceDeltasLandInTheirRoles(t *testing.T) {
	files := scaffoldEvidenceProject(t)
	deltas := map[string][]string{
		"eng": {
			"bd comments add <id> --author eng \"DONE:",
			"the five items of Delivering in the root",
			"initech deliver <id> -m \"<headline",
		},
		"qa": {
			"actual output, not adjectives",
			"Verified versus trusted",
			"evidence-contradicts-claim",
			"Pre-Mortem Review risks",
			"LIGHT bead",
		},
		"pm": {"Evidence tier:", "FULL", "mis-tiered"},
		"super": {
			"named to the operator by agent, with a recommendation",
			"Praise work that sets the bar",
		},
		"shipper": {"Never change release state on an inference or a relayed line", "cite the read in the same message"},
	}
	for role, wants := range deltas {
		c := files[role]
		for _, w := range wants {
			if !strings.Contains(c, w) {
				t.Errorf("%s's charter is missing %q", role, w)
			}
		}
	}
	// The old three-part DONE is replaced, not kept beside the new one.
	if c := files["eng"]; strings.Contains(c, "DONE: <what>. Tests: <added>. Commit: <hash>") {
		t.Errorf("eng's charter still teaches the old DONE: what, tests, commit")
	}
}
