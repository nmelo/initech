// Package update implements background version checking against GitHub Releases
// with a 24-hour state file cache to avoid redundant API calls.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ReleaseInfo describes a newer release available on GitHub.
type ReleaseInfo struct {
	Version     string    `json:"version"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"published_at"`
}

// StateFile is the cached result of the last version check.
type StateFile struct {
	CheckedAt     time.Time `json:"checked_at"`
	LatestVersion string    `json:"latest_version"`
	LatestURL     string    `json:"latest_url"`
	PublishedAt   time.Time `json:"published_at"`
}

const (
	cacheTTL  = 24 * time.Hour
	owner     = "nmelo"
	repo      = "initech"
	stateFile = "update-state.json"
)

// APIBaseURL is the GitHub API base. Override in tests.
var APIBaseURL = "https://api.github.com"

// CheckForUpdate queries GitHub Releases (with 24h cache) and returns a
// ReleaseInfo if a newer version exists. Returns nil if current or cached.
// The context allows cancellation if the parent command finishes first.
func CheckForUpdate(ctx context.Context, currentVersion string) (*ReleaseInfo, error) {
	statePath, err := stateFilePath()
	if err != nil {
		return nil, err
	}

	// Read cached state.
	state, _ := readState(statePath)

	// If cache is fresh, use it.
	if state != nil && time.Since(state.CheckedAt) < cacheTTL {
		if !isNewer(state.LatestVersion, currentVersion) {
			return nil, nil // current or ahead
		}
		return &ReleaseInfo{
			Version:     state.LatestVersion,
			URL:         state.LatestURL,
			PublishedAt: state.PublishedAt,
		}, nil
	}

	// Query GitHub.
	info, err := queryLatestRelease(ctx)
	if err != nil {
		return nil, err
	}

	// Write cache.
	writeState(statePath, &StateFile{
		CheckedAt:     time.Now().UTC(),
		LatestVersion: info.Version,
		LatestURL:     info.URL,
		PublishedAt:   info.PublishedAt,
	})

	if !isNewer(info.Version, currentVersion) {
		return nil, nil
	}
	return info, nil
}

// ShouldCheck returns false when version checking should be suppressed
// (CI environments, explicit opt-out, non-interactive).
func ShouldCheck() bool {
	if os.Getenv("INITECH_NO_UPDATE_NOTIFIER") != "" {
		return false
	}
	for _, v := range []string{"CI", "GITHUB_ACTIONS", "JENKINS_URL", "GITLAB_CI"} {
		if os.Getenv(v) != "" {
			return false
		}
	}
	return true
}

// githubRelease is the subset of the GitHub API response we parse.
type githubRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
}

func queryLatestRelease(ctx context.Context) (*ReleaseInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", APIBaseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 403 || resp.StatusCode == 429 {
		return nil, fmt.Errorf("rate limited (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}

	return &ReleaseInfo{
		Version:     strings.TrimPrefix(rel.TagName, "v"),
		URL:         rel.HTMLURL,
		PublishedAt: rel.PublishedAt,
	}, nil
}

// isNewer returns true if latest is a higher semver than current.
// Both should be bare versions without "v" prefix (e.g., "1.2.3").
func isNewer(latest, current string) bool {
	latestParts := parseSemver(latest)
	currentParts := parseSemver(current)
	if latestParts == nil || currentParts == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if latestParts[i] > currentParts[i] {
			return true
		}
		if latestParts[i] < currentParts[i] {
			return false
		}
	}
	return false // equal
}

func parseSemver(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		return nil
	}
	// Strip pre-release suffix (e.g., "3-rc1" -> "3").
	for i, p := range parts {
		if idx := strings.IndexAny(p, "-+"); idx >= 0 {
			parts[i] = p[:idx]
		}
	}
	nums := make([]int, 3)
	for i, p := range parts[:3] {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// stateFilePathFn returns the path to the state file. Override in tests.
var stateFilePathFn = defaultStateFilePath

func stateFilePath() (string, error) {
	return stateFilePathFn()
}

func defaultStateFilePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, "initech", stateFile), nil
}

func readState(path string) (*StateFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s StateFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeState(path string, s *StateFile) {
	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0700)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return
	}
	os.Rename(tmp, path)
}
