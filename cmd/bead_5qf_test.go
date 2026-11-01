package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nmelo/initech/internal/tui"
	"github.com/spf13/cobra"
)

// ini-5qf: initech bead takes only a bead id.

// beadCapture is a fake TUI socket that answers OK and keeps every request.
func beadCapture(t *testing.T) (sock string, got func() []tui.IPCRequest) {
	t.Helper()
	skipWindows(t)
	dir, err := os.MkdirTemp("/tmp", "b5qf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock = filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var reqs []tui.IPCRequest
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			sc := bufio.NewScanner(c)
			if sc.Scan() {
				var r tui.IPCRequest
				json.Unmarshal(sc.Bytes(), &r)
				mu.Lock()
				reqs = append(reqs, r)
				mu.Unlock()
			}
			data, _ := json.Marshal(tui.IPCResponse{OK: true})
			c.Write(append(data, '\n'))
			c.Close()
		}
	}()
	return sock, func() []tui.IPCRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]tui.IPCRequest(nil), reqs...)
	}
}

// runBeadWith runs initech bead args against the capture with bd's title
// lookup stubbed; it returns stdout, stderr, the error and the requests sent.
func runBeadWith(t *testing.T, lookup func(string) (string, error), clear bool, args ...string) (string, string, error, []tui.IPCRequest) {
	t.Helper()
	stubBdFns(t)
	bdShowTitleFn = lookup
	sock, got := beadCapture(t)
	t.Setenv("INITECH_SOCKET", sock)
	t.Setenv("INITECH_AGENT", "eng4")
	beadAgent, beadClear = "", clear
	t.Cleanup(func() { beadAgent, beadClear = "", false })
	var out, errb bytes.Buffer
	c := &cobra.Command{}
	c.SetOut(&out)
	c.SetErr(&errb)
	err := runBead(c, args)
	return out.String(), errb.String(), err, got()
}

func knownAs(title string) func(string) (string, error) {
	return func(string) (string, error) { return title, nil }
}

// bdUnknown is bd answering that the id does not exist (bdShowTitleImpl's
// error carries bd's own words).
func bdUnknown(id string) (string, error) {
	return "", fmt.Errorf("bead %s not found: Issue %s not found\n{\"error\": \"no issues found matching the provided IDs\"}", id, id)
}

func bdDown(id string) (string, error) {
	return "", fmt.Errorf("bead %s not found: ", id)
}

const pasted = "ini-y0n.2\tProgress step 2: a pane header shows its effort pace (closed/total, per hour, stuck)"

// AC1/AC2: a plain id and a pasted bd row both send the id, with bd's title
// for display; the pasted row prints the kept-id line. Red at base: the
// pasted row went out whole.
func TestBead_PastedRowSendsTheIDAndSaysWhatItKept(t *testing.T) {
	out, errOut, err, reqs := runBeadWith(t, knownAs("Progress step 2: pace"), false, pasted)
	if err != nil {
		t.Fatalf("pasted row: %v", err)
	}
	if len(reqs) != 1 || reqs[0].Text != "ini-y0n.2\tProgress step 2: pace" {
		t.Fatalf("sent %+v, want one bead action with text \"ini-y0n.2<TAB>Progress step 2: pace\"", reqs)
	}
	if !strings.Contains(errOut, "kept ini-y0n.2 from the pasted line") {
		t.Errorf("stderr %q, want the kept-id line", errOut)
	}
	if out != "Set ini-y0n.2 for eng4.\n" {
		t.Errorf("stdout %q, want \"Set ini-y0n.2 for eng4.\"", out)
	}

	_, errOut, err, reqs = runBeadWith(t, knownAs("t"), false, "ini-y0n.2")
	if err != nil || len(reqs) != 1 || reqs[0].Text != "ini-y0n.2\tt" || errOut != "" {
		t.Errorf("plain id: err %v stderr %q sent %+v", err, errOut, reqs)
	}
}

// AC2: not an id is refused with one line and exit 1, nothing sent; an id bd
// does not know is refused when bd answers, and set with a note when bd is
// unreachable.
func TestBead_RefusesNonIDsAndIDsBdDoesNotKnow(t *testing.T) {
	for _, arg := range []string{"not an id", "ini-y0n.2 Progress step 2", "garbage\tTitle"} {
		_, _, err, reqs := runBeadWith(t, knownAs("t"), false, arg)
		var ec exitCoder
		if err == nil || len(reqs) != 0 {
			t.Errorf("%q: err %v, sent %+v; want refused, nothing sent", arg, err, reqs)
			continue
		}
		if errors.As(err, &ec) && ec.ExitCode() != 1 {
			t.Errorf("%q: exit %d, want 1", arg, ec.ExitCode())
		}
		if strings.Contains(err.Error(), "\n") {
			t.Errorf("%q: refusal %q is more than one line", arg, err)
		}
	}

	_, _, err, reqs := runBeadWith(t, bdUnknown, false, "ini-zz9")
	if err == nil || len(reqs) != 0 || !strings.Contains(err.Error(), "bd does not know ini-zz9") {
		t.Errorf("unknown id, bd answering: err %v sent %+v, want refused", err, reqs)
	}

	_, errOut, err, reqs := runBeadWith(t, bdDown, false, "ini-zz9")
	if err != nil || len(reqs) != 1 || reqs[0].Text != "ini-zz9" || !strings.Contains(errOut, "bd did not answer") {
		t.Errorf("bd unreachable: err %v stderr %q sent %+v, want set with a note", err, errOut, reqs)
	}

	// Several ids: one bad one refuses the lot.
	_, _, err, reqs = runBeadWith(t, knownAs("t"), false, "ini-a", "per hour")
	if err == nil || len(reqs) != 0 {
		t.Errorf("ini-a + garbage: err %v sent %+v, want refused, nothing sent", err, reqs)
	}
}

// AC5 feed-it-nothing: empty, whitespace, a tab with nothing before it are
// refused; --clear still sends a clear.
func TestBead_EmptyWhitespaceAndBareTabRefusedClearUnchanged(t *testing.T) {
	for _, arg := range []string{"", "   ", "\tTitle", " \t "} {
		_, _, err, reqs := runBeadWith(t, knownAs("t"), false, arg)
		if err == nil || len(reqs) != 0 {
			t.Errorf("%q: err %v sent %+v, want refused, nothing sent", arg, err, reqs)
		}
	}
	out, _, err, reqs := runBeadWith(t, knownAs("t"), true)
	if err != nil || len(reqs) != 1 || reqs[0].Text != "" || out != "Cleared bead for eng4.\n" {
		t.Errorf("--clear: err %v out %q sent %+v, want one clear", err, out, reqs)
	}
}
