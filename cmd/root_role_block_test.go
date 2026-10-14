package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/scaffold"
)

// ini-vby5.3: the root names sec and arch only when the fleet has them, so
// hiring or firing one re-renders those lines (pm ruling).

const secRuling = "one joint ruling from pm and sec"
const noSecRuling = "pm rules on the approach alone"

// writeRootFor writes the root CLAUDE.md a project of roleNames gets at init.
func writeRootFor(t *testing.T, root string, roleNames []string) string {
	t.Helper()
	path := filepath.Join(root, "CLAUDE.md")
	body := scaffold.RenderRootCLAUDE(&config.Project{Name: "test", Root: root, Roles: roleNames})
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunAddAgent_HiringSecRerendersRootSecLines(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	path := writeRootFor(t, root, []string{"pm"})
	chdirTemp(t, root)
	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })
	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })

	if err := runAddAgent(addAgentCmd, []string{"sec"}); err != nil {
		t.Fatalf("hire sec: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), secRuling) || strings.Contains(string(got), noSecRuling) {
		t.Errorf("root after hiring sec does not carry the sec ruling line")
	}
	if !strings.Contains(buf.String(), "roles block") {
		t.Errorf("hire output does not say the root was updated: %q", buf.String())
	}
}

func TestRunDeleteAgent_FiringSecRerendersRootSecLines(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "sec"})
	path := writeRootFor(t, root, []string{"pm", "sec"})
	chdirTemp(t, root)
	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"sec"}); err != nil {
		t.Fatalf("fire sec: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), secRuling) || !strings.Contains(string(got), noSecRuling) {
		t.Errorf("root after firing sec still names sec")
	}
}

func TestRunDeleteAgent_HandWrittenRootIsReportedNotTouched(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "sec"})
	path := filepath.Join(root, "CLAUDE.md")
	const mine = "# ours\n\nwritten by hand\n"
	os.WriteFile(path, []byte(mine), 0644)
	chdirTemp(t, root)
	var buf bytes.Buffer
	deleteAgentCmd.SetOut(&buf)
	defer deleteAgentCmd.SetOut(nil)

	if err := runDeleteAgent(deleteAgentCmd, []string{"sec"}); err != nil {
		t.Fatalf("fire sec: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != mine {
		t.Error("a hand-written root was rewritten")
	}
	if !strings.Contains(buf.String(), "initech init --force rewrites it") {
		t.Errorf("fire output does not report the unrefreshed root: %q", buf.String())
	}
}
