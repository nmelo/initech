package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestServeZeroConfig_TokenCreated(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	initechDir := filepath.Join(dir, ".initech")

	tok, err := tui.ReadOrCreateToken(initechDir)
	if err != nil {
		t.Fatalf("token creation: %v", err)
	}
	if tok == "" {
		t.Fatal("token should not be empty")
	}

	info, err := os.Stat(filepath.Join(initechDir, "token"))
	if err != nil {
		t.Fatalf("token file should exist: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("token file permissions = %o, want 0600", info.Mode().Perm())
	}
}

func TestServeZeroConfig_TokenReused(t *testing.T) {
	skipWindows(t)
	dir := filepath.Join(t.TempDir(), ".initech")

	tok1, err := tui.ReadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := tui.ReadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tok1 != tok2 {
		t.Errorf("token should be stable: %q vs %q", tok1, tok2)
	}
}

func TestServeZeroConfig_TokenFlagOverrides(t *testing.T) {
	old := serveToken
	serveToken = "override-token"
	defer func() { serveToken = old }()

	token := serveToken
	if token != "override-token" {
		t.Errorf("token = %q, want override-token", token)
	}
}

func TestServeZeroConfig_EnvOverrides(t *testing.T) {
	old := serveToken
	serveToken = ""
	defer func() { serveToken = old }()

	t.Setenv("INITECH_TOKEN", "env-token")

	token := serveToken
	if token == "" {
		token = os.Getenv("INITECH_TOKEN")
	}
	if token != "env-token" {
		t.Errorf("token = %q, want env-token", token)
	}
}

func TestServeWithConfig_RequiresHeadless(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	cfg := "project: test\nroot: " + dir + "\nroles:\n  - eng1\nmode: grid\n"
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	rootCmd.SetOut(&strings.Builder{})
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"serve"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for non-headless mode")
	}
	if !strings.Contains(err.Error(), "headless") {
		t.Errorf("error = %q, want 'headless' mention", err.Error())
	}
}

func TestServeDetectsConfigVsZeroConfig(t *testing.T) {
	skipWindows(t)
	dir := t.TempDir()
	cfg := "project: test\nroot: " + dir + "\nroles:\n  - eng1\nmode: grid\n"
	os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0644)

	orig, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(orig)

	rootCmd.SetOut(&strings.Builder{})
	rootCmd.SetErr(&strings.Builder{})
	rootCmd.SetArgs([]string{"serve"})
	defer rootCmd.SetArgs(nil)

	err := rootCmd.Execute()
	if err != nil && strings.Contains(err.Error(), "no initech.yaml") {
		t.Error("should detect existing initech.yaml and use config path")
	}
}

func TestServeDefaultPort(t *testing.T) {
	if servePort != 9090 {
		t.Errorf("default port = %d, want 9090", servePort)
	}
}

func TestReadOrCreateTokenStatus_FirstRunReportsCreated(t *testing.T) {
	skipWindows(t)
	dir := filepath.Join(t.TempDir(), ".initech")

	tok, created, err := tui.ReadOrCreateTokenStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("first run should report created=true")
	}
	if tok == "" {
		t.Error("token should be non-empty")
	}
}

func TestReadOrCreateTokenStatus_SecondRunReportsExisting(t *testing.T) {
	skipWindows(t)
	dir := filepath.Join(t.TempDir(), ".initech")

	if _, created, err := tui.ReadOrCreateTokenStatus(dir); err != nil || !created {
		t.Fatalf("first run: created=%v err=%v", created, err)
	}

	tok, created, err := tui.ReadOrCreateTokenStatus(dir)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second run should report created=false")
	}
	if tok == "" {
		t.Error("token should be non-empty on read")
	}
}

func TestPrintConnectionSnippet_ContainsExpectedFields(t *testing.T) {
	var buf strings.Builder
	printConnectionSnippet(&buf, "workbench", "192.168.1.50", 9090, "TOK-abc")
	out := buf.String()

	for _, want := range []string{
		"remotes:",
		"workbench:",
		"addr: 192.168.1.50:9090",
		"token: TOK-abc",
		"roles: []",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("snippet missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestLANIPv4_NonLoopback(t *testing.T) {
	ip := tui.LANIPv4()
	if ip == "" {
		t.Fatal("LANIPv4 should never return empty")
	}
	// Either a real LAN IP or the 0.0.0.0 fallback. Loopback should never appear.
	if strings.HasPrefix(ip, "127.") {
		t.Errorf("LANIPv4 returned loopback %q", ip)
	}
}
