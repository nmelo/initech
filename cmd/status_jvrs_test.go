package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-jvrs: initech status shows every bead holder bd names in a shown
// status, and flags a ribbon that disagrees with bd.

// jvrsBd is a fake bd that honours --status (comma-separated, as bd does),
// answering with one JSON object per line.
const jvrsBd = `#!/bin/sh
[ "$1" = list ] || exit 1
want="$3"
[ "$2" = --status ] || want="in_progress,ready_for_qa,in_qa,qa_passed"
emit() { case ",$want," in *",$1,"*) echo "$2";; esac; }
emit in_progress '{"id":"ini-p","title":"Doing it","status":"in_progress","assignee":"eng1"}'
emit in_qa '{"id":"ini-q","title":"Checking it","status":"in_qa","assignee":"qa2"}'
emit ready_for_qa '{"id":"ini-r","title":"Waiting","status":"ready_for_qa","assignee":"qa3"}'
emit ready_for_qa '{"id":"ini-u","title":"Unclaimed","status":"ready_for_qa","assignee":""}'
emit qa_passed '{"id":"ini-z","title":"Passed","status":"qa_passed","assignee":"qa1"}'
`

// jvrsStatus runs initech status against the fake bd and a list reply made
// of panes, and returns each agent's row by name.
func jvrsStatus(t *testing.T, panes ...tui.PaneInfo) map[string]string {
	t.Helper()
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeBasicConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), jvrsBd)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, _ := json.Marshal(panes)
	resp, _ := json.Marshal(tui.IPCResponse{OK: true, Data: string(data)})
	_, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(resp) + "\n"})
	defer cleanup()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(out.String(), "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			rows[f[0]] = line
		}
	}
	return rows
}

func jvrsPane(name string, beads ...string) tui.PaneInfo {
	if beads == nil {
		beads = []string{}
	}
	return tui.PaneInfo{Name: name, Activity: "idle", Alive: true, Visible: true, Beads: beads}
}

// Criterion 1: holders in in_qa and ready_for_qa show; qa_passed does not.
func TestStatus_ShowsHoldersInQAAndReadyForQAButNotQAPassed(t *testing.T) {
	rows := jvrsStatus(t,
		jvrsPane("eng1", "ini-p"), jvrsPane("qa2", "ini-q"), jvrsPane("qa3", "ini-r"), jvrsPane("qa1"))
	for agent, want := range map[string]string{"eng1": "ini-p", "qa2": "ini-q", "qa3": "ini-r"} {
		if !strings.Contains(rows[agent], want) {
			t.Errorf("%s row = %q, want %s", agent, rows[agent], want)
		}
	}
	if !strings.Contains(rows["qa2"], "in_qa") {
		t.Errorf("qa2 row = %q, want the bead's status in_qa", rows["qa2"])
	}
	if strings.Contains(rows["qa1"], "ini-z") {
		t.Errorf("qa1 row = %q; a qa_passed bead has no holder to show", rows["qa1"])
	}
}

// Criterion 4: a ribbon that disagrees with bd is shown next to bd's answer.
func TestStatus_FlagsARibbonThatDisagreesWithBd(t *testing.T) {
	rows := jvrsStatus(t,
		jvrsPane("eng4", "ini-q"), // bd says qa2 holds ini-q
		jvrsPane("qa2"),           // and qa2's ribbon is empty
		jvrsPane("eng1", "ini-p"), // agrees
		jvrsPane("qa1"))           // agrees: nothing either side
	if !strings.Contains(rows["eng4"], "- ribbon: ini-q") {
		t.Errorf("eng4 row = %q, want \"- ribbon: ini-q\"", rows["eng4"])
	}
	if !strings.Contains(rows["qa2"], "ini-q") || !strings.Contains(rows["qa2"], "ribbon: -") {
		t.Errorf("qa2 row = %q, want bd's ini-q and \"ribbon: -\"", rows["qa2"])
	}
	for _, a := range []string{"eng1", "qa1"} {
		if strings.Contains(rows[a], "ribbon:") {
			t.Errorf("%s row = %q; ribbon and bd agree, nothing extra", a, rows[a])
		}
	}
}

// An agent holding two beads agrees with a ribbon showing both, in any order.
func TestStatus_RibbonMatchIsBySetNotOrder(t *testing.T) {
	// eng1 holds ini-p in bd; a ribbon of ini-p alone matches, a ribbon with
	// an extra bead does not.
	rows := jvrsStatus(t, jvrsPane("eng1", "ini-p", "ini-extra"))
	if !strings.Contains(rows["eng1"], "ribbon: ini-p,ini-extra") {
		t.Errorf("eng1 row = %q, want the ribbon's ids named", rows["eng1"])
	}
}

// A TUI that sends no beads field (an older TUI) gets no drift check.
func TestStatus_NoDriftNoteWithoutARibbonField(t *testing.T) {
	p := jvrsPane("eng4")
	p.Beads = nil
	rows := jvrsStatus(t, p, jvrsPane("qa2", "ini-q"))
	if strings.Contains(rows["eng4"], "ribbon:") {
		t.Errorf("eng4 row = %q; no ribbon was reported, so none can disagree", rows["eng4"])
	}
}

// The no-bead marker keeps winning and gets no note.
func TestStatus_NoBeadMarkerGetsNoDriftNote(t *testing.T) {
	p := jvrsPane("eng1")
	p.NoBead = "bump the version"
	rows := jvrsStatus(t, p)
	if !strings.Contains(rows["eng1"], "no bead") || strings.Contains(rows["eng1"], "ribbon:") {
		t.Errorf("eng1 row = %q, want the marker and no note", rows["eng1"])
	}
}
