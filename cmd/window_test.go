package cmd

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

// window_test.go covers ini-9ka.6's CLI surface: the --window flag, its
// discoverability, the viewer Project it constructs, and the config edges a
// window-N invocation has to get right.

// --- Discoverability (Ship-It Gate #4 / the ini-162m corollary) -------------

// TestWindowFlag_IsRegisteredAndDocumented is the first direction of the
// two-way guardrail: the flag must exist AND carry help text. A flag with no
// usage string is registered but invisible -- it would appear in --help as a
// bare name, which is how the v2.1.0 popup shipped as plumbing.
func TestWindowFlag_IsRegisteredAndDocumented(t *testing.T) {
	f := rootCmd.Flags().Lookup("window")
	if f == nil {
		t.Fatal("--window is not registered on rootCmd; the epic's only user-facing surface would not exist")
	}
	if strings.TrimSpace(f.Usage) == "" {
		t.Fatal("--window has no help text; it would appear in --help as a bare flag name")
	}
	// The AC requires the restore-on-reattach behavior stated in the help
	// itself, because that is the non-obvious half: a user can guess what
	// attaching does, but not what happens to their agents when the window
	// closes.
	usage := strings.ToLower(f.Usage)
	if !strings.Contains(usage, "fold") && !strings.Contains(usage, "back into window 1") {
		t.Errorf("--window help text does not state what happens on close: %q", f.Usage)
	}
	if !strings.Contains(usage, "restor") && !strings.Contains(usage, "rerun") {
		t.Errorf("--window help text does not state restore-on-reattach: %q", f.Usage)
	}
}

// TestWindowFlag_HelpTextMatchesRegistration is the second direction: help
// output must actually contain the flag. Direction one proves the flag object
// has a usage string; this proves it reaches rendered --help, which is what a
// user sees. They can diverge -- a flag hidden via Hidden=true keeps its usage
// string but vanishes from help.
func TestWindowFlag_HelpTextMatchesRegistration(t *testing.T) {
	help := rootCmd.UsageString()
	if !strings.Contains(help, "--window") {
		t.Fatal("--window does not appear in rendered usage output; it is registered but undiscoverable")
	}
	if f := rootCmd.Flags().Lookup("window"); f != nil && f.Hidden {
		t.Error("--window is marked Hidden; a new user-facing surface must be discoverable")
	}
}

// --- Peer identity derivation ----------------------------------------------

// TestWindowPeerName_IsDerivedAndValid pins the derivation contract. The user
// never types a peer_name, so this function is the only place the identity is
// decided -- and it must produce something the rest of the system accepts as
// an identity, not just a plausible-looking string.
func TestWindowPeerName_IsDerivedAndValid(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range []int{2, 3, 7, 12} {
		got := windowPeerName(n)
		if !config.ValidPeerName(got) {
			t.Errorf("windowPeerName(%d) = %q, which fails config.ValidPeerName -- it is used as a client map key and a per-window layout filename component, so an invalid name fails far from here", n, got)
		}
		if seen[got] {
			t.Errorf("windowPeerName(%d) = %q collides with an earlier window's identity; distinct windows would evict each other", n, got)
		}
		seen[got] = true
	}
}

// TestWindowPeerName_SameNumberCollidesDeliberately documents the collision
// story as a test rather than only prose: two `--window 2` invocations DO
// present the same identity, which per the eviction contract means the
// newcomer takes over.
//
// That is intended. The likely cause is an orphaned prior window (terminal
// closed, laptop slept), and refusing would strand the operator with an
// invisible zombie holding window 2 and no way to reclaim it. The loser
// degrades safely: its agents fold back to window 1 rather than stopping.
func TestWindowPeerName_SameNumberCollidesDeliberately(t *testing.T) {
	if windowPeerName(2) != windowPeerName(2) {
		t.Fatal("derivation is not deterministic")
	}
	if windowPeerName(2) == windowPeerName(3) {
		t.Fatal("distinct window numbers must not share an identity")
	}
}

// --- Viewer Project construction -------------------------------------------

func roleBearingProject() *config.Project {
	return &config.Project{
		Name:  "initech",
		Root:  "/tmp/initech",
		Roles: []string{"eng1", "eng2", "super"},
		RoleOverrides: map[string]config.RoleOverride{
			"eng1": {AgentType: config.AgentTypeClaudeCode},
		},
		WindowListen: "127.0.0.1:7500",
		Token:        "tok",
	}
}

// TestViewerProject_RoleBearingConfigProducesNoAgents is the single most
// likely real-world invocation: the operator runs --window 2 against his
// actual initech.yaml, which HAS roles. The flag decides viewer mode, so no
// local agents may be spawned -- and because the caller builds agents by
// iterating Roles, dropping Roles makes that structural rather than a check
// someone could bypass.
func TestViewerProject_RoleBearingConfigProducesNoAgents(t *testing.T) {
	proj := roleBearingProject()
	viewer, err := viewerProject(proj, 2)
	if err != nil {
		t.Fatalf("viewerProject: %v", err)
	}
	if len(viewer.Roles) != 0 {
		t.Errorf("viewer Roles = %v, want empty -- a secondary window must not spawn the operator's agents a second time", viewer.Roles)
	}
	if len(viewer.RoleOverrides) != 0 {
		t.Errorf("viewer RoleOverrides = %v, want empty (they only describe roles that are no longer present)", viewer.RoleOverrides)
	}
	// The original must be untouched: viewer mode is a view, not an edit.
	if len(proj.Roles) != 3 {
		t.Errorf("viewerProject mutated the loaded config's Roles: %v", proj.Roles)
	}
}

// TestViewerProject_PointsAtWindowOneAndServesNothing checks the two wiring
// facts a viewer depends on: it dials window 1 at the shared window_listen
// address, and it does not itself serve (or every secondary window would try
// to bind window 1's port).
func TestViewerProject_PointsAtWindowOneAndServesNothing(t *testing.T) {
	viewer, err := viewerProject(roleBearingProject(), 2)
	if err != nil {
		t.Fatalf("viewerProject: %v", err)
	}
	if viewer.PeerName != "window-2" {
		t.Errorf("viewer PeerName = %q, want window-2", viewer.PeerName)
	}
	if len(viewer.Remotes) != 1 {
		t.Fatalf("viewer Remotes = %v, want exactly one (window 1)", viewer.Remotes)
	}
	r, ok := viewer.Remotes[windowOnePeer]
	if !ok {
		t.Fatalf("viewer has no remote for window 1: %v", viewer.Remotes)
	}
	if r.Addr != "127.0.0.1:7500" {
		t.Errorf("window 1 remote addr = %q, want the shared window_listen value", r.Addr)
	}
	if viewer.WindowListen != "" {
		t.Error("viewer still has WindowListen set; every secondary window would try to bind window 1's port")
	}
}

// TestViewerProject_ValidatesAsARolesLessViewer closes the loop with
// ini-9ka.1: the Project this function builds must be exactly the shape that
// bead's validator relaxation legalized. If these two ever drift, window N
// stops starting.
func TestViewerProject_ValidatesAsARolesLessViewer(t *testing.T) {
	viewer, err := viewerProject(roleBearingProject(), 2)
	if err != nil {
		t.Fatalf("viewerProject: %v", err)
	}
	if err := config.Validate(viewer); err != nil {
		t.Errorf("the constructed viewer Project fails validation: %v -- ini-9ka.1's relaxation and this constructor must agree", err)
	}
}

// --- Config edges -----------------------------------------------------------

// TestViewerProject_WindowOneIsRejectedWithGuidance covers the alias trap:
// window 1 IS the session, so --window 1 must not silently mean "start it".
func TestViewerProject_WindowOneIsRejectedWithGuidance(t *testing.T) {
	_, err := viewerProject(roleBearingProject(), 1)
	if err == nil {
		t.Fatal("--window 1 was accepted; it is the session itself, not a secondary window")
	}
	if !strings.Contains(err.Error(), "initech") {
		t.Errorf("error does not name what to run instead: %q", err)
	}
}

// TestViewerProject_MultiWindowNotEnabledIsActionable is the "no session to
// attach to" family's first case, and the one a user hits first: the project
// has no window_listen, so there is nothing to attach to and never was. The
// message must name the fix, not just report a missing field.
func TestViewerProject_MultiWindowNotEnabledIsActionable(t *testing.T) {
	proj := roleBearingProject()
	proj.WindowListen = ""

	_, err := viewerProject(proj, 2)
	if err == nil {
		t.Fatal("viewerProject succeeded with no window_listen; there is no address to attach to")
	}
	msg := err.Error()
	if !strings.Contains(msg, "window_listen") {
		t.Errorf("error does not name the config key to set: %q", msg)
	}
	if !strings.Contains(msg, "initech.yaml") {
		t.Errorf("error does not name the file to set it in: %q", msg)
	}
}

// TestViewerProject_NegativeWindowRejected guards the remaining numeric edge.
func TestViewerProject_NegativeWindowRejected(t *testing.T) {
	if _, err := viewerProject(roleBearingProject(), -1); err == nil {
		t.Error("--window -1 was accepted")
	}
}

// TestCheckWindowOneReachable_UnreachableIsActionableNotAHang covers the
// "no session to attach to" AC. A viewer owns no local agents, so without a
// fail-fast the operator gets a blank window that retries forever with no
// explanation -- everything "worked", there is just nothing there. The AC
// calls that a hang, and it is the worst kind.
func TestCheckWindowOneReachable_UnreachableIsActionableNotAHang(t *testing.T) {
	orig := dialWindowOne
	t.Cleanup(func() { dialWindowOne = orig })
	dialWindowOne = func(addr string) error { return errors.New("connection refused") }

	err := checkWindowOneReachable("127.0.0.1:7500", 2)
	if err == nil {
		t.Fatal("unreachable window 1 did not produce an error; the viewer would start blank and retry forever")
	}
	msg := err.Error()
	// Actionable means it names what to run, not just what failed.
	if !strings.Contains(msg, "initech --window 2") {
		t.Errorf("error does not tell the user how to retry: %q", msg)
	}
	if !strings.Contains(msg, "Start window 1 first") {
		t.Errorf("error does not name what to start first: %q", msg)
	}
	if !strings.Contains(msg, "127.0.0.1:7500") {
		t.Errorf("error does not name the address it tried: %q", msg)
	}
}

// TestCheckWindowOneReachable_ReachablePasses is the other direction: a live
// listener must not be reported as unreachable, or the flag would never work.
func TestCheckWindowOneReachable_ReachablePasses(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	if err := checkWindowOneReachable(ln.Addr().String(), 2); err != nil {
		t.Errorf("a live listener was reported unreachable: %v", err)
	}
}
