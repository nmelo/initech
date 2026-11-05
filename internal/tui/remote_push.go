// remote_push.go implements the TUI->daemon push protocol for zero-config
// remotes. After the hello handshake, the TUI computes a role-diff against
// the daemon's reported running agents: pushes new/refresh roles via
// configure_agent, and stops via stop_agent only what this initech itself
// pushed and no longer lists. Every other agent on the peer is left running.
package tui

import (
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/nmelo/initech/internal/config"
)

// pushDecision describes one outcome of the role-diff computation.
type pushDecision struct {
	Configure []string // Roles to push via configure_agent (new + existing same-owner refresh).
	Stop      []string // Agents to stop_agent: this initech pushed them and no longer lists them.
	// LeftRunning are agents neither listed nor pushed by this initech: never
	// stopped, and named in the log.
	LeftRunning []string
}

// computePushDiff compares the daemon's running agents against the desired
// role list and returns the actions needed to converge.
//
// configRoles is the local desired roles (remotes.<peer>.roles).
// running is the agent list returned by hello_ok (and any subsequent state).
// owned is the set of agents this TUI owns (i.e. previously pushed).
//
// Configure includes every role in configRoles. The daemon's idempotent
// configure_agent handles new + refresh transparently.
//
// Stop includes agents that are running AND owned by us AND no longer in
// configRoles. Agents not owned by us are left alone (different client
// pushed them, or pre-existed from initech.yaml mode).
func computePushDiff(configRoles []string, running []AgentStatus, owned map[string]bool) pushDecision {
	roleSet := make(map[string]bool, len(configRoles))
	for _, r := range configRoles {
		if r != "" {
			roleSet[r] = true
		}
	}

	var dec pushDecision
	for _, r := range configRoles {
		if r != "" {
			dec.Configure = append(dec.Configure, r)
		}
	}
	for _, ag := range running {
		if roleSet[ag.Name] {
			continue
		}
		if owned[ag.Name] {
			dec.Stop = append(dec.Stop, ag.Name)
		} else {
			dec.LeftRunning = append(dec.LeftRunning, ag.Name)
		}
	}
	return dec
}

// pushedByThisInitech is the set of agents the peer says this initech pushed:
// the only agents its sweep may stop. A peer that names no pusher (one the
// serve started itself, or an older serve) owns nothing to us, and an initech
// with no peer name owns nothing (ini-a2nb).
func pushedByThisInitech(running []AgentStatus, self string) map[string]bool {
	owned := make(map[string]bool, len(running))
	if self == "" {
		return owned
	}
	for _, ag := range running {
		if ag.PushedBy == self {
			owned[ag.Name] = true
		}
	}
	return owned
}

// hiddenByRoles reports whether a roles list leaves an agent out of what this
// initech shows. No list hides nothing.
func hiddenByRoles(roles []string) func(string) bool {
	if len(roles) == 0 {
		return func(string) bool { return false }
	}
	listed := make(map[string]bool, len(roles))
	for _, r := range roles {
		listed[r] = true
	}
	return func(name string) bool { return !listed[name] }
}

// discardStream ends this initech's view of an agent it does not show. The
// close ends the peer's fan-out to the stream, which never touches the agent;
// the drain runs until the peer closes its side, so a replay already on its
// way larger than the stream's window cannot stall the peer.
func discardStream(stream net.Conn) {
	stream.Close()
	go io.Copy(io.Discard, stream) //nolint:errcheck // Ends with the stream or the session.
}

// configureAgentBuilder builds a ConfigureAgentCmd payload for a single role.
// It mirrors the local pane config build path but targets the remote's
// workspace root rather than the local project root, and skips local
// directory existence checks (the remote may or may not have the dir yet).
type configureAgentBuilder func(roleName string, project *config.Project, remote config.Remote) (ConfigureAgentCmd, error)

// defaultConfigureAgentBuilder is a placeholder until the cmd layer calls
// SetConfigureAgentBuilder (from a package init(), so it happens before any
// peer connection can be attempted) with a real builder that has access to
// role templates and project state. Keeping the placeholder here (rather
// than a nil func) means a caller who reaches it before registration gets a
// clear error instead of a nil-pointer panic; this file itself stays free of
// role-template/CLAUDE.md rendering deps (ini-om0).
var defaultConfigureAgentBuilder configureAgentBuilder = func(roleName string, project *config.Project, remote config.Remote) (ConfigureAgentCmd, error) {
	return ConfigureAgentCmd{}, fmt.Errorf("configureAgentBuilder not registered")
}

// configureAgentBuilderRegistered tracks whether SetConfigureAgentBuilder has
// ever been called with a non-nil builder, distinct from
// defaultConfigureAgentBuilder itself (which is never nil -- see above). Lets
// ConfigureAgentBuilderRegistered answer "was a real builder wired in" without
// invoking the builder (which could panic against production arguments if
// called speculatively) (ini-om0).
var configureAgentBuilderRegistered bool

// SetConfigureAgentBuilder registers the function used by pushRolesToPeer
// to construct configure_agent payloads. The cmd layer calls this once at
// startup (from an init(), not a per-connection or per-command path) with a
// builder that has access to role templates and project state.
func SetConfigureAgentBuilder(b configureAgentBuilder) {
	if b != nil {
		defaultConfigureAgentBuilder = b
		configureAgentBuilderRegistered = true
	}
}

// ConfigureAgentBuilderRegistered reports whether a real builder has been
// registered via SetConfigureAgentBuilder, as opposed to pushRolesToPeer
// still calling the placeholder above (which always errors, meaning no
// remote agent can ever be pushed or have its ownership established). Exists
// so a test can assert the cmd layer's normal startup wiring actually ran,
// rather than merely that SetConfigureAgentBuilder itself works when a test
// calls it directly -- the exact gap that let ini-om0 go unnoticed for three
// months: no cmd/ file ever called SetConfigureAgentBuilder in production.
func ConfigureAgentBuilderRegistered() bool {
	return configureAgentBuilderRegistered
}

// pushRolesToPeer applies the role-diff against a connected peer.
//
// For each role in configRoles, it sends configure_agent. For each agent
// in running that is owned by this TUI but no longer in configRoles, it
// sends stop_agent (cleans up orphaned agents from prior sessions).
//
// Errors on individual roles are logged and skipped — partial success is
// acceptable. The next reconnect retries.
//
// Returns the count of successful configure_agent and stop_agent actions.
func pushRolesToPeer(mux *ControlMux, peerName string, remote config.Remote, project *config.Project, running []AgentStatus, owned map[string]bool) (configured, stopped int) {
	dec := computePushDiff(remote.Roles, running, owned)

	for _, roleName := range dec.Configure {
		cmd, err := defaultConfigureAgentBuilder(roleName, project, remote)
		if err != nil {
			LogWarn("remote-push", "build payload failed", "peer", peerName, "role", roleName, "err", err)
			continue
		}
		cmd.Action = "configure_agent"
		cmd.Name = roleName
		cmd.ID = "push-cfg-" + roleName + "-" + strconv.FormatInt(int64(configured), 10)

		resp, err := mux.RequestRaw(cmd)
		if err != nil {
			LogWarn("remote-push", "configure_agent send failed", "peer", peerName, "role", roleName, "err", err)
			continue
		}
		if !resp.OK {
			LogWarn("remote-push", "configure_agent rejected", "peer", peerName, "role", roleName, "err", resp.Error)
			continue
		}
		configured++
		LogInfo("remote-push", "configured", "peer", peerName, "role", roleName)
	}

	for _, agentName := range dec.LeftRunning {
		LogInfo("remote-push", "left running: not pushed by this initech", "peer", peerName, "agent", agentName)
	}

	for _, agentName := range dec.Stop {
		stopCmd := StopAgentCmd{
			Action: "stop_agent",
			Name:   agentName,
			ID:     "push-stop-" + agentName,
		}
		resp, err := mux.RequestRaw(stopCmd)
		if err != nil {
			LogWarn("remote-push", "stop_agent send failed", "peer", peerName, "agent", agentName, "err", err)
			continue
		}
		if !resp.OK {
			LogWarn("remote-push", "stop_agent rejected", "peer", peerName, "agent", agentName, "err", resp.Error)
			continue
		}
		stopped++
		LogInfo("remote-push", "stopped orphan", "peer", peerName, "agent", agentName)
	}

	return configured, stopped
}
