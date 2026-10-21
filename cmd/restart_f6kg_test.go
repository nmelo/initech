package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-f6kg: initech restart names how many held messages went to the new
// process, and prints the plain line when there were none.
func TestRunRestart_NamesTheCarriedMessages(t *testing.T) {
	skipWindows(t)
	for data, want := range map[string]string{
		"carried 2 held message(s)": "Restarted eng2; carried 2 held message(s).",
		"":                          "Restarted eng2.",
	} {
		sock := i35wActionIPC(t, map[string]tui.IPCResponse{"restart": {OK: true, Data: data}})
		t.Setenv("INITECH_SOCKET", sock)
		out := &bytes.Buffer{}
		rootCmd.SetOut(out)
		rootCmd.SetErr(&bytes.Buffer{})
		rootCmd.SetArgs([]string{"restart", "eng2"})
		err := rootCmd.Execute()
		rootCmd.SetArgs(nil)
		if err != nil || !strings.Contains(out.String(), want) {
			t.Errorf("data %q: err %v, out %q, want %q", data, err, out.String(), want)
		}
	}
}
