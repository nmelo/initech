package tui

import (
	"strings"
	"testing"
)

// ini-jvrs criterion 5: every bead action over the socket leaves one line in
// the log -- target, the ids or "clear", and the result -- so a triage can
// tell whether a clear arrived.
func TestBeadAction_LogsOneLinePerCall(t *testing.T) {
	buf := captureLogs(t)
	eng := newEmuPane("eng4", 80, 24)
	sock := noBeadSocket(t, eng)

	ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: "ini-x\tFix it"})
	ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: "ini-a,ini-b"})
	ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "eng4", Text: ""})
	ipcAsk(t, sock, IPCRequest{Action: "bead", Target: "nobody", Text: "ini-y"})

	var lines []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, "bead action") {
			lines = append(lines, l)
		}
	}
	want := [][]string{
		{"target=eng4", "beads=ini-x", "result=ok"},
		{"target=eng4", "beads=ini-a,ini-b", "result=ok"},
		{"target=eng4", "beads=clear", "result=ok"},
		{"target=nobody", "beads=ini-y", `result="refused: pane \"nobody\" not found"`},
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d bead action lines, want %d:\n%s", len(lines), len(want), strings.Join(lines, "\n"))
	}
	for i, w := range want {
		for _, part := range w {
			if !strings.Contains(lines[i], part) {
				t.Errorf("line %d = %q, want %s", i, lines[i], part)
			}
		}
	}
}
