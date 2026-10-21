package cmd

import (
	"bytes"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// ini-ei8l: a send to a remote agent held behind a dialog reads deferred, as
// a local one does, exits 0 as a local one does, and never reads delivered.
// Before this the host case printed "delivered to host:agent" whatever the
// answer said.
func TestSendCommand_RemoteDeferralReadsAsDeferred(t *testing.T) {
	skipWindows(t)
	sockPath := startFakeIPC(t, tui.IPCResponse{OK: true, Data: "deferred to intern — modal open 4s, 2 queued"})
	t.Setenv("INITECH_SOCKET", sockPath)

	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs([]string{"send", "workbench:intern", "do the thing"})
	defer rootCmd.SetArgs(nil)

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("a deferred remote send failed (%v); a local deferral exits 0", err)
	}
	want := "deferred to workbench:intern — target has a modal open; will deliver when it closes\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}
