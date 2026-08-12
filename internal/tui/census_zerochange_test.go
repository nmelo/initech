//go:build !windows

package tui

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// census_zerochange_test.go enforces ini-9ka.2's zero-change scope guard: a
// single-window fleet must produce the same startup output and the same
// .initech/ file/socket census as the previous release. "Single-window users
// see zero change" is an epic-level release claim the operator publishes, so
// this test is the spec, not a convenience -- per the bead, a design that
// cannot pass it is an ESCALATION, not a test to weaken.
//
// The committed baseline (testdata/census-single-window-v2.5.2.txt) was
// captured from the SHIPPED v2.5.2 binary, deliberately not from a local
// build: a baseline derived from the branch under test would prove
// self-agreement rather than zero-change.
//
// Gated behind INITECH_CENSUS=1 because it builds and runs the real binary
// under a PTY (several seconds), so it stays out of `make test`.
// Run: INITECH_CENSUS=1 go test ./internal/tui/ -run TestCensus -v -count=1 -timeout 120s

const censusBaselineFixture = "testdata/census-single-window-v2.5.2.txt"

func TestCensus_SingleWindowMatchesShippedRelease(t *testing.T) {
	if os.Getenv("INITECH_CENSUS") != "1" {
		t.Skip("set INITECH_CENSUS=1 to run the single-window zero-change census check")
	}

	want, err := os.ReadFile(censusBaselineFixture)
	if err != nil {
		t.Fatalf("read baseline fixture: %v", err)
	}

	bin := buildInitechBinary(t)
	got := captureCensus(t, bin)

	if normalizeCensusDoc(string(want)) != normalizeCensusDoc(got) {
		t.Errorf(`SINGLE-WINDOW ZERO-CHANGE VIOLATED.

The single-window census/startup output differs from the shipped v2.5.2 baseline.
Per ini-9ka.2 this is an ESCALATION, not a test to weaken: "single-window users
see zero change" is a published release claim. If the listener design creates a
new .initech/ artifact or prints new startup output for single-window fleets,
take the design back for a decision rather than editing this fixture.

--- baseline (shipped v2.5.2) ---
%s
--- current build ---
%s`, want, got)
	}
}

// normalizeCensusDoc drops comment/provenance lines and blank lines so the
// comparison is over the census payload only -- the fixture's provenance
// header documents where the baseline came from and must not itself be
// something the current build has to reproduce.
func normalizeCensusDoc(s string) string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// buildInitechBinary compiles the current tree's initech binary into a temp
// dir and returns its path.
func buildInitechBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "initech")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "../.." // repo root, relative to internal/tui
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build initech: %v\n%s", err, out)
	}
	return bin
}

// captureCensus starts the given initech binary as a single-window fleet in
// an isolated temp project and returns the census document: which artifacts
// exist under .initech/ while running and after shutdown, plus any plain-text
// output printed before the alt-screen takes over.
func captureCensus(t *testing.T, bin string) string {
	t.Helper()

	// Deliberately NOT t.TempDir(): it derives the path from the test's
	// name, and this test's name is long enough that
	// <tmpdir>/<TestName><rand>/001/.initech/initech.sock reaches ~129
	// chars -- past the ~104-char macOS limit on unix socket paths. The
	// IPC bind then fails, startup aborts before creating initech.pid or
	// initech.sock, and the census comes back missing both. That looks
	// exactly like a zero-change violation while actually being a harness
	// artifact, so it is worth keeping short on purpose rather than
	// re-diagnosing later.
	root, err := os.MkdirTemp("", "ini-census-")
	if err != nil {
		t.Fatalf("mkdir temp root: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	// One role with a cheap shell command rather than a real agent: the
	// census is session-level (socket, log, pid), not agent-specific, and
	// this keeps the check from spawning real agent processes.
	cfg := "project: censusproj\nroot: " + root + `
roles:
    - solo
role_overrides:
    solo:
        agent_type: generic
        command:
            - /bin/sh
            - -c
            - "while true; do sleep 1; done"
`
	if err = os.WriteFile(filepath.Join(root, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "solo"), 0o755); err != nil {
		t.Fatalf("mkdir role dir: %v", err)
	}

	cmd := exec.Command(bin)
	cmd.Dir = root
	// Strip inherited session vars so this can never be mistaken for, or
	// reach, a live fleet.
	var env []string
	for _, e := range append(os.Environ(), "TERM=xterm-256color") {
		if strings.HasPrefix(e, "INITECH_SOCKET=") || strings.HasPrefix(e, "INITECH_AGENT=") {
			continue
		}
		env = append(env, e)
	}
	cmd.Env = env

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 120})
	if err != nil {
		t.Fatalf("start under pty: %v", err)
	}
	var buf strings.Builder
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, ptmx)
		close(done)
	}()

	time.Sleep(4 * time.Second)
	running := censusDir(filepath.Join(root, ".initech"))

	_ = cmd.Process.Signal(os.Interrupt)
	time.Sleep(1500 * time.Millisecond)
	_ = cmd.Process.Kill()
	ptmx.Close()
	<-done

	after := censusDir(filepath.Join(root, ".initech"))

	var b strings.Builder
	b.WriteString("\n[census: while running]\n")
	for _, l := range running {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n[census: after shutdown]\n")
	for _, l := range after {
		b.WriteString(l + "\n")
	}
	b.WriteString("\n[startup output: pre-alt-screen, normalized]\n")
	for _, l := range preAltScreenOutput(buf.String(), root) {
		b.WriteString(l + "\n")
	}
	return b.String()
}

// censusDir lists which artifacts exist in dir as NAME<TAB>TYPE, sorted.
// Sizes, mtimes, and contents are deliberately excluded: they vary per run,
// and the claim under test is which artifacts a single-window fleet creates.
func censusDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"(.initech/ does not exist)"}
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		typ := "file"
		switch {
		case info.IsDir():
			typ = "dir"
		case info.Mode()&os.ModeSocket != 0:
			typ = "socket"
		case info.Mode()&os.ModeSymlink != 0:
			typ = "symlink"
		}
		out = append(out, fmt.Sprintf("%s\t%s", e.Name(), typ))
	}
	sort.Strings(out)
	return out
}

// preAltScreenOutput returns the plain text printed before the process enters
// the alternate screen. For a TUI that is the only output a user reads as
// "startup output" -- errors, warnings, notices before the UI takes over.
// Everything after the alt-screen switch is the rendered UI, which contains
// live agent content and cursor positioning and is not byte-stable even for
// an identical binary; it is covered by the render tests, not by this census.
func preAltScreenOutput(s, root string) []string {
	if i := strings.Index(s, "\x1b[?1049h"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, root, "<ROOT>")
	s = stripANSIForCensus(s)
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimRight(line, " \r\t")
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return []string{"(no plain-text output before alt-screen entry)"}
	}
	return out
}

func stripANSIForCensus(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && (s[j] == '[' || s[j] == ']' || s[j] == '(') {
				j++
				for j < len(s) && !isANSIFinal(s[j]) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isANSIFinal(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == 0x07
}
