// remote_push.go implements the TUI->daemon push protocol for zero-config
// remotes. After the hello handshake, the TUI computes a role-diff against
// the daemon's reported running agents and converges the daemon to the
// local config: pushes new/refresh roles via configure_agent, stops orphans
// via stop_agent.
package tui

import (
	"fmt"
	"strconv"

	"github.com/nmelo/initech/internal/config"
)

// pushDecision describes one outcome of the role-diff computation.
type pushDecision struct {
	Configure []string // Roles to push via configure_agent (new + existing same-owner refresh).
	Stop      []string // Agents to stop_agent (running but no longer in config).
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
		if !owned[ag.Name] {
			continue // Different owner — leave alone.
		}
		if !roleSet[ag.Name] {
			dec.Stop = append(dec.Stop, ag.Name)
		}
	}
	return dec
}

// configureAgentBuilder builds a ConfigureAgentCmd payload for a single role.
// It mirrors the local pane config build path but targets the remote's
// workspace root rather than the local project root, and skips local
// directory existence checks (the remote may or may not have the dir yet).
type configureAgentBuilder func(roleName string, project *config.Project, remote config.Remote) (ConfigureAgentCmd, error)

// defaultConfigureAgentBuilder is a placeholder that callers should replace.
// The cmd layer wires in a real builder via SetConfigureAgentBuilder so this
// file stays free of role-template/CLAUDE.md rendering deps.
var defaultConfigureAgentBuilder configureAgentBuilder = func(roleName string, project *config.Project, remote config.Remote) (ConfigureAgentCmd, error) {
	return ConfigureAgentCmd{}, fmt.Errorf("configureAgentBuilder not registered")
}

// SetConfigureAgentBuilder registers the function used by pushRolesToPeer
// to construct configure_agent payloads. The cmd layer calls this once at
// startup with a builder that has access to role templates and project state.
func SetConfigureAgentBuilder(b configureAgentBuilder) {
	if b != nil {
		defaultConfigureAgentBuilder = b
	}
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
