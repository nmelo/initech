package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/recording"
	"github.com/nmelo/initech/internal/tui"
)

// ini-pqdy.2: --record and record_dir decide where a session records.
func TestResolveRecordDir_FlagConfigAndDefault(t *testing.T) {
	home, _ := os.UserHomeDir()
	abs, _ := filepath.Abs("rel/dir")
	for _, tc := range []struct {
		name    string
		flagSet bool
		flag    string
		config  string
		want    string
	}{
		{"off by default", false, "", "", ""},
		{"config key turns it on", false, "", "/tmp/recs", "/tmp/recs"},
		{"bare --record is the default dir", true, recordFlagDefault, "", tui.DefaultRecordingDir()},
		{"flag wins over config", true, "/tmp/flag", "/tmp/config", "/tmp/flag"},
		{"explicit empty flag turns config off", true, "", "/tmp/config", ""},
		{"tilde expands", false, "", "~/recs", filepath.Join(home, "recs")},
		{"relative becomes absolute", true, "rel/dir", "", abs},
	} {
		got, err := resolveRecordDir(tc.flagSet, tc.flag, tc.config)
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
	if d := tui.DefaultRecordingDir(); home != "" && !strings.HasPrefix(d, filepath.Join(home, ".initech")) {
		t.Errorf("default recording dir %q is not under the user's home", d)
	}
}

// The --record flag exists, is off unless given, and a bare --record parses.
func TestRecordFlag_BareFormTakesTheDefault(t *testing.T) {
	f := rootCmd.Flags().Lookup("record")
	if f == nil {
		t.Fatal("no --record flag")
	}
	if f.DefValue != "" || f.NoOptDefVal != recordFlagDefault {
		t.Errorf("--record default=%q bare=%q", f.DefValue, f.NoOptDefVal)
	}
}

// initech recording shape writes a shape-only copy and refuses to overwrite.
func TestRecordingShapeCommand_WritesAShapeCopy(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "eng1.irec")
	f, _ := os.Create(in)
	w, _ := recording.NewWriter(f, recording.Header{Pane: "eng1", Cols: 80, Rows: 24})
	_ = w.Output(time.Millisecond, []byte("top secret\r\n"))
	_ = w.Trailer(2*time.Millisecond, 0)
	f.Close()

	out := filepath.Join(dir, "eng1.shape.irec")
	var stdout bytes.Buffer
	recordingShapeCmd.SetOut(&stdout)
	if err := recordingShapeCmd.RunE(recordingShapeCmd, []string{in, out}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if bytes.Contains(b, []byte("secret")) {
		t.Error("shape copy still holds the text")
	}
	rd, err := recording.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if !rd.Header().ShapeOnly {
		t.Error("shape copy header not marked shape_only")
	}
	rd.Close()
	if !strings.Contains(stdout.String(), "2 records") {
		t.Errorf("output = %q", stdout.String())
	}
	if err := recordingShapeCmd.RunE(recordingShapeCmd, []string{in, out}); err == nil {
		t.Error("shape overwrote an existing output file")
	}
	if err := recordingShapeCmd.RunE(recordingShapeCmd, []string{in, in}); err == nil {
		t.Error("shape accepted the input as its own output")
	}
}
