package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hashicorp/yamux"
	"golang.org/x/term"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/roles"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check prerequisites and project health",
	Long:  `Checks prerequisites, project configuration, and terminal environment. Surfaces problems before initech up.`,
	RunE:  runDoctor,
}

func init() {
	rootCmd.AddCommand(doctorCmd)
}

// ── Result model ───────────────────────────────────────────────────

// checkResult is a single named check with a status and detail string.
type checkResult struct {
	Label  string // e.g. "Config", "claude", "workbench"
	Status string // "OK", "WARN", "FAIL", "NOTE", "INFO"
	Detail string // Human-readable description
}

// doctorReport holds all check results grouped by section.
type doctorReport struct {
	Prereqs     []checkResult
	Project     []checkResult
	Remotes     []checkResult
	Environment []checkResult
	ProjectName string // Empty if no project found.
	ProjectRoot string
}

// HasRequiredMissing returns true if any prerequisite check failed.
func (r doctorReport) HasRequiredMissing() bool {
	for _, c := range r.Prereqs {
		if c.Status == "FAIL" {
			return true
		}
	}
	return false
}

// WarningCount returns the total number of warnings across all sections.
func (r doctorReport) WarningCount() int {
	n := 0
	for _, checks := range [][]checkResult{r.Prereqs, r.Project, r.Remotes} {
		for _, c := range checks {
			if c.Status == "WARN" {
				n++
			}
		}
	}
	return n
}

// ── Injectable dependencies ────────────────────────────────────────

// doctorEnv provides injectable dependencies for doctor checks.
type doctorEnv struct {
	LookPath   func(string) (string, error)
	GetVersion func([]string) string
	Dial       func(string, string, time.Duration) (net.Conn, error)
	WorkDir    string
}

// defaultDoctorEnv returns the production dependencies.
func defaultDoctorEnv() doctorEnv {
	wd, _ := os.Getwd()
	return doctorEnv{
		LookPath:   exec.LookPath,
		GetVersion: getVersion,
		Dial:       net.DialTimeout,
		WorkDir:    wd,
	}
}

// ── Check orchestration ────────────────────────────────────────────

// runDoctorReport runs all doctor checks and returns a structured report.
func runDoctorReport(env doctorEnv) doctorReport {
	report := doctorReport{}
	report.Prereqs = runPrereqChecks(env)

	cfgPath, err := config.Discover(env.WorkDir)
	if err == nil {
		report.Project, report.ProjectName, report.ProjectRoot = runProjectChecks(cfgPath)
		if proj, loadErr := config.Load(cfgPath); loadErr == nil && len(proj.Remotes) > 0 {
			report.Remotes = runRemoteChecks(proj, env.Dial)
		}
	}

	report.Environment = runEnvironmentChecks()
	return report
}

// ── Prerequisite checks ────────────────────────────────────────────

// prereqDef describes a tool that doctor checks.
type prereqDef struct {
	Name        string
	Required    bool
	VersionCmd  []string
	InstallHint string
	Note        string // optional suffix on MISSING line
}

var prereqList = []prereqDef{
	{
		Name:        "claude",
		Required:    true,
		VersionCmd:  []string{"claude", "--version"},
		InstallHint: "See https://docs.anthropic.com/en/docs/claude-code",
	},
	{
		Name:        "git",
		Required:    true,
		VersionCmd:  []string{"git", "--version"},
		InstallHint: "brew install git",
	},
	{
		Name:        "bd",
		Required:    false,
		VersionCmd:  []string{"bd", "version"},
		InstallHint: "brew tap nmelo/tap && brew install bd",
		Note:        "optional, beads features disabled without it",
	},
}

func runPrereqChecks(env doctorEnv) []checkResult {
	var results []checkResult
	for _, p := range prereqList {
		path, _ := env.LookPath(p.Name)
		if path == "" {
			status := "WARN"
			if p.Required {
				status = "FAIL"
			}
			detail := p.InstallHint
			if p.Note != "" {
				detail += " (" + p.Note + ")"
			}
			results = append(results, checkResult{Label: p.Name, Status: status, Detail: detail})
		} else {
			version := env.GetVersion(p.VersionCmd)
			if version == "" {
				version = "unknown"
			}
			results = append(results, checkResult{
				Label:  p.Name,
				Status: "OK",
				Detail: fmt.Sprintf("%s (%s)", version, path),
			})
		}
	}
	return results
}

// ── Project health checks ──────────────────────────────────────────

func runProjectChecks(cfgPath string) (checks []checkResult, projectName, projectRoot string) {
	root := filepath.Dir(cfgPath)
	projectRoot = root

	proj, err := config.Load(cfgPath)
	if err != nil {
		return []checkResult{
			{Label: "Config", Status: "FAIL", Detail: "initech.yaml parse error: " + err.Error()},
		}, "?", root
	}
	projectName = proj.Name
	if err := config.Validate(proj); err != nil {
		return []checkResult{
			{Label: "Config", Status: "WARN", Detail: "initech.yaml invalid: " + err.Error()},
		}, proj.Name, root
	}

	checks = append(checks, checkResult{
		Label: "Config", Status: "OK",
		Detail: fmt.Sprintf("initech.yaml valid (%d roles)", len(proj.Roles)),
	})

	// Notify: webhook_url
	if proj.WebhookURL == "" {
		checks = append(checks, checkResult{
			Label: "Notify", Status: "WARN",
			Detail: "no webhook_url configured (Slack notifications disabled)",
		})
	} else {
		checks = append(checks, checkResult{
			Label: "Notify", Status: "OK",
			Detail: "webhook_url configured",
		})
	}

	// .beads/ directory (skip when disabled)
	if proj.Beads.IsEnabled() {
		beadsDir := filepath.Join(root, ".beads")
		if _, err := os.Stat(beadsDir); err != nil {
			checks = append(checks, checkResult{Label: "Beads", Status: "WARN", Detail: ".beads/ not found — run 'bd init'"})
		} else {
			prefix := proj.Beads.Prefix
			if prefix == "" {
				prefix = "?"
			}
			checks = append(checks, checkResult{Label: "Beads", Status: "OK",
				Detail: fmt.Sprintf(".beads/ present (prefix: %s)", prefix)})
		}
	} else {
		checks = append(checks, checkResult{Label: "Beads", Status: "OK", Detail: "disabled"})
	}

	// Agent workspaces: each role needs a CLAUDE.md
	var missingClaudes []string
	for _, role := range proj.Roles {
		claudePath := filepath.Join(root, role, "CLAUDE.md")
		if _, err := os.Stat(claudePath); err != nil {
			missingClaudes = append(missingClaudes, role)
		}
	}
	total := len(proj.Roles)
	ok := total - len(missingClaudes)
	if len(missingClaudes) > 0 {
		checks = append(checks, checkResult{
			Label: "Workspaces", Status: "WARN",
			Detail: fmt.Sprintf("%d/%d roles have CLAUDE.md (missing: %s)", ok, total, strings.Join(missingClaudes, ", ")),
		})
	} else {
		checks = append(checks, checkResult{
			Label: "Workspaces", Status: "OK",
			Detail: fmt.Sprintf("%d/%d roles have CLAUDE.md", ok, total),
		})
	}

	// src/ submodules for roles that need them
	var missingSubs []string
	var needsSrcRoles []string
	for _, role := range proj.Roles {
		def, inCatalog := roles.Catalog[role]
		if !inCatalog || !def.NeedsSrc {
			continue
		}
		needsSrcRoles = append(needsSrcRoles, role)
		gitRef := filepath.Join(root, role, "src", ".git")
		if _, err := os.Stat(gitRef); err != nil {
			missingSubs = append(missingSubs, role)
		}
	}
	if len(needsSrcRoles) > 0 {
		subTotal := len(needsSrcRoles)
		subOK := subTotal - len(missingSubs)
		if len(missingSubs) > 0 {
			checks = append(checks, checkResult{
				Label: "Submodules", Status: "WARN",
				Detail: fmt.Sprintf("%d/%d src/ submodules checked out (missing: %s)", subOK, subTotal, strings.Join(missingSubs, ", ")),
			})
		} else {
			checks = append(checks, checkResult{
				Label: "Submodules", Status: "OK",
				Detail: fmt.Sprintf("%d/%d src/ submodules checked out", subOK, subTotal),
			})
		}
	}

	// Session socket + PID file
	sockPath := filepath.Join(root, ".initech", "initech.sock")
	pidPath := filepath.Join(root, ".initech", "initech.pid")
	sockExists := fileExists(sockPath)
	pidExists := fileExists(pidPath)

	switch {
	case sockExists:
		conn, dialErr := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			pidStr := ""
			if pidExists {
				if data, err := os.ReadFile(pidPath); err == nil {
					pidStr = " (PID " + strings.TrimSpace(string(data)) + ")"
				}
			}
			checks = append(checks, checkResult{Label: "Session", Status: "OK", Detail: "Session running" + pidStr})
		} else {
			checks = append(checks, checkResult{Label: "Session", Status: "WARN", Detail: "stale socket found — run: rm " + sockPath})
		}
	case pidExists:
		if data, err := os.ReadFile(pidPath); err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil {
				proc, _ := os.FindProcess(pid)
				if proc != nil && proc.Signal(syscall.Signal(0)) == nil {
					checks = append(checks, checkResult{Label: "Session", Status: "OK", Detail: "no stale socket or PID file"})
				} else {
					checks = append(checks, checkResult{Label: "Session", Status: "WARN",
						Detail: fmt.Sprintf("stale PID file (PID %d not running)", pid)})
				}
			} else {
				checks = append(checks, checkResult{Label: "Session", Status: "OK", Detail: "no stale socket or PID file"})
			}
		} else {
			checks = append(checks, checkResult{Label: "Session", Status: "OK", Detail: "no stale socket or PID file"})
		}
	default:
		checks = append(checks, checkResult{Label: "Session", Status: "OK", Detail: "no stale socket or PID file"})
	}

	// Crash log
	crashLog := filepath.Join(root, ".initech", "crash.log")
	if info, err := os.Stat(crashLog); err == nil && info.Size() > 0 {
		checks = append(checks, checkResult{Label: "Crash log", Status: "NOTE", Detail: "crash.log present (run initech to see analysis)"})
	}

	return checks, projectName, root
}

// ── Remote connectivity checks ─────────────────────────────────────

func runRemoteChecks(proj *config.Project, dial func(string, string, time.Duration) (net.Conn, error)) []checkResult {
	var results []checkResult
	for peerName, remote := range proj.Remotes {
		conn, err := dial("tcp", remote.Addr, 5*time.Second)
		if err != nil {
			results = append(results, checkResult{Label: peerName, Status: "WARN",
				Detail: fmt.Sprintf("%s: unreachable (%s)", remote.Addr, err)})
			continue
		}

		session, err := yamux.Client(conn, yamux.DefaultConfig())
		if err != nil {
			conn.Close()
			results = append(results, checkResult{Label: peerName, Status: "WARN",
				Detail: fmt.Sprintf("%s: yamux failed (%s)", remote.Addr, err)})
			continue
		}

		ctrl, err := session.Open()
		if err != nil {
			session.Close()
			results = append(results, checkResult{Label: peerName, Status: "WARN",
				Detail: fmt.Sprintf("%s: control stream failed (%s)", remote.Addr, err)})
			continue
		}

		token := remote.Token
		if token == "" {
			token = proj.Token
		}
		hello := tui.HelloMsg{
			Action:   "hello",
			Version:  1,
			Token:    token,
			PeerName: proj.PeerName,
		}
		data, _ := json.Marshal(hello)
		ctrl.Write(data)
		ctrl.Write([]byte("\n"))

		scanner := tui.NewIPCScanner(ctrl)
		ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
		if !scanner.Scan() {
			ctrl.Close()
			session.Close()
			results = append(results, checkResult{Label: peerName, Status: "WARN",
				Detail: fmt.Sprintf("%s: no response to hello", remote.Addr)})
			continue
		}

		var helloOK tui.HelloOKMsg
		if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil || helloOK.Action != "hello_ok" {
			var errMsg tui.ErrorMsg
			json.Unmarshal(scanner.Bytes(), &errMsg)
			ctrl.Close()
			session.Close()
			if errMsg.Error != "" {
				results = append(results, checkResult{Label: peerName, Status: "WARN",
					Detail: fmt.Sprintf("%s: %s", remote.Addr, errMsg.Error)})
			} else {
				results = append(results, checkResult{Label: peerName, Status: "WARN",
					Detail: fmt.Sprintf("%s: unexpected response", remote.Addr)})
			}
			continue
		}

		ctrl.Close()
		session.Close()

		results = append(results, checkResult{Label: peerName, Status: "OK",
			Detail: fmt.Sprintf("%s (peer: %s, %d agents)", remote.Addr, helloOK.PeerName, len(helloOK.Agents))})
	}
	return results
}

// ── Environment checks ─────────────────────────────────────────────

func runEnvironmentChecks() []checkResult {
	var results []checkResult

	termVal := os.Getenv("TERM")
	if termVal == "" {
		termVal = "(not set)"
	}
	results = append(results, checkResult{Label: "TERM", Status: "INFO", Detail: termVal})

	sizeStr := "unknown"
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			sizeStr = fmt.Sprintf("%d x %d", cols, rows)
		}
	}
	results = append(results, checkResult{Label: "Terminal", Status: "INFO", Detail: sizeStr})

	colorSupport := "basic"
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))
	if colorterm == "truecolor" || colorterm == "24bit" {
		colorSupport = "truecolor"
	} else if strings.Contains(strings.ToLower(termVal), "256color") {
		colorSupport = "256-color"
	} else if termVal == "dumb" || termVal == "" || termVal == "(not set)" {
		colorSupport = "none"
	}
	results = append(results, checkResult{Label: "Colors", Status: "INFO", Detail: colorSupport})

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "(not set)"
	}
	results = append(results, checkResult{Label: "Shell", Status: "INFO", Detail: shell})

	results = append(results, checkResult{Label: "OS", Status: "INFO", Detail: runtime.GOOS + " " + runtime.GOARCH})

	return results
}

// ── Cobra command ──────────────────────────────────────────────────

func runDoctor(cmd *cobra.Command, args []string) error {
	env := defaultDoctorEnv()
	report := runDoctorReport(env)
	return formatDoctorReport(cmd.OutOrStdout(), report)
}

func formatDoctorReport(out io.Writer, report doctorReport) error {
	// Section 1: Prerequisites
	fmt.Fprintf(out, "\n%s\n", color.CyanBold("Prerequisites"))
	var missingPrereqs []prereqDef
	for i, r := range report.Prereqs {
		status := r.Status
		if status == "OK" {
			// For prereqs with OK status, display version + path from Detail.
			parts := strings.SplitN(r.Detail, " (", 2)
			version := parts[0]
			displayPath := "-"
			if len(parts) > 1 {
				displayPath = strings.TrimSuffix(parts[1], ")")
			}
			fmt.Fprintf(out, "  %s %s %s %s\n",
				color.Pad(color.Blue(r.Label), 14),
				color.Pad(color.Dim(version), 9),
				color.Pad(color.Dim(displayPath), 42),
				color.Green("ok"),
			)
		} else {
			statusStr := color.YellowBold("MISSING")
			if status == "FAIL" {
				statusStr = color.RedBold("MISSING")
			}
			fmt.Fprintf(out, "  %s %s %s %s\n",
				color.Pad(color.Blue(r.Label), 14),
				color.Pad(color.Dim("-"), 9),
				color.Pad(color.Dim("-"), 42),
				statusStr,
			)
			if i < len(prereqList) {
				missingPrereqs = append(missingPrereqs, prereqList[i])
			}
		}
	}
	if len(missingPrereqs) > 0 {
		fmt.Fprintf(out, "\n  %s\n", fmt.Sprintf("%d issue(s):", len(missingPrereqs)))
		for _, m := range missingPrereqs {
			note := ""
			if m.Note != "" {
				note = " (" + m.Note + ")"
			}
			fmt.Fprintf(out, "    %s: %s%s\n", m.Name, m.InstallHint, note)
		}
	}

	// Section 2: Project Health
	if len(report.Project) > 0 {
		fmt.Fprintf(out, "\n%s\n", color.CyanBold(fmt.Sprintf("Project: %s (%s)", report.ProjectName, report.ProjectRoot)))
		for _, r := range report.Project {
			printCheck(out, r.Label, r.Detail, r.Status)
		}
	} else if report.ProjectName == "" {
		fmt.Fprintf(out, "\n%s\n", color.Dim("No initech.yaml found. Run 'initech init' to set up."))
	}

	// Section 3: Remote Connectivity
	if len(report.Remotes) > 0 {
		fmt.Fprintf(out, "\n%s\n", color.CyanBold("Remote Connectivity"))
		for _, r := range report.Remotes {
			printCheck(out, r.Label, r.Detail, r.Status)
		}
	}

	// Section 4: Environment
	fmt.Fprintf(out, "\n%s\n", color.CyanBold("Environment"))
	for _, r := range report.Environment {
		printEnv(out, r.Label, r.Detail)
	}

	// Summary
	fmt.Fprintln(out)
	switch {
	case report.HasRequiredMissing():
		fmt.Fprintln(out, color.RedBold("Required prerequisites missing. Install them before running initech."))
		return fmt.Errorf("required prerequisites missing")
	case report.WarningCount() > 0:
		fmt.Fprintln(out, color.YellowBold(fmt.Sprintf(
			"%d warning(s) found. The session will start but some agents may not work correctly.",
			report.WarningCount(),
		)))
	default:
		fmt.Fprintln(out, color.GreenBold("All checks passed."))
	}
	return nil
}

// ── Formatting helpers ─────────────────────────────────────────────

// printCheck formats a single named check with a status tag.
func printCheck(out io.Writer, label, detail, status string) {
	statusStr := ""
	switch status {
	case "OK":
		statusStr = color.Green("ok")
	case "WARN":
		statusStr = color.YellowBold("WARNING")
	case "NOTE":
		statusStr = color.Cyan("NOTE")
	default: // FAIL, or any unknown
		statusStr = color.RedBold(status)
	}
	fmt.Fprintf(out, "  %s %s %s\n",
		color.Pad(color.Blue(label), 14),
		color.Pad(color.Dim(detail), 50),
		statusStr,
	)
}

// printEnv formats an environment row (label + dim value, no status column).
func printEnv(out io.Writer, label, value string) {
	fmt.Fprintf(out, "  %s %s\n",
		color.Pad(color.Blue(label), 14),
		color.Dim(value),
	)
}

// ── Utility functions ──────────────────────────────────────────────

// fileExists returns true if the path exists (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// getVersion runs versionCmd and extracts a version-looking token.
func getVersion(versionCmd []string) string {
	if len(versionCmd) == 0 {
		return ""
	}
	cmd := exec.Command(versionCmd[0], versionCmd[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		raw = raw[:idx]
	}
	for _, word := range strings.Fields(raw) {
		if word == "version" || word == "git" {
			continue
		}
		if len(word) > 0 && word[0] >= '0' && word[0] <= '9' {
			return word
		}
	}
	return raw
}
