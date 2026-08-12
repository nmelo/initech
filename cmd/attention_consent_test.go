package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/hooks"
)

func consentProject(t *testing.T, roles ...string) *config.Project {
	t.Helper()
	root := t.TempDir()
	for _, r := range roles {
		if err := os.MkdirAll(filepath.Join(root, r, ".claude"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Project{Name: "test", Root: root, Roles: roles}
}

// ── the one-time prompt ──────────────────────────────────────────────

func TestConsent_DefaultsToNoOnBareEnter(t *testing.T) {
	if PromptAttentionHooksConsent(strings.NewReader("\n"), &bytes.Buffer{}) {
		t.Error("bare Enter must mean No -- the operator-decided default")
	}
}

// TestConsent_DefaultsToNoOnEOF matters because a non-interactive start (CI,
// a piped invocation) must never be read as agreement to write files inside
// the operator's agents.
func TestConsent_DefaultsToNoOnEOF(t *testing.T) {
	if PromptAttentionHooksConsent(strings.NewReader(""), &bytes.Buffer{}) {
		t.Error("EOF must mean No, never an assumed Yes")
	}
}

func TestConsent_AcceptsOnlyExplicitYes(t *testing.T) {
	for _, in := range []string{"y\n", "Y\n", "yes\n", "YES\n"} {
		if !PromptAttentionHooksConsent(strings.NewReader(in), &bytes.Buffer{}) {
			t.Errorf("%q should be Yes", in)
		}
	}
	for _, in := range []string{"n\n", "no\n", "maybe\n", "yep\n", " \n"} {
		if PromptAttentionHooksConsent(strings.NewReader(in), &bytes.Buffer{}) {
			t.Errorf("%q should be No -- only an explicit yes grants consent", in)
		}
	}
}

// TestConsent_CopyDoesNotClaimTheFeatureDependsOnIt guards the honesty the OSC
// decision requires. Before OSC 777 the hook was the only live signal, so copy
// implying the feature needs it was true; it is not any more, and a prompt that
// overstates the cost of No buys consent under false pretences.
func TestConsent_CopyDoesNotClaimTheFeatureDependsOnIt(t *testing.T) {
	var out bytes.Buffer
	PromptAttentionHooksConsent(strings.NewReader("n\n"), &out)
	copy := strings.ToLower(out.String())

	if !strings.Contains(copy, "already detects") {
		t.Error("copy must say detection already works without this")
	}
	if !strings.Contains(copy, "redundancy") && !strings.Contains(copy, "second") {
		t.Error("copy must frame the hook as a second/redundant signal")
	}
	if !strings.Contains(copy, "[y/n]") {
		t.Error("copy must show the default-No affordance")
	}
	// The pre-amendment framing, which is now false.
	if strings.Contains(copy, "so the attention chime can detect") {
		t.Error("copy still implies the chime depends on the hook; OSC 777 is tier-1")
	}
}

// ── consent states, end to end ───────────────────────────────────────

func TestConsent_DeclinedWritesNothingAgentSide(t *testing.T) {
	proj := consentProject(t, "eng1", "qa1")
	var out bytes.Buffer

	if !EnsureAttentionHooksConsent(proj, strings.NewReader("n\n"), &out) {
		t.Fatal("declining should still RECORD the answer")
	}
	if proj.Attention.HooksGranted() {
		t.Error("declined but recorded as granted")
	}
	if !proj.Attention.HooksAnswered() {
		t.Error("answer was not recorded; the operator would be re-asked forever")
	}
	for _, role := range proj.Roles {
		if _, err := os.Stat(hooks.AgentSettingsPath(proj.Root, role)); !os.IsNotExist(err) {
			t.Errorf("declining wrote a settings file for %s -- consent means no agent-side writes", role)
		}
	}
}

func TestConsent_GrantedInstallsForEveryRole(t *testing.T) {
	proj := consentProject(t, "eng1", "qa1")
	var out bytes.Buffer

	if !EnsureAttentionHooksConsent(proj, strings.NewReader("y\n"), &out) {
		t.Fatal("granting should record the answer")
	}
	if !proj.Attention.HooksGranted() {
		t.Fatal("granted but not recorded")
	}
	for _, role := range proj.Roles {
		raw, err := os.ReadFile(hooks.AgentSettingsPath(proj.Root, role))
		if err != nil {
			t.Fatalf("%s: settings not written: %v", role, err)
		}
		if !strings.Contains(string(raw), hooks.HookCommand) {
			t.Errorf("%s: hook missing from settings", role)
		}
	}
}

// TestConsent_NeverReAsksOnceAnswered is the one-time property. A recorded No
// is indistinguishable from an absent answer unless the config field is a
// pointer -- which is why it is one.
func TestConsent_NeverReAsksOnceAnswered(t *testing.T) {
	for _, answer := range []bool{true, false} {
		proj := consentProject(t, "eng1")
		proj.Attention.Hooks = &answer

		var out bytes.Buffer
		// Feeding "y" proves the prompt is not consulted: if it were, a
		// recorded No would flip to Yes here.
		if EnsureAttentionHooksConsent(proj, strings.NewReader("y\n"), &out) {
			t.Errorf("answer=%v: re-asked an already-answered prompt", answer)
		}
		if out.Len() != 0 {
			t.Errorf("answer=%v: prompted again, output = %q", answer, out.String())
		}
		if proj.Attention.Hooks == nil || *proj.Attention.Hooks != answer {
			t.Errorf("answer=%v: the recorded answer was changed", answer)
		}
	}
}

// TestConsent_UnparseableAgentSettingsDoesNotBlockOtherAgents covers the
// degradation the whole fleet depends on: one agent with a broken settings file
// must not deny the hook to the others, and must not abort startup.
func TestConsent_UnparseableAgentSettingsDoesNotBlockOtherAgents(t *testing.T) {
	proj := consentProject(t, "eng1", "qa1")
	corrupt := []byte("{ not json")
	if err := os.WriteFile(hooks.AgentSettingsPath(proj.Root, "eng1"), corrupt, 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	installed := InstallAttentionHooks(proj, &out)
	if installed != 1 {
		t.Errorf("installed = %d, want 1 (qa1 only; eng1 is unparseable)", installed)
	}
	after, _ := os.ReadFile(hooks.AgentSettingsPath(proj.Root, "eng1"))
	if string(after) != string(corrupt) {
		t.Error("the unparseable agent's file was rewritten")
	}
	if !strings.Contains(out.String(), "eng1") {
		t.Errorf("the operator was not told which agent was skipped: %q", out.String())
	}
}

// TestConsent_HookDeletedAfterConsentDegradesLikeDeclined is the bead's
// silently-absent case: reinstalling is possible, and nothing is wedged.
func TestConsent_HookDeletedAfterConsentDegradesLikeDeclined(t *testing.T) {
	proj := consentProject(t, "eng1")
	var out bytes.Buffer
	EnsureAttentionHooksConsent(proj, strings.NewReader("y\n"), &out)

	// The operator deletes the settings file afterwards.
	if err := os.Remove(hooks.AgentSettingsPath(proj.Root, "eng1")); err != nil {
		t.Fatal(err)
	}
	// Consent is still recorded; nothing is broken; a reinstall restores it.
	if !proj.Attention.HooksGranted() {
		t.Error("deleting the file should not un-record consent")
	}
	if n := InstallAttentionHooks(proj, &out); n != 1 {
		t.Errorf("reinstall after deletion installed %d, want 1", n)
	}
}
