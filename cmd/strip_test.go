package cmd

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/tui"
)

// runStripWith runs `initech strip args...` against a capturing socket, from
// inside the pane of agent ("" = outside a pane), with stdin.
func runStripWith(t *testing.T, agent, stdin string, args ...string) (out string, err error, sent []tui.IPCRequest) {
	t.Helper()
	sock, received := startCapturingFakeIPC(t, tui.IPCResponse{OK: true, Data: "Set it."})
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", agent)
	stripStdin, stripClear = false, false
	t.Cleanup(func() {
		stripStdin, stripClear = false, false
		stripCmd.Flags().Lookup("stdin").Changed = false
		stripCmd.Flags().Lookup("clear").Changed = false
	})
	var o bytes.Buffer
	rootCmd.SetOut(&o)
	rootCmd.SetErr(&o)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(append([]string{"strip"}, args...))
	err = rootCmd.Execute()
	rootCmd.SetIn(nil)
	return o.String(), err, *received
}

// AC2: the forms that change the list send exactly the list asked for.
func TestStripCmd_ReplaceStdinAndClearSendTheList(t *testing.T) {
	for name, c := range map[string]struct {
		stdin string
		args  []string
		want  []string
	}{
		"items":           {"", []string{"Release hover?", "  Register the runner?  "}, []string{"Release hover?", "Register the runner?"}},
		"stdin":           {"one\n\n  \ntwo\r\n", []string{"--stdin"}, []string{"one", "two"}},
		"stdin with none": {"\n  \n", []string{"--stdin"}, nil},
		"clear":           {"", []string{"--clear"}, nil},
	} {
		out, err, sent := runStripWith(t, "super", c.stdin, c.args...)
		if err != nil || len(sent) != 1 {
			t.Fatalf("%s: err %v, %d requests", name, err, len(sent))
		}
		if sent[0].Action != "strip" || sent[0].Target != "super" || !reflect.DeepEqual(sent[0].Items, c.want) {
			t.Errorf("%s: sent %+v, want items %q for super", name, sent[0], c.want)
		}
		if strings.TrimSpace(out) != "Set it." {
			t.Errorf("%s: printed %q", name, out)
		}
	}
}

// AC2: each refusal is one line and exit 1 (an error from the command), and
// nothing reaches the socket, so the list stays as it was.
func TestStripCmd_EachRefusalSendsNothing(t *testing.T) {
	eleven := strings.Fields(strings.Repeat("x ", 11))
	for name, c := range map[string]struct {
		agent string
		args  []string
		want  string
	}{
		"bare call":       {"super", nil, "a bare call changes nothing"},
		"blank items":     {"super", []string{"  ", ""}, "every item is blank"},
		"outside a pane":  {"", []string{"x"}, "INITECH_AGENT is not set"},
		"11 items":        {"super", eleven, "11 items; the limit is 10"},
		"301 characters":  {"super", []string{strings.Repeat("a", 301)}, "the limit is 300"},
		"a tab":           {"super", []string{"a\tb"}, "control character"},
		"clear and items": {"super", []string{"--clear", "x"}, "--clear takes no items"},
		"stdin and items": {"super", []string{"--stdin", "x"}, "not both"},
	} {
		_, err, sent := runStripWith(t, c.agent, "", c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), "\n") {
			t.Errorf("%s: err %v, want one line with %q", name, err, c.want)
		}
		if len(sent) != 0 {
			t.Errorf("%s: %d request(s) reached the socket: %+v", name, len(sent), sent)
		}
	}
}
