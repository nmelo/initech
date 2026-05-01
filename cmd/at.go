package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

var (
	atIn     string
	atAt     string
	atList   bool
	atCancel string
)

var atCmd = &cobra.Command{
	Use:   "at <target> <message...>",
	Short: "Schedule a timed send to an agent",
	Long: `Schedules a message to be delivered to an agent at a future time.

Examples:
  initech at eng1 "run make test" --in 30m
  initech at eng1 "check status" --at 14:00
  initech at workbench:eng1 "deploy" --at 2026-04-01 09:00
  initech at --list
  initech at --cancel at-1`,
	RunE: runAt,
}

func init() {
	atCmd.Flags().StringVar(&atIn, "in", "", "Schedule after duration (e.g., 30s, 5m, 2h)")
	atCmd.Flags().StringVar(&atAt, "at", "", "Schedule at time (HH:MM, h:MMam/pm, or YYYY-MM-DD HH:MM)")
	atCmd.Flags().BoolVar(&atList, "list", false, "List pending timers")
	atCmd.Flags().StringVar(&atCancel, "cancel", "", "Cancel a timer by ID")
	rootCmd.AddCommand(atCmd)
}

func runAt(cmd *cobra.Command, args []string) error {
	out := cmd.OutOrStdout()

	// List mode.
	if atList {
		return runAtList(out)
	}

	// Cancel mode.
	if atCancel != "" {
		return runAtCancel(out, atCancel)
	}

	// Create mode: need target + message + time.
	if len(args) < 2 {
		return fmt.Errorf("usage: initech at <target> <message> --in <duration> or --at <time>")
	}
	if atIn == "" && atAt == "" {
		return fmt.Errorf("must specify --in or --at")
	}
	if atIn != "" && atAt != "" {
		return fmt.Errorf("cannot use both --in and --at")
	}

	target := args[0]
	text := strings.Join(args[1:], " ")

	// Parse target (host:agent or just agent).
	host := ""
	if idx := strings.IndexByte(target, ':'); idx >= 0 {
		host = target[:idx]
		target = target[idx+1:]
	}

	// Parse fire time.
	var fireAt time.Time
	var label string
	if atIn != "" {
		d, err := time.ParseDuration(atIn)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", atIn, err)
		}
		fireAt = time.Now().Add(d)
		label = fmt.Sprintf("in %s", d)
	} else {
		t, err := parseAtTime(atAt)
		if err != nil {
			return err
		}
		if t.Before(time.Now()) {
			return fmt.Errorf("scheduled time %s is in the past", t.Format("15:04"))
		}
		fireAt = t
		label = fmt.Sprintf("at %s", fireAt.Local().Format("15:04"))
	}

	// IPCRequest doesn't have host/fire_at fields, so we use a custom
	// JSON payload directly.
	type schedReq struct {
		Action string `json:"action"`
		Target string `json:"target"`
		Host   string `json:"host,omitempty"`
		Text   string `json:"text"`
		Enter  bool   `json:"enter"`
		FireAt string `json:"fire_at"`
	}

	resp, err := ipcCallCustom(schedReq{
		Action: "schedule",
		Target: target,
		Host:   host,
		Text:   text,
		Enter:  true,
		FireAt: fireAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	// Parse timer ID from response data.
	displayTarget := target
	if host != "" {
		displayTarget = host + ":" + target
	}
	fmt.Fprintf(out, "Scheduled: %s %s (%s) [id: %s]\n", displayTarget, label, fireAt.Local().Format("15:04"), resp.Data)
	return nil
}

func runAtList(out interface{ Write([]byte) (int, error) }) error {
	resp, err := ipcCallCustom(struct {
		Action string `json:"action"`
	}{"list_timers"})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}

	var timers []tui.Timer
	if err := json.Unmarshal([]byte(resp.Data), &timers); err != nil {
		return fmt.Errorf("parse timer list: %w", err)
	}

	if len(timers) == 0 {
		fmt.Fprintln(out, "No pending timers.")
		return nil
	}

	fmt.Fprintf(out, "%-6s %-10s %-24s %-18s %s\n", "ID", "TARGET", "MESSAGE", "FIRES AT", "REMAINING")
	for _, t := range timers {
		msg := t.Text
		if len(msg) > 22 {
			msg = msg[:19] + "..."
		}
		target := t.Target
		if t.Host != "" {
			target = t.Host + ":" + t.Target
		}
		remaining := time.Until(t.FireAt).Truncate(time.Second)
		if remaining < 0 {
			remaining = 0
		}
		fmt.Fprintf(out, "%-6s %-10s %-24s %-18s %s\n",
			t.ID, target, msg,
			t.FireAt.Local().Format("15:04"),
			remaining)
	}
	return nil
}

func runAtCancel(out interface{ Write([]byte) (int, error) }, id string) error {
	resp, err := ipcCallCustom(struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}{"cancel_timer", id})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	fmt.Fprintf(out, "Canceled: %s\n", resp.Data)
	return nil
}

// parseAtTime parses time strings in multiple formats.
func parseAtTime(s string) (time.Time, error) {
	now := time.Now()
	loc := now.Location()

	// Try YYYY-MM-DD HH:MM
	if t, err := time.ParseInLocation("2006-01-02 15:04", s, loc); err == nil {
		return t, nil
	}

	// Try HH:MM (24h)
	if t, err := time.ParseInLocation("15:04", s, loc); err == nil {
		return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc), nil
	}

	// Try h:MMpm / h:MMam
	for _, fmt := range []string{"3:04pm", "3:04PM", "3:04 pm", "3:04 PM"} {
		if t, err := time.ParseInLocation(fmt, s, loc); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, loc), nil
		}
	}

	return time.Time{}, fmt.Errorf("cannot parse time %q (try HH:MM, h:MMam/pm, or YYYY-MM-DD HH:MM)", s)
}

// ipcCallCustom sends a custom JSON payload to the TUI socket and returns
// the response. Used for IPC actions not covered by the standard IPCRequest.
func ipcCallCustom(payload any) (*tui.IPCResponse, error) {
	sockPath, _, err := discoverSocket()
	if err != nil {
		return nil, err
	}
	conn, err := tui.DialIPC(sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to TUI: %w", err)
	}
	defer conn.Close()

	data, _ := json.Marshal(payload)
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
