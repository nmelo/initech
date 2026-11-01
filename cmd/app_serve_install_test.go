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
	link   string // the path install is run as: a link, as Homebrew's is
	src    string // the real file it resolves to
}

// unsignedCodesign is codesign over a file that carries no signature.
func unsignedCodesign(args ...string) (string, error) {
	return "code object is not signed at all", errors.New("exit status 1")
}

// args is the job's command line for these settings: the copy, then the
// source link, then rest.
func (r *installRig) args(rest ...string) []string {
	return append([]string{appserve.CopyPath(r.home), "app-serve", "--source", r.link}, rest...)
}

// newInstallRig is a temp HOME with a token issued, a fake launchctl, a named
// binary and settings that need no rig folder.
func newInstallRig(t *testing.T) *installRig {
	t.Helper()
	skipWindows(t)
	r := &installRig{home: t.TempDir(), runner: &iexec.FakeRunner{}}
	t.Setenv("HOME", r.home)
	oldRunner, oldBin, oldDir, oldCodesign := appServeRunner, appServeBinary, appServeStateDir, appServeCodesign
	oldListen, oldPort, oldProjects, oldNotify := appServeListen, appServePort, appServeProjects, appServeNotify
	t.Cleanup(func() {
		appServeRunner, appServeBinary, appServeStateDir, appServeCodesign = oldRunner, oldBin, oldDir, oldCodesign
		appServeListen, appServePort, appServeProjects, appServeNotify = oldListen, oldPort, oldProjects, oldNotify
	})
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.src = filepath.Join(root, "Cellar", "initech", "2.0", "bin", "initech")
	r.link = filepath.Join(root, "bin", "initech")
	os.MkdirAll(filepath.Dir(r.src), 0o755)
	os.MkdirAll(filepath.Dir(r.link), 0o755)
	if err := os.WriteFile(r.src, []byte("#!/bin/sh\necho build 2.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Cellar/initech/2.0/bin/initech", r.link); err != nil {
		t.Fatal(err)
	}
	appServeRunner = r.runner
	appServeCodesign = unsignedCodesign
	appServeBinary = func() (string, error) { return r.link, nil }
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
	want := appserve.RenderPlist(r.home, os.Getenv("PATH"), r.args("--listen", "local-network", "--port", "7392", "--projects", "/rigs", "--notify", "/usr/local/bin/tell-me"))
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
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	appServeBinary = func() (string, error) { return bin, nil }
	if got := run(); !strings.Contains(got, "warning: "+bin+" ") {
		t.Errorf("the binary: warned %q", got)
	}
	appServeBinary = func() (string, error) { return r.link, nil }
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

// bootstrapProbe is a runner that, when launchctl bootstrap runs, records the
// log's mode at that moment.
type bootstrapProbe struct {
	iexec.FakeRunner
	log  string
	seen os.FileMode
	had  bool
}

func (b *bootstrapProbe) Run(name string, args ...string) (string, error) {
	if name == "launchctl" && len(args) > 0 && args[0] == "bootstrap" {
		if st, err := os.Stat(b.log); err == nil {
			b.seen, b.had = st.Mode().Perm(), true
		}
	}
	return b.FakeRunner.Run(name, args...)
}

// ini-r0q.21 AC2: install creates the log 0600 before bootstrap, so launchd
// appends to a private file; the job stays byte for byte as before. A
// reinstall narrows an older 0644 log and says so; one it cannot write is a
// warning and the install goes on.
func TestInstall_TheLogIsPrivateBeforeBootstrap(t *testing.T) {
	r := newInstallRig(t)
	probe := &bootstrapProbe{log: appserve.LogPath(r.home)}
	appServeRunner = probe
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if !probe.had || probe.seen != 0o600 {
		t.Fatalf("at bootstrap the log existed %v with mode %o, want 0600", probe.had, probe.seen)
	}
	want := appserve.RenderPlist(r.home, os.Getenv("PATH"), r.args("--listen", "local-network", "--port", "7392", "--projects", "/rigs"))
	if raw, _ := os.ReadFile(r.plist()); !bytes.Equal(raw, want) {
		t.Errorf("the job changed:\n%s", raw)
	}

	os.Chmod(probe.log, 0o644)
	var errw bytes.Buffer
	c := *appServeInstallCmd
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&errw)
	if err := runAppServeInstall(&c, nil); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(probe.log); fi.Mode().Perm() != 0o600 || !strings.Contains(errw.String(), "now 0600") {
		t.Errorf("reinstall over a 0644 log: mode %o, said %q", fi.Mode().Perm(), errw.String())
	}

	if os.Getuid() != 0 {
		os.Chmod(probe.log, 0o400)
		errw.Reset()
		if err := runAppServeInstall(&c, nil); err != nil {
			t.Errorf("an unwritable log stopped the install: %v", err)
		}
		if !strings.Contains(errw.String(), "could not be made private") {
			t.Errorf("an unwritable log: said %q, want a warning", errw.String())
		}
	}
}

// ini-unf AC2: the job runs a copy at one fixed path. Install writes a
// byte-identical copy of the file the as-run link resolves to, 0755 in a 0700
// folder, before the job; the plist's program is the copy and --source the
// link. Red at base: the plist ran the link and nothing was copied.
func TestInstall_TheJobRunsACopyAtTheFixedPath(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	cp := appserve.CopyPath(r.home)
	want, _ := os.ReadFile(r.src)
	got, err := os.ReadFile(cp)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the copy at %s: %q %v, want the bytes of %s", cp, got, err, r.src)
	}
	if fi, _ := os.Stat(cp); fi.Mode().Perm() != 0o755 {
		t.Errorf("copy mode %o, want 755", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(cp)); fi.Mode().Perm() != 0o700 {
		t.Errorf("copy folder mode %o, want 700", fi.Mode().Perm())
	}
	raw, _ := os.ReadFile(r.plist())
	if !bytes.Equal(raw, appserve.RenderPlist(r.home, os.Getenv("PATH"), r.args("--listen", "local-network", "--port", "7392", "--projects", "/rigs"))) {
		t.Errorf("the job does not run the copy with --source the link:\n%s", raw)
	}
	entries, _ := os.ReadDir(filepath.Dir(cp))
	if len(entries) != 1 {
		t.Errorf("the copy folder holds %d files, want only the copy (no temp left)", len(entries))
	}
}

// ini-unf AC2: a second install with the same settings and the same build
// says already installed and rewrites nothing (the copy is the same file);
// a new build under the link with the same settings is copied and the job
// restarted.
func TestInstall_ReinstallRewritesNothingUntilTheBuildChanges(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	cp := appserve.CopyPath(r.home)
	before, _ := os.Stat(cp)
	r.runner.Calls = nil
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(cp)
	if !strings.Contains(r.out.String(), "already installed") || len(r.runner.Calls) != 0 || !os.SameFile(before, after) {
		t.Errorf("same settings, same build: printed %q, calls %q, copy rewritten %v", r.out.String(), r.runner.Calls, !os.SameFile(before, after))
	}
	os.WriteFile(r.src, []byte("#!/bin/sh\necho build 2.1\n"), 0o755)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if !appserve.SameBytes(r.src, cp) || strings.Contains(r.out.String(), "already installed") || len(r.runner.Calls) != 2 {
		t.Errorf("a new build: copy current %v, printed %q, calls %q (want bootout and bootstrap)", appserve.SameBytes(r.src, cp), r.out.String(), r.runner.Calls)
	}
}

// ini-unf AC2: uninstall removes the copy and its folder.
func TestUninstall_RemovesTheCopyAndItsFolder(t *testing.T) {
	r := newInstallRig(t)
	if err := r.install(); err != nil {
		t.Fatal(err)
	}
	if err := r.uninstall(); err != nil {
		t.Fatal(err)
	}
	cp := appserve.CopyPath(r.home)
	for _, p := range []string{cp, filepath.Dir(cp)} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s remains after uninstall (%v)", p, err)
		}
	}
}

// ini-unf AC2 feed-it-nothing: a source that cannot be read, a link to
// nothing, and a copy that does not verify each refuse install with the
// reason; no job is written and no launchctl runs.
func TestInstall_ACopyThatCannotBeMadeOrCheckedWritesNoJob(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	for name, tc := range map[string]struct {
		setup func(r *installRig)
		want  string
	}{
		"unreadable": {func(r *installRig) { os.Chmod(r.src, 0) }, "permission denied"},
		"link to nothing": {func(r *installRig) { os.Remove(r.src) }, "resolve"},
		"does not verify": {func(r *installRig) {
			appServeCodesign = func(args ...string) (string, error) {
				if args[0] == "--verify" {
					return "invalid signature (code or signature have been modified)", errors.New("exit status 1")
				}
				return "Identifier=com.neblihq.initech\nTeamIdentifier=K7Z8YKVA8G\n", nil
			}
		}, "does not verify"},
		"another identity": {func(r *installRig) {
			// The copy verifies, but reads back another team than its source.
			appServeCodesign = func(args ...string) (string, error) {
				if args[0] == "--verify" {
					return "", nil
				}
				if strings.Contains(args[len(args)-1], "Cellar") {
					return "Identifier=com.neblihq.initech\nTeamIdentifier=K7Z8YKVA8G\n", nil
				}
				return "Identifier=com.neblihq.initech\nTeamIdentifier=OTHERTEAM1\n", nil
			}
		}, "not com.neblihq.initech (team K7Z8YKVA8G)"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newInstallRig(t)
			tc.setup(r)
			err := r.install()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("install: %v, want a refusal naming %q", err, tc.want)
			}
			if r.plistThere() || len(r.runner.Calls) != 0 {
				t.Errorf("a refused copy wrote the job (%v) or ran launchctl %q", r.plistThere(), r.runner.Calls)
			}
			if _, err := os.Stat(appserve.CopyPath(r.home)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused copy left a file at the fixed path (%v)", err)
			}
		})
	}
}
