package appserve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deviceReq(t *testing.T, h http.Handler, method, path, auth, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code == http.StatusUnauthorized && rec.Body.Len() != 0 {
		t.Errorf("a 401 carried a body: %q", rec.Body.String())
	}
	return rec.Code, out
}

func deviceServer(t *testing.T, dir string) http.Handler {
	t.Helper()
	now := time.Unix(1_800_000_000, 0)
	return New(Config{Backend: &fakeBackend{}, Digest: shaToken(), HasToken: true, Now: func() time.Time { return now }, StateDir: dir}).Handler()
}

const devA = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

// ini-r0q.16 AC4: PUT registers once however often it comes, in any case of
// hex; DELETE removes; the file is 0600, holds only the tokens and when they
// came, and survives a restart; both routes are behind the token.
func TestDevices_RegisterOnceRemoveAndSurviveARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	h := deviceServer(t, dir)
	auth := "Bearer " + testToken
	for i, tok := range []string{devA, devA, strings.ToUpper(devA)} {
		code, out := deviceReq(t, h, "PUT", "/v1/devices", auth, `{"token":"`+tok+`"}`)
		if code != 200 || out["devices"] != float64(1) {
			t.Fatalf("PUT %d: %d %v, want 200 and one device", i, code, out)
		}
	}
	st, err := os.Stat(filepath.Join(dir, DevicesFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("devices file: %v %v, want mode 0600", st, err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, DevicesFile))
	var doc map[string][]map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc["devices"]) != 1 || doc["devices"][0]["token"] != devA || doc["devices"][0]["added"] == nil || len(doc["devices"][0]) != 2 {
		t.Errorf("file = %s (%v), want {devices: [{token, added}]}", raw, err)
	}

	if code, out := deviceReq(t, h, "PUT", "/v1/devices", auth, `{"token":"ab12","name":"phone"}`); code != 400 || out["verdict"] != "refused" {
		t.Errorf("an unknown field with room to spare: %d %v, want 400 refused", code, out)
	}

	h2 := deviceServer(t, dir) // a restart
	code, out := deviceReq(t, h2, "PUT", "/v1/devices", auth, `{"token":"0f0f"}`)
	if code != 200 || out["devices"] != float64(2) {
		t.Errorf("after a restart, a second device: %d %v, want 2", code, out)
	}
	code, out = deviceReq(t, h2, "DELETE", "/v1/devices/"+devA, auth, "")
	if code != 200 || out["devices"] != float64(1) {
		t.Errorf("DELETE: %d %v", code, out)
	}
	code, out = deviceReq(t, h2, "DELETE", "/v1/devices/"+devA, auth, "")
	if code != 404 || out["verdict"] != "refused" || out["words"] == "" {
		t.Errorf("DELETE again: %d %v, want 404 refused with words", code, out)
	}
	for _, bad := range []string{"", "Bearer wrong"} {
		if code, _ := deviceReq(t, h2, "PUT", "/v1/devices", bad, `{"token":"ab"}`); code != 401 {
			t.Errorf("PUT with %q: %d", bad, code)
		}
		if code, _ := deviceReq(t, h2, "DELETE", "/v1/devices/0f0f", bad, ""); code != 401 {
			t.Errorf("DELETE with %q: %d", bad, code)
		}
	}
	if d, _ := LoadDevices(dir, time.Now); len(d.Tokens()) != 1 || d.Tokens()[0] != "0f0f" {
		t.Errorf("stored after the refusals: %v", d.Tokens())
	}
}

// ini-r0q.16 AC4: the 11th device, a token that is not hex or is over 200
// characters, a bad body and an unknown field are refused with 400 and
// words, and the stored set stands.
func TestDevices_RefusalsLeaveTheSetStanding(t *testing.T) {
	dir := t.TempDir()
	h := deviceServer(t, dir)
	auth := "Bearer " + testToken
	for i := 0; i < MaxDevices; i++ {
		if code, out := deviceReq(t, h, "PUT", "/v1/devices", auth, fmt.Sprintf(`{"token":"%04x"}`, i)); code != 200 {
			t.Fatalf("device %d: %d %v", i, code, out)
		}
	}
	before, _ := os.ReadFile(filepath.Join(dir, DevicesFile))
	for name, body := range map[string]string{
		"an 11th":       `{"token":"ffff"}`,
		"not hex":       `{"token":"zz12"}`,
		"too long":      `{"token":"` + strings.Repeat("a", MaxDeviceTokenLen+2) + `"}`,
		"empty token":   `{"token":""}`,
		"no token":      `{}`,
		"unknown field": `{"token":"abcd","name":"phone"}`,
		"not json":      `{`,
		"a list":        `["abcd"]`,
	} {
		code, out := deviceReq(t, h, "PUT", "/v1/devices", auth, body)
		if code != 400 || out["verdict"] != "refused" || out["words"] == "" {
			t.Errorf("%s: %d %v, want 400 refused with words", name, code, out)
		}
	}
	if code, out := deviceReq(t, h, "PUT", "/v1/devices", auth, `{"token":"0003"}`); code != 200 || out["devices"] != float64(MaxDevices) {
		t.Errorf("a registered token at the limit: %d %v, want 200 and no change", code, out)
	}
	after, _ := os.ReadFile(filepath.Join(dir, DevicesFile))
	if string(after) != string(before) {
		t.Errorf("the refusals changed the stored set")
	}
	if code, _ := deviceReq(t, h, "DELETE", "/v1/devices/zz", auth, ""); code != 400 {
		t.Errorf("DELETE of a non-hex token: %d, want 400", code)
	}
}
