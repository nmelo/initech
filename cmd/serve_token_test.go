package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-hl6r: initech serve prints its token only when it creates it. Before
// the fix every start printed "Token: <value>" to stdout, so a daemon under
// a supervisor wrote the key that opens every agent's terminal into its
// journal on each restart. The tests compare the token; they never print it.

// serveOnce runs initech serve (zero-config: dir has no initech.yaml) in dir
// with the daemon stubbed
// and returns stdout and stderr.
func serveOnce(t *testing.T, dir string, flagToken string) (stdout, stderr string) {
	t.Helper()
	chdirTemp(t, dir)
	origDaemon, origTok, origShow := runDaemon, serveToken, serveShowToken
	t.Cleanup(func() { runDaemon, serveToken, serveShowToken = origDaemon, origTok, origShow })
	runDaemon = func(tui.DaemonConfig) error { return nil }
	serveToken = flagToken
	serveShowToken = false
	t.Setenv("INITECH_TOKEN", "")

	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	// Through runServe, the command's real entry, so a mutant in its
	// dispatch (show-token, config detection) is reached.
	if err := runServe(cmd, nil); err != nil {
		t.Fatalf("serve: %v", err)
	}
	return out.String(), errOut.String()
}

func storedToken(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".initech", "token"))
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	return strings.TrimSpace(string(b))
}

// AC 1 and 2: the first run shows the token exactly once; a restart shows it
// nowhere, and says where it is stored.
func TestServe_TheTokenIsPrintedOnlyWhenItIsCreated(t *testing.T) {
	dir := t.TempDir()
	first, firstErr := serveOnce(t, dir, "")
	tok := storedToken(t, dir)
	if n := strings.Count(first+firstErr, tok); n != 1 {
		t.Errorf("first run printed the token %d times, want exactly once (in the connection snippet)", n)
	}
	fi, err := os.Stat(filepath.Join(dir, ".initech", "token"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode: %v %v, want 0600", fi.Mode().Perm(), err)
	}

	second, secondErr := serveOnce(t, dir, "")
	if strings.Contains(second+secondErr, tok) {
		t.Fatal("a restart printed the token; under a supervisor that writes the key into the journal on every start")
	}
	if !strings.Contains(second, filepath.Join(".initech", "token")) || !strings.Contains(second, "--show-token") {
		t.Errorf("a restart does not say where the token is stored and how to see it:\n%s", second)
	}
}

// AC 3: --show-token prints the token and nothing else; with no token yet
// it fails and says why, without creating one.
func TestServe_ShowTokenPrintsOnlyTheTokenAndNeverCreatesOne(t *testing.T) {
	dir := t.TempDir()
	chdirTemp(t, dir)
	origTok, origShow := serveToken, serveShowToken
	t.Cleanup(func() { serveToken, serveShowToken = origTok, origShow })
	serveToken, serveShowToken = "", true

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	err := runServe(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "no token yet") {
		t.Fatalf("--show-token with no token: err=%v, want the no-token-yet reason", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".initech", "token")); !os.IsNotExist(statErr) {
		t.Fatal("--show-token created a token")
	}

	serveOnce(t, dir, "") // create one
	tok := storedToken(t, dir)
	serveShowToken = true
	out.Reset()
	if err := runServe(cmd, nil); err != nil {
		t.Fatalf("--show-token: %v", err)
	}
	if out.String() != tok+"\n" {
		t.Fatalf("--show-token printed something other than the token alone (%d bytes)", out.Len())
	}
}

// AC 5 and feed-it-nothing: a token given with --token is never echoed, and
// it wins over an existing file, which is left as it was.
func TestServe_ATokenFlagIsNeverEchoedAndLeavesTheFile(t *testing.T) {
	dir := t.TempDir()
	serveOnce(t, dir, "")
	fileTok := storedToken(t, dir)

	const flagTok = "flag-token-value-0123456789"
	out, errOut := serveOnce(t, dir, flagTok)
	if strings.Contains(out+errOut, flagTok) || strings.Contains(out+errOut, fileTok) {
		t.Fatal("serve --token echoed a token")
	}
	if storedToken(t, dir) != fileTok {
		t.Fatal("serve --token changed the token file")
	}
	f := serveCmd.Flags().Lookup("token")
	if f == nil || !strings.Contains(strings.ToLower(f.Usage), "deprecated") || !strings.Contains(f.Usage, "ps") {
		t.Errorf("--token help does not say it is deprecated and why: %q", f.Usage)
	}
}

// Feed it nothing: an empty token file is treated as missing, so a token is
// created and shown once.
func TestServe_AnEmptyTokenFileIsReplacedAndShownOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".initech"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".initech", "token"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	out, errOut := serveOnce(t, dir, "")
	tok := storedToken(t, dir)
	if tok == "" || strings.Count(out+errOut, tok) != 1 {
		t.Fatal("an empty token file was not replaced by a token shown exactly once")
	}
}
