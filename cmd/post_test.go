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
		// No clock: a client that never writes is ended by closing the
		// connection at cleanup; the test binary's -timeout bounds the rest.
		t.Cleanup(func() { conn.Close() })
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
			req := <-requests // waits on the event, not a clock
			if req.Action != "post" || req.Text != body || req.DefaultText != "keep working" || !req.Chime || req.Target != "" {
				t.Fatalf("post wire: %+v", req)
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

// ini-232c: every initech post call prints one stderr line that the command
// is deprecated, and otherwise does exactly what it did: the same stdout, the
// same request, the same result (a success stays a success, a refusal its
// error).
func TestPostCLI_SaysItIsDeprecatedAndChangesNothingElse(t *testing.T) {
	const line = "initech post is deprecated and is removed in the next release\n"
	for _, c := range []struct {
		name    string
		args    []string
		resp    tui.IPCResponse
		out     string
		wantErr string
	}{
		{"post", []string{"a question", "--default", "keep going"}, tui.IPCResponse{OK: true, Data: "posted p7"}, "posted p7\n", ""},
		{"mine", []string{"--mine"}, tui.IPCResponse{OK: true, Data: "p7 unread"}, "p7 unread\n", ""},
		{"a refusal", []string{"--check", "p99"}, tui.IPCResponse{Error: "no item p99"}, "", "no item p99"},
	} {
		t.Run(c.name, func(t *testing.T) {
			requests := postCLIExchange(t, c.resp)
			command := newPostCommand()
			var out, errOut bytes.Buffer
			command.SetOut(&out)
			command.SetErr(&errOut)
			command.SetArgs(c.args)
			err := command.Execute()
			if c.wantErr == "" && err != nil || c.wantErr != "" && (err == nil || err.Error() != c.wantErr) {
				t.Fatalf("result %v, want %q", err, c.wantErr)
			}
			if out.String() != c.out {
				t.Errorf("stdout %q, want it unchanged %q", out.String(), c.out)
			}
			if got := strings.Count(errOut.String(), line); got != 1 {
				t.Errorf("stderr %q: the deprecation line %d times, want once", errOut.String(), got)
			}
			<-requests // the request still reaches window 1
		})
	}
	if !strings.HasPrefix(newPostCommand().Long, strings.TrimSuffix(line, "\n")) {
		t.Error("the help text does not start with the deprecation sentence")
	}
}
