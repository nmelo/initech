package cmd

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/nmelo/initech/internal/color"
	"github.com/spf13/cobra"
)

type prereq struct {
	Name       string
	Required   bool
	VersionCmd []string // command to get version (e.g., ["tmux", "-V"])
	InstallHint string
}

var prereqs = []prereq{
	{"tmux", true, []string{"tmux", "-V"}, "brew install tmux"},
	{"tmuxinator", true, []string{"tmuxinator", "version"}, "gem install tmuxinator"},
	{"claude", true, []string{"claude", "--version"}, "See https://docs.anthropic.com/en/docs/claude-code"},
	{"git", true, []string{"git", "--version"}, "brew install git"},
	{"bd", false, []string{"bd", "version"}, "brew tap nmelo/tap && brew install bd"},
	{"gn", false, []string{"gn", "--version"}, "brew tap nmelo/tap && brew install gn"},
	{"gp", false, []string{"gp", "--version"}, "brew tap nmelo/tap && brew install gp"},
	{"gm", false, []string{"gm", "--version"}, "brew tap nmelo/tap && brew install gm"},
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check prerequisites for initech",
	Long:  `Checks that all required tools are installed and shows their versions and paths.`,
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Checking prerequisites...")
	fmt.Fprintln(out)

	var missing []prereq
	var requiredMissing bool

	for _, p := range prereqs {
		path, _ := exec.LookPath(p.Name)
		version := ""
		status := "ok"

		if path == "" {
			status = "MISSING"
			missing = append(missing, p)
			if p.Required {
				requiredMissing = true
			}
		} else {
			version = getVersion(p.VersionCmd)
		}

		if path == "" {
			path = "-"
		}
		if version == "" {
			version = "-"
		}

		statusStr := color.Green(status)
		if status == "MISSING" {
			statusStr = color.RedBold(status)
		}
		fmt.Fprintf(out, "  %s %s %s %s\n",
			color.Pad(color.Blue(p.Name), 14),
			color.Pad(color.Dim(version), 8),
			color.Pad(color.Dim(path), 40),
			statusStr,
		)
	}

	fmt.Fprintln(out)

	if len(missing) > 0 {
		// Group by install hint
		hints := make(map[string][]string)
		var hintOrder []string
		for _, m := range missing {
			if _, seen := hints[m.InstallHint]; !seen {
				hintOrder = append(hintOrder, m.InstallHint)
			}
			hints[m.InstallHint] = append(hints[m.InstallHint], m.Name)
		}

		fmt.Fprintf(out, "%s\n\n", color.YellowBold(fmt.Sprintf("%d issue(s) found:", len(missing))))
		for _, hint := range hintOrder {
			names := hints[hint]
			fmt.Fprintf(out, "  %s: %s\n", strings.Join(names, ", "), hint)
		}
		fmt.Fprintln(out)

		if requiredMissing {
			return fmt.Errorf("required prerequisites missing")
		}
	} else {
		fmt.Fprintln(out, color.GreenBold("All prerequisites satisfied.")+" Run 'initech init' in a project directory to get started.")
	}

	return nil
}

func getVersion(versionCmd []string) string {
	if len(versionCmd) == 0 {
		return ""
	}
	cmd := exec.Command(versionCmd[0], versionCmd[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}

	// Extract just the version number from output like "tmux 3.4" or "git version 2.43.0"
	raw := strings.TrimSpace(string(out))
	// Take first line only
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		raw = raw[:idx]
	}

	// Try to extract a version-looking substring
	for _, word := range strings.Fields(raw) {
		// Skip common prefixes
		if word == "tmux" || word == "version" || word == "git" {
			continue
		}
		// Looks like a version if it starts with a digit
		if len(word) > 0 && word[0] >= '0' && word[0] <= '9' {
			return word
		}
	}
	return raw
}
