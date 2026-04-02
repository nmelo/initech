package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send <role> <text>",
	Short: "Send text to an agent's terminal",
	Long: `Injects text into the specified agent's PTY. By default appends Enter
to execute the text as a command. Use --no-enter to send text without Enter.

Requires a running initech TUI (connects via INITECH_SOCKET).`,
	Args: cobra.MinimumNArgs(2),
	RunE: runSend,
}

var sendNoEnter bool

func init() {
	sendCmd.Flags().BoolVar(&sendNoEnter, "no-enter", false, "Don't append Enter after the text")
	rootCmd.AddCommand(sendCmd)
}

func runSend(cmd *cobra.Command, args []string) error {
	target := args[0]
	text := strings.Join(args[1:], " ")

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
	return nil
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

// ipcCallSocket sends a request to the TUI's IPC socket at the given path.
// Used by commands that run outside the TUI and derive the socket path from config.
func ipcCallSocket(sockPath string, req tui.IPCRequest) (*tui.IPCResponse, error) {
	conn, err := net.Dial("unix", sockPath)
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
	// Probe the socket with a dial instead of stat. A stale socket file
	// (from a crashed TUI) passes stat but fails to connect.
	conn, dialErr := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if dialErr != nil {
		// Clean up the stale socket file so the next 'initech' can start
		// without manual deletion (ini-db1).
		os.Remove(sockPath)
		return "", nil, fmt.Errorf("session '%s' is not running (stale socket removed). Use 'initech' to start", p.Name)
	}
	conn.Close()
	return sockPath, p, nil
}
