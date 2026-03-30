package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/spf13/cobra"
)

var (
	updateCheck bool
	updateForce bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update initech to the latest version",
	Long:  `Downloads the latest release from GitHub, verifies the SHA256 checksum, and replaces the current binary.`,
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().BoolVar(&updateCheck, "check", false, "Check for updates without downloading")
	updateCmd.Flags().BoolVar(&updateForce, "force", false, "Re-download even if versions match")
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	// Homebrew guard: self-update would be overwritten by brew on next upgrade.
	if isHomebrewInstall() {
		return fmt.Errorf("initech was installed via Homebrew. Run 'brew upgrade initech' instead")
	}

	currentVersion := Version
	fmt.Fprintf(out, "Current version: %s\n", color.Blue(currentVersion))

	// Create updater with checksum validation.
	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Validator: &selfupdate.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
	if err != nil {
		return fmt.Errorf("create updater: %w", err)
	}

	ctx := context.Background()
	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug("nmelo/initech"))
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	if !found {
		fmt.Fprintln(out, "No releases found.")
		return nil
	}

	latestVersion := latest.Version()

	if updateCheck {
		if latestVersion != currentVersion && (updateForce || isNewer(latestVersion, currentVersion)) {
			fmt.Fprintf(out, "%s available (current: %s)\n", color.Green(latestVersion), currentVersion)
			fmt.Fprintf(out, "  %s\n", latest.ReleaseNotes)
			fmt.Fprintf(out, "\nRun %s to update.\n", color.Bold("initech update"))
		} else {
			fmt.Fprintf(out, "Already up to date: %s\n", color.Green(currentVersion))
		}
		return nil
	}

	if !updateForce && !isNewer(latestVersion, currentVersion) {
		fmt.Fprintf(out, "Already up to date: %s\n", color.Green(currentVersion))
		return nil
	}

	fmt.Fprintf(out, "Updating: %s -> %s\n", currentVersion, color.Green(latestVersion))

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	if err := updater.UpdateTo(ctx, latest, exePath); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Fprintf(out, "%s Updated to %s\n", color.Green("\u2713"), color.Green(latestVersion))

	// Check for live session and warn.
	if pid, running := liveSession(); running {
		fmt.Fprintf(out, "\n%s An initech session is running (PID %d).\n", color.Yellow("Warning:"), pid)
		fmt.Fprintf(out, "Restart to use the new version:\n")
		fmt.Fprintf(out, "  %s\n", color.Bold("initech down && initech"))
	}

	return nil
}

// isNewer compares semver strings (bare, no v prefix).
func isNewer(latest, current string) bool {
	latest = strings.TrimPrefix(latest, "v")
	current = strings.TrimPrefix(current, "v")

	lp := parseParts(latest)
	cp := parseParts(current)
	if lp == nil || cp == nil {
		return latest > current // fallback to string comparison
	}
	for i := 0; i < 3; i++ {
		if lp[i] > cp[i] {
			return true
		}
		if lp[i] < cp[i] {
			return false
		}
	}
	return false
}

func parseParts(v string) []int {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 3 {
		return nil
	}
	nums := make([]int, 3)
	for i, p := range parts[:3] {
		// Strip pre-release suffix.
		if idx := strings.IndexAny(p, "-+"); idx >= 0 {
			p = p[:idx]
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		nums[i] = n
	}
	return nums
}

// isHomebrewInstall checks if the running binary is under a Homebrew prefix.
func isHomebrewInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	exe, _ = filepath.EvalSymlinks(exe)
	return strings.HasPrefix(exe, "/opt/homebrew/") ||
		strings.HasPrefix(exe, "/usr/local/Cellar/") ||
		strings.HasPrefix(exe, "/home/linuxbrew/")
}

// liveSession checks if an initech TUI/daemon is running in the current
// project by reading the PID file and probing the process.
func liveSession() (int, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return 0, false
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return 0, false
	}
	root := filepath.Dir(cfgPath)
	pidPath := filepath.Join(root, ".initech", "initech.pid")

	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, false
	}
	// Signal 0: check if process exists without killing it.
	if proc.Signal(syscall.Signal(0)) != nil {
		return 0, false
	}
	return pid, true
}
