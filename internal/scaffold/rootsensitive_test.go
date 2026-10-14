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

// ini-vby5.3: one question decides the path of every change. The root names
// only the roles the fleet has (pm ruling): sec and arch lines change with the
// roles list, and hiring or firing re-renders them.

func projectWith(roleNames ...string) *config.Project {
	return &config.Project{Name: "acme", Root: "/work/acme", Roles: roleNames}
}

var standardFleet = []string{"super", "pm", "eng1", "eng2", "qa1", "qa2", "shipper"}

func TestRenderRootCLAUDE_NamesOnlyTheRolesTheFleetHas(t *testing.T) {
	cases := []struct {
		name    string
		extra   []string
		want    []string
		mustNot []string
	}{
		{"no sec no arch", nil,
			[]string{"pm rules on the approach alone", "QA gets the branch head", "goes to super, who rules or asks the operator"},
			[]string{"sec", "arch"}},
		{"sec", []string{"sec"},
			[]string{"one joint ruling from pm and sec", "sec's conditions quoted verbatim", "sec and QA get the same SHA at once", "goes back to sec and QA"},
			[]string{"pm rules on the approach alone", "arch"}},
		{"arch", []string{"arch"},
			[]string{"goes to arch through super", "ask arch for one structural review"},
			[]string{"sec", "goes to super, who rules"}},
		{"sec and arch", []string{"sec", "arch"},
			[]string{"one joint ruling from pm and sec", "goes to arch through super"},
			[]string{"pm rules on the approach alone", "goes to super, who rules"}},
	}
	for _, c := range cases {
		root := RenderRootCLAUDE(projectWith(append(append([]string{}, standardFleet...), c.extra...)...))
		block := roleBlockOf(t, root)
		for _, w := range c.want {
			if !strings.Contains(block, w) {
				t.Errorf("%s: block missing %q", c.name, w)
			}
		}
		for _, n := range c.mustNot {
			if containsWord(block, n) {
				t.Errorf("%s: block mentions %q, which this fleet does not have", c.name, n)
			}
		}
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

func TestRefreshRootRoleBlock_HiringSecRewritesTheBlock(t *testing.T) {
	p, path := writeRoot(t, standardFleet...)
	edited := strings.Replace(readFile(t, path), "This project also treats ", "This project also treats billing exports and ", 1)
	edited += "\n## Our own notes\n\nkept\n"
	os.WriteFile(path, []byte(edited), 0644)

	p.Roles = append(p.Roles, "sec")
	changed, err := RefreshRootRoleBlock(p)
	if err != nil || !changed {
		t.Fatalf("RefreshRootRoleBlock = %v, %v; want changed", changed, err)
	}
	got := readFile(t, path)
	if !strings.Contains(roleBlockOf(t, got), "one joint ruling from pm and sec") {
		t.Error("hiring sec did not put sec into the rendered block")
	}
	if strings.Contains(got, "pm rules on the approach alone") {
		t.Error("the no-sec line survived hiring sec")
	}
	before, after := splitAroundBlock(t, edited)
	gotBefore, gotAfter := splitAroundBlock(t, got)
	if before != gotBefore || after != gotAfter {
		t.Error("text outside the block changed; a project's own edits must survive")
	}
}

func TestRefreshRootRoleBlock_FiringSecRewritesTheBlockBack(t *testing.T) {
	p, path := writeRoot(t, append(append([]string{}, standardFleet...), "sec", "arch")...)
	p.Roles = standardFleet
	changed, err := RefreshRootRoleBlock(p)
	if err != nil || !changed {
		t.Fatalf("RefreshRootRoleBlock = %v, %v; want changed", changed, err)
	}
	// Only the block is re-rendered; the Roles list above it is outside.
	if got := readFile(t, path); roleBlockOf(t, got) != roleBlockOf(t, RenderRootCLAUDE(p)) {
		t.Error("firing sec and arch did not give the block a fleet without them would get")
	}
}

func TestRefreshRootRoleBlock_UnchangedRolesLeaveFileAlone(t *testing.T) {
	p, path := writeRoot(t, standardFleet...)
	before := readFile(t, path)
	changed, err := RefreshRootRoleBlock(p)
	if err != nil || changed {
		t.Fatalf("RefreshRootRoleBlock = %v, %v; want unchanged", changed, err)
	}
	if readFile(t, path) != before {
		t.Error("file rewritten although nothing changed")
	}
}

func TestRefreshRootRoleBlock_FedNothingTouchesNothing(t *testing.T) {
	// No root file: nothing to refresh, nothing created.
	p := projectWith(standardFleet...)
	p.Root = t.TempDir()
	if changed, err := RefreshRootRoleBlock(p); err != nil || changed {
		t.Fatalf("missing root: got %v, %v; want false, nil", changed, err)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "CLAUDE.md")); !os.IsNotExist(err) {
		t.Fatal("refresh created a root CLAUDE.md")
	}
	// A hand-written root with no markers, one marker, or markers out of
	// order: refused with ErrNoRootRoleBlock, file byte-identical.
	for name, body := range map[string]string{
		"empty":      "",
		"no markers": "# mine\n\nhand written\n",
		"begin only": "# mine\n" + roles.RootRoleBlockBegin + "\nx\n",
		"end only":   "# mine\n" + roles.RootRoleBlockEnd + "\n",
		"end first":  roles.RootRoleBlockEnd + "\nx\n" + roles.RootRoleBlockBegin + "\n",
		"two begins": roles.RootRoleBlockBegin + "\n" + roles.RootRoleBlockBegin + "\n" + roles.RootRoleBlockEnd + "\n",
	} {
		path := filepath.Join(p.Root, "CLAUDE.md")
		os.WriteFile(path, []byte(body), 0644)
		changed, err := RefreshRootRoleBlock(p)
		if changed || !errors.Is(err, ErrNoRootRoleBlock) {
			t.Errorf("%s: got %v, %v; want false, ErrNoRootRoleBlock", name, changed, err)
		}
		if readFile(t, path) != body {
			t.Errorf("%s: file changed", name)
		}
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

func roleBlockOf(t *testing.T, root string) string {
	t.Helper()
	i, j := strings.Index(root, roles.RootRoleBlockBegin), strings.Index(root, roles.RootRoleBlockEnd)
	if i < 0 || j < i {
		t.Fatalf("root has no role block")
	}
	return root[i:j]
}

func splitAroundBlock(t *testing.T, root string) (string, string) {
	t.Helper()
	i, j := strings.Index(root, roles.RootRoleBlockBegin), strings.Index(root, roles.RootRoleBlockEnd)
	if i < 0 || j < i {
		t.Fatalf("root has no role block")
	}
	return root[:i], root[j:]
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
