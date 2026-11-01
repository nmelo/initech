package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/appserve"
	iexec "github.com/nmelo/initech/internal/exec"
)

// ini-r0q.12: initech app-serve install and uninstall. Every test runs under a
// temp HOME with a fake launchctl: none reaches the real gui domain.

type installRig struct {
	home   string
	runner *iexec.FakeRunner
	out    bytes.Buffer
}

// newInstallRig is a temp HOME with a token issued, a fake launchctl, a named
// binary and settings that need no rig folder.
func newInstallRig(t *testing.T) *installRig {
	t.Helper()
	skipWindows(t)
	r := &installRig{home: t.TempDir(), runner: &iexec.FakeRunner{}}
	t.Setenv("HOME", r.home)
	oldRunner, oldBin, oldDir := appServeRunner, appServeBinary, appServeStateDir
	oldListen, oldPort, oldProjects, oldNotify := appServeListen, appServePort, appServeProjects, appServeNotify
	t.Cleanup(func() {
		appServeRunner, appServeBinary, appServeStateDir = oldRunner, oldBin, oldDir
		appServeListen, appServePort, appServeProjects, appServeNotify = oldListen, oldPort, oldProjects, oldNotify
	})
	appServeRunner = r.runner
	appServeBinary = func() (string, error) { return "/opt/homebrew/bin/initech", nil }
	appServeStateDir = func() string { return filepath.Join(r.home, ".config", "initech", "app-serve") }
	appServeListen, appServePort, appServeProjects, appServeNotify = appserve.ListenLocalNetwork, 7392, []string{"/rigs"}, ""
	if _, err := appserve.IssueToken(appServeStateDir()); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r *installRig) install() error {
	r.out.Reset()
	c := *appServeInstallCmd
	c.SetOut(&r.out)
	return runAppServeInstall(&c, nil)
}

func (r *installRig) uninstall() error {
	r.out.Reset()
	c := *appServeUninstallCmd
	c.SetOut(&r.out)
	return runAppServeUninstall(&c, nil)
}

func (r *installRig) plist() string { return appserve.PlistPath(r.home) }

func (r *installRig) plistThere() bool {
	_, err := os.Stat(r.plist())
	return err == nil
}

func (r *installRig) bootstrapCall() string {
	return fmt.Sprintf("|launchctl bootstrap gui/%d %s", os.Getuid(), r.plist())
}

func bootoutCall() string {
	return fmt.Sprintf("|launchctl bootout gui/%d/com.initech.app-serve", os.Getuid())
}

// AC1: install writes the job, makes the two folders 0700, and bootstraps it
// in the gui domain.
func TestInstall_WritesTheJobAndBootstrapsIt(t *testing.T) {
	r := newInstallRig(t)
	appServeNotify = "/usr/local/bin/tell-me"
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(r.plist())
	if err != nil {
		t.Fatal(err)
	}
	want := appserve.RenderPlist(r.home, os.Getenv("PATH"), []string{"/opt/homebrew/bin/initech", "app-serve", "--listen", "local-network", "--port", "7392", "--projects", "/rigs", "--notify", "/usr/local/bin/tell-me"})
	if !bytes.Equal(raw, want) {
		t.Errorf("the job:\n%s\nwant:\n%s", raw, want)
	}
	if fi, _ := os.Stat(r.plist()); fi.Mode().Perm() != 0o644 {
		t.Errorf("job mode %o, want 644", fi.Mode().Perm())
	}
	for _, d := range []string{filepath.Dir(r.plist()), filepath.Dir(appserve.LogPath(r.home))} {
		fi, err := os.Stat(d)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v, mode %v, want 0700", d, err, fi)
		}
	}
	if len(r.runner.Calls) != 1 || r.runner.Calls[0] != r.bootstrapCall() {
		t.Errorf("launchctl calls %q, want %q", r.runner.Calls, r.bootstrapCall())
	}
	if !strings.Contains(r.out.String(), r.plist()) {
		t.Errorf("printed %q", r.out.String())
	}
}

// A folder that already exists keeps its mode.
func TestInstall_LeavesAnExistingFoldersModeAlone(t *testing.T) {
	r := newInstallRig(t)
	dir := filepath.Dir(r.plist())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o755 {
		t.Errorf("LaunchAgents mode %o, want its own 755", fi.Mode().Perm())
	}
}

// With --notify unset the job carries no --notify.
func TestInstall_NoNotifyMeansNoNotifyArgument(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(r.plist()); strings.Contains(string(raw), "--notify") {
		t.Errorf("the job names --notify:\n%s", raw)
	}
}

// AC1: a second install with the same settings changes nothing: same bytes,
// already installed, no launchctl call.
func TestInstall_ASecondInstallIsIdempotent(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(r.plist())
	if err := r.install(); err != nil {
		t.Fatalf("second install: %v", err)
	}
	second, _ := os.ReadFile(r.plist())
	if !bytes.Equal(first, second) || !strings.HasPrefix(r.out.String(), "already installed") {
		t.Errorf("same %v, printed %q", bytes.Equal(first, second), r.out.String())
	}
	if len(r.runner.Calls) != 1 {
		t.Errorf("launchctl calls %q, want only the first bootstrap", r.runner.Calls)
	}
}

// New settings replace the job: bootout first, then bootstrap.
func TestInstall_NewSettingsReplaceTheJob(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	appServePort = 7400
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(r.plist()); !strings.Contains(string(raw), "<string>7400</string>") {
		t.Errorf("the job was not replaced:\n%s", raw)
	}
	want := []string{r.bootstrapCall(), bootoutCall(), r.bootstrapCall()}
	if strings.Join(r.runner.Calls, ";") != strings.Join(want, ";") {
		t.Errorf("launchctl calls %q, want %q", r.runner.Calls, want)
	}
}

// AC1: uninstall boots the job out and removes the file; with nothing
// installed it says so, exit 0, with no launchctl call.
func TestUninstall_BootsOutAndRemoves(t *testing.T) {
	r := newInstallRig(t)
	if err := r.uninstall(); err != nil || !strings.HasPrefix(r.out.String(), "not installed") || len(r.runner.Calls) != 0 {
		t.Fatalf("nothing installed: err %v, printed %q, calls %q", err, r.out.String(), r.runner.Calls)
	}
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if err := r.uninstall(); err != nil {
		t.Fatal(err)
	}
	if r.plistThere() {
		t.Error("the job file remains")
	}
	if got := r.runner.Calls[len(r.runner.Calls)-1]; got != bootoutCall() {
		t.Errorf("last launchctl call %q, want %q", got, bootoutCall())
	}
}

// launchctl's own words for a job not loaded mean it is gone already; any
// other failure keeps the file and is named.
func TestUninstall_ANotLoadedJobIsGoneAndAnyOtherFailureKeepsIt(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	r.runner.Err = errors.New("launchctl bootout: exit status 3\nBoot-out failed: 3: No such process")
	if err := r.uninstall(); err != nil || r.plistThere() {
		t.Errorf("not loaded: err %v, file there %v", err, r.plistThere())
	}
	r.runner.Err = nil
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	r.runner.Err = errors.New("launchctl bootout: exit status 5\nBoot-out failed: 5: Input/output error")
	err := r.uninstall()
	if v69Exit(err) != 1 || err == nil || !strings.Contains(err.Error(), "launchctl bootout failed") || !r.plistThere() {
		t.Errorf("other failure: err %v, file there %v", err, r.plistThere())
	}
}

// AC5: launchctl failing: the job file is removed again and the error named,
// exit 1. launchctl missing, through the real runner, the same.
func TestInstall_ALaunchctlFailureRemovesTheJobAgain(t *testing.T) {
	r := newInstallRig(t)
	r.runner.Err = errors.New("launchctl bootstrap: exit status 5")
	err := r.install()
	if v69Exit(err) != 1 || err == nil || !strings.Contains(err.Error(), "launchctl bootstrap failed") || r.plistThere() {
		t.Errorf("err %v, file there %v", err, r.plistThere())
	}
	m := newInstallRig(t)
	appServeRunner = &iexec.DefaultRunner{}
	t.Setenv("PATH", t.TempDir())
	err = m.install()
	if v69Exit(err) != 1 || err == nil || !strings.Contains(err.Error(), "launchctl") || m.plistThere() {
		t.Errorf("missing: err %v, file there %v", err, m.plistThere())
	}
}

// AC5: every refusal names itself and leaves nothing written and nothing
// called.
func TestInstall_RefusalsWriteNothing(t *testing.T) {
	for name, c := range map[string]struct {
		set  func(*testing.T, *installRig)
		want string
	}{
		"HOME unset": {func(t *testing.T, r *installRig) { t.Setenv("HOME", "") }, "HOME is not set"},
		"PATH unset": {func(t *testing.T, r *installRig) { t.Setenv("PATH", "") }, "PATH is not set"},
		"no token": {func(t *testing.T, r *installRig) {
			os.Remove(filepath.Join(appServeStateDir(), appserve.TokenFile))
		}, "run initech app-serve token first"},
		"no rig and no --projects": {func(t *testing.T, r *installRig) {
			appServeProjects = nil
			t.Chdir(t.TempDir())
		}, "no rig folder"},
		"an unknown --listen": {func(t *testing.T, r *installRig) { appServeListen = "everywhere" }, `--listen takes tailnet or local-network, not "everywhere"`},
		"no binary path": {func(t *testing.T, r *installRig) {
			appServeBinary = func() (string, error) { return "", errors.New("find initech on PATH: not found") }
		}, "find initech on PATH"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInstallRig(t)
			c.set(t, r)
			err := r.install()
			if v69Exit(err) != 1 || err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
			if len(r.runner.Calls) != 0 {
				t.Errorf("launchctl calls %q", r.runner.Calls)
			}
			if _, statErr := os.Stat(filepath.Join(r.home, "Library")); statErr == nil {
				t.Error("~/Library was written")
			}
		})
	}
}

// AC5: uninstall with HOME unset is the same named error.
func TestUninstall_HomeUnsetIsANamedError(t *testing.T) {
	r := newInstallRig(t)
	t.Setenv("HOME", "")
	err := r.uninstall()
	if err == nil || !strings.Contains(err.Error(), "HOME is not set") || len(r.runner.Calls) != 0 {
		t.Errorf("err %v, calls %q", err, r.runner.Calls)
	}
}

// G4: --projects is resolved at install time and written in full: launchd
// starts the job in /. --listen tailnet is not refused while Tailscale is
// stopped.
func TestInstall_ProjectsAreResolvedNow(t *testing.T) {
	r := newInstallRig(t)
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rig := filepath.Join(parent, "myrig")
	if err := os.MkdirAll(rig, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "project: myrig\nroot: " + rig + "\nroles: [a]\n"
	if err := os.WriteFile(filepath.Join(rig, "initech.yaml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(rig)
	appServeProjects = nil
	appServeListen = appserve.ListenTailnet
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(r.plist())
	if !strings.Contains(string(raw), "<string>--projects</string>\n\t\t<string>"+parent+"</string>") {
		t.Errorf("the job does not name %s:\n%s", parent, raw)
	}
	if !strings.Contains(string(raw), "<string>tailnet</string>") {
		t.Errorf("the job does not say tailnet:\n%s", raw)
	}
	// A relative --projects is made absolute, each one kept.
	r2 := newInstallRig(t)
	t.Chdir(parent)
	appServeProjects = []string{"myrig", "/other"}
	if err := r2.install(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(r2.plist())
	for _, want := range []string{"<string>" + rig + "</string>", "<string>/other</string>"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the job lacks %s:\n%s", want, raw)
		}
	}
}

// G6: the binary is what was run: through PATH when bare, a symlink kept, so
// a Homebrew link never becomes the Cellar path behind it.
func TestInstall_TheBinaryPathKeepsItsSymlink(t *testing.T) {
	skipWindows(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cellar := filepath.Join(root, "Cellar", "initech", "2.0", "bin")
	bin := filepath.Join(root, "bin")
	for _, d := range []string{cellar, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(cellar, "initech")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bin, "initech")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got, err := binaryAsRun("initech"); err != nil || got != link {
		t.Errorf("bare: %q, %v; want %s", got, err, link)
	}
	if got, err := binaryAsRun(link); err != nil || got != link {
		t.Errorf("by path: %q, %v; want %s", got, err, link)
	}
	t.Chdir(root)
	if got, err := binaryAsRun("bin/initech"); err != nil || got != link {
		t.Errorf("relative: %q, %v; want %s", got, err, link)
	}
	if _, err := binaryAsRun("no-such-binary-here"); err == nil || !strings.Contains(err.Error(), "on PATH") {
		t.Errorf("not on PATH: %v", err)
	}
}

// The job carries the shell's PATH, alone, so the service finds bd; a changed
// PATH is new settings and replaces the job.
func TestInstall_TheJobCarriesTheShellsPath(t *testing.T) {
	r := newInstallRig(t)
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin")
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(r.plist())
	want := "<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>PATH</key>\n\t\t<string>/opt/homebrew/bin:/usr/bin</string>\n\t</dict>"
	if !strings.Contains(string(raw), want) {
		t.Errorf("the job lacks the PATH:\n%s", raw)
	}
	t.Setenv("PATH", "/elsewhere/bin:/usr/bin")
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(r.plist())
	if !strings.Contains(string(raw), "<string>/elsewhere/bin:/usr/bin</string>") {
		t.Errorf("a changed PATH did not replace the job:\n%s", raw)
	}
	wantCalls := []string{r.bootstrapCall(), bootoutCall(), r.bootstrapCall()}
	if strings.Join(r.runner.Calls, ";") != strings.Join(wantCalls, ";") {
		t.Errorf("launchctl calls %q, want %q", r.runner.Calls, wantCalls)
	}
}

// Super's finding: install warns when --projects or the binary is under a
// folder macOS guards for launchd jobs, names it, and goes on; a folder merely
// named like one, or none, says nothing. Nothing here reads those folders.
func TestInstall_WarnsAboutGuardedFolders(t *testing.T) {
	r := newInstallRig(t)
	var errOut bytes.Buffer
	run := func() string {
		errOut.Reset()
		c := *appServeInstallCmd
		c.SetOut(&r.out)
		c.SetErr(&errOut)
		if err := runAppServeInstall(&c, nil); err != nil {
			t.Fatal(err)
		}
		return errOut.String()
	}
	if got := run(); got != "" {
		t.Errorf("no guarded folder, warned %q", got)
	}
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		dir := filepath.Join(r.home, name, "Projects")
		appServeProjects = []string{dir}
		if got := run(); !strings.Contains(got, "warning: "+dir+" is in a folder macOS guards") {
			t.Errorf("%s: warned %q", name, got)
		}
		if !r.plistThere() {
			t.Errorf("%s: install did not go on", name)
		}
	}
	appServeProjects = []string{filepath.Join(r.home, "Desktopish", "x")}
	if got := run(); got != "" {
		t.Errorf("a folder merely named like one warned %q", got)
	}
	appServeProjects = []string{"/rigs"}
	bin := filepath.Join(r.home, "Desktop", "initech")
	appServeBinary = func() (string, error) { return bin, nil }
	if got := run(); !strings.Contains(got, "warning: "+bin+" ") {
		t.Errorf("the binary: warned %q", got)
	}
	appServeBinary = func() (string, error) { return "/opt/homebrew/bin/initech", nil }
	entry := filepath.Join(r.home, "Desktop", "tools", "bin")
	t.Setenv("PATH", "/usr/bin"+string(filepath.ListSeparator)+entry)
	if got := run(); !strings.Contains(got, "warning: "+entry+" ") {
		t.Errorf("a PATH entry: warned %q", got)
	}
}

// The subcommands and --notify are on app-serve.
func TestInstall_TheCommandsAreRegistered(t *testing.T) {
	for _, name := range []string{"install", "uninstall", "token"} {
		c, _, err := appServeCmd.Find([]string{name})
		if err != nil || c.Name() != name {
			t.Errorf("app-serve %s: %v", name, err)
		}
	}
	for _, c := range []*struct {
		name string
		ok   bool
	}{{"serve --notify", appServeCmd.Flags().Lookup("notify") != nil}, {"install --notify", appServeInstallCmd.Flags().Lookup("notify") != nil}, {"install --projects", appServeInstallCmd.Flags().Lookup("projects") != nil}} {
		if !c.ok {
			t.Errorf("%s is missing", c.name)
		}
	}
}
