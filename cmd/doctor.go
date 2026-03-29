package cmd

import (
	"bufio"
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

// doctorState accumulates pass/fail counts across sections for the summary line.
type doctorState struct {
	warnings        int
	requiredMissing bool
}

func runDoctor(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()
	state := &doctorState{}

	// Section 1: Prerequisites
	checkPrereqs(out, state)

	// Section 2: Project Health (only when initech.yaml is found)
	wd, _ := os.Getwd()
	cfgPath, err := config.Discover(wd)
	if err == nil {
		checkProjectHealth(out, cfgPath, state)
	} else {
		fmt.Fprintf(out, "\n%s\n", color.Dim("No initech.yaml found. Run 'initech init' to set up."))
	}

	// Section 3: Remote Connectivity (if remotes configured)
	if err == nil {
		if proj, loadErr := config.Load(cfgPath); loadErr == nil && len(proj.Remotes) > 0 {
			checkRemotes(out, proj, state)
		}
	}

	// Section 4: Terminal Environment
	checkEnvironment(out)

	// Summary
	fmt.Fprintln(out)
	switch {
	case state.requiredMissing:
		fmt.Fprintln(out, color.RedBold("Required prerequisites missing. Install them before running initech."))
		return fmt.Errorf("required prerequisites missing")
	case state.warnings > 0:
		fmt.Fprintln(out, color.YellowBold(fmt.Sprintf(
			"%d warning(s) found. The session will start but some agents may not work correctly.",
			state.warnings,
		)))
	default:
		fmt.Fprintln(out, color.GreenBold("All checks passed."))
	}
	return nil
}

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

// checkPrereqs prints the Prerequisites section and updates state.
func checkPrereqs(out io.Writer, state *doctorState) {
	fmt.Fprintf(out, "\n%s\n", color.CyanBold("Prerequisites"))

	type missing struct {
		p prereqDef
	}
	var missingList []missing

	for _, p := range prereqList {
		path, _ := exec.LookPath(p.Name)
		version := "-"
		displayPath := "-"
		var statusStr string

		if path == "" {
			if p.Required {
				statusStr = color.RedBold("MISSING")
				state.requiredMissing = true
			} else {
				statusStr = color.YellowBold("MISSING")
				state.warnings++
			}
			missingList = append(missingList, missing{p})
		} else {
			displayPath = path
			version = getVersion(p.VersionCmd)
			if version == "" {
				version = "-"
			}
			statusStr = color.Green("ok")
		}

		fmt.Fprintf(out, "  %s %s %s %s\n",
			color.Pad(color.Blue(p.Name), 14),
			color.Pad(color.Dim(version), 9),
			color.Pad(color.Dim(displayPath), 42),
			statusStr,
		)
	}

	if len(missingList) > 0 {
		fmt.Fprintf(out, "\n  %s\n", fmt.Sprintf("%d issue(s):", len(missingList)))
		for _, m := range missingList {
			note := ""
			if m.p.Note != "" {
				note = " (" + m.p.Note + ")"
			}
			fmt.Fprintf(out, "    %s: %s%s\n", m.p.Name, m.p.InstallHint, note)
		}
	}
}

// checkProjectHealth prints the Project Health section.
func checkProjectHealth(out io.Writer, cfgPath string, state *doctorState) {
	root := filepath.Dir(cfgPath)

	// Load and validate config — if this fails, report the error and stop.
	proj, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(out, "\n%s\n", color.CyanBold(fmt.Sprintf("Project: %s (%s)", "?", root)))
		printCheck(out, "Config", "initech.yaml parse error: "+err.Error(), "ERROR")
		state.warnings++
		return
	}
	if err := config.Validate(proj); err != nil {
		fmt.Fprintf(out, "\n%s\n", color.CyanBold(fmt.Sprintf("Project: %s (%s)", proj.Name, root)))
		printCheck(out, "Config", "initech.yaml invalid: "+err.Error(), "WARNING")
		state.warnings++
		return
	}

	// Header: "Project: aegis (/path)"
	fmt.Fprintf(out, "\n%s\n", color.CyanBold(fmt.Sprintf("Project: %s (%s)", proj.Name, root)))

	// Config valid
	printCheck(out, "Config",
		fmt.Sprintf("initech.yaml valid (%d roles)", len(proj.Roles)), "ok")

	// .beads/ present
	beadsDir := filepath.Join(root, ".beads")
	if _, err := os.Stat(beadsDir); err != nil {
		printCheck(out, "Beads", ".beads/ not found — run 'bd init'", "WARNING")
		state.warnings++
	} else {
		prefix := proj.Beads.Prefix
		if prefix == "" {
			prefix = "?"
		}
		printCheck(out, "Beads",
			fmt.Sprintf(".beads/ present (prefix: %s)", prefix), "ok")
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
		printCheck(out, "Workspaces",
			fmt.Sprintf("%d/%d roles have CLAUDE.md", ok, total), "WARNING")
		for _, r := range missingClaudes {
			fmt.Fprintf(out, "  %s%s/ missing CLAUDE.md\n",
				strings.Repeat(" ", 17), r)
		}
		state.warnings++
	} else {
		printCheck(out, "Workspaces",
			fmt.Sprintf("%d/%d roles have CLAUDE.md", ok, total), "ok")
	}

	// src/ submodules: roles with NeedsSrc need a src/.git entry
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
			printCheck(out, "Submodules",
				fmt.Sprintf("%d/%d src/ submodules checked out", subOK, subTotal), "WARNING")
			for _, r := range missingSubs {
				fmt.Fprintf(out, "  %s%s/src/ not initialized (run: git submodule update --init %s/src)\n",
					strings.Repeat(" ", 17), r, r)
			}
			state.warnings++
		} else {
			printCheck(out, "Submodules",
				fmt.Sprintf("%d/%d src/ submodules checked out", subOK, subTotal), "ok")
		}
	}

	// Session socket + PID file
	sockPath := filepath.Join(root, ".initech", "initech.sock")
	pidPath := filepath.Join(root, ".initech", "initech.pid")
	sockExists := fileExists(sockPath)
	pidExists := fileExists(pidPath)

	switch {
	case sockExists:
		// Probe whether something is actually listening.
		conn, dialErr := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			// Active session running — read PID if available.
			pidStr := ""
			if pidExists {
				if data, err := os.ReadFile(pidPath); err == nil {
					pidStr = " (PID " + strings.TrimSpace(string(data)) + ")"
				}
			}
			printCheck(out, "Session", "Session running"+pidStr, "ok")
		} else {
			printCheck(out, "Session", "stale socket found — run: rm "+sockPath, "WARNING")
			state.warnings++
		}
	case pidExists:
		// No socket but PID file present — check if process is alive.
		if data, err := os.ReadFile(pidPath); err == nil {
			pidStr := strings.TrimSpace(string(data))
			if pid, err := strconv.Atoi(pidStr); err == nil {
				proc, _ := os.FindProcess(pid)
				if proc != nil && proc.Signal(syscall.Signal(0)) == nil {
					printCheck(out, "Session", "no stale socket or PID file", "ok")
				} else {
					printCheck(out, "Session", fmt.Sprintf("stale PID file (PID %d not running)", pid), "WARNING")
					state.warnings++
				}
			} else {
				printCheck(out, "Session", "no stale socket or PID file", "ok")
			}
		} else {
			printCheck(out, "Session", "no stale socket or PID file", "ok")
		}
	default:
		printCheck(out, "Session", "no stale socket or PID file", "ok")
	}

	// Crash log
	crashLog := filepath.Join(root, ".initech", "crash.log")
	if info, err := os.Stat(crashLog); err == nil && info.Size() > 0 {
		printCheck(out, "Crash log", "crash.log present (run initech to see analysis)", "NOTE")
	}
}

// checkEnvironment prints the Terminal Environment section (informational only).
func checkEnvironment(out io.Writer) {
	fmt.Fprintf(out, "\n%s\n", color.CyanBold("Environment"))

	termVal := os.Getenv("TERM")
	if termVal == "" {
		termVal = "(not set)"
	}
	printEnv(out, "TERM", termVal)

	// Terminal size — only meaningful when stdout is a TTY.
	sizeStr := "unknown"
	if term.IsTerminal(int(os.Stdout.Fd())) {
		if cols, rows, err := term.GetSize(int(os.Stdout.Fd())); err == nil {
			sizeStr = fmt.Sprintf("%d x %d", cols, rows)
		}
	}
	printEnv(out, "Terminal", sizeStr)

	// Color depth from COLORTERM + TERM.
	colorSupport := "basic"
	colorterm := strings.ToLower(os.Getenv("COLORTERM"))
	if colorterm == "truecolor" || colorterm == "24bit" {
		colorSupport = "truecolor"
	} else if strings.Contains(strings.ToLower(termVal), "256color") {
		colorSupport = "256-color"
	} else if termVal == "dumb" || termVal == "" || termVal == "(not set)" {
		colorSupport = "none"
	}
	printEnv(out, "Colors", colorSupport)

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "(not set)"
	}
	printEnv(out, "Shell", shell)

	printEnv(out, "OS", runtime.GOOS+" "+runtime.GOARCH)
}

// printCheck formats a single named check with a status tag.
// label is left-padded to 12 chars; detail fills the middle; status is right-aligned.
func printCheck(out io.Writer, label, detail, status string) {
	statusStr := ""
	switch status {
	case "ok":
		statusStr = color.Green("ok")
	case "WARNING":
		statusStr = color.YellowBold("WARNING")
	case "NOTE":
		statusStr = color.Cyan("NOTE")
	default: // ERROR, or any unknown
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

// fileExists returns true if the path exists (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// checkRemotes tests connectivity to each configured remote peer.
func checkRemotes(out io.Writer, proj *config.Project, state *doctorState) {
	fmt.Fprintf(out, "\n%s\n", color.CyanBold("Remote Connectivity"))

	for peerName, remote := range proj.Remotes {
		// TCP dial with timeout.
		conn, err := net.DialTimeout("tcp", remote.Addr, 5*time.Second)
		if err != nil {
			printCheck(out, peerName,
				fmt.Sprintf("%s: unreachable (%s)", remote.Addr, err), "WARNING")
			state.warnings++
			continue
		}

		// Attempt yamux + hello handshake.
		session, err := yamux.Client(conn, yamux.DefaultConfig())
		if err != nil {
			conn.Close()
			printCheck(out, peerName,
				fmt.Sprintf("%s: yamux failed (%s)", remote.Addr, err), "WARNING")
			state.warnings++
			continue
		}

		ctrl, err := session.Open()
		if err != nil {
			session.Close()
			printCheck(out, peerName,
				fmt.Sprintf("%s: control stream failed (%s)", remote.Addr, err), "WARNING")
			state.warnings++
			continue
		}

		// Send hello.
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

		// Read hello_ok.
		scanner := bufio.NewScanner(ctrl)
		ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
		if !scanner.Scan() {
			ctrl.Close()
			session.Close()
			printCheck(out, peerName,
				fmt.Sprintf("%s: no response to hello", remote.Addr), "WARNING")
			state.warnings++
			continue
		}

		var helloOK tui.HelloOKMsg
		if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil || helloOK.Action != "hello_ok" {
			// Check if it was an error response.
			var errMsg tui.ErrorMsg
			json.Unmarshal(scanner.Bytes(), &errMsg)
			ctrl.Close()
			session.Close()
			if errMsg.Error != "" {
				printCheck(out, peerName,
					fmt.Sprintf("%s: %s", remote.Addr, errMsg.Error), "WARNING")
			} else {
				printCheck(out, peerName,
					fmt.Sprintf("%s: unexpected response", remote.Addr), "WARNING")
			}
			state.warnings++
			continue
		}

		ctrl.Close()
		session.Close()

		printCheck(out, peerName,
			fmt.Sprintf("%s (peer: %s, %d agents)",
				remote.Addr, helloOK.PeerName, len(helloOK.Agents)), "ok")
	}
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
