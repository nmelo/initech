package tui

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ini-g8n9 investigation probe, PROMOTED into the suite by ini-uz42 (it was
// expected-red at baseline and lived as .txt for that reason).
// Install as internal/tui/g8n9_contract_probe_test.go to reproduce at baseline.
// Does not prescribe PM-owned copy or an implementation.
func TestG8N9_AllHiddenViewerExplainsItsEmptyPlan(t *testing.T) {
	u := g8n9Viewer(t, false)
	if len(u.visiblePanesForWindow()) != 10 || len(u.plan.Panes) != 0 {
		t.Fatal("probe did not reach the captured owned-but-hidden state")
	}
	if got := u.viewerEmptyExplanation(); got == "" {
		t.Fatal("10 owned streams are hidden: viewer stays silently blank instead of explaining the empty plan")
	} else if got == emptyViewerHint || got == unservedViewerHint {
		t.Fatalf("hidden state misclassified: %q", got)
	}
}

func TestG8N9_UnchangedEmptyStateDoesNotWarnEveryFrame(t *testing.T) {
	u := g8n9Viewer(t, true)
	root := t.TempDir()
	cleanup := InitLogger(root, slog.LevelWarn)
	defer cleanup()
	for i := 0; i < 30; i++ {
		u.viewerEmptyExplanation()
	}
	data, err := os.ReadFile(filepath.Join(root, ".initech", "initech.log"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "viewer owns agents but is rendering none"); got > 1 {
		t.Fatalf("unchanged hidden state emitted %d identical defect warnings in 30 frames; want <= 1", got)
	}
}
