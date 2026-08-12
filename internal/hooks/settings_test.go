package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for ini-2x8.4's create-or-merge install.

// realWorldSettings is the SHAPE captured from the live project's own
// .claude/settings.json (bd prime + tokf), used as the merge fixture. Modeled
// on a real file rather than invented, so the merge is exercised against the
// nesting Claude Code actually produces -- and so the preservation assertions
// name hooks that really exist in an operator's tree.
//
// initech never writes the project-root file this was copied from; it is the
// operator's own tooling. It is reproduced here only as realistic per-agent
// content, which is the file initech does write.
const realWorldSettings = `{
  "hooks": {
    "PreCompact": [
      {"hooks": [{"command": "bd prime", "type": "command"}], "matcher": ""}
    ],
    "PreToolUse": [
      {"hooks": [{"command": "'/Users/nmelo/Desktop/Projects/initech/.tokf/hooks/pre-tool-use.sh'", "type": "command"}], "matcher": "Bash"}
    ],
    "SessionStart": [
      {"hooks": [{"command": "bd prime", "type": "command"}], "matcher": ""}
    ]
  },
  "permissions": {"allow": ["Bash(make test:*)"]}
}`

func roleDir(t *testing.T) (root, role string) {
	t.Helper()
	root = t.TempDir()
	role = "eng1"
	if err := os.MkdirAll(filepath.Join(root, role, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	return root, role
}

func readSettings(t *testing.T, root, role string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(AgentSettingsPath(root, role))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("settings not valid JSON after write: %v\n%s", err, raw)
	}
	return m
}

// commandsIn flattens every hook command in the file, so preservation can be
// asserted BY NAME rather than by counting.
func commandsIn(t *testing.T, settings map[string]any) []string {
	t.Helper()
	var out []string
	hooksMap, _ := settings["hooks"].(map[string]any)
	for _, groups := range hooksMap {
		gs, _ := groups.([]any)
		for _, g := range gs {
			gm, _ := g.(map[string]any)
			inner, _ := gm["hooks"].([]any)
			for _, h := range inner {
				hm, _ := h.(map[string]any)
				if c, ok := hm["command"].(string); ok {
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func containsCmd(cmds []string, want string) bool {
	for _, c := range cmds {
		if strings.Contains(c, want) {
			return true
		}
	}
	return false
}

// ── create path ──────────────────────────────────────────────────────

// TestInstall_CreatesWhenNoSettingsExist covers the case the spec's premise
// missed: scaffold makes the .claude/ DIRECTORY and no settings.json, so
// create -- not merge -- is what happens on every agent today.
func TestInstall_CreatesWhenNoSettingsExist(t *testing.T) {
	root, role := roleDir(t)

	res, err := InstallNotificationHook(root, role)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Created || res.Merged {
		t.Errorf("result = %+v, want Created", res)
	}
	if cmds := commandsIn(t, readSettings(t, root, role)); !containsCmd(cmds, HookCommand) {
		t.Errorf("hook not installed; commands = %v", cmds)
	}
}

func TestInstall_CreatesParentDirIfMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallNotificationHook(root, "eng9"); err != nil {
		t.Fatalf("install into a role with no .claude/ dir: %v", err)
	}
	if _, err := os.Stat(AgentSettingsPath(root, "eng9")); err != nil {
		t.Errorf("settings not created: %v", err)
	}
}

// ── merge path: base-and-new ON THE INPUT ────────────────────────────

// TestInstall_MergePreservesForeignHooks is the base-and-new verification.
//
// It asserts the PRE-write file provably contains the specific foreign hooks
// BY NAME first. That ordering is the whole point: "the file still has stuff in
// it afterwards" passes vacuously against an empty base, which is the
// ini-9ka.10 grooming-overwrite family -- a preservation claim that never had
// anything to preserve.
func TestInstall_MergePreservesForeignHooks(t *testing.T) {
	root, role := roleDir(t)
	path := AgentSettingsPath(root, role)
	if err := os.WriteFile(path, []byte(realWorldSettings), 0644); err != nil {
		t.Fatal(err)
	}

	// BASE: prove the input contains what we mean to preserve.
	var base map[string]any
	if err := json.Unmarshal([]byte(realWorldSettings), &base); err != nil {
		t.Fatal(err)
	}
	baseCmds := commandsIn(t, base)
	for _, want := range []string{"bd prime", "pre-tool-use.sh"} {
		if !containsCmd(baseCmds, want) {
			t.Fatalf("PRECONDITION: base must contain %q, else preservation passes vacuously; base = %v", want, baseCmds)
		}
	}
	if _, ok := base["permissions"]; !ok {
		t.Fatal("PRECONDITION: base must contain a non-hooks key to prove unknown keys survive")
	}

	res, err := InstallNotificationHook(root, role)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if !res.Merged || res.Created {
		t.Errorf("result = %+v, want Merged", res)
	}

	// NEW: every base command survives AND ours is present.
	after := readSettings(t, root, role)
	afterCmds := commandsIn(t, after)
	for _, want := range []string{"bd prime", "pre-tool-use.sh"} {
		if !containsCmd(afterCmds, want) {
			t.Errorf("merge clobbered a foreign hook %q; after = %v", want, afterCmds)
		}
	}
	if !containsCmd(afterCmds, HookCommand) {
		t.Errorf("merge did not add our hook; after = %v", afterCmds)
	}
	// Unknown TOP-LEVEL keys survive too -- a typed-struct decode would have
	// silently dropped this, which is how a merge clobbers invisibly.
	if _, ok := after["permissions"]; !ok {
		t.Error("merge dropped the unknown top-level 'permissions' key")
	}
	// And the three original families are all still present.
	hooksMap, _ := after["hooks"].(map[string]any)
	for _, fam := range []string{"PreCompact", "PreToolUse", "SessionStart"} {
		if _, ok := hooksMap[fam]; !ok {
			t.Errorf("merge dropped the %q hook family", fam)
		}
	}
}

// TestInstall_IsIdempotent guards against a second install duplicating the
// entry, which would run the hook twice per dialog.
func TestInstall_IsIdempotent(t *testing.T) {
	root, role := roleDir(t)
	if _, err := InstallNotificationHook(root, role); err != nil {
		t.Fatal(err)
	}
	res, err := InstallNotificationHook(root, role)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Unchanged {
		t.Errorf("second install result = %+v, want Unchanged", res)
	}
	count := 0
	for _, c := range commandsIn(t, readSettings(t, root, role)) {
		if c == HookCommand {
			count++
		}
	}
	if count != 1 {
		t.Errorf("hook appears %d times, want exactly 1", count)
	}
}

// ── unparseable: never rewrite ───────────────────────────────────────

// TestInstall_UnparseableSettingsAreLeftUntouched is the ini-9ka.9 rule applied
// to a file initech does not own: a corrupt config is not an absent one, so the
// install refuses rather than replacing the operator's real settings with a
// near-empty file carrying only our hook.
func TestInstall_UnparseableSettingsAreLeftUntouched(t *testing.T) {
	root, role := roleDir(t)
	path := AgentSettingsPath(root, role)
	corrupt := []byte("{ this is not json, but it IS the operator's file")
	if err := os.WriteFile(path, corrupt, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallNotificationHook(root, role); err != ErrSettingsUnparseable {
		t.Fatalf("err = %v, want ErrSettingsUnparseable", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file was removed: %v", err)
	}
	if string(after) != string(corrupt) {
		t.Errorf("the unparseable file was rewritten:\nwant %q\ngot  %q", corrupt, after)
	}
}

// TestInstall_WrongTypedHooksKeyIsTreatedAsUnparseable covers a file that is
// valid JSON but not a shape we understand. Overwriting what we cannot read is
// the same failure as overwriting what we cannot parse.
func TestInstall_WrongTypedHooksKeyIsTreatedAsUnparseable(t *testing.T) {
	root, role := roleDir(t)
	path := AgentSettingsPath(root, role)
	weird := []byte(`{"hooks": "not-an-object"}`)
	if err := os.WriteFile(path, weird, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallNotificationHook(root, role); err != ErrSettingsUnparseable {
		t.Fatalf("err = %v, want ErrSettingsUnparseable", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(weird) {
		t.Error("a wrong-typed settings file was rewritten")
	}
}

// ── removal (consent withdrawn) ──────────────────────────────────────

func TestRemove_LeavesForeignHooksIntact(t *testing.T) {
	root, role := roleDir(t)
	path := AgentSettingsPath(root, role)
	if err := os.WriteFile(path, []byte(realWorldSettings), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallNotificationHook(root, role); err != nil {
		t.Fatal(err)
	}
	if _, err := RemoveNotificationHook(root, role); err != nil {
		t.Fatal(err)
	}

	cmds := commandsIn(t, readSettings(t, root, role))
	if containsCmd(cmds, HookCommand) {
		t.Error("remove left our hook behind")
	}
	for _, want := range []string{"bd prime", "pre-tool-use.sh"} {
		if !containsCmd(cmds, want) {
			t.Errorf("remove clobbered foreign hook %q; got %v", want, cmds)
		}
	}
}

func TestRemove_MissingFileIsNotAnError(t *testing.T) {
	root, role := roleDir(t)
	res, err := RemoveNotificationHook(root, role)
	if err != nil {
		t.Fatalf("remove with no settings file: %v", err)
	}
	if !res.Unchanged {
		t.Errorf("result = %+v, want Unchanged", res)
	}
}
