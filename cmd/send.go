package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send [host:]<agent> <text> | --stdin | -f <file>",
	Short: "Send text to an agent's terminal",
	Long: `Injects text into the specified agent's PTY. By default appends Enter
to execute the text as a command. Use --no-enter to send text without Enter.

WARNING: backticks in a double-quoted message are consumed by YOUR shell
(command substitution) before initech ever sees them — the backticked text is
silently deleted from the delivered message. For bodies containing backticks,
use the shell-safe input paths, or single quotes:

  printf 'use %s to disable the job' '` + "`if: false`" + `' | initech send eng1 --stdin
  initech send eng1 -f draft.txt        # read body from a file
  initech send eng1 -f -                # same as --stdin
  initech send eng1 'use ` + "`if: false`" + ` to disable'   # single quotes are safe

The body from --stdin or -f is delivered byte-for-byte; the shell never
re-evaluates it.

A bare agent name resolves to any agent connected to this TUI, whether
local or remote — no host prefix required:

  initech send eng1 "status?"
  initech send eng9 "rebase onto main"

Agents on a connected peer machine are addressed exactly like local ones, so
the second example reaches eng9 on its peer without naming the host.

Use the qualified host:agent form to name a machine explicitly:

  initech send laptop:super "Phase 2 complete"
  initech send workbench:shipper "Release v1.9"

The host name is the peer_name from the remote machine's initech.yaml.
Run 'initech status' to see all agents and their hosts.

Disambiguation: when the same agent name exists on more than one machine,
a bare name resolves to the first matching pane, which is not guaranteed to
be the one you meant. Use host:agent to target a specific machine's agent.

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSend,
}

var (
	sendNoEnter bool
	sendStdin   bool
	sendFile    string
)

func init() {
	sendCmd.Flags().BoolVar(&sendNoEnter, "no-enter", false, "Don't append Enter after the text")
	sendCmd.Flags().BoolVar(&sendStdin, "stdin", false, "Read the message body from stdin (shell-safe: no command substitution)")
	sendCmd.Flags().StringVarP(&sendFile, "file", "f", "", "Read the message body from a file; \"-\" means stdin")
	rootCmd.AddCommand(sendCmd)
}

func runSend(cmd *cobra.Command, args []string) error {
	target := args[0]

	text, err := resolveSendBody(cmd, args)
	if err != nil {
		return err
	}

	// Parse host:agent format for cross-machine routing.
	var host string
	if idx := strings.Index(target, ":"); idx >= 0 {
		host = target[:idx]
		target = target[idx+1:]
	}

	req := tui.IPCRequest{
		Action: "send",
		Target: target,
		Host:   host,
		Text:   text,
		Enter:  !sendNoEnter,
	}

	resp, err := ipcCall(req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	switch {
	case host != "":
		fmt.Fprintf(cmd.ErrOrStderr(), "delivered to %s:%s\n", host, target)
	case strings.Contains(resp.Data, "deferred"):
		fmt.Fprintf(cmd.ErrOrStderr(), "deferred to %s — target has a modal open; will deliver when it closes\n", target)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "delivered to %s\n", target)
	}
	return nil
}

// resolveSendBody returns the message body for a send invocation. Positional
// args after the target are joined with spaces (existing behavior). With
// --stdin or -f the body is read verbatim from stdin or a file instead —
// byte-identical, no trimming — because the caller's shell performs command
// substitution on backticks in double-quoted arguments before initech sees
// argv (ini-da7f / GitHub #26), and content already mangled by the shell
// cannot be recovered here.
func resolveSendBody(cmd *cobra.Command, args []string) (string, error) {
	fromStdin := sendStdin || sendFile == "-"
	fromFile := sendFile != "" && sendFile != "-"

	if sendStdin && sendFile != "" {
		return "", fmt.Errorf("cannot use both --stdin and --file")
	}
	if !fromStdin && !fromFile {
		if len(args) < 2 {
			return "", fmt.Errorf("message text required (or read the body from --stdin / -f <file>)")
		}
		return strings.Join(args[1:], " "), nil
	}

	if len(args) > 1 {
		return "", fmt.Errorf("unexpected message arguments %q: the body comes from %s", strings.Join(args[1:], " "), bodySourceName(fromStdin))
	}

	var body []byte
	var err error
	if fromStdin {
		body, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
	} else {
		body, err = os.ReadFile(sendFile)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", sendFile, err)
		}
	}
	if len(body) == 0 {
		return "", fmt.Errorf("message body from %s is empty", bodySourceName(fromStdin))
	}
	return string(body), nil
}

// bodySourceName names the active body source for error messages.
func bodySourceName(fromStdin bool) string {
	if fromStdin {
		return "stdin"
	}
	return "file"
}

// ipcCall sends a request to the TUI's IPC socket and returns the response.
// Resolution order: (1) INITECH_SOCKET env var, (2) discoverSocket() fallback
// which locates the socket from the project's initech.yaml.
func ipcCall(req tui.IPCRequest) (*tui.IPCResponse, error) {
	sockPath := os.Getenv("INITECH_SOCKET")
	if sockPath == "" {
		discovered, _, err := discoverSocket()
		if err != nil {
			return nil, err
		}
		sockPath = discovered
	}
	return ipcCallSocket(sockPath, req)
}

// ipcCallSocket sends a request to the TUI's IPC endpoint at the given path.
// Uses tui.DialIPC which handles Unix sockets on POSIX and TCP via .port file
// on Windows.
func ipcCallSocket(sockPath string, req tui.IPCRequest) (*tui.IPCResponse, error) {
	conn, err := tui.DialIPC(sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to TUI: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(req)
	conn.Write(data)
	conn.Write([]byte("\n"))

	scanner := tui.NewIPCScanner(conn)
	if !scanner.Scan() {
		return nil, fmt.Errorf("no response from TUI")
	}

	var resp tui.IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &resp, nil
}

// discoverSocket finds the IPC socket path for the current project.
// Returns the socket path and project config, or an error.
func discoverSocket() (string, *config.Project, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("get working directory: %w", err)
	}
	cfgPath, err := config.Discover(wd)
	if err != nil {
		return "", nil, fmt.Errorf("no initech.yaml found. Run 'initech init' first")
	}
	p, err := config.Load(cfgPath)
	if err != nil {
		return "", nil, fmt.Errorf("load config: %w", err)
	}
	sockPath := tui.SocketPath(p.Root, p.Name)
	// Probe the endpoint with a dial instead of stat. A stale socket file
	// (from a crashed TUI) passes stat but fails to connect.
	conn, dialErr := tui.DialIPC(sockPath)
	if dialErr != nil {
		// Clean up the stale socket/port file so the next 'initech' can
		// start without manual deletion (ini-db1).
		os.Remove(sockPath)
		return "", nil, fmt.Errorf("session '%s' is not running (stale socket removed). Use 'initech' to start", p.Name)
	}
	conn.Close()
	return sockPath, p, nil
}
