package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mockGitHubServer(tag string, published time.Time) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{
			TagName:     tag,
			HTMLURL:     "https://github.com/nmelo/initech/releases/tag/" + tag,
			PublishedAt: published,
		}
		json.NewEncoder(w).Encode(rel)
	}))
}

func TestCheckForUpdate_NewerVersionAvailable(t *testing.T) {
	srv := mockGitHubServer("v2.0.0", time.Now())
	defer srv.Close()
	origBase := APIBaseURL
	APIBaseURL = srv.URL
	defer func() { APIBaseURL = origBase }()

	// Use temp state file.
	dir := t.TempDir()
	origFn := stateFilePathFn
	stateFilePathFn = func() (string, error) { return filepath.Join(dir, "state.json"), nil }
	defer func() { stateFilePathFn = origFn }()

	info, err := CheckForUpdate(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if info == nil {
		t.Fatal("expected newer version, got nil")
	}
	if info.Version != "2.0.0" {
		t.Errorf("version = %q, want '2.0.0'", info.Version)
	}
}

func TestCheckForUpdate_CurrentVersion(t *testing.T) {
	srv := mockGitHubServer("v1.0.0", time.Now())
	defer srv.Close()
	origBase := APIBaseURL
	APIBaseURL = srv.URL
	defer func() { APIBaseURL = origBase }()

	dir := t.TempDir()
	origFn := stateFilePathFn
	stateFilePathFn = func() (string, error) { return filepath.Join(dir, "state.json"), nil }
	defer func() { stateFilePathFn = origFn }()

	info, err := CheckForUpdate(context.Background(), "1.0.0")
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if info != nil {
		t.Errorf("expected nil (current version), got %+v", info)
	}
}

func TestCheckForUpdate_CacheHit(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		rel := githubRelease{TagName: "v2.0.0", HTMLURL: "https://example.com", PublishedAt: time.Now()}
		json.NewEncoder(w).Encode(rel)
	}))
	defer srv.Close()
	origBase := APIBaseURL
	APIBaseURL = srv.URL
	defer func() { APIBaseURL = origBase }()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	origFn := stateFilePathFn
	stateFilePathFn = func() (string, error) { return statePath, nil }
	defer func() { stateFilePathFn = origFn }()

	// First call: hits API.
	CheckForUpdate(context.Background(), "1.0.0")
	if callCount != 1 {
		t.Fatalf("first call: API calls = %d, want 1", callCount)
	}

	// Second call: should use cache.
	info, _ := CheckForUpdate(context.Background(), "1.0.0")
	if callCount != 1 {
		t.Errorf("second call: API calls = %d, want 1 (cached)", callCount)
	}
	if info == nil || info.Version != "2.0.0" {
		t.Errorf("cached result should return version 2.0.0, got %v", info)
	}
}

func TestCheckForUpdate_RateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
	}))
	defer srv.Close()
	origBase := APIBaseURL
	APIBaseURL = srv.URL
	defer func() { APIBaseURL = origBase }()

	dir := t.TempDir()
	origFn := stateFilePathFn
	stateFilePathFn = func() (string, error) { return filepath.Join(dir, "state.json"), nil }
	defer func() { stateFilePathFn = origFn }()

	_, err := CheckForUpdate(context.Background(), "1.0.0")
	if err == nil {
		t.Error("expected rate limit error")
	}
}

func TestShouldCheck_Default(t *testing.T) {
	// Clear all suppression env vars.
	for _, v := range []string{"INITECH_NO_UPDATE_NOTIFIER", "CI", "GITHUB_ACTIONS", "JENKINS_URL", "GITLAB_CI"} {
		t.Setenv(v, "")
		os.Unsetenv(v)
	}
	if !ShouldCheck() {
		t.Error("ShouldCheck should be true by default")
	}
}

func TestShouldCheck_CI(t *testing.T) {
	for _, env := range []string{"CI", "GITHUB_ACTIONS", "JENKINS_URL", "GITLAB_CI"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "1")
			if ShouldCheck() {
				t.Errorf("ShouldCheck should be false when %s is set", env)
			}
		})
	}
}

func TestShouldCheck_OptOut(t *testing.T) {
	t.Setenv("INITECH_NO_UPDATE_NOTIFIER", "1")
	if ShouldCheck() {
		t.Error("ShouldCheck should be false when INITECH_NO_UPDATE_NOTIFIER is set")
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"2.0.0", "1.0.0", true},
		{"1.1.0", "1.0.0", true},
		{"1.0.1", "1.0.0", true},
		{"1.0.0", "1.0.0", false},
		{"0.9.0", "1.0.0", false},
		{"1.0.0", "2.0.0", false},
		{"0.24.0", "0.23.28", true},
		{"0.23.28", "0.23.28", false},
		{"0.23.29", "0.23.28", true},
	}
	for _, tc := range tests {
		t.Run(tc.latest+"_vs_"+tc.current, func(t *testing.T) {
			if got := isNewer(tc.latest, tc.current); got != tc.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
			}
		})
	}
}

func TestIsNewer_WithVPrefix(t *testing.T) {
	if !isNewer("v2.0.0", "v1.0.0") {
		t.Error("isNewer should handle v prefix")
	}
}

func TestIsNewer_PreRelease(t *testing.T) {
	// Pre-release suffix is stripped for comparison.
	if !isNewer("2.0.0-rc1", "1.0.0") {
		t.Error("isNewer should handle pre-release suffix")
	}
}

func TestStateFileAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := &StateFile{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: "1.2.3",
		LatestURL:     "https://example.com",
	}
	writeState(path, s)

	// File should exist and be valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	var loaded StateFile
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if loaded.LatestVersion != "1.2.3" {
		t.Errorf("version = %q, want '1.2.3'", loaded.LatestVersion)
	}

	// Temp file should not exist.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should be cleaned up")
	}
}
