// homebrew.go detects whether initech was installed via Homebrew and adapts
// update behavior accordingly. Homebrew installations must use `brew upgrade`
// instead of self-updating to keep Homebrew's version tracking correct.
package update

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InstallMethod is set at compile time via ldflags:
//
//	-X github.com/nmelo/initech/internal/update.InstallMethod=homebrew  (Homebrew formula)
//	-X github.com/nmelo/initech/internal/update.InstallMethod=release   (GitHub Release)
//
// Empty string means unknown (development builds, go install).
var InstallMethod string

// CanSelfUpdate returns true if the binary can safely self-update without
// conflicting with a package manager. False for Homebrew installations
// (detected via compile-time flag or runtime path check).
func CanSelfUpdate() bool {
	if InstallMethod == "homebrew" {
		return false
	}
	return !IsUnderHomebrew()
}

// IsUnderHomebrew returns true if the running binary lives inside Homebrew's
// prefix directory (e.g., /opt/homebrew/bin/ or /usr/local/bin/ on Intel Macs).
// Falls back to false on any error (brew not installed, symlink resolution
// fails, etc.).
func IsUnderHomebrew() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}

	brewExe, err := exec.LookPath("brew")
	if err != nil {
		return false // Homebrew not installed.
	}
	out, err := exec.Command(brewExe, "--prefix").Output()
	if err != nil {
		return false
	}
	prefix := filepath.Join(strings.TrimSpace(string(out)), "bin") + string(filepath.Separator)
	return strings.HasPrefix(exe, prefix)
}

// ShouldSuppressNotification returns true if the update notification should
// be suppressed because the release is too new for Homebrew to have updated
// its formula. Homebrew formula updates are typically batched and can take
// up to 24 hours after a GitHub Release is published.
func ShouldSuppressNotification(publishedAt time.Time) bool {
	if !IsUnderHomebrew() && InstallMethod != "homebrew" {
		return false // Not Homebrew, never suppress.
	}
	return time.Since(publishedAt) < 24*time.Hour
}

// UpdateInstruction returns the appropriate update command for the user.
// Homebrew installations get "brew upgrade initech"; others get "initech update".
func UpdateInstruction() string {
	if !CanSelfUpdate() {
		return "brew upgrade initech"
	}
	return "initech update"
}
