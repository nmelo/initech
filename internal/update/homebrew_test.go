package update

import (
	"testing"
	"time"
)

func TestCanSelfUpdate_DefaultTrue(t *testing.T) {
	old := InstallMethod
	InstallMethod = ""
	defer func() { InstallMethod = old }()

	// With no ldflags and binary not under brew prefix,
	// CanSelfUpdate should return true (development build).
	// This test runs from go test which is not under Homebrew.
	if !CanSelfUpdate() {
		t.Error("CanSelfUpdate should be true for dev builds not under Homebrew")
	}
}

func TestCanSelfUpdate_HomebrewLdflags(t *testing.T) {
	old := InstallMethod
	InstallMethod = "homebrew"
	defer func() { InstallMethod = old }()

	if CanSelfUpdate() {
		t.Error("CanSelfUpdate should be false when InstallMethod=homebrew")
	}
}

func TestCanSelfUpdate_ReleaseLdflags(t *testing.T) {
	old := InstallMethod
	InstallMethod = "release"
	defer func() { InstallMethod = old }()

	if !CanSelfUpdate() {
		t.Error("CanSelfUpdate should be true when InstallMethod=release")
	}
}

func TestUpdateInstruction_Homebrew(t *testing.T) {
	old := InstallMethod
	InstallMethod = "homebrew"
	defer func() { InstallMethod = old }()

	got := UpdateInstruction()
	if got != "brew upgrade initech" {
		t.Errorf("UpdateInstruction = %q, want 'brew upgrade initech'", got)
	}
}

func TestUpdateInstruction_NonHomebrew(t *testing.T) {
	old := InstallMethod
	InstallMethod = "release"
	defer func() { InstallMethod = old }()

	got := UpdateInstruction()
	if got != "initech update" {
		t.Errorf("UpdateInstruction = %q, want 'initech update'", got)
	}
}

func TestShouldSuppressNotification_RecentRelease(t *testing.T) {
	old := InstallMethod
	InstallMethod = "homebrew"
	defer func() { InstallMethod = old }()

	// Published 12 hours ago: suppress (formula may not be updated yet).
	published := time.Now().Add(-12 * time.Hour)
	if !ShouldSuppressNotification(published) {
		t.Error("should suppress notification for release published 12h ago under Homebrew")
	}
}

func TestShouldSuppressNotification_OldRelease(t *testing.T) {
	old := InstallMethod
	InstallMethod = "homebrew"
	defer func() { InstallMethod = old }()

	// Published 36 hours ago: show (formula should be updated by now).
	published := time.Now().Add(-36 * time.Hour)
	if ShouldSuppressNotification(published) {
		t.Error("should NOT suppress notification for release published 36h ago")
	}
}

func TestShouldSuppressNotification_NonHomebrew(t *testing.T) {
	old := InstallMethod
	InstallMethod = "release"
	defer func() { InstallMethod = old }()

	// Non-Homebrew: never suppress, even for recent releases.
	published := time.Now().Add(-1 * time.Hour)
	if ShouldSuppressNotification(published) {
		t.Error("should NOT suppress for non-Homebrew installations")
	}
}
