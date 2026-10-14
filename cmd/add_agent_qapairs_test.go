package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/roles"
)

// addAgentWithRoot writes a project with roster and a root CLAUDE.md holding
// rootDoc, runs add-agent for role, and returns the new root and the output.
func addAgentWithRoot(t *testing.T, roster []string, rootDoc, role string) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeTestConfig(t, root, roster)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte(rootDoc), 0644); err != nil {
		t.Fatal(err)
	}
	chdirTemp(t, root)
	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })
	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{role}); err != nil {
		t.Fatalf("add-agent %s: %v", role, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(got), buf.String()
}

func TestRunAddAgent_EngRerendersQAPairsTable(t *testing.T) {
	roster := []string{"super", "eng1", "qa1"}
	doc := "# test\n\n" + roles.RenderQAPairsBlock(roster)
	got, out := addAgentWithRoot(t, roster, doc, "eng2")
	if !strings.Contains(got, "| eng1, eng2 | qa1 |") {
		t.Errorf("table not re-rendered with eng2:\n%s", got)
	}
	if !strings.Contains(out, "Re-rendered the QA pairs table") {
		t.Errorf("add-agent did not say it re-rendered the table: %q", out)
	}
}

func TestRunAddAgent_QAFillsNoQATable(t *testing.T) {
	roster := []string{"super", "eng1", "eng2"}
	doc := "# test\n\n" + roles.RenderQAPairsBlock(roster)
	got, _ := addAgentWithRoot(t, roster, doc, "qa1")
	if !strings.Contains(got, "| eng1, eng2 | qa1 |") || strings.Contains(got, "no QA role") {
		t.Errorf("adding the first QA did not replace the no-QA line:\n%s", got)
	}
}

func TestRunAddAgent_HandEditedQAPairsTableLeftAlone(t *testing.T) {
	roster := []string{"super", "eng1", "eng2", "qa1"}
	doc := "# test\n\n" + roles.RenderQAPairsBlock(roster)
	edited := strings.Replace(doc, "| eng1, eng2 | qa1 |", "| eng1 | qa1 |\n| eng2 | super |", 1)
	if edited == doc {
		t.Fatal("fixture edit did not apply")
	}
	got, out := addAgentWithRoot(t, roster, edited, "eng3")
	if got != edited {
		t.Errorf("hand-edited root was rewritten:\n%s", got)
	}
	if !strings.Contains(out, "edited by hand") || !strings.Contains(out, "eng3") {
		t.Errorf("add-agent did not say the table was left alone for eng3: %q", out)
	}
}

// A hand-edited table is the fixture because an unedited one re-renders to the
// same bytes for a non-eng/qa role, which would hide a missing family gate.
func TestRunAddAgent_OtherRoleLeavesQAPairsTable(t *testing.T) {
	roster := []string{"super", "eng1", "qa1"}
	doc := strings.Replace("# test\n\n"+roles.RenderQAPairsBlock(roster), "| eng1 | qa1 |", "| eng1 | super |", 1)
	if !strings.Contains(doc, "| eng1 | super |") {
		t.Fatal("fixture edit did not apply")
	}
	got, out := addAgentWithRoot(t, roster, doc, "arch")
	if got != doc {
		t.Errorf("adding arch changed the root:\n%s", got)
	}
	if strings.Contains(out, "QA pairs") {
		t.Errorf("adding arch mentioned the QA pairs table: %q", out)
	}
}
