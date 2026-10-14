package scaffold

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
)

// ini-vby5.3: the root and the charters name only the roles the fleet has
// (pm ruling; end pass): sentences that name sec or arch are role blocks in
// the root and role conditions in the charters, and hiring or firing
// re-renders the root's blocks.

func projectWith(roleNames ...string) *config.Project {
	return &config.Project{Name: "acme", Root: "/work/acme", Roles: roleNames}
}

var standardFleet = []string{"super", "pm", "eng1", "eng2", "qa1", "qa2", "shipper"}

// renderFleet is the root and every charter a fleet of roleNames gets.
func renderFleet(roleNames []string) map[string]string {
	p := projectWith(roleNames...)
	out := map[string]string{"root": RenderRootCLAUDE(p)}
	for _, r := range roleNames {
		out[r] = RenderCharter(p, p.Root, r)
	}
	return out
}

func TestRenderedFleet_NamesSecAndArchOnlyWhenTheFleetHasThem(t *testing.T) {
	cases := []struct {
		extra     []string
		sec, arch bool
	}{
		{nil, false, false}, {[]string{"sec"}, true, false}, {[]string{"arch"}, false, true}, {[]string{"sec", "arch"}, true, true},
	}
	for _, c := range cases {
		fleet := append(append([]string{}, standardFleet...), c.extra...)
		named := map[string]bool{}
		for file, text := range renderFleet(fleet) {
			for _, role := range []string{"sec", "arch"} {
				if containsWord(text, role) {
					named[role] = true
					if !hasRoleName(fleet, role) {
						t.Errorf("fleet %v: %s names %s, which the fleet does not have", c.extra, file, role)
					}
				}
			}
			if strings.Contains(text, "{{") {
				t.Errorf("fleet %v: %s has an unrendered marker", c.extra, file)
			}
		}
		// Positive twin: a fleet that has the role is told about it.
		if named["sec"] != c.sec || named["arch"] != c.arch {
			t.Errorf("fleet %v: named sec=%v arch=%v, want sec=%v arch=%v", c.extra, named["sec"], named["arch"], c.sec, c.arch)
		}
	}
}

func TestRenderRootCLAUDE_NoQAFleetIsNotSentToAQAPair(t *testing.T) {
	p := projectWith("super", "pm", "eng1", "eng2")
	noQA := RenderRootCLAUDE(p) + RenderCharter(p, p.Root, "super")
	for _, assumes := range []string{"your QA pair", "QA's verdict", "Only QA moves", "QA are done", "QA has passed it", "QA is not yours to route", "hands ready work to the QA"} {
		if strings.Contains(noQA, assumes) {
			t.Errorf("a fleet with no QA role is told %q", assumes)
		}
	}
	std := projectWith(standardFleet...)
	if !strings.Contains(RenderCharter(std, std.Root, "super"), "QA is not yours to route") {
		t.Error("super in a fleet with QA lost its QA-routing line")
	}
	if !strings.Contains(RenderRootCLAUDE(projectWith(standardFleet...)), "send it to your QA pair") {
		t.Error("a fleet with QA is not told to send work to its QA pair")
	}
}

func hasRoleName(roleNames []string, role string) bool {
	for _, r := range roleNames {
		if r == role {
			return true
		}
	}
	return false
}

func TestApplyRoleConditions_KeepsOnlyTheMatchingSpans(t *testing.T) {
	in := "a{{if sec}} sec{{end}}{{if !sec}} nosec{{end}}\n{{if arch}}line\n{{end}}z"
	if got := roles.ApplyRoleConditions(in, []string{"sec"}); got != "a sec\nz" {
		t.Errorf("with sec: %q", got)
	}
	if got := roles.ApplyRoleConditions(in, []string{"arch"}); got != "a nosec\nline\nz" {
		t.Errorf("with arch: %q", got)
	}
	if got := roles.ApplyRoleConditions(in, nil); got != "a nosec\nz" {
		t.Errorf("fed nothing: %q", got)
	}
	// "qa" is the QA family: qa1 counts, an exact "qa" role is not needed.
	if got := roles.ApplyRoleConditions("{{if qa}}Q{{end}}{{if !qa}}N{{end}}", []string{"qa2"}); got != "Q" {
		t.Errorf("qa family: %q", got)
	}
}

// writeRoot writes the root CLAUDE.md a project of roleNames would get.
func writeRoot(t *testing.T, roleNames ...string) (*config.Project, string) {
	t.Helper()
	p := projectWith(roleNames...)
	p.Root = t.TempDir()
	path := filepath.Join(p.Root, "CLAUDE.md")
	if err := os.WriteFile(path, []byte(RenderRootCLAUDE(p)), 0644); err != nil {
		t.Fatal(err)
	}
	return p, path
}

// outsideBlocks is root with every role block's contents removed, so two
// roots can be compared on what the project owns.
func outsideBlocks(t *testing.T, root string) string {
	t.Helper()
	for _, name := range roles.RoleBlockNames {
		i, j := strings.Index(root, roles.RoleBlockBegin(name)), strings.Index(root, roles.RoleBlockEnd(name))
		if i < 0 || j < i {
			t.Fatalf("root has no %s block", name)
		}
		root = root[:i] + root[j:]
	}
	return root
}

func TestRefreshRootRoleBlocks_HiringRewritesTheBlocksAndKeepsProjectEdits(t *testing.T) {
	p, path := writeRoot(t, standardFleet...)
	edited := readFile(t, path)
	edited = strings.Replace(edited, "(none yet)", "billing exports", 1)
	edited = strings.Replace(edited, "Time box each step to 10-15 minutes", "Time box each step to 20 minutes", 1)
	edited += "\n## Our own notes\n\nkept\n"
	os.WriteFile(path, []byte(edited), 0644)

	p.Roles = append(append([]string{}, standardFleet...), "sec", "arch")
	changed, err := RefreshRootRoleBlocks(p)
	if err != nil || !changed {
		t.Fatalf("RefreshRootRoleBlocks = %v, %v; want changed", changed, err)
	}
	got := readFile(t, path)
	if outsideBlocks(t, got) != outsideBlocks(t, edited) {
		t.Error("text outside the blocks changed; a project's own edits must survive a hire")
	}
	if !strings.Contains(got, "- `sec/` - ") {
		t.Error("the Roles list does not name the hired sec")
	}
	want := RenderRootCLAUDE(p)
	for _, name := range roles.RoleBlockNames {
		if !strings.Contains(got, roles.RenderRoleBlock(name, p.Roles)) || !strings.Contains(want, roles.RenderRoleBlock(name, p.Roles)) {
			t.Errorf("block %s does not match a fleet with sec and arch", name)
		}
	}
}

func TestRefreshRootRoleBlocks_FiringRewritesTheBlocksBack(t *testing.T) {
	p, path := writeRoot(t, append(append([]string{}, standardFleet...), "sec", "arch")...)
	p.Roles = standardFleet
	changed, err := RefreshRootRoleBlocks(p)
	if err != nil || !changed {
		t.Fatalf("RefreshRootRoleBlocks = %v, %v; want changed", changed, err)
	}
	got := readFile(t, path)
	for _, name := range roles.RoleBlockNames {
		if !strings.Contains(got, roles.RenderRoleBlock(name, standardFleet)) {
			t.Errorf("block %s does not match a fleet without sec and arch", name)
		}
	}
}

func TestRefreshRootRoleBlocks_UnchangedRolesLeaveFileAlone(t *testing.T) {
	p, path := writeRoot(t, standardFleet...)
	before := readFile(t, path)
	changed, err := RefreshRootRoleBlocks(p)
	if err != nil || changed {
		t.Fatalf("RefreshRootRoleBlocks = %v, %v; want unchanged", changed, err)
	}
	if readFile(t, path) != before {
		t.Error("file rewritten although nothing changed")
	}
}

func TestRefreshRootRoleBlocks_FedNothingTouchesNothing(t *testing.T) {
	p := projectWith(standardFleet...)
	p.Root = t.TempDir()
	if changed, err := RefreshRootRoleBlocks(p); err != nil || changed {
		t.Fatalf("missing root: got %v, %v; want false, nil", changed, err)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("refresh created a root CLAUDE.md")
	}
	b, e := roles.RoleBlockBegin("bigger"), roles.RoleBlockEnd("bigger")
	for name, body := range map[string]string{
		"empty":      "",
		"no markers": "# mine\n\nhand written\n",
		"begin only": "# mine\n" + b + "\nx\n",
		"end only":   "# mine\n" + e + "\n",
		"end first":  e + "\nx\n" + b + "\n",
		"two begins": b + "\n" + b + "\n" + e + "\n",
	} {
		path := filepath.Join(p.Root, "CLAUDE.md")
		os.WriteFile(path, []byte(body), 0644)
		changed, err := RefreshRootRoleBlocks(p)
		if changed || !errors.Is(err, ErrNoRootRoleBlock) {
			t.Errorf("%s: got %v, %v; want false, ErrNoRootRoleBlock", name, changed, err)
		}
		if readFile(t, path) != body {
			t.Errorf("%s: file changed", name)
		}
	}
}

func TestRefreshRootRoleBlocks_OneBlockMissingStillRefreshesTheOthers(t *testing.T) {
	p, path := writeRoot(t, standardFleet...)
	root := readFile(t, path)
	i, j := strings.Index(root, roles.RoleBlockBegin("docs-owners")), strings.Index(root, roles.RoleBlockEnd("docs-owners"))
	root = root[:i] + root[j+len(roles.RoleBlockEnd("docs-owners")):]
	os.WriteFile(path, []byte(root), 0644)
	p.Roles = append(append([]string{}, standardFleet...), "arch")
	changed, err := RefreshRootRoleBlocks(p)
	if !changed || !errors.Is(err, ErrNoRootRoleBlock) {
		t.Fatalf("got %v, %v; want changed and ErrNoRootRoleBlock", changed, err)
	}
	if !strings.Contains(readFile(t, path), roles.RenderRoleBlock("bigger", p.Roles)) {
		t.Error("the well-formed bigger block was not refreshed")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// containsWord reports whether w appears as a whole word (so "sec" does not
// match "second" or "secret").
func containsWord(s, w string) bool {
	for i := 0; ; {
		k := strings.Index(s[i:], w)
		if k < 0 {
			return false
		}
		k += i
		before := k == 0 || !isWordByte(s[k-1])
		end := k + len(w)
		after := end == len(s) || !isWordByte(s[end])
		if before && after {
			return true
		}
		i = k + 1
	}
}

func isWordByte(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
