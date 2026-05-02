package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/nmelo/initech/internal/color"
	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// remoteCheckResult holds the rich output of a single-peer doctor check.
type remoteCheckResult struct {
	PeerName       string // Name from initech.yaml.
	Addr           string // host:port.
	Connected      bool
	TokenValid     bool
	ServerPeerName string // peer_name returned by daemon.
	ProtocolVer    int    // Protocol version returned by daemon.
	AgentCount     int
	Err            string // Empty on success.
}

// dialDoctorRemote performs a hello handshake against the named remote and
// returns a structured result. Cleanup of network resources is handled here.
// 5-second timeouts are used end-to-end for snappy doctor feedback.
func dialDoctorRemote(proj *config.Project, peerName string,
	dial func(string, string, time.Duration) (net.Conn, error)) remoteCheckResult {

	remote, ok := proj.Remotes[peerName]
	res := remoteCheckResult{PeerName: peerName, Addr: remote.Addr}
	if !ok {
		res.Err = fmt.Sprintf("remote %q not configured in initech.yaml", peerName)
		return res
	}

	conn, err := dial("tcp", remote.Addr, 5*time.Second)
	if err != nil {
		res.Err = fmt.Sprintf("dial: %s", err)
		return res
	}
	defer conn.Close()
	res.Connected = true

	session, err := yamux.Client(conn, yamux.DefaultConfig())
	if err != nil {
		res.Err = fmt.Sprintf("yamux: %s", err)
		return res
	}
	defer session.Close()

	ctrl, err := session.Open()
	if err != nil {
		res.Err = fmt.Sprintf("control stream: %s", err)
		return res
	}
	defer ctrl.Close()

	token := remote.Token
	if token == "" {
		token = proj.Token
	}
	hello := tui.HelloMsg{
		Action:   "hello",
		Version:  1,
		Token:    token,
		PeerName: proj.PeerName,
	}
	data, _ := json.Marshal(hello)
	ctrl.Write(data)
	ctrl.Write([]byte("\n"))

	scanner := tui.NewIPCScanner(ctrl)
	ctrl.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !scanner.Scan() {
		res.Err = "no response to hello"
		return res
	}

	var helloOK tui.HelloOKMsg
	if err := json.Unmarshal(scanner.Bytes(), &helloOK); err != nil || helloOK.Action != "hello_ok" {
		var errMsg tui.ErrorMsg
		json.Unmarshal(scanner.Bytes(), &errMsg)
		if errMsg.Error != "" {
			res.Err = errMsg.Error
		} else {
			res.Err = "unexpected response from daemon"
		}
		return res
	}

	res.TokenValid = true
	res.ServerPeerName = helloOK.PeerName
	res.ProtocolVer = helloOK.Version
	res.AgentCount = len(helloOK.Agents)
	return res
}

// formatRemoteCheck prints a multi-line health report for a single remote
// peer. Returns true if the check passed (connected + token valid).
func formatRemoteCheck(out io.Writer, r remoteCheckResult) bool {
	fmt.Fprintf(out, "\n%s %s (%s)\n", color.CyanBold("Remote:"), color.Bold(r.PeerName), r.Addr)

	statusLabel := func(label, detail, status string) {
		var tag string
		switch status {
		case "OK":
			tag = color.Green("ok")
		case "FAIL":
			tag = color.RedBold("FAIL")
		default:
			tag = color.YellowBold(status)
		}
		fmt.Fprintf(out, "  %s %s %s\n", color.Pad(color.Blue(label), 14), color.Pad(detail, 40), tag)
	}

	switch {
	case r.Err != "" && !r.Connected:
		statusLabel("Status", r.Err, "FAIL")
		return false
	case r.Err != "" && !r.TokenValid:
		statusLabel("Status", "connected", "OK")
		statusLabel("Token", r.Err, "FAIL")
		return false
	case r.Err != "":
		statusLabel("Status", r.Err, "FAIL")
		return false
	}

	statusLabel("Status", "connected", "OK")
	statusLabel("Token", "valid", "OK")
	statusLabel("Peer name", r.ServerPeerName, "OK")
	statusLabel("Protocol", fmt.Sprintf("v%d", r.ProtocolVer), protocolStatus(r.ProtocolVer))
	statusLabel("Agents", fmt.Sprintf("%d running", r.AgentCount), "OK")
	return true
}

// protocolStatus returns OK for the supported protocol version, WARN otherwise.
func protocolStatus(v int) string {
	if v == 1 {
		return "OK"
	}
	return "WARN"
}

// runDoctorRemote loads the project config, dials the named remote, and
// prints a health report. Returns a non-nil error on failure (used as the
// command's exit code).
func runDoctorRemote(env doctorEnv, peerName string, out io.Writer) error {
	cfgPath, err := config.Discover(env.WorkDir)
	if err != nil {
		fmt.Fprintln(out, color.RedBold("No initech.yaml found. Run 'initech init' first."))
		return fmt.Errorf("no initech.yaml found")
	}
	proj, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if _, ok := proj.Remotes[peerName]; !ok {
		fmt.Fprintf(out, "%s %s\n", color.RedBold("Unknown remote:"), peerName)
		if len(proj.Remotes) > 0 {
			fmt.Fprint(out, "Configured remotes:")
			for n := range proj.Remotes {
				fmt.Fprintf(out, " %s", n)
			}
			fmt.Fprintln(out)
		} else {
			fmt.Fprintln(out, "No remotes configured. Add a 'remotes:' block to initech.yaml.")
		}
		return fmt.Errorf("remote %q not configured", peerName)
	}

	res := dialDoctorRemote(proj, peerName, env.Dial)
	if !formatRemoteCheck(out, res) {
		return fmt.Errorf("remote check failed: %s", res.Err)
	}
	fmt.Fprintln(out)
	return nil
}

