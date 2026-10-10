package cmd

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nmelo/initech/internal/recording"
	"github.com/nmelo/initech/internal/replay"
	"github.com/spf13/cobra"
)

var (
	replayOffset    time.Duration
	replayScale     float64
	replayLoop      bool
	replayStopAfter time.Duration
)

// replayCmd is the lab replayer (ini-pqdy.3): a child process that plays a
// recorded agent's output back as if it were the agent, so a rig can run N
// panes of real fleet output without N real agents. Used as a role command:
//
//	command: [initech, replay, <file.irec>, --offset, 30s]
//	agent_type: generic
//	no_bracketed_paste: true
//
// A subcommand rather than a separate binary so it ships in the install every
// lab host already has. Hidden: it is a lab tool, not an operator verb.
var replayCmd = &cobra.Command{
	Use:    "replay <recording.irec>",
	Short:  "Play a recorded agent's output back on its recorded schedule (lab tool)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE:   runReplay,
}

func init() {
	replayCmd.Flags().DurationVar(&replayOffset, "offset", 0, "Start playback at this recording time; earlier output is skipped")
	replayCmd.Flags().Float64Var(&replayScale, "scale", 1, "Divide every interval by this (2 plays twice as fast)")
	replayCmd.Flags().BoolVar(&replayLoop, "loop", false, "Play the recording again when it ends; passes after the first start from the beginning (--offset only shifts the first)")
	replayCmd.Flags().DurationVar(&replayStopAfter, "stop-after", 0, "Exit after this much wall time (0 = never)")
	rootCmd.AddCommand(replayCmd)
}

func runReplay(cmd *cobra.Command, args []string) error {
	path := args[0]
	// Refuse a bad file before playing anything, with the reader's own error.
	r, err := recording.Open(path)
	if err != nil {
		return err
	}
	r.Close()

	// Clean exit on SIGTERM/SIGINT and on stdin EOF (pqdy.3 behaviour 4). As a
	// pane child, stdin is the pane: keys initech sends are read and dropped,
	// and the PTY closing ends the process.
	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// The reader is taken HERE, on the command's own goroutine (ini-m3tc). The
	// watcher outlives the command -- it blocks on stdin until the pane closes
	// -- so calling cmd.InOrStdin() inside it read cobra's state after the
	// command returned, racing whoever touches the command next (the race
	// detector caught it at the v2.19.0 gate).
	in := cmd.InOrStdin()
	go func() {
		_, _ = io.Copy(io.Discard, in)
		cancel()
	}()

	return replay.Play(ctx, replay.FileOpener(path), cmd.OutOrStdout(), replay.Options{
		Offset:    replayOffset,
		Scale:     replayScale,
		Loop:      replayLoop,
		StopAfter: replayStopAfter,
	}, replay.Real())
}
