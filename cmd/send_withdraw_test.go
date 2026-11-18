package cmd

import (
	"bytes"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

func TestSendWithdrawCLI(t *testing.T) {
	skipWindows(t)
	f := sendCmd.Flags().Lookup("withdraw")
	if f == nil || f.Value.Type() != "string" {
		t.Fatal("send has no withdrawal receipt flag")
	}
	// Cobra retains Changed across Execute calls in this test process.
	// A CLI invocation starts with just its own command-line flags.
	for _, name := range []string{"stdin", "file", "no-enter", "urgent"} {
		flag := sendCmd.Flags().Lookup(name)
		previous := flag.Changed
		flag.Changed = false
		t.Cleanup(func() { flag.Changed = previous })
	}
	var calls atomic.Int32
	sock := startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		calls.Add(1)
		if req.Action != "withdraw" || req.Target != "seat" || req.Host != "host" || req.From != "owner" || req.Enter {
			t.Errorf("unexpected request %+v", req)
		}
		switch req.Text {
		case "mine":
			return tui.IPCResponse{OK: true, Data: "withdrawn: mine"}
		case "typed":
			return tui.IPCResponse{Error: "receipt typed was already typed"}
		case "foreign":
			return tui.IPCResponse{Error: "receipt foreign belongs to another sender"}
		}
		return tui.IPCResponse{Error: "unknown receipt " + req.Text}
	})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "owner")
	old, changed := sendWithdraw, f.Changed
	oldUsage, oldErrors := sendCmd.SilenceUsage, sendCmd.SilenceErrors
	t.Cleanup(func() {
		sendWithdraw = old
		f.Changed = changed
		sendCmd.SetErr(nil)
		sendCmd.SilenceUsage, sendCmd.SilenceErrors = oldUsage, oldErrors
	})
	for _, tc := range []struct {
		id, extra, want string
		exit            int
	}{
		{"mine", "", "withdrawn: mine", 0}, {"typed", "", "already typed", 1}, {"foreign", "", "another sender", 1}, {"missing", "", "unknown receipt", 1}, {"", "", "required", 1}, {"mine", "body", "only the target and receipt", 1},
	} {
		if err := sendCmd.Flags().Set("withdraw", tc.id); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		sendCmd.SetErr(&output)
		args := []string{"host:seat"}
		if tc.extra != "" {
			args = append(args, tc.extra)
		}
		err := runSend(sendCmd, args)
		got := output.String()
		if err != nil {
			got += err.Error() + "\n"
		}
		if code := v69Exit(err); code != tc.exit || !strings.Contains(got, tc.want) || strings.Count(got, "\n") != 1 {
			t.Errorf("%q: exit %d, %q", args, code, got)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("IPC requests=%d want 4", calls.Load())
	}
}

func TestSendAfterIsAnExplicitWireAction(t *testing.T) {
	skipWindows(t)
	f := sendCmd.Flags().Lookup("after")
	if f == nil {
		t.Fatal("missing --after")
	}
	old, changed := sendAfter, f.Changed
	t.Cleanup(func() { sendAfter = old; f.Changed = changed; sendCmd.SetErr(nil) })
	line := "held for seat: queued behind a running task, 2 ahead (receipt id)"
	t.Setenv("INITECH_SOCKET", startClearIPC(t, func(req tui.IPCRequest) tui.IPCResponse {
		if req.Action != "send_after" || req.Target != "seat" || req.From != "owner" || req.Text != "later" || !req.Enter {
			t.Errorf("wire request %+v", req)
		}
		return tui.IPCResponse{OK: true, Delivery: tui.DeliveryHeld, Data: line}
	}))
	t.Setenv("INITECH_AGENT", "owner")
	if err := sendCmd.Flags().Set("after", "true"); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	sendCmd.SetErr(&buf)
	if err := runSend(sendCmd, []string{"seat", "later"}); err != nil || buf.String() != line+"\n" {
		t.Fatalf("exit %d output %q", v69Exit(err), buf.String())
	}
}
