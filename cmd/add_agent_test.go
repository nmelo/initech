package cmd

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

func TestRunAddAgent_UnknownRole(t *testing.T) {
	err := runAddAgent(addAgentCmd, []string{"unknownrole"})
	if err == nil {
		t.Fatal("expected error for unknown role")
	}
	if !strings.Contains(err.Error(), "unknown agent") {
		t.Errorf("error = %q, want to contain 'unknown agent'", err.Error())
	}
}

func TestRunAddAgent_AlreadyExists(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "arch"})

	chdirTemp(t, root)

	err := runAddAgent(addAgentCmd, []string{"arch"})
	if err == nil {
		t.Fatal("expected error for duplicate role")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want to contain 'already exists'", err.Error())
	}
}

func TestRunAddAgent_NoConfig(t *testing.T) {
	root := t.TempDir()
	chdirTemp(t, root)

	err := runAddAgent(addAgentCmd, []string{"arch"})
	if err == nil {
		t.Fatal("expected error when no initech.yaml")
	}
	if !strings.Contains(err.Error(), "initech.yaml") {
		t.Errorf("error = %q, want to mention initech.yaml", err.Error())
	}
}

func TestRunAddAgent_Success(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	if err := os.MkdirAll(filepath.Join(root, "pm"), 0755); err != nil {
		t.Fatal(err)
	}

	chdirTemp(t, root)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "arch")); err != nil {
		t.Error("arch/ directory not created")
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range updated.Roles {
		if r == "arch" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("arch not added to config roles: %v", updated.Roles)
	}

	if !strings.Contains(buf.String(), "arch") {
		t.Errorf("output missing role name: %q", buf.String())
	}
}

func TestRunAddAgent_SuccessOutput(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	if err := os.MkdirAll(filepath.Join(root, "pm"), 0755); err != nil {
		t.Fatal(err)
	}

	chdirTemp(t, root)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{"sec"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "initech.yaml") {
		t.Errorf("output missing initech.yaml mention: %q", out)
	}
	if !strings.Contains(out, "Restart") {
		t.Errorf("output missing restart instruction: %q", out)
	}
}

func TestRunAddAgentList_ShowsAllRoles(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "super"})

	chdirTemp(t, root)

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	addAgentList = true
	t.Cleanup(func() { addAgentList = false })

	if err := runAddAgent(addAgentCmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	for _, name := range []string{"super", "pm", "arch", "eng1", "qa1", "shipper"} {
		if !strings.Contains(out, name) {
			t.Errorf("output missing role %q", name)
		}
	}
	if !strings.Contains(out, "✓") {
		t.Error("output missing installed checkmark")
	}
	if !strings.Contains(out, "-") {
		t.Error("output missing uninstalled marker")
	}
}

func TestCompleteAddAgent_ExcludesInstalled(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm", "super", "eng1"})

	chdirTemp(t, root)

	completions, directive := completeAddAgent(addAgentCmd, nil, "")

	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want ShellCompDirectiveNoFileComp", directive)
	}

	names := make(map[string]bool, len(completions))
	for _, c := range completions {
		name := strings.SplitN(c, "\t", 2)[0]
		names[name] = true
	}

	for _, installed := range []string{"pm", "super", "eng1"} {
		if names[installed] {
			t.Errorf("installed role %q should not appear in completions", installed)
		}
	}
	if !names["arch"] {
		t.Error("uninstalled role 'arch' should appear in completions")
	}
}

func TestCompleteAddAgent_NoArgAfterFirst(t *testing.T) {
	completions, _ := completeAddAgent(addAgentCmd, []string{"arch"}, "")
	if completions != nil {
		t.Errorf("expected nil completions after first arg, got %v", completions)
	}
}

func TestRunAddAgent_NeedsSrcCallsSubmodule(t *testing.T) {
	root := t.TempDir()
	p := &config.Project{
		Name:  "test",
		Root:  root,
		Roles: []string{"pm"},
		Beads: config.BeadsConfig{Enabled: boolPtr(false)},
		Repos: []config.Repo{{URL: "git@github.com:example/repo.git", Name: "repo"}},
	}
	if err := config.Write(filepath.Join(root, "initech.yaml"), p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pm"), 0755); err != nil {
		t.Fatal(err)
	}

	chdirTemp(t, root)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })

	var submoduleCalled bool
	origGitSub := gitAddSubmodule
	t.Cleanup(func() { gitAddSubmodule = origGitSub })
	gitAddSubmodule = func(runner iexec.Runner, repoDir, repoURL, subPath string) error {
		submoduleCalled = true
		return nil
	}

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{"eng1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !submoduleCalled {
		t.Error("gitAddSubmodule not called for NeedsSrc role")
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range updated.Roles {
		if r == "eng1" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("eng1 not added to config roles: %v", updated.Roles)
	}
}

func TestRunAddAgent_NoSession_PrintsRestart(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	os.MkdirAll(filepath.Join(root, "pm"), 0755)
	chdirTemp(t, root)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{"arch"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restart") {
		t.Errorf("expected restart message when no session running, got: %q", buf.String())
	}
	if strings.Contains(buf.String(), "activated") {
		t.Errorf("should not say activated when no session running, got: %q", buf.String())
	}
}

func TestTryHotAdd_ReturnsTrue(t *testing.T) {
	skipWindows(t)
	root, err := os.MkdirTemp("", "iha-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	writeTestConfig(t, root, []string{"pm"})
	chdirTemp(t, root)

	sockPath := tui.SocketPath(root, "test")
	os.MkdirAll(filepath.Dir(sockPath), 0700)
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sockPath) })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			scanner := tui.NewIPCScanner(conn)
			if scanner.Scan() {
				resp, _ := json.Marshal(tui.IPCResponse{OK: true})
				conn.Write(resp)
				conn.Write([]byte("\n"))
			}
			conn.Close()
		}
	}()

	var buf bytes.Buffer
	ok := tryHotAdd(&buf, "sec")
	if !ok {
		t.Errorf("tryHotAdd returned false, want true; output: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "hot-added") {
		t.Errorf("expected hot-added message, got: %q", buf.String())
	}
}

func TestTryHotAdd_ReturnsFalse_NoSession(t *testing.T) {
	root := t.TempDir()
	writeTestConfig(t, root, []string{"pm"})
	chdirTemp(t, root)

	var buf bytes.Buffer
	ok := tryHotAdd(&buf, "sec")
	if ok {
		t.Error("tryHotAdd should return false when no session running")
	}
}

func TestAddAgentCmd_HireAlias(t *testing.T) {
	if !contains(addAgentCmd.Aliases, "hire") {
		t.Error("add-agent command should have 'hire' alias")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func writeTestConfig(t *testing.T, root string, roles []string) {
	t.Helper()
	p := &config.Project{
		Name:  "test",
		Root:  root,
		Roles: roles,
		Beads: config.BeadsConfig{Enabled: boolPtr(false)},
	}
	if err := config.Write(filepath.Join(root, "initech.yaml"), p); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func chdirTemp(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

// TestRunAddAgent_NumberedRoleAccepted: ini-j2u opens the CLI gate to
// arbitrary numbered eng/qa roles. This proves runAddAgent("qa10") goes all
// the way through scaffold + config update + NeedsSrc submodule clone, which
// is the load-bearing path the catalog gate previously blocked.
func TestRunAddAgent_NumberedRoleAccepted(t *testing.T) {
	root := t.TempDir()
	p := &config.Project{
		Name:  "test",
		Root:  root,
		Roles: []string{"pm"},
		Beads: config.BeadsConfig{Enabled: boolPtr(false)},
		Repos: []config.Repo{{URL: "git@github.com:example/repo.git", Name: "repo"}},
	}
	if err := config.Write(filepath.Join(root, "initech.yaml"), p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "pm"), 0755); err != nil {
		t.Fatal(err)
	}

	chdirTemp(t, root)

	origRunner := newAddAgentRunner
	newAddAgentRunner = func() iexec.Runner { return &iexec.FakeRunner{} }
	t.Cleanup(func() { newAddAgentRunner = origRunner })

	var submoduleCalled bool
	origGitSub := gitAddSubmodule
	t.Cleanup(func() { gitAddSubmodule = origGitSub })
	gitAddSubmodule = func(runner iexec.Runner, repoDir, repoURL, subPath string) error {
		submoduleCalled = true
		return nil
	}

	var buf bytes.Buffer
	addAgentCmd.SetOut(&buf)
	t.Cleanup(func() { addAgentCmd.SetOut(nil) })
	if err := runAddAgent(addAgentCmd, []string{"qa10"}); err != nil {
		t.Fatalf("unexpected error for qa10: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "qa10")); err != nil {
		t.Error("qa10/ directory not created by scaffold")
	}
	// LookupRole("qa10") must report NeedsSrc=true so the submodule clone fires.
	if !submoduleCalled {
		t.Error("gitAddSubmodule not called for qa10 — LookupRole defaults are not flowing through")
	}

	updated, err := config.Load(filepath.Join(root, "initech.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range updated.Roles {
		if r == "qa10" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("qa10 not appended to config roles: %v", updated.Roles)
	}
}

// TestRunAddAgent_TypoRoleRejected: anchored regex must reject typos like
// qaa1. The error message must mention the numbered-family pattern so the
// operator knows what shape names are acceptable.
func TestRunAddAgent_TypoRoleRejected(t *testing.T) {
	err := runAddAgent(addAgentCmd, []string{"qaa1"})
	if err == nil {
		t.Fatal("expected error for typo 'qaa1'")
	}
	msg := err.Error()
	if !strings.Contains(msg, "unknown agent") {
		t.Errorf("error = %q, want 'unknown agent'", msg)
	}
	if !strings.Contains(msg, "Numbered families") {
		t.Errorf("error = %q, want mention of 'Numbered families' so operators see the qa\\d+/eng\\d+ shape", msg)
	}
}
