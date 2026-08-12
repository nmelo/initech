package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/config"
)

// assignment_readonly_test.go covers ini-9ka.9: a corrupt assignment store
// must degrade to READ-ONLY, never to erasure. The bug was at the seam of two
// individually-defensible fallbacks -- .6 rendering everything on a corrupt
// store, and .5 synthesizing a write-capable store from that nil -- so these
// tests pin the seam, not either side.

// corruptStore writes an unparseable assignments.yaml holding a real
// arrangement, and returns the project root plus a hash of the file.
func corruptStore(t *testing.T) (root, hash string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".initech"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Truncated YAML, but the operator's real assignments are still in here --
	// which is exactly why it must not be overwritten.
	body := "group_window:\n  eng: window-2\n  qa: window-3\n  mkt: window-2\n  [truncated"
	if err := os.WriteFile(assignmentPath(root), []byte(body), 0o600); err != nil {
		t.Fatalf("write store: %v", err)
	}
	return root, hashFile(t, root)
}

func hashFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(assignmentPath(root))
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestFallbackStore_MoveGroupLeavesTheCorruptFileByteIdentical is the core
// regression: the exact sequence that destroyed the arrangement at HEAD --
// corrupt store, one MoveGroup -- must now leave the file untouched.
//
// Hashing the whole file rather than checking "did it still parse" is
// deliberate: the bug produced a perfectly VALID file, just one containing
// almost nothing. Only the bytes distinguish preserved from replaced.
func TestFallbackStore_MoveGroupLeavesTheCorruptFileByteIdentical(t *testing.T) {
	root, before := corruptStore(t)

	tui := &TUI{projectRoot: root}
	a := tui.agentsAssignment()

	err := a.MoveGroup("eng", "window-2")
	if err == nil {
		t.Fatal("MoveGroup succeeded against a fallback store; it would have overwritten the operator's arrangement")
	}
	if !errors.Is(err, ErrAssignmentReadOnly) {
		t.Errorf("error = %v, want ErrAssignmentReadOnly so callers can distinguish a refusal from a validation failure", err)
	}
	if after := hashFile(t, root); after != before {
		t.Errorf("the corrupt file was modified.\n  before: %s\n  after:  %s\nA fallback must never write the file it failed to read", before, after)
	}
}

// TestFallbackStore_RefusesEveryWindowIdentity guards against the refusal
// being accidentally specific to one shape of move -- moving TO window 1
// takes the delete branch in MoveGroup rather than the assign branch, and
// both must be refused.
func TestFallbackStore_RefusesEveryWindowIdentity(t *testing.T) {
	root, before := corruptStore(t)
	a := (&TUI{projectRoot: root}).agentsAssignment()

	for _, target := range []string{"window-2", "window-3", WindowOne} {
		if err := a.MoveGroup("eng", target); !errors.Is(err, ErrAssignmentReadOnly) {
			t.Errorf("MoveGroup(eng, %q) error = %v, want ErrAssignmentReadOnly", target, err)
		}
	}
	if after := hashFile(t, root); after != before {
		t.Error("the corrupt file was modified by a refused move")
	}
}

// TestFallbackStore_ReadsStillWorkAndReportWindowOne is why the fix is
// write-only. The degraded view is "everything on window 1", and reads must
// keep answering it -- that is what lets the modal and the render filter draw
// at all while the store is unreadable.
func TestFallbackStore_ReadsStillWorkAndReportWindowOne(t *testing.T) {
	root, _ := corruptStore(t)
	a := (&TUI{projectRoot: root}).agentsAssignment()

	if got := a.WindowOfGroup("eng"); got != WindowOne {
		t.Errorf("WindowOfGroup(eng) = %q during fallback, want window 1 (the degraded view)", got)
	}
	groupOf := map[string]string{"eng1": "eng"}
	if got := a.WindowOfAgent("eng1", groupOf); got != WindowOne {
		t.Errorf("WindowOfAgent = %q, want window 1", got)
	}
	if got := a.GroupsForWindow("window-2", []string{"eng", "qa"}); len(got) != 0 {
		t.Errorf("GroupsForWindow(window-2) = %v during fallback, want none", got)
	}
}

// TestFallbackStore_KeepsItsRootRatherThanBlankingIt pins the finding that
// disqualified the obvious sentinel. assignmentPath("") is RELATIVE, so a
// root="" fallback would have MkdirAll'd and written a stray
// .initech/assignments.yaml into whatever directory initech was launched
// from -- reporting success while writing somewhere the operator would never
// find. The refusal must come from the readOnly flag, not from a blank root.
func TestFallbackStore_KeepsItsRootRatherThanBlankingIt(t *testing.T) {
	root, _ := corruptStore(t)
	a := (&TUI{projectRoot: root}).agentsAssignment()

	if a.root != root {
		t.Errorf("fallback root = %q, want the real project root %q -- a blank root would make save() write a relative path into the launch directory", a.root, root)
	}
	if !a.readOnly {
		t.Error("fallback store is not marked read-only; nothing would prevent a write")
	}
}

// TestHealthyStore_StillPersistsNormally is the far side: this bead severs the
// write path only for FALLBACK stores. A store that loaded fine must still
// save on every edit, which is .4's contract.
func TestHealthyStore_StillPersistsNormally(t *testing.T) {
	root := t.TempDir()
	a, err := LoadAssignment(root)
	if err != nil {
		t.Fatalf("LoadAssignment on a fresh project: %v", err)
	}
	if a.readOnly {
		t.Fatal("a healthy store was marked read-only")
	}
	if err := a.MoveGroup("eng", "window-2"); err != nil {
		t.Fatalf("MoveGroup on a healthy store: %v", err)
	}
	reloaded, err := LoadAssignment(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := reloaded.WindowOfGroup("eng"); got != "window-2" {
		t.Errorf("healthy store did not persist: WindowOfGroup(eng) = %q", got)
	}
}

// TestOpenAgentsModal_DropsFallbackSoRepairTakesEffect covers the recovery
// edge. The notice tells the operator to repair the file and reopen the
// modal, so reopening must actually reload -- otherwise the guidance is
// wrong and they would need a restart nobody told them about.
func TestOpenAgentsModal_DropsFallbackSoRepairTakesEffect(t *testing.T) {
	root, _ := corruptStore(t)
	tui := &TUI{projectRoot: root}

	if a := tui.agentsAssignment(); !a.readOnly {
		t.Fatal("precondition: corrupt store should yield a read-only fallback")
	}

	// Operator repairs the file.
	good := "group_window:\n  eng: window-2\n"
	if err := os.WriteFile(assignmentPath(root), []byte(good), 0o600); err != nil {
		t.Fatalf("repair store: %v", err)
	}

	tui.openAgentsModal()

	a := tui.agentsAssignment()
	if a.readOnly {
		t.Error("still using the read-only fallback after repair + reopen; the notice's guidance would be wrong")
	}
	if got := a.WindowOfGroup("eng"); got != "window-2" {
		t.Errorf("repaired store not loaded: WindowOfGroup(eng) = %q, want window-2", got)
	}
}

// TestOpenAgentsModal_KeepsAHealthyCachedStore guards the other direction:
// dropping the cache must be specific to the fallback, or every modal open
// would re-read the file.
func TestOpenAgentsModal_KeepsAHealthyCachedStore(t *testing.T) {
	root := t.TempDir()
	tui := &TUI{projectRoot: root}
	first := tui.agentsAssignment()
	if first.readOnly {
		t.Fatal("precondition: fresh project should load a healthy store")
	}

	tui.openAgentsModal()

	if tui.assignment != first {
		t.Error("a healthy cached store was dropped on modal open; this would re-read the file on every open")
	}
}

// TestFallbackStore_RefusedMoveRaisesAVisibleNotice is the bead's "visible
// feedback" AC, driven through the REAL m-move path rather than the notice
// helper -- the operator pressed a key and something must say why nothing
// happened. A silent no-op is the failure mode being fixed: they would
// believe the move applied.
func TestFallbackStore_RefusedMoveRaisesAVisibleNotice(t *testing.T) {
	root, before := corruptStore(t)

	pane := testPane("eng1")
	tui := &TUI{
		projectRoot: root,
		panes:       []PaneView{pane},
		project:     &config.Project{WindowListen: "127.0.0.1:7500"}, // tiers active
		agentEvents: make(chan AgentEvent, 8),
	}
	tui.layoutState.GroupOf = map[string]string{"eng1": "eng"}
	tui.layoutState.Groups = []string{"eng"}
	tui.agents.selected = 0

	tui.agentsMoveGroupToNextWindow()

	select {
	case ev := <-tui.agentEvents:
		if ev.Type != EventAssignmentWriteRefused {
			t.Errorf("event type = %v, want EventAssignmentWriteRefused", ev.Type)
		}
		// The notice must name the recovery action and reassure that nothing
		// was lost -- "it failed" alone would leave the operator assuming
		// their arrangement is gone, which is the opposite of what happened.
		for _, want := range []string{"assignments.yaml", "NOT overwritten"} {
			if !strings.Contains(ev.Detail, want) {
				t.Errorf("notice does not mention %q: %q", want, ev.Detail)
			}
		}
		if ev.Pane != "" {
			t.Errorf("notice attached to pane %q; it is session-level and must render in every window", ev.Pane)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a refused move raised NO notice; the operator would believe it applied")
	}

	if after := hashFile(t, root); after != before {
		t.Error("the m-move path modified the corrupt file")
	}
}

// TestFallbackStore_PruneDoesNotAttemptAWrite checks the third MoveGroup call
// site, which is housekeeping rather than an operator action. On a fallback
// store every group already reads as window 1, so the prune loop skips before
// reaching MoveGroup -- meaning it neither writes nor raises a spurious
// notice for something the operator never asked for.
func TestFallbackStore_PruneDoesNotAttemptAWrite(t *testing.T) {
	root, before := corruptStore(t)
	tui := &TUI{projectRoot: root, agentEvents: make(chan AgentEvent, 4)}
	a := tui.agentsAssignment()

	for _, g := range []string{"eng", "qa", "mkt"} {
		if a.WindowOfGroup(g) != WindowOne {
			t.Fatalf("precondition: %q should read as window 1 on a fallback store", g)
		}
	}
	if after := hashFile(t, root); after != before {
		t.Error("reading a fallback store modified the file")
	}
	select {
	case ev := <-tui.agentEvents:
		t.Errorf("a read-only path raised a notice the operator did not ask for: %v", ev.Detail)
	default:
	}
}
