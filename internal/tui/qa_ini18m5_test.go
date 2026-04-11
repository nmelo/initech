// QA tests for ini-18m.5: Bead auto-detection from JSONL tool_use entries.
// Covers edge cases beyond the unit tests written by eng.
package tui

import "testing"

// AC: --status=in_progress with equals sign (common CLI variant) triggers claim.
func TestDetectBeadClaim_EqualSignInProgress(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status=in_progress --assignee eng1", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "ini-abc.1" {
		t.Errorf("beadID = %q, want ini-abc.1", beadID)
	}
	if clear {
		t.Error("clear should be false on claim")
	}
}

// AC: --status=ready_for_qa with equals sign triggers clear.
func TestDetectBeadClaim_EqualSignReadyForQA(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status=ready_for_qa", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	if !clear {
		t.Error("clear should be true for ready_for_qa")
	}
	if beadID != "" {
		t.Errorf("beadID should be empty, got %q", beadID)
	}
}

// AC: --status in_qa should not trigger (only in_progress and ready_for_qa are signals).
func TestDetectBeadClaim_StatusInQAIgnored(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status in_qa --assignee qa1", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("in_qa should be ignored, got beadID=%q clear=%v", beadID, clear)
	}
}

// AC: --status closed should not trigger.
func TestDetectBeadClaim_StatusClosedIgnored(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status closed", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	if beadID != "" || clear {
		t.Errorf("closed should be ignored, got beadID=%q clear=%v", beadID, clear)
	}
}

// Edge case: "bd update" appears in middle of a multi-command string.
func TestDetectBeadClaim_InlineCommand(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "echo 'done' && bd update ini-abc.1 --status in_progress", ExitCode: 0},
	}
	// The string contains "bd update" so detectBeadClaim should find it.
	// extractBeadID parses by fields; "&&" and "echo" will be before the bd token.
	// The function scans for the "update" field then takes the next field as candidate.
	// Whether it works depends on how strings.Fields splits the compound command.
	// Document and verify actual behavior (should parse ini-abc.1 if Fields works correctly).
	beadID, clear := detectBeadClaim(entries)
	// extractBeadID looks for field=="update" then checks next field.
	// "echo 'done' && bd update ini-abc.1 --status in_progress" splits to:
	// ["echo", "'done'", "&&", "bd", "update", "ini-abc.1", "--status", "in_progress"]
	// So "update" at idx 4, next field is "ini-abc.1" which has "-" and "." → valid.
	// This IS a claim: content contains "bd update" and "--status in_progress".
	_ = clear
	if beadID != "ini-abc.1" {
		t.Errorf("inline compound command: beadID = %q, want ini-abc.1", beadID)
	}
}

// Edge case: first entry is clear, second is claim — last signal wins (claim).
// ini-a1e.5: the old early-return-on-clear would drop the subsequent claim.
func TestDetectBeadClaim_ClearBeforeClaimLastWins(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-old.1 --status ready_for_qa", ExitCode: 0},
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-new.1 --status in_progress --assignee eng1", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	// Last signal is claim; it should win.
	if beadID != "ini-new.1" {
		t.Errorf("claim (last entry) should win, got beadID=%q", beadID)
	}
	if clear {
		t.Error("clear should be false when claim is the last signal")
	}
}

// Edge case: first entry is claim, second is clear — last signal wins (clear).
func TestDetectBeadClaim_ClaimBeforeClearLastWins(t *testing.T) {
	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-new.1 --status in_progress --assignee eng1", ExitCode: 0},
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-new.1 --status ready_for_qa", ExitCode: 0},
	}
	beadID, clear := detectBeadClaim(entries)
	// Last signal is clear; it should win.
	if !clear {
		t.Error("clear (last entry) should win")
	}
	if beadID != "" {
		t.Errorf("beadID should be empty when clear is last signal, got %q", beadID)
	}
}

// AC: IPC override replaces auto-detected bead.
// The IPC "bead" action calls pane.SetBead directly; this test verifies SetBead
// can overwrite whatever auto-detection set.
func TestIPCOverrideReplacesAutoDetectedBead(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng1", eventCh: ch, cfg: PaneConfig{BeadsEnabled: true}}

	// Simulate auto-detection setting a bead.
	autoEntries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status in_progress", ExitCode: 0},
	}
	p.applyBeadDetection(autoEntries)
	if p.BeadID() != "ini-abc.1" {
		t.Fatalf("auto-detected beadID = %q, want ini-abc.1", p.BeadID())
	}

	// Simulate IPC override (initech bead ini-xyz.2).
	p.SetBead("ini-xyz.2", "Override Title")
	if p.BeadID() != "ini-xyz.2" {
		t.Errorf("after IPC override, BeadID() = %q, want ini-xyz.2", p.BeadID())
	}
}

// AC: Failed bd command (ExitCode != 0) does not change bead display.
func TestApplyBeadDetection_FailedCommandNoop(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng1", eventCh: ch, cfg: PaneConfig{BeadsEnabled: true}}
	p.beadIDs = []string{"ini-existing.1"}

	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-18m.5 --status in_progress", ExitCode: 1},
	}
	p.applyBeadDetection(entries)

	if p.BeadID() != "ini-existing.1" {
		t.Errorf("failed command changed beadID to %q, should stay ini-existing.1", p.BeadID())
	}
	if len(ch) != 0 {
		t.Errorf("failed command emitted event, expected none")
	}
}

// Root-level bead IDs without a dot (e.g. "ini-noid") are now accepted by extractBeadID.
func TestExtractBeadID_RootLevelAccepted(t *testing.T) {
	id := extractBeadID("bd update ini-noid --claim")
	if id != "ini-noid" {
		t.Errorf("root-level bead ID without dot should be accepted, got %q", id)
	}
}

// Edge case: bead ID with no hyphen is rejected by extractBeadID.
func TestExtractBeadID_NoHyphenRejected(t *testing.T) {
	id := extractBeadID("bd update abc.1 --claim")
	if id != "" {
		t.Errorf("ID without hyphen should be rejected, got %q", id)
	}
}

// Edge case: bd update with no ID token (flag follows immediately).
func TestExtractBeadID_FlagAsCandidate(t *testing.T) {
	// "bd update --claim" → next token after "update" is "--claim", no dot+hyphen → empty.
	id := extractBeadID("bd update --claim")
	if id != "" {
		t.Errorf("flag token should not be treated as bead ID, got %q", id)
	}
}

// AC: applyBeadDetection event detail includes pane name and bead ID.
func TestApplyBeadDetection_EventDetailFormat(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng2", eventCh: ch, cfg: PaneConfig{BeadsEnabled: true}}

	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-xyz.9 --claim", ExitCode: 0},
	}
	p.applyBeadDetection(entries)

	if len(ch) == 0 {
		t.Fatal("expected event, got none")
	}
	ev := <-ch
	if ev.Pane != "eng2" {
		t.Errorf("ev.Pane = %q, want eng2", ev.Pane)
	}
	if ev.BeadID != "ini-xyz.9" {
		t.Errorf("ev.BeadID = %q, want ini-xyz.9", ev.BeadID)
	}
	if ev.Type != EventBeadClaimed {
		t.Errorf("ev.Type = %v, want EventBeadClaimed", ev.Type)
	}
}

// AC: applyBeadDetection with nil eventCh does not panic.
// The guard in watchJSONL is `if p.eventCh != nil`, but applyBeadDetection
// itself also calls EmitEvent which handles nil safely. Verify no panic.
func TestApplyBeadDetection_NilEventChNoPanic(t *testing.T) {
	p := &Pane{name: "eng1", eventCh: nil, cfg: PaneConfig{BeadsEnabled: true}}

	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --claim", ExitCode: 0},
	}
	// Should not panic even with nil channel.
	p.applyBeadDetection(entries)
	// SetBead should still work (it doesn't use eventCh).
	if p.BeadID() != "ini-abc.1" {
		t.Errorf("BeadID() = %q, want ini-abc.1 even with nil eventCh", p.BeadID())
	}
}

// AC: ready_for_qa on a pane with no current bead is a no-op (no panic, bead stays empty).
func TestApplyBeadDetection_ClearWithNoBead(t *testing.T) {
	ch := make(chan AgentEvent, 4)
	p := &Pane{name: "eng1", eventCh: ch, cfg: PaneConfig{BeadsEnabled: true}}
	// beadID is empty by default.

	entries := []JournalEntry{
		{Type: "tool_result", ToolName: "Bash", Content: "bd update ini-abc.1 --status ready_for_qa", ExitCode: 0},
	}
	p.applyBeadDetection(entries)

	if p.BeadID() != "" {
		t.Errorf("BeadID() = %q after clear with no prior bead, want empty", p.BeadID())
	}
	if len(ch) != 0 {
		t.Errorf("clear should not emit event, got %d events", len(ch))
	}
}
