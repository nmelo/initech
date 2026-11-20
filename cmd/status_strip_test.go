package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	iexec "github.com/nmelo/initech/internal/exec"
	"github.com/nmelo/initech/internal/tui"
)

// ini-xmlr: each agent in initech status --json carries "strip", its list
// strip exactly as the agent set it: [] for none, null when initech did not
// read it. GET /v1/status carries the same value.

// stripStatus runs status --json over a list reply and returns eng1's object.
func stripStatus(t *testing.T, panesJSON string) map[string]any {
	t.Helper()
	stdout, _, _ := l6omStatus(t, panesJSON)
	return l6omByRole(t, l6omObjects(t, stdout))["eng1"]
}

// stripJSONList is items as a JSON array literal.
func stripJSONList(t *testing.T, items []string) string {
	t.Helper()
	b, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func stripAny(items []string) []any {
	out := []any{}
	for _, s := range items {
		out = append(out, s)
	}
	return out
}

// AC2: three items, in the agent's order.
func TestStatusStrip_ThreeItemsInOrder(t *testing.T) {
	items := []string{"Release north?", "Put the drone on the bench", "CI runner: register it?"}
	o := stripStatus(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true,"strip":`+stripJSONList(t, items)+`}]`)
	if !reflect.DeepEqual(o["strip"], stripAny(items)) {
		t.Errorf("strip = %#v, want %q in order", o["strip"], items)
	}
}

// AC3: the window answered and the agent has no strip (a Codex pane's
// answer too): [], never null and never absent.
func TestStatusStrip_NoneIsAnEmptyList(t *testing.T) {
	for _, agentType := range []string{"claude-code", "codex"} {
		o := stripStatus(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true,"agent_type":"`+agentType+`","strip":[]}]`)
		if got, ok := o["strip"].([]any); !ok || len(got) != 0 {
			t.Errorf("%s: strip = %#v, want []", agentType, o["strip"])
		}
	}
}

// AC3: a window too old to send strips sends no key: null, never [].
func TestStatusStrip_AnOlderWindowIsNull(t *testing.T) {
	o := stripStatus(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true}]`)
	if v, ok := o["strip"]; !ok || v != nil {
		t.Errorf("strip = %#v (present %v), want null", v, ok)
	}
}

// AC3 and AC5: an agent no window answered for (merged from the peer table)
// is null: initech did not read its strip.
func TestStatusStrip_AnAgentNoWindowAnsweredForIsNull(t *testing.T) {
	skipWindows(t)
	_, dir := ownRigFolder(t)
	sock := tui.SocketPath(dir, "demo")
	serveFakeTUI(t, sock, tui.IPCResponse{OK: true, Data: `[{"name":"here","agents":["eng1"]},{"name":"mac2","agents":["eng9"]}]`})
	panes := mergeRemotePeers(sock, []paneInfo{{Name: "eng1", Alive: true, Strip: &[]string{"mine"}}})
	if len(panes) != 2 || panes[1].Name != "eng9" {
		t.Fatalf("merged %+v, want eng9 from mac2", panes)
	}
	raw, _ := json.Marshal(toAgentJSON(panes[1], nil))
	var o map[string]any
	json.Unmarshal(raw, &o)
	if v, ok := o["strip"]; !ok || v != nil {
		t.Errorf("merged peer strip = %#v (present %v), want null", v, ok)
	}
	raw, _ = json.Marshal(toAgentJSON(panes[0], nil))
	json.Unmarshal(raw, &o)
	if !reflect.DeepEqual(o["strip"], []any{"mine"}) { // the twin: a read list is carried
		t.Errorf("local strip = %#v, want [mine]", o["strip"])
	}
}

// AC4: ten items, one of 300 characters, byte for byte: no mark, no wrap, no
// cut, whatever the characters.
func TestStatusStrip_ItemsAreExactAtTheLimits(t *testing.T) {
	long := string([]rune(strings.Repeat("界\"▶ x", 100))[:300]) // 300 characters, wide, quoted, marked
	items := []string{long}
	for i := 1; i < 10; i++ {
		items = append(items, strings.Repeat("w", i*31)+" end")
	}
	o := stripStatus(t, `[{"name":"eng1","activity":"idle","alive":true,"visible":true,"strip":`+stripJSONList(t, items)+`}]`)
	if !reflect.DeepEqual(o["strip"], stripAny(items)) {
		t.Errorf("strip changed on the way:\n got  %#v\n want %q", o["strip"], items)
	}
}

// AC2's app leg: GET /v1/status, read through the real backend from the same
// list reply, carries the same strip values per agent, unchanged.
func TestStatusStrip_ReachesV1Status(t *testing.T) {
	skipWindows(t)
	restoreColor := disableColor(t)
	defer restoreColor()
	projects, dir := ownRigFolder(t)
	writeBasicConfig(t, dir, "demo")
	binDir := filepath.Join(dir, "bin")
	os.MkdirAll(binDir, 0o755)
	writeExecutable(t, filepath.Join(binDir, "bd"), "#!/bin/sh\necho '[]'\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("INITECH_SOCKET", "")
	items := []string{"one", "two ▶", "three"}
	serveFakeTUI(t, tui.SocketPath(dir, "demo"), tui.IPCResponse{OK: true, Data: `[
	 {"name":"eng1","activity":"idle","alive":true,"visible":true,"banner":"none","strip":` + stripJSONList(t, items) + `},
	 {"name":"eng2","activity":"idle","alive":true,"visible":true,"banner":"none","strip":[]},
	 {"name":"eng3","activity":"idle","alive":true,"visible":true,"banner":"none"}]`})
	defer chdirForTest(t, t.TempDir())()

	b := &appBackend{dirs: []string{projects}, runner: &iexec.DefaultRunner{}}
	s := appserve.New(appserve.Config{Backend: b, Digest: sha256.Sum256([]byte("tok")), HasToken: true})
	req := httptest.NewRequest("GET", "/v1/status/demo", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/status/demo: %d %s", rec.Code, rec.Body.String())
	}
	var rig struct {
		Agents []map[string]any `json:"agents"`
		Error  string           `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(rec.Body.Bytes())).Decode(&rig); err != nil || rig.Error != "" || len(rig.Agents) != 3 {
		t.Fatalf("status %+v %v:\n%s", rig, err, rec.Body.String())
	}
	want := map[string]any{"eng1": stripAny(items), "eng2": []any{}, "eng3": nil}
	for _, a := range rig.Agents {
		role, _ := a["role"].(string)
		v, ok := a["strip"]
		if !ok || !reflect.DeepEqual(v, want[role]) {
			t.Errorf("%s: strip %#v (present %v), want %#v", role, v, ok, want[role])
		}
	}
}
