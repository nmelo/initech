package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newPostCommand()) }

// newPostCommand keeps flag state on the command, so separate invocations and
// tests cannot inherit a previous post's mode or body source.
func newPostCommand() *cobra.Command {
	var check, withdraw, file, defaultText string
	var mine, stdin, chime bool
	c := &cobra.Command{
		Use:          "post [text] | --stdin | -f <file> | --check <id> | --mine | --withdraw <id>",
		Short:        "Post an item to the operator inbox and keep working",
		SilenceUsage: true,
		Long: `Post from inside your agent's pane. Attribution comes from the connection.
Use --default to state what you will do while waiting for an answer.
Use --stdin or -f for multiline text and shell-safe bodies containing backticks.
Replies arrive in your pane; --check recovers the recorded answer. Do not poll.
Windows posting is unavailable in this release because TCP cannot identify your pane.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			modes := 0
			for _, set := range []bool{cmd.Flags().Changed("check"), mine, cmd.Flags().Changed("withdraw")} {
				if set {
					modes++
				}
			}
			if modes > 1 {
				return fmt.Errorf("use only one of --check, --mine, or --withdraw")
			}
			req := tui.IPCRequest{}
			if modes > 0 {
				if len(args) > 0 || stdin || cmd.Flags().Changed("file") || cmd.Flags().Changed("default") || cmd.Flags().Changed("chime") {
					return fmt.Errorf("status and withdrawal modes do not accept a body, --default, or --chime")
				}
				switch {
				case cmd.Flags().Changed("check"):
					if strings.TrimSpace(check) == "" {
						return fmt.Errorf("--check requires an item id")
					}
					req.Action, req.ItemID = "post_check", check
				case cmd.Flags().Changed("withdraw"):
					if strings.TrimSpace(withdraw) == "" {
						return fmt.Errorf("--withdraw requires an item id")
					}
					req.Action, req.ItemID = "post_withdraw", withdraw
				default:
					req.Action = "post_mine"
				}
			} else {
				if stdin && cmd.Flags().Changed("file") {
					return fmt.Errorf("use either --stdin or -f, not both")
				}
				if (stdin || cmd.Flags().Changed("file")) && len(args) != 0 {
					return fmt.Errorf("use either a text argument or --stdin/-f, not both")
				}
				var body string
				var err error
				switch {
				case stdin || file == "-":
					body, err = readPostBody(cmd.InOrStdin())
				case cmd.Flags().Changed("file"):
					var f *os.File
					f, err = os.Open(file)
					if err == nil {
						body, err = readPostBody(f)
						_ = f.Close()
					}
				case len(args) == 1:
					body = args[0]
				}
				if err != nil {
					return err
				}
				if err = tui.ValidateInboxPost(body, defaultText); err != nil {
					return err
				}
				req.Action, req.Text, req.DefaultText, req.Chime = "post", body, defaultText, chime
			}
			resp, err := ipcCall(req)
			if err != nil {
				return err
			}
			return printPostResponse(cmd.OutOrStdout(), resp)
		},
	}
	c.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		if strings.Contains(err.Error(), "unknown flag: --as") {
			return fmt.Errorf("posts are attributed to the agent that runs them; run initech post from inside your agent's pane; there is no --as")
		}
		return err
	})
	c.Flags().StringVar(&check, "check", "", "Read the recorded state and reply for an item")
	c.Flags().BoolVar(&mine, "mine", false, "List your newest 50 items, with an explicit omitted count")
	c.Flags().StringVar(&withdraw, "withdraw", "", "Withdraw one of your open items")
	c.Flags().BoolVar(&stdin, "stdin", false, "Read a shell-safe body from stdin")
	c.Flags().StringVarP(&file, "file", "f", "", "Read a shell-safe body from a file; - means stdin")
	c.Flags().StringVar(&defaultText, "default", "", "What you will do if the operator does not reply")
	c.Flags().BoolVar(&chime, "chime", false, "Request an arrival chime (at most one per agent per 10 minutes)")
	return c
}

func readPostBody(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, tui.MaxInboxBodyBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > tui.MaxInboxBodyBytes {
		return "", fmt.Errorf("post body too large (max %d bytes)", tui.MaxInboxBodyBytes)
	}
	return string(b), nil
}

func printPostResponse(out io.Writer, resp *tui.IPCResponse) error {
	if resp.OK && resp.Data != "" {
		if _, err := fmt.Fprintln(out, resp.Data); err != nil {
			return err
		}
	}
	for _, notice := range resp.Notices {
		if _, err := fmt.Fprintln(out, notice); err != nil {
			return err
		}
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
