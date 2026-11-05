package tui

import "strings"

// agentIdentityEnv returns the per-agent identity variables a pane's process
// gets, appended after the inherited environment so they win over any value
// the launching shell had (ini-u2hy):
//
//   - INITECH_SOCKET, when the caller passes the session socket (the sites that
//     set it today pass it; the daemon passes it only when it has one);
//   - INITECH_AGENT=<role>, which initech send, peek and whoami read;
//   - BEADS_ACTOR=<role>, which bd records as the actor of a bead change when
//     no --actor is given, so the audit trail names the agent.
//
// Every place that starts, restarts, adds or wakes an agent calls this, so a
// new identity variable is added here once (agent_env_test.go fails on a file
// that sets INITECH_AGENT= by hand).
func agentIdentityEnv(name string, socket ...string) []string {
	var env []string
	for _, s := range socket {
		env = append(env, "INITECH_SOCKET="+s)
	}
	return append(env, "INITECH_AGENT="+name, "BEADS_ACTOR="+name)
}

// serveIdentityKeys are the variables the serve sets in each pane it starts;
// a pushed configuration's own values for them are replaced.
var serveIdentityKeys = []string{"INITECH_SOCKET=", "INITECH_AGENT=", "BEADS_ACTOR="}

// withServeIdentity is env with the serve's socket and the agent's name set,
// replacing any the push carried: the Mac does not know this machine's
// socket, and a pane must never name another agent.
func withServeIdentity(env []string, name, sockPath string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		keep := true
		for _, k := range serveIdentityKeys {
			if strings.HasPrefix(kv, k) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	var sock []string
	if sockPath != "" {
		sock = append(sock, sockPath)
	}
	return append(out, agentIdentityEnv(name, sock...)...)
}
