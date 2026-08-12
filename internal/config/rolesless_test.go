package config

import "testing"

// rolesless_test.go covers ini-9ka.1: a viewer Project (zero local agents,
// remotes only) must validate, so a secondary multi-monitor window can start
// without spawning its own agents.
//
// The relaxation is deliberately NARROW. The only new accept is a config that
// has remotes -- either the viewer path's internally-constructed Project
// (ini-9ka.6 does the constructing) or an explicitly remotes-only config. A
// config with nothing to do at all stays invalid with today's message, so
// this does not become a general loosening of config validation.

// TestValidate_RolesLessWithRemotesIsAccepted is the capability this bead
// exists for: the pure-viewer config must validate. Confirmed RED against the
// pre-fix validator, which rejected empty Roles unconditionally.
func TestValidate_RolesLessWithRemotesIsAccepted(t *testing.T) {
	p := &Project{
		Name: "viewerproj",
		Root: "/tmp/viewerproj",
		// Roles deliberately absent: a viewer window owns no agents.
		Remotes: map[string]Remote{
			"window1": {Addr: "127.0.0.1:7500"},
		},
	}
	if err := Validate(p); err != nil {
		t.Errorf("Validate(roles-less + remotes) = %v, want nil (a viewer window has no local roles by design)", err)
	}
}

// TestValidate_RolesLessWithoutRemotesStillRejected is the other half of the
// AC, and the reason the relaxation is safe: a config with no roles AND no
// remotes has nothing to do, and must still fail exactly as it does today.
// Asserting the MESSAGE, not just that an error occurred, keeps the
// operator-facing diagnostic from silently changing for a case this bead was
// never meant to touch.
func TestValidate_RolesLessWithoutRemotesStillRejected(t *testing.T) {
	p := &Project{
		Name: "emptyproj",
		Root: "/tmp/emptyproj",
	}
	err := Validate(p)
	if err == nil {
		t.Fatal("Validate(no roles, no remotes) = nil, want an error (a config with nothing to do must stay invalid)")
	}
	if err.Error() != "at least one role is required" {
		t.Errorf("error = %q, want the unchanged %q -- the relaxation must not alter this diagnostic", err, "at least one role is required")
	}
}

// TestValidate_RoleBearingConfigUnaffected guards the far side of the
// relaxation: adding remotes to the roles check must not change how ordinary
// role-bearing configs validate, with or without remotes present.
func TestValidate_RoleBearingConfigUnaffected(t *testing.T) {
	withRemotes := &Project{
		Name:    "proj",
		Root:    "/tmp/proj",
		Roles:   []string{"eng1"},
		Remotes: map[string]Remote{"peer": {Addr: "127.0.0.1:7500"}},
	}
	if err := Validate(withRemotes); err != nil {
		t.Errorf("Validate(roles + remotes) = %v, want nil", err)
	}

	withoutRemotes := &Project{
		Name:  "proj",
		Root:  "/tmp/proj",
		Roles: []string{"eng1"},
	}
	if err := Validate(withoutRemotes); err != nil {
		t.Errorf("Validate(roles, no remotes) = %v, want nil", err)
	}

	// A bad role name must still be caught when remotes are present -- the
	// relaxation gates only the EMPTY-roles case, never role validation
	// itself.
	badRole := &Project{
		Name:    "proj",
		Root:    "/tmp/proj",
		Roles:   []string{"bad name"},
		Remotes: map[string]Remote{"peer": {Addr: "127.0.0.1:7500"}},
	}
	if err := Validate(badRole); err == nil {
		t.Error("Validate(invalid role name + remotes) = nil, want an error (remotes must not bypass role validation)")
	}
}
