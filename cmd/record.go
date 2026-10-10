package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nmelo/initech/internal/recording"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// recordFlagDefault is what a bare --record carries; resolveRecordDir turns
// it into the default directory. A sentinel rather than the path itself, so
// the help text does not bake in one user's home.
const recordFlagDefault = "default"

// recordFlagDefaultHint names the default directory in --help.
const recordFlagDefaultHint = "~/.initech/recordings"

// resolveRecordDir decides where this session records PTY output, or "" for
// off (ini-pqdy.2). The flag wins over the record_dir config key; a bare
// --record means the default directory under the user's home; ~/ expands.
func resolveRecordDir(flagSet bool, flagVal, configVal string) (string, error) {
	v := configVal
	if flagSet {
		v = flagVal
	}
	v = strings.TrimSpace(v)
	switch {
	case v == "":
		return "", nil
	case v == recordFlagDefault:
		return tui.DefaultRecordingDir(), nil
	case v == "~" || strings.HasPrefix(v, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("record directory %q: %w", v, err)
		}
		v = filepath.Join(home, strings.TrimPrefix(v, "~"))
	}
	abs, err := filepath.Abs(v)
	if err != nil {
		return "", fmt.Errorf("record directory %q: %w", v, err)
	}
	return abs, nil
}

var recordingCmd = &cobra.Command{
	Use:   "recording",
	Short: "Work with PTY recordings made by initech --record",
}

var recordingShapeCmd = &cobra.Command{
	Use:   "shape <in.irec> <out.irec>",
	Short: "Write a shape-only copy of a recording: same timing, sizes and escape sequences, text replaced",
	Long: `Write a shape-only copy of a PTY recording for sharing.

Escape sequences pass through untouched; printable ASCII outside them becomes
'x'; other bytes (controls, UTF-8) are kept. Record count, timings and sizes
are unchanged, so the copy costs a terminal the same work as the original.

Text inside escape sequences is kept: window titles and hyperlink targets
set by OSC survive in the copy.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		in, out := args[0], args[1]
		if abs := func(p string) string { a, _ := filepath.Abs(p); return a }; abs(in) == abs(out) {
			return fmt.Errorf("shape: input and output are the same file")
		}
		src, err := os.Open(in)
		if err != nil {
			return err
		}
		defer src.Close()
		dst, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		n, err := recording.ShapeCopy(dst, src)
		if cerr := dst.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(out)
			return fmt.Errorf("shape %s: %w", in, err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %s (%d records)\n", out, n)
		return nil
	},
}

func init() {
	recordingCmd.AddCommand(recordingShapeCmd)
	rootCmd.AddCommand(recordingCmd)
}
