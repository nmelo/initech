package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nmelo/initech/internal/tui"
)

func postCLIExchange(t *testing.T, response tui.IPCResponse) <-chan tui.IPCRequest {
	t.Helper()
	skipWindows(t)
	dir, err := os.MkdirTemp("/tmp", "post-cli-")
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "sock")
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close(); os.RemoveAll(dir) })
	t.Setenv("INITECH_SOCKET", sock)
	requests := make(chan tui.IPCRequest, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		scanner := tui.NewIPCScanner(conn)
		if !scanner.Scan() {
			return
		}
		var req tui.IPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			return
		}
		requests <- req
		_ = json.NewEncoder(conn).Encode(response)
	}()
	return requests
}

func TestPostCLI_BodyModesPreserveTextAndAllNotices(t *testing.T) {
	body := "multiline `literal`\n$(not executed)\n"
	file := filepath.Join(t.TempDir(), "post.txt")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{body}, {"--stdin"}, {"-f", "-"}, {"-f", file}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			requests := postCLIExchange(t, tui.IPCResponse{OK: true, Data: "posted p7", Notices: []string{"re-post teaching", "threshold teaching", "chime teaching"}})
			command := newPostCommand()
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(io.Discard)
			command.SetIn(strings.NewReader(body))
			command.SetArgs(append(args, "--default", "keep working", "--chime"))
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if got := out.String(); got != "posted p7\nre-post teaching\nthreshold teaching\nchime teaching\n" {
				t.Fatalf("teaching order: %q", got)
			}
			select {
			case req := <-requests:
				if req.Action != "post" || req.Text != body || req.DefaultText != "keep working" || !req.Chime || req.Target != "" {
					t.Fatalf("post wire: %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("request not captured")
			}
		})
	}
}

func TestPostCLI_ReadAndWithdrawModes(t *testing.T) {
	for _, tc := range []struct {
		args             []string
		action, id, data string
	}{
		{[]string{"--check", "p7"}, "post_check", "p7", "answered: yes — delivery: held"},
		{[]string{"--mine"}, "post_mine", "", "p7 answered\n… and 4 more — initech post --check <id>"},
		{[]string{"--withdraw", "p7"}, "post_withdraw", "p7", "withdrawn p7"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			requests := postCLIExchange(t, tui.IPCResponse{OK: true, Data: tc.data})
			command := newPostCommand()
			var out bytes.Buffer
			command.SetOut(&out)
			command.SetErr(io.Discard)
			command.SetArgs(tc.args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.data+"\n" {
				t.Fatalf("output: %q", out.String())
			}
			req := <-requests
			if req.Action != tc.action || req.ItemID != tc.id {
				t.Fatalf("wire: %+v", req)
			}
		})
	}
}

func TestPostCLI_InvalidModesRefuseBeforeIPC(t *testing.T) {
	t.Setenv("INITECH_SOCKET", filepath.Join(t.TempDir(), "never-dial"))
	for _, args := range [][]string{
		{}, {" "}, {"--as", "super", "hello"}, {"hello", "extra"}, {"--stdin", "-f", "-"},
		{"body", "--stdin"}, {"--check", "p1", "--mine"}, {"--withdraw", "p1", "--check", "p2"},
		{"--mine", "body"}, {"--check", "p1", "--default", "x"}, {"--mine", "--chime"},
		{"--check", ""}, {"--withdraw", ""}, {"body", "--default", strings.Repeat("x", tui.MaxInboxDefaultBytes+1)},
	} {
		command := newPostCommand()
		command.SetArgs(args)
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		err := command.Execute()
		if err == nil || strings.Contains(err.Error(), "connect to TUI") {
			t.Errorf("%v did not refuse before dial: %v", args, err)
		}
	}
}

func TestPostCLI_RefusalStillPrintsTeaching(t *testing.T) {
	requests := postCLIExchange(t, tui.IPCResponse{Error: "no item p99", Notices: []string{"use --mine"}})
	command := newPostCommand()
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--check", "p99"})
	err := command.Execute()
	if err == nil || err.Error() != "no item p99" || out.String() != "use --mine\n" {
		t.Fatalf("refusal: %v, %q", err, out.String())
	}
	<-requests
}

func TestPostCLI_DevGuardOnlyGuardsMutations(t *testing.T) {
	for _, action := range []string{"post", "post_withdraw"} {
		if !deliveryEffectActions[action] {
			t.Errorf("unguarded mutation %s", action)
		}
	}
	for _, action := range []string{"post_check", "post_mine"} {
		if deliveryEffectActions[action] {
			t.Errorf("read guarded %s", action)
		}
	}
	oldTest, oldVersion, oldAllow := isRunningUnderGoTest, Version, allowDevDelivery
	isRunningUnderGoTest = func() bool { return false }
	Version = "dev"
	allowDevDelivery = false
	t.Cleanup(func() { isRunningUnderGoTest = oldTest; Version = oldVersion; allowDevDelivery = oldAllow })
	for _, action := range []string{"post", "post_withdraw"} {
		_, err := ipcCallSocket("nonexistent-post-test-socket", tui.IPCRequest{Action: action})
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("refusing %q", action)) {
			t.Fatalf("guard did not refuse: %v", err)
		}
	}
	for _, action := range []string{"post_check", "post_mine"} {
		_, err := ipcCallSocket("nonexistent-post-test-socket", tui.IPCRequest{Action: action})
		if err == nil || strings.Contains(err.Error(), "refusing") {
			t.Fatalf("read should reach dial: %v", err)
		}
	}
}

func TestPostCLI_BoundedReader(t *testing.T) {
	if _, err := readPostBody(strings.NewReader(strings.Repeat("x", tui.MaxInboxBodyBytes+1))); err == nil {
		t.Fatal("oversized stdin accepted")
	}
	got, err := readPostBody(strings.NewReader(strings.Repeat("x", tui.MaxInboxBodyBytes)))
	if err != nil || len(got) != tui.MaxInboxBodyBytes {
		t.Fatalf("boundary body: %d, %v", len(got), err)
	}
}
