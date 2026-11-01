package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nmelo/initech/internal/appserve"
	"github.com/spf13/cobra"
)

var appServeInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Run the app service under launchd, which restarts it if it dies",
	Long: `Writes ~/Library/LaunchAgents/com.initech.app-serve.plist and starts it
with launchctl bootstrap in your login session. launchd is the watchdog: it
restarts the service ten seconds after it dies, and starts it at login. Stdout
and stderr go to ~/Library/Logs/initech/app-serve.log, which is not rotated.

The job runs this binary, as you ran it (found through PATH when you ran it by
name, so a Homebrew install stays valid across upgrades), with the --listen,
--port, --projects and --notify given here, and with your shell's PATH, since
launchd's own has no /opt/homebrew/bin and the service runs bd. A tool that moves
later needs install again. launchd starts jobs in /, so
--projects defaults here, now, to the folder holding the rig you are in, and is
written out in full. With no rig found and no --projects, nothing is written.

Issue a token first (initech app-serve token): with none, nothing is written.
--listen tailnet is not refused while Tailscale is stopped: launchd keeps
retrying, and the log says why meanwhile.

macOS guards ~/Desktop, ~/Documents and ~/Downloads for launchd jobs: a job
reading there waits on a permission dialog until the binary is allowed. Install
warns when --projects, the binary or a PATH entry is under one and goes on; the running
service says so in the log and through --notify if a rig folder cannot be read.

--notify names a command to run after an unclean exit (a crash or kill -9),
with one argument: "` + appserve.NotifyLine + `". It
runs at most once an hour, so a crash loop does not repeat it; the log records
every restart. It runs with the PATH written here. A plain
kill, a logout and uninstall are clean exits and notify nothing.

Running it again with the same settings says already installed and changes
nothing; with new settings it replaces the job.`,
	Args: cobra.NoArgs,
	RunE: runAppServeInstall,
}

var appServeUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop the app service under launchd and remove its job",
	Long: `Stops the service with launchctl bootout and removes
~/Library/LaunchAgents/com.initech.app-serve.plist. With nothing installed it
says so. The token, the certificate and the log are kept.`,
	Args: cobra.NoArgs,
	RunE: runAppServeUninstall,
}

// appServeNotify is --notify: the command to run after an unclean exit.
var appServeNotify string

// appServeBinary is the path the launchd job runs: the name this process was
// run as, found through PATH when bare, never resolved through a symlink (so
// /opt/homebrew/bin/initech does not become a Cellar path that an upgrade
// removes). A var so a test can name one.
var appServeBinary = func() (string, error) {
	return binaryAsRun(os.Args[0])
}

func init() {
	const notifyHelp = "a command to run, with one line, after an unclean exit; at most once an hour"
	appServeCmd.Flags().StringVar(&appServeNotify, "notify", "", notifyHelp)
	f := appServeInstallCmd.Flags()
	f.StringVar(&appServeListen, "listen", appserve.ListenTailnet, "where to listen: tailnet (the default and the release setting) or local-network (the LAN, while Tailscale is stopped)")
	f.IntVar(&appServePort, "port", appserve.DefaultPort, "TCP port")
	f.StringSliceVar(&appServeProjects, "projects", nil, "a folder whose subfolders are rigs (repeatable); default: the folder holding the rig found from the current directory, resolved now")
	f.StringVar(&appServeNotify, "notify", "", notifyHelp)
	appServeCmd.AddCommand(appServeInstallCmd, appServeUninstallCmd)
}

// binaryAsRun is argv0 as a path launchd can run: through PATH when it has no
// folder in it, else made absolute. Symlinks are kept.
func binaryAsRun(argv0 string) (string, error) {
	if !strings.ContainsRune(argv0, filepath.Separator) {
		p, err := exec.LookPath(argv0)
		if err != nil {
			return "", fmt.Errorf("find %s on PATH: %w", argv0, err)
		}
		argv0 = p
	}
	return filepath.Abs(argv0)
}

// appServeHome is the home folder, or a named error when HOME is not set.
func appServeHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("HOME is not set: cannot find ~/Library/LaunchAgents, nothing written")
	}
	return home, nil
}

// launchdDomain is this user's login session, where the job loads.
func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

// appServeJobArgs is the job's whole command line, or why there can be none.
// Every refusal happens here, before anything is written.
func appServeJobArgs() ([]string, error) {
	if appServeListen != appserve.ListenTailnet && appServeListen != appserve.ListenLocalNetwork {
		return nil, fmt.Errorf("--listen takes %s or %s, not %q; nothing written", appserve.ListenTailnet, appserve.ListenLocalNetwork, appServeListen)
	}
	if _, err := appserve.LoadDigest(appServeStateDir()); err != nil {
		if errors.Is(err, appserve.ErrNoToken) {
			return nil, fmt.Errorf("no token issued: run initech app-serve token first; nothing written")
		}
		return nil, fmt.Errorf("read the token digest: %w; nothing written", err)
	}
	dirs := appServeProjects
	if len(dirs) == 0 {
		if p := discoverProjectBestEffort(); p != nil {
			dirs = []string{filepath.Dir(p.Root)}
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no rig folder: run from inside a rig or pass --projects <folder>; nothing written")
	}
	bin, err := appServeBinary()
	if err != nil {
		return nil, fmt.Errorf("%w; nothing written", err)
	}
	args := []string{bin, "app-serve", "--listen", appServeListen, "--port", strconv.Itoa(appServePort)}
	for _, d := range dirs {
		abs, err := filepath.Abs(d)
		if err != nil {
			return nil, fmt.Errorf("--projects %s: %w; nothing written", d, err)
		}
		args = append(args, "--projects", abs)
	}
	if appServeNotify != "" {
		args = append(args, "--notify", appServeNotify)
	}
	return args, nil
}

// appServeGuardedPaths is each path the job would touch (the binary, every
// --projects, every PATH entry: a tool lookup stats each) that is under a
// folder macOS guards for launchd jobs.
func appServeGuardedPaths(home, path string, jobArgs []string) []string {
	var out []string
	check := func(p string) {
		for _, name := range []string{"Desktop", "Documents", "Downloads"} {
			root := filepath.Join(home, name)
			if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
				out = append(out, p)
				return
			}
		}
	}
	check(jobArgs[0])
	for i, a := range jobArgs {
		if a == "--projects" && i+1 < len(jobArgs) {
			check(jobArgs[i+1])
		}
	}
	for _, entry := range filepath.SplitList(path) {
		check(entry)
	}
	return out
}

func runAppServeInstall(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	home, err := appServeHome()
	if err != nil {
		return err
	}
	path := os.Getenv("PATH")
	if path == "" {
		return fmt.Errorf("PATH is not set: the service could not find bd under launchd; nothing written")
	}
	jobArgs, err := appServeJobArgs()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, guarded := range appServeGuardedPaths(home, path, jobArgs) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s is in a folder macOS guards for launchd jobs: the service will wait on a permission dialog there until the binary is allowed; the log says when\n", guarded)
	}
	plist := appserve.PlistPath(home)
	want := appserve.RenderPlist(home, path, jobArgs)
	old, readErr := os.ReadFile(plist)
	if readErr == nil && bytes.Equal(old, want) {
		appServePrivateLog(cmd.ErrOrStderr(), home)
		fmt.Fprintf(out, "already installed: %s\n", plist)
		return nil
	}
	if readErr == nil {
		// New settings: the running job goes first, or bootstrap finds it loaded.
		if err := launchdBootout(); err != nil {
			return err
		}
	}
	for _, dir := range []string{filepath.Dir(plist), filepath.Dir(appserve.LogPath(home))} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("make %s: %w", dir, err)
		}
	}
	appServePrivateLog(cmd.ErrOrStderr(), home)
	if err := os.WriteFile(plist, want, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", plist, err)
	}
	if _, err := appServeRunner.Run("launchctl", "bootstrap", launchdDomain(), plist); err != nil {
		os.Remove(plist) //nolint:errcheck // the error below is the one that matters
		return fmt.Errorf("launchctl bootstrap failed, the job file is removed again: %w", err)
	}
	fmt.Fprintf(out, "installed: %s\nlog: %s\n", plist, appserve.LogPath(home))
	return nil
}

// appServePrivateLog creates the service log 0600 before launchd appends to
// it, or narrows an older one; a log it cannot open is a warning, and the
// install goes on.
func appServePrivateLog(errw io.Writer, home string) {
	path := appserve.LogPath(home)
	narrowed, err := appserve.EnsurePrivateLog(path)
	switch {
	case err != nil:
		fmt.Fprintf(errw, "warning: the log %s could not be made private: %v\n", path, err)
	case narrowed:
		fmt.Fprintf(errw, "the log %s was readable by others; it is now 0600\n", path)
	}
}

func runAppServeUninstall(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	home, err := appServeHome()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	plist := appserve.PlistPath(home)
	if _, err := os.Stat(plist); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "not installed: nothing to do")
		return nil
	}
	if err := launchdBootout(); err != nil {
		return err
	}
	if err := os.Remove(plist); err != nil {
		return fmt.Errorf("the service is stopped but its job file remains: %w", err)
	}
	fmt.Fprintf(out, "uninstalled: %s\n", plist)
	return nil
}

// launchdBootout stops the job. launchctl's "No such process" means it is not
// loaded, which is what was asked for; any other failure leaves the job file
// in place and is named.
func launchdBootout() error {
	if out, err := appServeRunner.Run("launchctl", "bootout", launchdDomain()+"/"+appserve.LaunchLabel); err != nil {
		if strings.Contains(out, "No such process") || strings.Contains(err.Error(), "No such process") {
			return nil
		}
		return fmt.Errorf("launchctl bootout failed: %w", err)
	}
	return nil
}
