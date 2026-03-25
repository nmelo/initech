package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

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

	req := tui.IPCRequest{
		Action: "send",
		Target: target,
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
func ipcCall(req tui.IPCRequest) (*tui.IPCResponse, error) {
	sockPath := os.Getenv("INITECH_SOCKET")
	if sockPath == "" {
		return nil, fmt.Errorf("INITECH_SOCKET not set (are you running inside initech TUI?)")
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to TUI: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(req)
	conn.Write(data)
	conn.Write([]byte("\n"))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return nil, fmt.Errorf("no response from TUI")
	}

	var resp tui.IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &resp, nil
}
