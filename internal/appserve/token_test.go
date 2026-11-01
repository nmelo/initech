package appserve

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// A token issued while the service runs takes effect at once: the new one
// passes and the old one is refused, with no restart; no token issued
// refuses every request, and so does a digest file gone bad.
func TestToken_ANewTokenTakesEffectWithoutARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	h := New(Config{Backend: &fakeBackend{}, TokenDir: dir, StateDir: dir}).Handler()
	if code, body := get(t, h, "/v1/rigs", "Bearer anything"); code != http.StatusUnauthorized || body != "" {
		t.Fatalf("no token issued: %d %q", code, body)
	}
	first, err := IssueToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, h, "/v1/rigs", "Bearer "+first); code != 200 {
		t.Errorf("the first token: %d", code)
	}
	second, _ := IssueToken(dir)
	if code, _ := get(t, h, "/v1/rigs", "Bearer "+second); code != 200 {
		t.Errorf("the new token: %d, want 200 with no restart", code)
	}
	if code, body := get(t, h, "/v1/rigs", "Bearer "+first); code != http.StatusUnauthorized || body != "" {
		t.Errorf("the revoked token: %d %q, want 401 with no restart", code, body)
	}
	os.WriteFile(filepath.Join(dir, TokenFile), []byte("not a digest\n"), 0o600)
	if code, _ := get(t, h, "/v1/rigs", "Bearer "+second); code != http.StatusUnauthorized {
		t.Errorf("a bad digest file: %d, want refused", code)
	}
}
