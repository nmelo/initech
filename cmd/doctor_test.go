package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// ── Prereq checks ──────────────────────────────────────────────────

func TestDoctorPrereqs_MissingClaude(t *testing.T) {
	env := doctorEnv{
		LookPath: func(name string) (string, error) {
			if name == "claude" {
				return "", fmt.Errorf("not found")
			}
			return "/usr/bin/" + name, nil
		},
		GetVersion: func(cmd []string) string { return "1.0" },
	}

	results := runPrereqChecks(env)

	var claudeResult *checkResult
	for i := range results {
		if results[i].Label == "claude" {
			claudeResult = &results[i]
			break
		}
	}
	if claudeResult == nil {
		t.Fatal("expected a result for 'claude'")
	}
	if claudeResult.Status != "FAIL" {
		t.Errorf("claude status = %q, want FAIL (required binary missing)", claudeResult.Status)
	}
}

func TestDoctorPrereqs_AllPresent(t *testing.T) {
	env := doctorEnv{
		LookPath:   func(name string) (string, error) { return "/usr/bin/" + name, nil },
		GetVersion: func(cmd []string) string { return "2.5.0" },
	}

	results := runPrereqChecks(env)

	for _, r := range results {
		if r.Status != "OK" {
			t.Errorf("prereq %q: status = %q, want OK", r.Label, r.Status)
		}
	}
	if len(results) != len(prereqList) {
		t.Errorf("got %d results, want %d", len(results), len(prereqList))
	}
}

func TestDoctorPrereqs_OptionalMissing(t *testing.T) {
	env := doctorEnv{
		LookPath: func(name string) (string, error) {
			if name == "bd" {
				return "", fmt.Errorf("not found")
			}
			return "/usr/bin/" + name, nil
		},
		GetVersion: func(cmd []string) string { return "1.0" },
	}

	results := runPrereqChecks(env)

	var bdResult *checkResult
	for i := range results {
		if results[i].Label == "bd" {
			bdResult = &results[i]
			break
		}
	}
	if bdResult == nil {
		t.Fatal("expected a result for 'bd'")
	}
	if bdResult.Status != "WARN" {
		t.Errorf("bd status = %q, want WARN (optional binary missing)", bdResult.Status)
	}
}

// ── Project checks ─────────────────────────────────────────────────

func TestDoctorProject_Valid(t *testing.T) {
	dir := t.TempDir()

	yaml := fmt.Sprintf("project: testproj\nroot: %s\nwebhook_url: https://hooks.slack.com/test\nroles:\n  - super\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)

	for _, role := range []string{"super"} {
		roleDir := filepath.Join(dir, role)
		os.MkdirAll(roleDir, 0755)
		os.WriteFile(filepath.Join(roleDir, "CLAUDE.md"), []byte("# "+role), 0644)
	}
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	checks, name, root := runProjectChecks(filepath.Join(dir, "initech.yaml"))

	if name != "testproj" {
		t.Errorf("project name = %q, want testproj", name)
	}
	if root != dir {
		t.Errorf("project root = %q, want %q", root, dir)
	}

	var configCheck *checkResult
	for i := range checks {
		if checks[i].Label == "Config" {
			configCheck = &checks[i]
			break
		}
	}
	if configCheck == nil {
		t.Fatal("expected Config check result")
	}
	if configCheck.Status != "OK" {
		t.Errorf("Config status = %q, want OK; detail: %s", configCheck.Status, configCheck.Detail)
	}

	for _, c := range checks {
		if c.Status == "WARN" || c.Status == "FAIL" {
			t.Errorf("check %q has status %q: %s", c.Label, c.Status, c.Detail)
		}
	}
}

func TestDoctorProject_MissingWebhookURL(t *testing.T) {
	dir := t.TempDir()

	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - super\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)

	os.MkdirAll(filepath.Join(dir, "super"), 0755)
	os.WriteFile(filepath.Join(dir, "super", "CLAUDE.md"), []byte("# super"), 0644)
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	checks, _, _ := runProjectChecks(filepath.Join(dir, "initech.yaml"))

	var notifyCheck *checkResult
	for i := range checks {
		if checks[i].Label == "Notify" {
			notifyCheck = &checks[i]
			break
		}
	}
	if notifyCheck == nil {
		t.Fatal("expected Notify check result")
	}
	if notifyCheck.Status != "WARN" {
		t.Errorf("Notify status = %q, want WARN", notifyCheck.Status)
	}
	if notifyCheck.Detail != "no webhook_url configured (Slack notifications disabled)" {
		t.Errorf("Notify detail = %q", notifyCheck.Detail)
	}
}

func TestDoctorProject_Invalid(t *testing.T) {
	dir := t.TempDir()

	// Missing 'name' field makes Validate fail.
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte("roles: [eng1]\n"), 0644)

	checks, _, _ := runProjectChecks(filepath.Join(dir, "initech.yaml"))

	if len(checks) == 0 {
		t.Fatal("expected at least one check result")
	}
	if checks[0].Label != "Config" {
		t.Errorf("first check label = %q, want Config", checks[0].Label)
	}
	if checks[0].Status != "WARN" && checks[0].Status != "FAIL" {
		t.Errorf("Config status = %q, want WARN or FAIL", checks[0].Status)
	}
}

func TestDoctorProject_MissingWorkspace(t *testing.T) {
	dir := t.TempDir()

	yaml := fmt.Sprintf("project: testproj\nroot: %s\nroles:\n  - super\n  - eng1\n", dir)
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(yaml), 0644)

	// Create super workspace but not eng1.
	os.MkdirAll(filepath.Join(dir, "super"), 0755)
	os.WriteFile(filepath.Join(dir, "super", "CLAUDE.md"), []byte("# super"), 0644)
	os.MkdirAll(filepath.Join(dir, ".beads"), 0755)

	checks, _, _ := runProjectChecks(filepath.Join(dir, "initech.yaml"))

	var wsCheck *checkResult
	for i := range checks {
		if checks[i].Label == "Workspaces" {
			wsCheck = &checks[i]
			break
		}
	}
	if wsCheck == nil {
		t.Fatal("expected Workspaces check result")
	}
	if wsCheck.Status != "WARN" {
		t.Errorf("Workspaces status = %q, want WARN (eng1 missing CLAUDE.md)", wsCheck.Status)
	}
}

// ── Remote checks ──────────────────────────────────────────────────

func TestDoctorRemotes_Unreachable(t *testing.T) {
	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes: map[string]config.Remote{
			"deadhost": {Addr: "192.0.2.1:9999"},
		},
	}

	dial := func(network, addr string, timeout time.Duration) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}

	results := runRemoteChecks(proj, dial)

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status != "WARN" {
		t.Errorf("status = %q, want WARN", results[0].Status)
	}
	if results[0].Label != "deadhost" {
		t.Errorf("label = %q, want deadhost", results[0].Label)
	}
}

func TestDoctorRemotes_Reachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		session, err := yamux.Server(conn, yamux.DefaultConfig())
		if err != nil {
			return
		}
		defer session.Close()

		ctrl, err := session.Accept()
		if err != nil {
			return
		}
		defer ctrl.Close()

		scanner := bufio.NewScanner(ctrl)
		if !scanner.Scan() {
			return
		}

		resp := tui.HelloOKMsg{
			Action:   "hello_ok",
			Version:  1,
			PeerName: "workbench",
			Agents: []tui.AgentStatus{
				{Name: "eng1", Alive: true, Activity: "idle"},
			},
		}
		data, _ := json.Marshal(resp)
		ctrl.Write(data)
		ctrl.Write([]byte("\n"))
	}()

	proj := &config.Project{
		Name:     "test",
		PeerName: "laptop",
		Remotes: map[string]config.Remote{
			"workbench": {Addr: ln.Addr().String()},
		},
	}

	results := runRemoteChecks(proj, net.DialTimeout)

	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Status != "OK" {
		t.Errorf("status = %q, want OK; detail: %s", results[0].Status, results[0].Detail)
	}
	if results[0].Label != "workbench" {
		t.Errorf("label = %q, want workbench", results[0].Label)
	}
}

// ── Report summary ─────────────────────────────────────────────────

func TestDoctorReport_HasRequiredMissing(t *testing.T) {
	report := doctorReport{
		Prereqs: []checkResult{
			{Label: "claude", Status: "FAIL", Detail: "missing"},
			{Label: "git", Status: "OK", Detail: "ok"},
		},
	}
	if !report.HasRequiredMissing() {
		t.Error("HasRequiredMissing should be true when a prereq has FAIL status")
	}

	report.Prereqs[0].Status = "OK"
	if report.HasRequiredMissing() {
		t.Error("HasRequiredMissing should be false when all prereqs are OK")
	}
}

func TestDoctorReport_WarningCount(t *testing.T) {
	report := doctorReport{
		Prereqs: []checkResult{{Status: "WARN"}, {Status: "OK"}},
		Project: []checkResult{{Status: "WARN"}, {Status: "WARN"}},
		Remotes: []checkResult{{Status: "OK"}},
	}
	if got := report.WarningCount(); got != 3 {
		t.Errorf("WarningCount = %d, want 3", got)
	}
}

// ── Utility ────────────────────────────────────────────────────────

func TestGetVersionDoctor(t *testing.T) {
	v := getVersion([]string{"git", "--version"})
	if v == "" {
		t.Skip("git not found in PATH")
	}
	if len(v) == 0 || v[0] < '0' || v[0] > '9' {
		t.Errorf("getVersion returned non-version string: %q", v)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if fileExists(path) {
		t.Error("fileExists should return false for nonexistent file")
	}
	os.WriteFile(path, []byte("x"), 0644)
	if !fileExists(path) {
		t.Error("fileExists should return true after creating file")
	}
}
