package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ini-r0q.1 AC2: the detailed pane list names each pane's newest session
// transcript, the file its journal reads, for a stopped pane too; a pane
// whose session folder is empty or missing names none.
func TestFillPaneDetail_NamesThePanesNewestTranscript(t *testing.T) {
	dir := t.TempDir()
	old, cur := filepath.Join(dir, "old.jsonl"), filepath.Join(dir, "cur.jsonl")
	os.WriteFile(old, []byte("{}\n"), 0o600)
	os.WriteFile(cur, []byte("{}\n"), 0o600)
	os.Mkdir(filepath.Join(dir, "subagents"), 0o700)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)

	info := PaneInfo{Name: "eng1", Alive: false}
	fillPaneDetail(&info, &Pane{name: "eng1", jsonlDir: dir})
	if info.SessionFile != cur {
		t.Errorf("session_file = %q, want the newest %q", info.SessionFile, cur)
	}

	for name, d := range map[string]string{"empty folder": t.TempDir(), "no folder": filepath.Join(dir, "none")} {
		info := PaneInfo{Name: "eng2"}
		fillPaneDetail(&info, &Pane{name: "eng2", jsonlDir: d})
		if info.SessionFile != "" {
			t.Errorf("%s: session_file = %q, want none", name, info.SessionFile)
		}
	}
}
