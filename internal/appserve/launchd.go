package appserve

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	iexec "github.com/nmelo/initech/internal/exec"
)

// LaunchLabel is the service's launchd label.
const LaunchLabel = "com.initech.app-serve"

// launchThrottleSeconds is how long launchd waits before starting the job
// again: the watchdog's pace.
const launchThrottleSeconds = 10

// NotifyLine is the one line the --notify command gets after an unclean exit.
// It is fixed and holds no token.
const NotifyLine = "initech app-serve restarted after an unclean exit"

// MarkerFile says the service is running: a start that finds it knows the
// last run did not exit cleanly. NotifiedAtFile and FolderNotifiedAtFile are
// when --notify last ran for each kind of message, so a restart's notice never
// silences the folder notice that may follow it a second later. All live in
// the state dir and hold no token.
const (
	MarkerFile           = "running"
	NotifiedAtFile       = "notified-at"
	FolderNotifiedAtFile = "notified-at-folder"
)

// notifyEvery is the guard on a crash loop: launchd restarts the job every
// launchThrottleSeconds, and --notify runs at most this often.
const notifyEvery = time.Hour

// notifyBound stops a --notify command that hangs. A var so a test can
// shorten it.
var notifyBound = 10 * time.Second

// StuckFolderLine is what the log and --notify get when a rig folder cannot be
// read: the folder and why. The macOS hint is added only for a folder under
// ~/Desktop, ~/Documents or ~/Downloads, which macOS guards for launchd jobs; a
// missing or mistyped folder is not blamed on it. It holds no token.
func StuckFolderLine(dir, why string) string {
	line := "initech app-serve cannot read " + dir + ": " + why
	if underGuardedFolder(dir) {
		line += "; macOS guards ~/Desktop, ~/Documents and ~/Downloads for launchd jobs until the binary is allowed"
	}
	return line
}

// underGuardedFolder reports whether dir is under ~/Desktop, ~/Documents or
// ~/Downloads.
func underGuardedFolder(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		root := filepath.Join(home, name)
		if dir == root || strings.HasPrefix(dir, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// notifyMu makes a guard's look at its stamp and its write one step: a scan's
// folders may report at once.
var notifyMu sync.Mutex

// PlistPath is where the service's launchd job lives under home.
func PlistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", LaunchLabel+".plist")
}

// LogPath is where launchd sends the service's stdout and stderr under home.
func LogPath(home string) string {
	return filepath.Join(home, "Library", "Logs", "initech", "app-serve.log")
}

// RenderPlist is the launchd job for the service: exactly Label,
// ProgramArguments, EnvironmentVariables holding PATH alone, KeepAlive true
// (launchd is the watchdog), ThrottleInterval, and stdout and stderr to the log
// under home. args is the whole command line, the binary first. path is the
// shell PATH install was run with: launchd's own has no /opt/homebrew/bin, so
// without it the service cannot find bd.
func RenderPlist(home, path string, args []string) []byte {
	var b bytes.Buffer
	esc := func(s string) string {
		var e bytes.Buffer
		xml.EscapeText(&e, []byte(s)) //nolint:errcheck // a bytes.Buffer does not fail
		return e.String()
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
`)
	fmt.Fprintf(&b, "\t<key>Label</key>\n\t<string>%s</string>\n", LaunchLabel)
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, a := range args {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", esc(a))
	}
	b.WriteString("\t</array>\n")
	fmt.Fprintf(&b, "\t<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n\t\t<string>%s</string>\n\t</dict>\n", esc(path))
	b.WriteString("\t<key>KeepAlive</key>\n\t<true/>\n")
	fmt.Fprintf(&b, "\t<key>ThrottleInterval</key>\n\t<integer>%d</integer>\n", launchThrottleSeconds)
	fmt.Fprintf(&b, "\t<key>StandardOutPath</key>\n\t<string>%s</string>\n", esc(LogPath(home)))
	fmt.Fprintf(&b, "\t<key>StandardErrorPath</key>\n\t<string>%s</string>\n", esc(LogPath(home)))
	b.WriteString("</dict>\n</plist>\n")
	return b.Bytes()
}

// ErrPlannedRestart is wrapped by a serve that ends on purpose for launchd to
// start it again (the bound address moved): RunMarked treats it as a clean
// exit, so a network change is not reported as a crash.
var ErrPlannedRestart = errors.New("planned restart")

// RunMarked runs serve under the unclean-exit marker. Call it once the
// service is listening, so a failed bind leaves no marker. It first handles a
// marker a previous run left (the log records the restart every time; notify
// runs at most once per notifyEvery; the marker is cleared), then writes its
// own, then runs serve. SIGTERM and SIGINT are a clean exit: launchd's stop, a
// logout, uninstall and a plain kill remove the marker and return nil, so they
// notify nothing; so does a serve that returns nil or wraps ErrPlannedRestart.
// A serve that returns any other error keeps the marker, as does kill -9, and
// the next start reports it. stateDir must exist.
func RunMarked(stateDir, notify string, log io.Writer, serve func() error) error {
	return runMarked(stateDir, notify, log, time.Now, serve)
}

func runMarked(stateDir, notify string, log io.Writer, now func() time.Time, serve func() error) error {
	checkUncleanExit(stateDir, notify, log, now)
	marker := filepath.Join(stateDir, MarkerFile)
	body := fmt.Sprintf("pid %d started %s\n", os.Getpid(), now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(marker, []byte(body), 0o600); err != nil {
		// The service still runs: a missing marker costs one notify, not the
		// service.
		fmt.Fprintf(log, "initech app-serve: could not write the running marker: %v\n", err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(stop)
	done := make(chan error, 1)
	go func() { done <- serve() }()
	select {
	case sig := <-stop:
		os.Remove(marker) //nolint:errcheck // gone is what we want
		fmt.Fprintf(log, "initech app-serve: stopping on %v\n", sig)
		return nil
	case err := <-done:
		if err == nil || errors.Is(err, ErrPlannedRestart) {
			os.Remove(marker) //nolint:errcheck // gone is what we want
		}
		return err // any other error keeps the marker: not a clean exit
	}
}

// checkUncleanExit is the start's look at the marker: nothing when there is
// none; otherwise it logs the restart, runs notify when the guard allows, and
// clears the marker whatever became of notify.
func checkUncleanExit(stateDir, notify string, log io.Writer, now func() time.Time) {
	marker := filepath.Join(stateDir, MarkerFile)
	if _, err := os.Stat(marker); err != nil {
		return
	}
	fmt.Fprintln(log, NotifyLine)
	notifyGuarded(stateDir, NotifiedAtFile, notify, NotifyLine, log, now)
	if err := os.Remove(marker); err != nil {
		fmt.Fprintf(log, "initech app-serve: could not clear the running marker: %v\n", err)
	}
}

// ReportFolder is what the rig scan calls when a folder does not answer, is
// refused or is missing: it logs the line, and runs notify with it under the
// folder notice's own one-per-hour guard, so a restart's notice never silences
// it. dir is named; no token is.
func ReportFolder(stateDir, notify string, log io.Writer, dir, why string) {
	line := StuckFolderLine(dir, why)
	fmt.Fprintln(log, line)
	notifyGuarded(stateDir, FolderNotifiedAtFile, notify, line, log, time.Now)
}

// notifyGuarded runs notify with line when this kind of message's one-per-hour
// guard, the stamp file named stampName, allows; a failure is logged. The
// command runs outside the lock: it may take notifyBound.
func notifyGuarded(stateDir, stampName, notify, line string, log io.Writer, now func() time.Time) {
	if notify == "" {
		return
	}
	notifyMu.Lock()
	if !notifyAllowed(filepath.Join(stateDir, stampName), now()) {
		notifyMu.Unlock()
		return
	}
	err := os.WriteFile(filepath.Join(stateDir, stampName), []byte(now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	notifyMu.Unlock()
	if err != nil {
		fmt.Fprintf(log, "initech app-serve: could not write the notify stamp: %v\n", err)
	}
	if err := runNotify(notify, line); err != nil {
		fmt.Fprintf(log, "initech app-serve: the --notify command failed: %v\n", err)
	}
}

// notifyAllowed is the one-per-hour guard: no stamp, or an unreadable one,
// allows it, so a damaged stamp can only cost an extra notify.
func notifyAllowed(stamp string, now time.Time) bool {
	raw, err := os.ReadFile(stamp)
	if err != nil {
		return true
	}
	last, err := time.Parse(time.RFC3339, strings.TrimSpace(string(raw)))
	if err != nil {
		return true
	}
	return now.Sub(last) >= notifyEvery
}

// runNotify runs the operator's command with line as its last argument,
// bounded by notifyBound. The command's own words are not logged: only that it
// failed, and how.
func runNotify(notify, line string) error {
	ctx, cancel := context.WithTimeout(context.Background(), notifyBound)
	defer cancel()
	r := &iexec.DefaultRunner{}
	if _, err := r.RunContext(ctx, "/bin/sh", "-c", notify+` "$1"`, "initech-notify", line); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("it did not finish in %v", notifyBound)
		}
		return firstLine(err)
	}
	return nil
}

// firstLine keeps an exec error's first line: its later lines are the
// command's own output.
func firstLine(err error) error {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return fmt.Errorf("%s", s)
}
