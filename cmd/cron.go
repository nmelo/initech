package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Schedule a message or a short command into a pane",
	Long: `The rig keeps its own schedule in .initech/schedule.yaml, and window 1 runs
it whenever the rig is up, through restarts and after the session that added a
job has ended.

A job is a time of day (09:30, optionally followed by weekdays, weekends or days
like mon,thu) or an interval of a minute or more (--every 15m), and either a
message or a one-line command for one pane. A command runs in the rig root, is
stopped at 30 seconds, and its output, cut to 2 KB, arrives as a message with
its exit code. Deliveries go through the send path, so a dialog or
do-not-disturb holds them as usual. A job whose time passed while initech was
down runs once at the next start and says so. A delivery that cannot happen,
or a command that fails, is one message to super.

Not a job runner: one line, no retries, no chaining, and no secrets in the
file. To change a job, remove it and add it again.`,
}

var cronListCmd = &cobra.Command{
	Use:   "list",
	Short: "Every job, its owner and its next run",
	Args:  cobra.NoArgs,
	RunE:  runCronList,
}

var (
	cronTo, cronAt, cronEvery, cronMessage, cronCommand string
)

var cronAddCmd = &cobra.Command{
	Use:   "add --to <pane> (--at <time> | --every <interval>) (--message <text> | --command <line>)",
	Short: "Add a job, owned by the agent whose pane runs this",
	Args:  cobra.NoArgs,
	RunE:  runCronAdd,
}

var cronRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a job: your own, or any if you are super",
	Args:  cobra.ExactArgs(1),
	RunE:  runCronRemove,
}

func init() {
	cronAddCmd.Flags().StringVar(&cronTo, "to", "", "The pane to deliver to")
	cronAddCmd.Flags().StringVar(&cronAt, "at", "", `A time of day, "09:30" or "09:30 weekdays"`)
	cronAddCmd.Flags().StringVar(&cronEvery, "every", "", "An interval of a minute or more, like 15m")
	cronAddCmd.Flags().StringVar(&cronMessage, "message", "", "The message to deliver")
	cronAddCmd.Flags().StringVar(&cronCommand, "command", "", "One line to run in the rig root; its output is delivered")
	cronCmd.AddCommand(cronListCmd, cronAddCmd, cronRemoveCmd)
	rootCmd.AddCommand(cronCmd)
}

func cronCall(cmd *cobra.Command, req tui.IPCRequest) (*tui.IPCResponse, error) {
	cmd.SilenceUsage = true
	resp, err := ipcCall(req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		if strings.HasPrefix(resp.Error, "unknown action") {
			return nil, fmt.Errorf("this initech does not keep a schedule: upgrade initech and restart the rig")
		}
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func runCronAdd(cmd *cobra.Command, _ []string) error {
	body, _ := json.Marshal(tui.CronAddRequest{To: cronTo, At: cronAt, Every: cronEvery, Message: cronMessage, Command: cronCommand})
	resp, err := cronCall(cmd, tui.IPCRequest{Action: "cron_add", Text: string(body)})
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), resp.Data)
	return nil
}

func runCronRemove(cmd *cobra.Command, args []string) error {
	resp, err := cronCall(cmd, tui.IPCRequest{Action: "cron_remove", Target: args[0]})
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), resp.Data)
	return nil
}

func runCronList(cmd *cobra.Command, _ []string) error {
	resp, err := cronCall(cmd, tui.IPCRequest{Action: "cron_list"})
	if err != nil {
		return err
	}
	var jobs []tui.CronListing
	if err := json.Unmarshal([]byte(resp.Data), &jobs); err != nil {
		return fmt.Errorf("unreadable schedule from initech: %v", err)
	}
	out := cmd.OutOrStdout()
	if len(jobs) == 0 {
		fmt.Fprintln(out, "no jobs")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "OWNER\tID\tTO\tWHEN\tNEXT\tLAST\tWHAT")
	for _, j := range jobs {
		when := j.At
		if when == "" {
			when = "every " + j.Every
		}
		what := "message: " + j.Message
		if j.Command != "" {
			what = "command: " + j.Command
		}
		if r := []rune(what); len(r) > 60 {
			what = string(r[:57]) + "..."
		}
		next := "-"
		if !j.Next.IsZero() {
			next = j.Next.Local().Format("Mon 15:04")
		}
		last := "-"
		if j.LastRun != nil {
			last = j.LastRun.Local().Format("Mon 15:04") + " " + j.LastResult
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", j.Owner, j.ID, j.To, when, next, last, what)
	}
	return w.Flush()
}
