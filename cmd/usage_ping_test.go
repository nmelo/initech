package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/nmelo/initech/internal/usage"
	"github.com/spf13/cobra"
)

func noEnv(string) string { return "" }

// ini-62tn scope and switches: the hook exists only for window 1 with
// telemetry not turned off by the file or by DO_NOT_TRACK.
func TestUsagePingHook_OnlyWindowOneWithTelemetryOn(t *testing.T) {
	no := false
	dnt := func(k string) string {
		if k == "DO_NOT_TRACK" {
			return "1"
		}
		return ""
	}
	cases := []struct {
		name   string
		proj   *config.Project
		window int
		getenv func(string) string
		want   bool
	}{
		{"window 1, key absent: on", &config.Project{}, 0, noEnv, true},
		{"window 2 viewer: off", &config.Project{}, 2, noEnv, false},
		{"telemetry: false: off", &config.Project{Telemetry: &no}, 0, noEnv, false},
		{"DO_NOT_TRACK=1: off", &config.Project{}, 0, dnt, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usagePingHook(c.proj, c.window, c.getenv) != nil; got != c.want {
				t.Fatalf("hook present = %v, want %v", got, c.want)
			}
		})
	}
}

// Reachability inside the command: runTUI hands the hook to the TUI for a
// plain start, and hands nil when the config turns telemetry off.
func TestRunTUI_PassesUsageHookOnlyWhenEnabled(t *testing.T) {
	skipWindows(t)
	no := false
	for _, c := range []struct {
		name      string
		telemetry *bool
		want      bool
	}{{"absent key", nil, true}, {"telemetry false", &no, false}} {
		t.Run(c.name, func(t *testing.T) {
			dir := shortProjectDir(t)
			mustWriteFile(t, filepath.Join(dir, "eng1", "CLAUDE.md"), "# eng1")
			if err := config.Write(filepath.Join(dir, "initech.yaml"), &config.Project{Name: "demo", Root: dir, Roles: []string{"eng1"}, Telemetry: c.telemetry}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("INITECH_MOCK_AGENT", "/bin/echo")
			t.Setenv("DO_NOT_TRACK", "")
			defer chdirForTest(t, dir)()
			var got bool
			defer stubTUIRun(t, func(cfg tui.Config) error { got = cfg.AfterFirstFrame != nil; return nil })()
			if err := runTUI(&cobra.Command{}, nil); err != nil {
				t.Fatalf("runTUI: %v", err)
			}
			if got != c.want {
				t.Fatalf("AfterFirstFrame present = %v, want %v", got, c.want)
			}
		})
	}
}

// The hook does nothing on disk until it runs: building it for a plain start
// creates no install id (the id is made inside the ping, after the first frame).
func TestUsagePingHook_BuildingItTouchesNoFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if usagePingHook(&config.Project{}, 0, noEnv) == nil {
		t.Fatal("hook nil for a plain start")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "initech")); !os.IsNotExist(err) {
		t.Fatalf("config dir created by building the hook: %v", err)
	}
}

// ini-62tn: initech --help ends with the usage notice; a subcommand's help
// does not carry it (positive twin keeps the root check honest).
func TestHelp_RootEndsWithUsageNoticeSubcommandsDoNot(t *testing.T) {
	root := strings.TrimRight(rootCmd.UsageString(), "\n")
	if !strings.HasSuffix(root, usage.Notice) {
		t.Fatalf("root help does not end with the notice:\n...%s", root[max(0, len(root)-300):])
	}
	for _, c := range rootCmd.Commands() {
		if strings.Contains(c.UsageString(), usage.Notice) {
			t.Errorf("%s help carries the usage notice", c.Name())
		}
	}
}
