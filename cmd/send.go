package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

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
	rootCmd.PersistentFlags().BoolVar(&allowDevDelivery, "allow-dev-delivery", false,
		"Allow a locally-built dev binary to run a delivery-effect command against a live session (ini-grg3)")
}

// allowDevDelivery bypasses the dev-build guard below when set explicitly.
// Deliberately a FLAG, not an environment variable: an env var would be a
// second copy of the exact "inherited, never consciously set" failure this
// guard exists to close (INITECH_SOCKET is injected into every agent pane,
// which is what let a dev build silently drive the live fleet in the first
// place). A flag must be typed by hand on every invocation.
var allowDevDelivery bool

// deliveryEffectActions are the IPC actions that mutate a LIVE session's
// state or inject an action into it (as opposed to read-only queries). Every
// command below shares this list via ipcCallSocket, the one chokepoint every
// IPC-backed command in this package funnels through (verified: only 2 direct
// tui.DialIPC callers exist anywhere in cmd/, both inside this file).
//
//   - send, clear (clear's own Action is "send" with Text: "/clear")
//   - restart, stop, quit (down's Action) — kill/respawn/shut down a pane or
//     the whole session
//   - interrupt — Ctrl+C equivalent into a live pane
//   - bead — sets bead ID/title metadata (assign and deliver route through
//     this action too)
//   - start, add — launch a new live agent pane/process
//   - remove — kill/remove a pane
//   - emit_event — assign's event emission
//
// status/peek/patrol/peers/whoami use "list"/"peek"/"peers_query"/etc., which
// deliberately are NOT in this set, so read-only commands keep their current
// behavior unchanged (ini-grg3 AC).
var deliveryEffectActions = map[string]bool{
	"send":       true,
	"restart":    true,
	"stop":       true,
	"quit":       true,
	"interrupt":  true,
	"bead":       true,
	"start":      true,
	"add":        true,
	"remove":     true,
	"emit_event": true,
}

// isDevBuild reports whether this binary looks like a local, uncommitted-tree
// build rather than a properly released one (ini-grg3).
//
// Version=='dev' (cmd.Version, ldflag-injected at release time by
// goreleaser; "dev" is the hardcoded default for any local `go
// build`/`make build`) is SUFFICIENT ON ITS OWN. Do not add a second gate
// that can override it to false: a prior version of this function also
// consulted runtime/debug's build info and required "(devel)" (or absent)
// before agreeing the binary was a dev build, treating any other value as
// evidence of a legitimate `go install pkg@version`. That was wrong on this
// toolchain (go1.26.4): a real `make build`/`go build .` run from a clean,
// tagged, NON-submodule checkout embeds a genuine computed pseudo-version
// (e.g. "v1.31.2-0.20260730144731-4f96ef884dbd"), not "(devel)" — verified
// empirically against a plain `git clone`, not assumed (the submodule-style
// checkout every agent workspace uses for src/ happens to suppress VCS
// stamping, which is why an earlier check from inside one produced "(devel)"
// and looked correct). So the build-info condition silently disabled the
// guard for exactly the common case it exists to catch. `go install` is
// separately confirmed to be blocked outright by this repo's go.mod replace
// directive (reverified, not just carried forward), so there is no live
// scenario the extra check was protecting anyway, and no unit test can
// express the distinction it was chasing: a go-test binary's own build info
// never matches what a sibling `go build`/`make build` produces, so a test
// asserting build-info behavior only ever proves go-test's shape, not a real
// build's. Verify manually instead: `make build`, then run the binary
// against a nonexistent socket path; the guard's refusal error is the
// expected result, a raw dial error means it did not fire.
func isDevBuild() bool {
	if isRunningUnderGoTest() {
		return false
	}
	return Version == "dev"
}

// isRunningUnderGoTest reports whether this binary was compiled and run by
// `go test`, in which case the dev-build guard never applies: every existing
// test in this package dials a throwaway fake socket it spun up itself, never
// a real live session, so it is not the scenario the guard exists to stop
// (ini-grg3). Wraps testing.Testing (Go 1.21+), the standard library's own
// sanctioned way to detect this from production code. Overridable so
// ini-grg3's own regression tests can force it off to exercise the real
// refusal/allow logic; every other test in this package needs no changes.
var isRunningUnderGoTest = testing.Testing

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

// ipcCallTimeout bounds how long ipcCallSocket waits on the connection
// (write + read-response) before giving up, so a stalled or SIGSTOPped TUI
// cannot hang the CLI forever (ini-ousx). The server's own read deadline
// (internal/tui/ipc.go, 5s) only bounds it reading the incoming request
// line, not producing the response, so this needs a bit more margin, not
// less. 10s matches the client-side round-trip timeout convention already
// established for a comparable persistent request/response channel
// (ControlMux.Request, internal/tui/control_mux.go). A package-level var,
// not a const, so tests can shrink it instead of waiting out the real value.
var ipcCallTimeout = 10 * time.Second

// sendActionTimeout is the deadline used specifically for the "send" action
// (ini-ousx qa7). handleIPCSend blocks synchronously on resumePane when the
// target pane is suspended (internal/tui/ipc.go:232-239), and the client has
// no way to know in advance whether a given send will hit that path, so
// every send must budget for the worst case. Enumerated every other
// delivery-effect action's server-side handler (internal/tui/ipc.go,
// ipc_lifecycle.go): none has a comparable long synchronous wait, so this
// deadline applies ONLY to "send" (which "clear" also uses, since it sends
// literal "/clear" via the same action) — every other action keeps the
// shorter ipcCallTimeout, preserving fast-stall detection where no legitimate
// long wait exists.
//
// Justified maximum, not a convention: resumeTimeout (internal/tui/
// resource.go:430) = 30s, the dominant term. Plus up to (maxMessageQueue-1)
// = 19 gaps of queueDrainInterval (resource.go:319, :434) = 9.5s for a fully
// queued suspended pane. Plus ~3s margin for NewPane (fork/exec) and
// runOnMain pane-replacement overhead (normally sub-second; this is safety
// margin, not a measured figure). Total 42.5s, rounded up to 45s.
var sendActionTimeout = 45 * time.Second

// ipcCallSocket sends a request to the TUI's IPC endpoint at the given path.
// Uses tui.DialIPC which handles Unix sockets on POSIX and TCP via .port file
// on Windows.
func ipcCallSocket(sockPath string, req tui.IPCRequest) (*tui.IPCResponse, error) {
	// Refuse BEFORE dialing (ini-grg3): a dev build must never deliver
	// against a live session, even silently discovered via an inherited
	// INITECH_SOCKET. Checked ahead of the dial so a refusal never puts a
	// single byte on the wire.
	if deliveryEffectActions[req.Action] && isDevBuild() && !allowDevDelivery {
		return nil, fmt.Errorf(
			"refusing %q: this looks like a local dev build (not a released initech), "+
				"and %q is a delivery-effect command against a live session at %s. "+
				"A dev build silently delivering into another agent's live pane is exactly "+
				"what caused ini-grg3. If you really mean to run this against that session, "+
				"pass --allow-dev-delivery.",
			req.Action, req.Action, sockPath)
	}

	conn, err := tui.DialIPC(sockPath)
	if err != nil {
		return nil, fmt.Errorf("connect to TUI: %w", err)
	}
	defer conn.Close()

	// Bound both the write and the read-response wait: a stalled TUI could
	// block either side (a full socket recv buffer blocks Write; a wedged
	// event loop that never responds blocks Scan). Without this, a
	// SIGSTOPped or wedged TUI hangs the CLI forever (ini-ousx) — the
	// server already protects itself this way (ipc.go's 5s read deadline)
	// but the client never did.
	//
	// "send" gets the longer, resume-aware budget (ini-ousx qa7): the client
	// can't know in advance whether this send will hit a suspended pane and
	// block server-side on resumePane for up to resumeTimeout. Every other
	// action keeps the short ipcCallTimeout, since none has a comparable
	// legitimate long wait (enumerated on the bead).
	deadline := ipcCallTimeout
	if req.Action == "send" {
		deadline = sendActionTimeout
	}
	conn.SetDeadline(time.Now().Add(deadline))

	data, _ := json.Marshal(req)
	conn.Write(data)
	conn.Write([]byte("\n"))

	scanner := tui.NewIPCScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return nil, fmt.Errorf("timed out waiting for TUI response after %s — the session may be stalled or unresponsive", deadline)
			}
			return nil, fmt.Errorf("read response: %w", err)
		}
		return nil, fmt.Errorf("no response from TUI")
	}

	var resp tui.IPCResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return &resp, nil
}

// discoverSocketDial is overridable for tests to supply controlled dial
// error values, since reproducing exotic OS-level conditions (a full accept
// backlog, a SIGSTOPped process) deterministically isn't practical (ini-0fvf).
var discoverSocketDial = tui.DialIPC

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
	conn, dialErr := discoverSocketDial(sockPath)
	if dialErr != nil {
		if !isStaleSocketError(dialErr) {
			// A live TUI with a momentarily full/busy accept queue also
			// fails to dial. Deleting the socket over that ambiguous signal
			// orphans a LIVE session until manual restart (ini-0fvf) — worse
			// than the stale-socket annoyance this cleanup was meant to fix.
			return "", nil, fmt.Errorf("session '%s' appears busy or unresponsive (%w); the socket was left in place in case it recovers", p.Name, dialErr)
		}
		// Clean up the stale socket/port file so the next 'initech' can
		// start without manual deletion (ini-db1).
		os.Remove(sockPath)
		return "", nil, fmt.Errorf("session '%s' is not running (stale socket removed). Use 'initech' to start", p.Name)
	}
	conn.Close()
	return sockPath, p, nil
}

// isStaleSocketError reports whether err indicates NO listener is present at
// all — the only case where deleting the socket file is safe (ini-0fvf).
// Checks Timeout() first: a definite timeout always means "maybe still
// busy," regardless of what it might also match underneath. ECONNREFUSED is
// the classic Linux dead-listener shape (also how Go's Windows syscall
// package reports WSAECONNREFUSED, via an aliased Errno meant for exactly
// this kind of portable errors.Is check — verified in syscall/
// zerrors_windows.go, not assumed). ENOENT is what this platform's own
// net.DialTimeout actually returns for a dead listener on Unix (verified
// empirically), and is also Windows' ERROR_FILE_NOT_FOUND — ini-fxeu found
// that Windows distinguishes a missing FINAL path component (ENOENT) from a
// missing INTERMEDIATE one: a socket path whose containing .initech
// directory doesn't exist at all fails with ERROR_PATH_NOT_FOUND ("The
// system cannot find the path specified" — the literal text from the CI
// failure that traced this), which Go aliases to ENOTDIR on Windows.
// EINVAL is what this platform returns for a unix socket path exceeding the
// sun_path length limit (~104 bytes on macOS) — a path that long could
// never have been bound by any listener in the first place, so it is just
// as unambiguously "no listener" as the others, not a "maybe busy" signal
// (also verified empirically). A missing directory is at least as strong
// evidence of "no session running" as a missing file, so ENOTDIR belongs
// with the same unambiguous group. Anything else defaults to "not stale" —
// leaving a genuinely dead socket around a little longer (the original
// ini-db1 annoyance) is a far smaller cost than orphaning a live-but-busy
// session.
func isStaleSocketError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTDIR)
}
