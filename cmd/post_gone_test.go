package cmd

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// ini-jxjl.2 AC1: initech post is gone, with no stub: it fails as any unknown
// command does.
func TestPostGone_IsAnUnknownCommand(t *testing.T) {
	// Hermetic: a shell inside a live rig carries its socket and agent, and a
	// build where post still exists would post to that rig's inbox.
	t.Setenv("INITECH_SOCKET", filepath.Join(t.TempDir(), "no-window.sock"))
	t.Setenv("INITECH_AGENT", "")
	for _, args := range [][]string{{"post", "hello"}, {"post", "--mine"}} {
		var out bytes.Buffer
		rootCmd.SetOut(&out)
		rootCmd.SetErr(&out)
		rootCmd.SetArgs(args)
		err := rootCmd.Execute()
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		if err == nil || !strings.Contains(err.Error(), `unknown command "post"`) {
			t.Errorf("initech %s: %v, want unknown command", strings.Join(args, " "), err)
		}
	}
	// The twin: a command that exists is found.
	if cmd, _, err := rootCmd.Find([]string{"strip"}); err != nil || cmd.Name() != "strip" {
		t.Errorf("strip not found: %v", err)
	}
}
