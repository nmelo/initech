package appserve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ini-r0q.11: POST /v1/keys/{rig}/{pane} (and /v1/send) type into a pane as
// the operator through the backend; refusals type nothing.

// typingBackend is fakeBackend that can type, counting every key pressed.
type typingBackend struct {
	*fakeBackend
	mu    sync.Mutex
	keys  []string // "rig/pane/key" per Key call
	reply Typed
}

func (b *typingBackend) KeyRefusal(pane, key string) (string, bool) {
	switch key {
	case "enter", "esc", "ctrl-c", "y", "n":
		return "", true
	}
	return "not delivered to " + pane + ": unknown key " + `"` + key + `"`, false
}

func (b *typingBackend) Key(r Rig, pane, key string) Typed {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keys = append(b.keys, r.Name+"/"+pane+"/"+key)
	return b.reply
}

func (b *typingBackend) pressed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.keys...)
}

func newTypingBackend() *typingBackend {
	return &typingBackend{
		fakeBackend: &fakeBackend{
			rigs:  []Rig{{Name: "initech", Root: "/r/initech", Socket: "/r/initech/.initech/initech.sock"}},
			panes: map[string][]PaneState{"/r/initech": {{Name: "a", State: "waiting"}, {Name: "b", State: "idle"}}},
		},
		reply: Typed{Verdict: "delivered", Words: "key enter delivered to a"},
	}
}

// typingServer is the service with the typing routes registered as
// server.go registers them (eng5's two lines), behind the same token gate.
func typingServer(b Backend) http.Handler {
	now := time.Unix(1_800_000_000, 0)
	s := newTestServer(b, &now)
	s.mux.HandleFunc("POST /v1/keys/{rig}/{pane}", s.handleKeys)
	return s.Handler()
}

func post(t *testing.T, h http.Handler, path, auth, body string) (int, Typed, string) {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	raw := rec.Body.String()
	var got Typed
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: body %q is not {verdict, words}: %v", path, raw, err)
		}
	}
	return rec.Code, got, raw
}

const bearer = "Bearer " + testToken

// AC1: a key the command takes reaches the backend once, for that rig and
// pane, and the answer is the backend's own verdict and words, unchanged.
func TestKeys_TheAnswerIsTheCommandsVerdictAndWords(t *testing.T) {
	for _, reply := range []Typed{
		{Verdict: "delivered", Words: "key esc delivered to a"},
		{Verdict: "not_delivered", Words: "not delivered to a: a is in do-not-disturb (2m); a key is never queued"},
	} {
		b := newTypingBackend()
		b.reply = reply
		code, got, _ := post(t, typingServer(b), "/v1/keys/initech/a", bearer, `{"key":"esc"}`)
		if code != http.StatusOK || got != reply {
			t.Errorf("answer %d %+v, want 200 %+v", code, got, reply)
		}
		if p := b.pressed(); len(p) != 1 || p[0] != "initech/a/esc" {
			t.Errorf("pressed %v, want one esc for initech/a", p)
		}
	}
}

// AC2, AC5: a key outside the five, and every malformed request, is turned
// away with {verdict: refused, words} and nothing pressed.
func TestKeys_RefusalsPressNothing(t *testing.T) {
	for name, c := range map[string]struct {
		path, body string
		code       int
		words      string
	}{
		"a key outside the five": {"/v1/keys/initech/a", `{"key":"tab"}`, 400, `not delivered to a: unknown key "tab"`},
		"an empty key":           {"/v1/keys/initech/a", `{"key":""}`, 400, `not delivered to a: unknown key ""`},
		"no key":                 {"/v1/keys/initech/a", `{}`, 400, `not delivered to a: unknown key ""`},
		"an empty body":          {"/v1/keys/initech/a", ``, 400, "the body is empty; it must be a JSON object"},
		"malformed JSON":         {"/v1/keys/initech/a", `{"key":`, 400, "the body is not a JSON object: "},
		"a body too large":       {"/v1/keys/initech/a", `{"key":"` + strings.Repeat("y", 5000) + `"}`, 400, "the body is over 4096 bytes"},
		"an unknown rig":         {"/v1/keys/nope/a", `{"key":"y"}`, 404, `no running rig "nope"`},
		"an unknown pane":        {"/v1/keys/initech/zz", `{"key":"y"}`, 404, `pane "zz" not found in rig "initech"`},
	} {
		b := newTypingBackend()
		code, got, _ := post(t, typingServer(b), c.path, bearer, c.body)
		if code != c.code || got.Verdict != VerdictRefused || !strings.HasPrefix(got.Words, c.words) {
			t.Errorf("%s: %d %+v, want %d refused %q", name, code, got, c.code, c.words)
		}
		if p := b.pressed(); len(p) != 0 {
			t.Errorf("%s: pressed %v", name, p)
		}
	}
}

// A rig whose TUI stopped answering after the scan: not found, nothing
// pressed.
func TestKeys_ARigThatStoppedAnsweringIsNotFound(t *testing.T) {
	b := newTypingBackend()
	h := typingServer(b)
	if code, _, _ := post(t, h, "/v1/keys/initech/a", bearer, `{"key":"y"}`); code != http.StatusOK {
		t.Fatalf("precondition: %d", code)
	}
	b.fakeBackend.mu.Lock()
	b.down = map[string]bool{"/r/initech": true}
	b.fakeBackend.mu.Unlock()
	code, got, _ := post(t, h, "/v1/keys/initech/a", bearer, `{"key":"y"}`)
	if code != http.StatusNotFound || got.Verdict != VerdictRefused || got.Words != `no running rig "initech"` {
		t.Errorf("%d %+v", code, got)
	}
	if p := b.pressed(); len(p) != 1 {
		t.Errorf("pressed %v, want only the first key", p)
	}
}

// A backend that cannot type answers 501 refused.
func TestKeys_ABackendThatCannotTypeIs501(t *testing.T) {
	code, got, _ := post(t, typingServer(newTypingBackend().fakeBackend), "/v1/keys/initech/a", bearer, `{"key":"y"}`)
	if code != http.StatusNotImplemented || got.Verdict != VerdictRefused || got.Words == "" {
		t.Errorf("%d %+v", code, got)
	}
}

// AC4: no token, a wrong one: 401 with no body, nothing pressed, through the
// same gate as every route.
func TestKeys_TheTokenGateGuardsTheRoute(t *testing.T) {
	for _, auth := range []string{"", "Bearer wrong", testToken} {
		b := newTypingBackend()
		code, _, raw := post(t, typingServer(b), "/v1/keys/initech/a", auth, `{"key":"y"}`)
		if code != http.StatusUnauthorized || raw != "" || len(b.pressed()) != 0 {
			t.Errorf("auth %q: %d %q, pressed %v", auth, code, raw, b.pressed())
		}
	}
}

// Only POST reaches the handler.
func TestKeys_OnlyPost(t *testing.T) {
	b := newTypingBackend()
	if code, _ := get(t, typingServer(b), "/v1/keys/initech/a", bearer); code != http.StatusMethodNotAllowed || len(b.pressed()) != 0 {
		t.Errorf("GET answered %d, pressed %v", code, b.pressed())
	}
}
