package appserve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func putPins(t *testing.T, h http.Handler, auth, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("PUT", "/v1/pins", strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The pinned list is stored on the Mac: none stored is [] (AC6
// feed-it-nothing), a PUT replaces it and GET returns it in order, the file
// is 0600, and both routes are behind the token.
func TestPins_StoredOnTheMacInOrderBehindTheToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	now := time.Unix(1_800_000_000, 0)
	s := New(Config{Backend: &fakeBackend{}, Digest: shaToken(), HasToken: true, Now: func() time.Time { return now }, StateDir: dir})
	h := s.Handler()
	auth := "Bearer " + testToken

	if code, body := get(t, h, "/v1/pins", auth); code != 200 || strings.TrimSpace(body) != "[]" {
		t.Fatalf("no pins stored: %d %s, want []", code, body)
	}
	list := `[{"rig":"theguy","pane":"super","name":"theguy","order":2},{"rig":"initech","pane":"eng9","name":"eng9 on the Rust twin","order":1}]`
	if code, body := putPins(t, h, auth, list); code != 200 {
		t.Fatalf("PUT: %d %s", code, body)
	}
	_, body := get(t, h, "/v1/pins", auth)
	var pins []Pin
	if err := json.Unmarshal([]byte(body), &pins); err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 || pins[0].Pane != "eng9" || pins[0].Name != "eng9 on the Rust twin" || pins[1].Rig != "theguy" {
		t.Errorf("pins = %+v, want eng9 then theguy's super", pins)
	}
	if st, err := os.Stat(filepath.Join(dir, PinsFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("pins file mode: %v %v", st, err)
	}

	// A second server over the same folder (a restart) has the same list.
	s2 := New(Config{Backend: &fakeBackend{}, Digest: shaToken(), HasToken: true, StateDir: dir})
	if _, body2 := get(t, s2.Handler(), "/v1/pins", auth); body2 != body {
		t.Errorf("after a restart: %s, want %s", body2, body)
	}

	if code, _ := putPins(t, h, auth, "[]"); code != 200 {
		t.Errorf("emptying the list: %d", code)
	}
	if _, body := get(t, h, "/v1/pins", auth); strings.TrimSpace(body) != "[]" {
		t.Errorf("after emptying: %s", body)
	}

	for _, bad := range []string{"", "  ", "a", "wrong"} {
		if code, body := putPins(t, h, bad, list); code != 401 || body != "" {
			t.Errorf("PUT with %q: %d %q, want 401 and no body", bad, code, body)
		}
	}
}

// A PUT that is not a valid list changes nothing: a pin with no pane, the
// same pane twice, a name too long, an unknown field, null, or not JSON.
func TestPins_ABadListIsRefusedAndTheStoredOneStands(t *testing.T) {
	dir := t.TempDir()
	s := New(Config{Backend: &fakeBackend{}, Digest: shaToken(), HasToken: true, StateDir: dir})
	h := s.Handler()
	auth := "Bearer " + testToken
	good := `[{"rig":"r","pane":"p","name":"n","order":0}]`
	if code, _ := putPins(t, h, auth, good); code != 200 {
		t.Fatal("good list refused")
	}
	for name, body := range map[string]string{
		"no pane":       `[{"rig":"r","pane":"","name":"n","order":0}]`,
		"twice":         `[{"rig":"r","pane":"p","name":"a","order":0},{"rig":"r","pane":"p","name":"b","order":1}]`,
		"long name":     `[{"rig":"r","pane":"p","name":"` + strings.Repeat("x", MaxPinName+1) + `","order":0}]`,
		"unknown field": `[{"rig":"r","pane":"p","name":"n","order":0,"body":"x"}]`,
		"null":          `null`,
		"not json":      `{`,
	} {
		if code, _ := putPins(t, h, auth, body); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if _, body := get(t, h, "/v1/pins", auth); !strings.Contains(body, `"pane":"p"`) || strings.Count(body, `"rig"`) != 1 {
		t.Errorf("after refused PUTs the list changed: %s", body)
	}
}
