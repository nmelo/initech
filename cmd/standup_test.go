package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/color"
	"github.com/spf13/cobra"
)

func TestRunStandup_PrintsFormattedSummary(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	writeStandupConfig(t, dir, "demo")

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "bd"), `#!/bin/sh
case "$*" in
  "list --status closed --json")
    cat <<'EOF'
[{"id":"ini-s.1","title":"Ship 1"},{"id":"ini-s.2","title":"Ship 2"},{"id":"ini-s.3","title":"Ship 3"},{"id":"ini-s.4","title":"Ship 4"},{"id":"ini-s.5","title":"Ship 5"},{"id":"ini-s.6","title":"Ship 6"},{"id":"ini-s.7","title":"Ship 7"},{"id":"ini-s.8","title":"Ship 8"},{"id":"ini-s.9","title":"Ship 9"},{"id":"ini-s.10","title":"Ship 10"},{"id":"ini-s.11","title":"Ship 11"},{"id":"ini-s.12","title":"Ship 12"}]
EOF
    ;;
  "list --status in_progress --json")
    cat <<'EOF'
[{"id":"ini-p.1","title":"Active item","assignee":"eng1"},{"id":"ini-p.2","title":"Needs owner","assignee":""}]
EOF
    ;;
  "ready --json")
    cat <<'EOF'
[{"id":"ini-r.1","title":"Ready 1"},{"id":"ini-r.2","title":"Ready 2"},{"id":"ini-r.3","title":"Ready 3"},{"id":"ini-r.4","title":"Ready 4"},{"id":"ini-r.5","title":"Ready 5"},{"id":"ini-r.6","title":"Ready 6"},{"id":"ini-r.7","title":"Ready 7"}]
EOF
    ;;
  *)
    echo "unexpected args: $*" >&2
    exit 1
    ;;
esac
`)

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	if err := runStandup(cmd, nil); err != nil {
		t.Fatalf("runStandup: %v", err)
	}

	got := out.String()
	wantHeader := "## demo Daily - " + time.Now().Format("2006-01-02")
	for _, want := range []string{
		wantHeader,
		"### What's New",
		"- ini-s.1: Ship 1 (shipped)",
		"- ini-s.10: Ship 10 (shipped)",
		"- ... and 2 more",
		"### In Progress",
		"- ini-p.1: Active item (eng1)",
		"- ini-p.2: Needs owner (unassigned)",
		"### Next Up",
		"- ini-r.5: Ready 5",
		"- ... and 2 more ready",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("standup output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ini-r.6: Ready 6") {
		t.Fatalf("standup output should limit next-up items to 5:\n%s", got)
	}
}

func TestRunStandup_ReturnsErrorWithoutConfig(t *testing.T) {
	restoreWD := chdirForTest(t, t.TempDir())
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	err := runStandup(cmd, nil)
	if err == nil {
		t.Fatal("runStandup should fail when no initech.yaml is present")
	}
	if !strings.Contains(err.Error(), "no initech.yaml found") {
		t.Fatalf("runStandup error = %v, want missing-config error", err)
	}
}

func TestRunStandup_PrintsMessageWhenBdMissing(t *testing.T) {
	restoreColor := disableColor(t)
	defer restoreColor()

	dir := t.TempDir()
	writeStandupConfig(t, dir, "demo")

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	writeExecutable(t, filepath.Join(binDir, "which"), "#!/bin/sh\nexit 1\n")

	t.Setenv("PATH", binDir)
	restoreWD := chdirForTest(t, dir)
	defer restoreWD()

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)

	if err := runStandup(cmd, nil); err != nil {
		t.Fatalf("runStandup: %v", err)
	}
	if !strings.Contains(out.String(), "bd not found. Install beads to generate standups.") {
		t.Fatalf("unexpected output when bd missing:\n%s", out.String())
	}
}

func disableColor(t *testing.T) func() {
	t.Helper()
	prev := color.Enabled()
	color.SetEnabled(false)
	return func() {
		color.SetEnabled(prev)
	}
}

func writeStandupConfig(t *testing.T, dir, project string) {
	t.Helper()
	cfg := "project: " + project + "\nroot: " + dir + "\nroles:\n  - eng1\n"
	if err := os.WriteFile(filepath.Join(dir, "initech.yaml"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("WriteFile initech.yaml: %v", err)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func chdirForTest(t *testing.T, dir string) func() {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%s): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("restore Chdir(%s): %v", wd, err)
		}
	}
}
