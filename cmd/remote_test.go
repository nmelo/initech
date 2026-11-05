package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ini-7op8: initech remote add and remove edit initech.yaml and never take a
// token on the command line.

const plantedToken = "PLANTED-7op8-TOKEN"

// handYAML is shaped as initech init writes one: four-space steps.
const handYAML = `project: rig
root: /tmp/rig
beads:
    prefix: rig
roles:
    - eng1
# remotes are added below this line by hand or by initech remote add
`

func remoteRig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "initech.yaml")
	if err := os.WriteFile(path, []byte(handYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// No initech running: the signal says so.
	t.Setenv("INITECH_SOCKET", filepath.Join(dir, "none.sock"))
	old := readRemoteToken
	t.Cleanup(func() { readRemoteToken = old })
	readRemoteToken = func(string) (string, bool, error) { return "", false, nil }
	return path
}

func runRemote(t *testing.T, args ...string) (string, error) {
	t.Helper()
	remoteAddr, remoteTokenFile, remoteTokenFlag, remoteRoles = "", "", "", nil
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"remote"}, args...))
	defer rootCmd.SetArgs(nil)
	err := rootCmd.Execute()
	// The flag set keeps "changed" across Execute calls; reset it.
	if f := remoteAddCmd.Flags().Lookup("token"); f != nil {
		f.Changed = false
	}
	return out.String(), err
}

func readRemoteYAML(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRemoteAdd_RefusesATokenOnTheCommandLine(t *testing.T) {
	path := remoteRig(t)
	out, err := runRemote(t, "add", "workbench", "--addr", "127.0.0.1:7391", "--token", plantedToken)
	if err == nil || !strings.Contains(err.Error(), "never taken on the command line") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(out+err.Error(), plantedToken) {
		t.Fatal("the refusal repeats the token")
	}
	if readRemoteYAML(t, path) != handYAML {
		t.Fatal("a refused add changed initech.yaml")
	}
}

func TestRemoteAdd_ATokenFileOthersCanReadIsRefused(t *testing.T) {
	skipWindows(t) // No owner-only mode bits there.
	path := remoteRig(t)
	tf := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(tf, []byte(plantedToken+"\n"), 0o644)
	os.Chmod(tf, 0o644)
	_, err := runRemote(t, "add", "workbench", "--addr", "127.0.0.1:7391", "--token-file", tf)
	if err == nil || !strings.Contains(err.Error(), "mode 0644") || strings.Contains(err.Error(), plantedToken) {
		t.Fatalf("err %v", err)
	}
	if readRemoteYAML(t, path) != handYAML {
		t.Fatal("a refused add changed initech.yaml")
	}
}

// TestRemoteAdd_WritesTheEntryAsAHandWouldAndNeverPrintsTheToken is AC4.
func TestRemoteAdd_WritesTheEntryAsAHandWouldAndNeverPrintsTheToken(t *testing.T) {
	path := remoteRig(t)
	tf := filepath.Join(t.TempDir(), "tok")
	os.WriteFile(tf, []byte(plantedToken+"\n"), 0o600)
	out, err := runRemote(t, "add", "workbench", "--addr", "127.0.0.1:7391", "--token-file", tf, "--roles", "eng8,eng9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, plantedToken) {
		t.Fatalf("the output carries the token:\n%s", out)
	}
	if !strings.Contains(out, "added remote workbench (127.0.0.1:7391)") || !strings.Contains(out, "initech is not running here") {
		t.Fatalf("printed:\n%s", out)
	}
	want := `project: rig
root: /tmp/rig
beads:
    prefix: rig
roles:
    - eng1
remotes:
    workbench:
        addr: 127.0.0.1:7391
        token: ` + plantedToken + `
        roles:
            - eng8
            - eng9
# remotes are added below this line by hand or by initech remote add
`
	if got := readRemoteYAML(t, path); got != want {
		t.Fatalf("initech.yaml is\n%s\nwant\n%s", got, want)
	}
	if fi, _ := os.Stat(path); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("initech.yaml mode %o", fi.Mode().Perm())
	}
	// Remove gives back the file as it was, byte for byte.
	out, err = runRemote(t, "remove", "workbench")
	if err != nil || !strings.Contains(out, "nothing on it was stopped") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if got := readRemoteYAML(t, path); got != handYAML {
		t.Fatalf("after remove initech.yaml is\n%s", got)
	}
}

func TestRemoteAdd_ThePromptIsUsedWithNoFileAndTheTokenIsNotShown(t *testing.T) {
	path := remoteRig(t)
	var asked string
	readRemoteToken = func(prompt string) (string, bool, error) { asked = prompt; return plantedToken, true, nil }
	out, err := runRemote(t, "add", "workbench", "--addr", "127.0.0.1:7391")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(asked, "nothing is shown") || strings.Contains(out, plantedToken) {
		t.Fatalf("asked %q, printed %q", asked, out)
	}
	if !strings.Contains(readRemoteYAML(t, path), "token: "+plantedToken) {
		t.Fatal("the typed token was not written")
	}
}

func TestRemoteAdd_NoTerminalAndNoFileUsesTheProjectsToken(t *testing.T) {
	path := remoteRig(t)
	out, err := runRemote(t, "add", "workbench", "--addr", "127.0.0.1:7391")
	if err != nil || !strings.Contains(out, "uses the project's token") {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(readRemoteYAML(t, path), "token:") {
		t.Fatal("a token key was written with no token")
	}
}

func TestRemote_RefusesWhatIsNotARemote(t *testing.T) {
	path := remoteRig(t)
	if _, err := runRemote(t, "add", "work.bench", "--addr", "h:1"); err == nil {
		t.Error("a name with a dot was accepted")
	}
	if _, err := runRemote(t, "add", "workbench", "--addr", "nowhere"); err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Errorf("an addr with no port: %v", err)
	}
	if _, err := runRemote(t, "remove", "ghost"); err == nil || !strings.Contains(err.Error(), "no remotes") {
		t.Errorf("removing a name that is not there: %v", err)
	}
	runRemote(t, "add", "workbench", "--addr", "h:1")
	if _, err := runRemote(t, "add", "workbench", "--addr", "h:2"); err == nil || !strings.Contains(err.Error(), "remove it first") {
		t.Errorf("a second add of the same name: %v", err)
	}
	if _, err := runRemote(t, "remove", "ghost"); err == nil || !strings.Contains(err.Error(), "it has workbench") {
		t.Errorf("removing a name that is not there: %v", err)
	}
	if !strings.Contains(readRemoteYAML(t, path), "addr: h:1") {
		t.Fatal("a refused add replaced the entry")
	}
}
