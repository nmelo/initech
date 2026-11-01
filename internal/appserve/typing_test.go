package appserve

import (
	"encoding/json"
	"fmt"
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
	sent  []string // "rig/pane/enter/text" per Send call
	reply Typed
}

func (b *typingBackend) Send(r Rig, pane, text string, enter bool) Typed {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, fmt.Sprintf("%s/%s/%v/%s", r.Name, pane, enter, text))
	return b.reply
}

func (b *typingBackend) sends() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.sent...)
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

// typingServer is the service as New registers it, typing routes and token
// gate included.
func typingServer(b Backend) http.Handler {
	now := time.Unix(1_800_000_000, 0)
	return newTestServer(b, &now).Handler()
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

// AC3: a text reaches the backend once, verbatim, for that rig and pane,
// with one Enter unless the body says enter false; the answer is the
// backend's verdict and words, unchanged.
func TestSend_TheAnswerIsTheCommandsVerdictAndWords(t *testing.T) {
	for _, c := range []struct {
		body, want string
	}{
		{`{"text":"hello \"there\" & $x"}`, `initech/a/true/hello "there" & $x`},
		{`{"text":"line one\nline two","enter":true}`, "initech/a/true/line one\nline two"},
		{`{"text":"draft","enter":false}`, "initech/a/false/draft"},
	} {
		for _, reply := range []Typed{
			{Verdict: "delivered", Words: "delivered to a"},
			{Verdict: "deferred", Words: "deferred to a — target has a modal open; will deliver when it closes"},
			{Verdict: "held", Words: "held: a is in do-not-disturb (2m, 0 messages ahead)"},
		} {
			b := newTypingBackend()
			b.reply = reply
			code, got, _ := post(t, typingServer(b), "/v1/send/initech/a", bearer, c.body)
			if code != http.StatusOK || got != reply {
				t.Errorf("%s: %d %+v, want 200 %+v", c.body, code, got, reply)
			}
			if s := b.sends(); len(s) != 1 || s[0] != strings.ReplaceAll(c.want, "\\n", "\n") {
				t.Errorf("%s: sent %q, want %q", c.body, s, c.want)
			}
		}
	}
}

// AC5: every malformed send is refused with words and nothing sent.
func TestSend_RefusalsSendNothing(t *testing.T) {
	for name, c := range map[string]struct {
		path, body string
		code       int
		words      string
	}{
		"empty text":        {"/v1/send/initech/a", `{"text":""}`, 400, "nothing to send: the text is empty"},
		"no text":           {"/v1/send/initech/a", `{"enter":true}`, 400, "nothing to send: the text is empty"},
		"an empty body":     {"/v1/send/initech/a", ``, 400, "the body is empty; it must be a JSON object"},
		"malformed JSON":    {"/v1/send/initech/a", `{"text":"x"`, 400, "the body is not a JSON object: "},
		"text not a string": {"/v1/send/initech/a", `{"text":5}`, 400, "the body is not a JSON object: "},
		"an unknown rig":    {"/v1/send/nope/a", `{"text":"x"}`, 404, `no running rig "nope"`},
		"an unknown pane":   {"/v1/send/initech/zz", `{"text":"x"}`, 404, `pane "zz" not found in rig "initech"`},
	} {
		b := newTypingBackend()
		code, got, _ := post(t, typingServer(b), c.path, bearer, c.body)
		if code != c.code || got.Verdict != VerdictRefused || !strings.HasPrefix(got.Words, c.words) {
			t.Errorf("%s: %d %+v, want %d refused %q", name, code, got, c.code, c.words)
		}
		if s := b.sends(); len(s) != 0 {
			t.Errorf("%s: sent %v", name, s)
		}
	}
	if code, got, _ := post(t, typingServer(newTypingBackend().fakeBackend), "/v1/send/initech/a", bearer, `{"text":"x"}`); code != http.StatusNotImplemented || got.Verdict != VerdictRefused {
		t.Errorf("a backend that cannot send: %d %+v", code, got)
	}
}

// AC4: the send route is behind the same token gate.
func TestSend_TheTokenGateGuardsTheRoute(t *testing.T) {
	for _, auth := range []string{"", "Bearer wrong", testToken} {
		b := newTypingBackend()
		code, _, raw := post(t, typingServer(b), "/v1/send/initech/a", auth, `{"text":"x"}`)
		if code != http.StatusUnauthorized || raw != "" || len(b.sends()) != 0 {
			t.Errorf("auth %q: %d %q, sent %v", auth, code, raw, b.sends())
		}
	}
}
