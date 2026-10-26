package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-l6om: initech status --json prints one object per agent. Every key is
// always present; a value initech did not read is null.

// l6omStatus runs initech status --json against a fake socket whose list
// reply carries panesJSON, and returns stdout, stderr and the list request.
func l6omStatus(t *testing.T, panesJSON string) (string, string, tui.IPCRequest) {
	t.Helper()
	return l6omRun(t, panesJSON, true)
}

// l6omRun is l6omStatus with or without --json.
func l6omRun(t *testing.T, panesJSON string, asJSON bool) (string, string, tui.IPCRequest) {
	t.Helper()
	restoreColor := disableColor(t)
	defer restoreColor()

	projectDir := shortProjectDir(t)
	writeBasicConfig(t, projectDir, "demo")
	restoreWD := chdirForTest(t, projectDir)
	defer restoreWD()

	// --json must not need bd: beads come from the ribbon. A bd that fails
	// shows on stderr if --json asks it.
	binDir := filepath.Join(projectDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\necho bd was called >&2\nexit 1\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	respBytes, err := json.Marshal(tui.IPCResponse{OK: true, Data: panesJSON})
	if err != nil {
		t.Fatalf("Marshal IPC response: %v", err)
	}
	reqCh, cleanup := startATIPCServer(t, tui.SocketPath(projectDir, "demo"), responseMode{raw: string(respBytes) + "\n"})
	defer cleanup()

	statusJSON = asJSON
	defer func() { statusJSON = false }()
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if err := runStatus(cmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}
	var req tui.IPCRequest
	waitATRequest(t, reqCh, &req)
	return out.String(), errOut.String(), req
}

// l6omObjects decodes stdout, failing unless it is exactly one JSON array.
func l6omObjects(t *testing.T, stdout string) []map[string]any {
	t.Helper()
	var objs []map[string]any
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&objs); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if dec.More() {
		t.Fatalf("stdout has more than the array:\n%s", stdout)
	}
	return objs
}

var l6omKeys = []string{"role", "host", "state", "alive", "visible", "beads", "bead_note",
	"context_percent", "background", "banner", "banner_age_sec", "queued_count", "account",
	"background_age_sec"}

func l6omByRole(t *testing.T, objs []map[string]any) map[string]map[string]any {
	t.Helper()
	m := map[string]map[string]any{}
	for _, o := range objs {
		for _, k := range l6omKeys {
			if _, ok := o[k]; !ok {
				t.Errorf("%v: key %q missing", o["role"], k)
			}
		}
		if len(o) != len(l6omKeys) {
			t.Errorf("%v: %d keys, want %d: %v", o["role"], len(o), len(l6omKeys), o)
		}
		m[o["role"].(string)] = o
	}
	return m
}

// Criterion 1 and 3: a new TUI's reply, every field carried through.
func TestStatusJSON_ANewReplyCarriesEveryField(t *testing.T) {
	panes := `[
	 {"name":"eng1","activity":"running","alive":true,"visible":true,"beads":["ini-a","ini-b"],
	  "context_percent":42,"background":{"shells":1,"monitors":0,"tasks":2},"banner":"none",
	  "account":{"user":"Personal","model":"Haiku 5.5"}},
	 {"name":"eng2","activity":"waiting","alive":true,"visible":false,"beads":[],"no_bead":"look at the logs",
	  "modal_latched":true,"modal_age_sec":75,"queued_count":3,"banner":"permission_prompt"}
	]`
	stdout, stderr, req := l6omStatus(t, panes)
	if req.Action != "list" || !req.Detail {
		t.Errorf("request = %+v, want list with detail", req)
	}
	if stderr != "" {
		t.Errorf("stderr: %q", stderr)
	}
	got := l6omByRole(t, l6omObjects(t, stdout))
	want := map[string]map[string]any{
		"eng1": {"role": "eng1", "host": "", "state": "running", "alive": true, "visible": true,
			"beads": []any{"ini-a", "ini-b"}, "bead_note": "", "context_percent": 42.0,
			"background": map[string]any{"shells": 1.0, "monitors": 0.0, "tasks": 2.0},
			"banner":     "none", "banner_age_sec": nil, "queued_count": 0.0,
			"account": map[string]any{"user": "Personal", "model": "Haiku 5.5"}, "background_age_sec": nil},
		"eng2": {"role": "eng2", "host": "", "state": "waiting", "alive": true, "visible": false,
			"beads": []any{}, "bead_note": "look at the logs", "context_percent": nil,
			"background": map[string]any{"shells": nil, "monitors": nil, "tasks": nil},
			"banner":     "permission_prompt", "banner_age_sec": 75.0, "queued_count": 3.0,
			"account": map[string]any{"user": nil, "model": nil}, "background_age_sec": nil},
	}
	for role, w := range want {
		if !reflect.DeepEqual(got[role], w) {
			t.Errorf("%s:\n got  %v\n want %v", role, got[role], w)
		}
	}
}

// Criterion 2: state is the activity label, stopped for an agent whose
// process exited, suspended for a suspended one; any other label is null.
func TestStatusJSON_StateMapsEveryLabel(t *testing.T) {
	panes := `[
	 {"name":"a","activity":"running","alive":true},
	 {"name":"b","activity":"idle","alive":true},
	 {"name":"c","activity":"waiting","alive":true},
	 {"name":"d","activity":"suspended","alive":false},
	 {"name":"e","activity":"dead","alive":false},
	 {"name":"f","activity":"idle","alive":false},
	 {"name":"g","activity":"unknown","alive":true},
	 {"name":"h","activity":"connected","alive":true}
	]`
	stdout, _, _ := l6omStatus(t, panes)
	got := l6omByRole(t, l6omObjects(t, stdout))
	want := map[string]any{"a": "running", "b": "idle", "c": "waiting", "d": "suspended",
		"e": "stopped", "f": "stopped", "g": nil, "h": nil}
	for role, w := range want {
		if got[role]["state"] != w {
			t.Errorf("%s: state %v, want %v", role, got[role]["state"], w)
		}
	}
}

// Criterion 5: an older TUI sends none of the new fields and no beads field:
// every one reads null, and the objects still have every key.
func TestStatusJSON_AnOlderTUIReadsNull(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true}]`)
	o := l6omByRole(t, l6omObjects(t, stdout))["eng1"]
	for _, k := range []string{"beads", "context_percent", "banner", "banner_age_sec"} {
		if o[k] != nil {
			t.Errorf("%s = %v, want null", k, o[k])
		}
	}
	if !reflect.DeepEqual(o["background"], map[string]any{"shells": nil, "monitors": nil, "tasks": nil}) {
		t.Errorf("background = %v", o["background"])
	}
	if !reflect.DeepEqual(o["account"], map[string]any{"user": nil, "model": nil}) {
		t.Errorf("account = %v", o["account"])
	}
}

// No agents is an empty array, not null.
func TestStatusJSON_NoAgentsIsAnEmptyArray(t *testing.T) {
	stdout, _, _ := l6omStatus(t, `[]`)
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("stdout = %q, want []", stdout)
	}
}

// Without --json the request asks for no detail, so the table costs the TUI
// no screen reads.
func TestStatus_TheTableAsksForNoDetail(t *testing.T) {
	_, _, req := l6omRun(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true}]`, false)
	if req.Detail {
		t.Error("the table's list request asks for detail")
	}
}
