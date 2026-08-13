package cmd

import (
	"fmt"
	"net"
	"time"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

// windowDialTimeout bounds the pre-flight reachability check. Window 1 is on
// loopback, so a live listener answers in microseconds; this only has to be
// long enough not to false-negative under load.
const windowDialTimeout = 2 * time.Second

// dialWindowOne is the pre-flight connectivity check, indirected for tests.
var dialWindowOne = func(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, windowDialTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

// checkWindowOneReachable fails fast with an actionable message when there is
// no session to attach to.
//
// Without this a viewer would start successfully and sit empty: it owns no
// local agents, and the peer manager retries in the background forever, so
// the operator would get a blank window with no explanation and nothing to
// act on. The AC calls that a hang, and it is the worst kind -- everything
// "worked", there is just nothing there.
//
// This is deliberately a pre-flight check rather than a permanent
// requirement: once attached, a viewer that LOSES window 1 should keep
// retrying (that is reconnect, not a startup failure). The distinction is
// whether there was ever a session to attach to.
func checkWindowOneReachable(addr string, n int) error {
	if err := dialWindowOne(addr); err != nil {
		return fmt.Errorf("no initech session to attach to at %s.\n"+
			"Start window 1 first by running 'initech' (with no flag) in this project,\n"+
			"then rerun 'initech --window %d' here.\n"+
			"(underlying error: %v)", addr, n, err)
	}
	return nil
}

// windowPeerName derives the peer identity a secondary window presents to
// window 1. The operator never types a peer_name -- they type `--window 2` --
// so this is the only place the identity is decided.
//
// The derivation is total and collision-free across distinct N, and the result
// (window-2, window-3, ...) satisfies config.ValidPeerName: letters, digits
// and hyphens only, no colons. That matters beyond aesthetics -- the identity
// is used as a map key for client routing and as a filename component for
// per-window layout state (ini-9ka.3), so a name that failed validation would
// fail somewhere far away from here.
func windowPeerName(n int) string {
	return fmt.Sprintf("window-%d", n)
}

// viewerProject derives the Project a secondary window runs with, from the
// project's ordinary config. The operator maintains ONE initech.yaml -- window
// N does not get a config file of its own -- so the difference between window
// 1 and window N is this function, not two files that can drift.
//
// It drops Roles. That is what makes "a role-bearing config plus --window N
// must not spawn agents" structural rather than a check: the caller builds
// agents by iterating Roles, so an empty list produces zero agents and there
// is no path where the config's contents can override the flag. Dropping
// Roles is also why ini-9ka.1's validator relaxation had to exist -- this
// Project is precisely the roles-less, remotes-only shape it legalized.
func viewerProject(proj *config.Project, n int) (*config.Project, error) {
	if n < 0 {
		return nil, fmt.Errorf("--window %d is not valid: window numbers start at 1, and secondary windows are 2 or higher", n)
	}
	if n == 1 {
		return nil, fmt.Errorf("--window 1 is the session itself, not a secondary window. Run 'initech' with no flag to start it")
	}
	if proj.WindowListen == "" {
		return nil, fmt.Errorf("multi-window is not enabled for this project: set 'window_listen' in initech.yaml (e.g. window_listen: \":7500\") and restart window 1, then rerun 'initech --window %d'", n)
	}

	// Copy so the loaded config is left untouched -- viewer mode is a view of
	// the project, not an edit to it.
	viewer := *proj
	viewer.Roles = nil
	viewer.RoleOverrides = nil
	viewer.PeerName = windowPeerName(n)

	// Window 1 is this viewer's only peer. Its address is the same
	// window_listen both windows read from the shared config, so no discovery
	// artifact is needed -- which is also what keeps a single-window fleet's
	// .initech/ census unchanged (ini-9ka.2).
	viewer.Remotes = map[string]config.Remote{
		windowOnePeer: {Addr: proj.WindowListen, Token: proj.Token},
	}
	// A viewer serves nothing: it renders another window's agents and owns
	// none. Leaving this set would make every secondary window try to bind
	// window 1's port.
	viewer.WindowListen = ""

	return &viewer, nil
}

// windowOnePeer is the name a secondary window uses for the session owner in
// its remotes map. Window 1's own identity in the ASSIGNMENT model is the
// empty string (tui.WindowOne); this is the label for the CONNECTION, which
// needs a non-empty key.
//
// It aliases tui.WindowOnePeerName rather than repeating the literal: window 1
// advertises that same constant in its handshake, and connectPeer rejects the
// connection if the two disagree. When they were two independent literals --
// one here, one implicitly "" on the server -- every attach failed (ini-1ch).
const windowOnePeer = tui.WindowOnePeerName
