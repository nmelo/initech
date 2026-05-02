package tui

import (
	"reflect"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

func TestComputePushDiff_FirstConnect(t *testing.T) {
	configRoles := []string{"eng2", "eng3", "shipper"}
	running := []AgentStatus{} // empty: nothing running yet
	owned := map[string]bool{}

	dec := computePushDiff(configRoles, running, owned)

	if !reflect.DeepEqual(dec.Configure, []string{"eng2", "eng3", "shipper"}) {
		t.Errorf("Configure = %v, want all 3 roles", dec.Configure)
	}
	if len(dec.Stop) != 0 {
		t.Errorf("Stop = %v, want empty (nothing running)", dec.Stop)
	}
}

func TestComputePushDiff_NoChange(t *testing.T) {
	configRoles := []string{"eng2", "eng3"}
	running := []AgentStatus{
		{Name: "eng2", Alive: true},
		{Name: "eng3", Alive: true},
	}
	owned := map[string]bool{"eng2": true, "eng3": true}

	dec := computePushDiff(configRoles, running, owned)

	// Configure still includes everything (idempotent path refreshes CLAUDE.md).
	if !reflect.DeepEqual(dec.Configure, []string{"eng2", "eng3"}) {
		t.Errorf("Configure = %v, want eng2,eng3", dec.Configure)
	}
	if len(dec.Stop) != 0 {
		t.Errorf("Stop = %v, want empty (no orphans)", dec.Stop)
	}
}

func TestComputePushDiff_RoleRemoved(t *testing.T) {
	configRoles := []string{"eng2"} // eng3 removed
	running := []AgentStatus{
		{Name: "eng2", Alive: true},
		{Name: "eng3", Alive: true},
	}
	owned := map[string]bool{"eng2": true, "eng3": true}

	dec := computePushDiff(configRoles, running, owned)

	if !reflect.DeepEqual(dec.Configure, []string{"eng2"}) {
		t.Errorf("Configure = %v, want eng2 only", dec.Configure)
	}
	if !reflect.DeepEqual(dec.Stop, []string{"eng3"}) {
		t.Errorf("Stop = %v, want eng3 (orphan)", dec.Stop)
	}
}

func TestComputePushDiff_RoleAdded(t *testing.T) {
	configRoles := []string{"eng2", "eng3"} // eng3 newly added
	running := []AgentStatus{
		{Name: "eng2", Alive: true},
	}
	owned := map[string]bool{"eng2": true}

	dec := computePushDiff(configRoles, running, owned)

	if !reflect.DeepEqual(dec.Configure, []string{"eng2", "eng3"}) {
		t.Errorf("Configure = %v, want eng2,eng3", dec.Configure)
	}
	if len(dec.Stop) != 0 {
		t.Errorf("Stop = %v, want empty", dec.Stop)
	}
}

func TestComputePushDiff_NotOwnedSkipped(t *testing.T) {
	configRoles := []string{"eng2"}
	running := []AgentStatus{
		{Name: "eng2", Alive: true},
		{Name: "intern", Alive: true}, // owned by another client
	}
	owned := map[string]bool{"eng2": true} // we don't own intern

	dec := computePushDiff(configRoles, running, owned)

	// intern should not appear in Stop because we don't own it.
	for _, name := range dec.Stop {
		if name == "intern" {
			t.Errorf("Stop should not include 'intern' (different owner): %v", dec.Stop)
		}
	}
}

func TestComputePushDiff_AllRolesRemoved(t *testing.T) {
	configRoles := []string{} // operator emptied the roles list
	running := []AgentStatus{
		{Name: "eng2", Alive: true},
		{Name: "eng3", Alive: true},
	}
	owned := map[string]bool{"eng2": true, "eng3": true}

	dec := computePushDiff(configRoles, running, owned)

	if len(dec.Configure) != 0 {
		t.Errorf("Configure = %v, want empty", dec.Configure)
	}
	if len(dec.Stop) != 2 {
		t.Errorf("Stop = %v, want both eng2 and eng3", dec.Stop)
	}
}

func TestComputePushDiff_EmptyRoleNamesSkipped(t *testing.T) {
	configRoles := []string{"eng2", "", "eng3"}
	dec := computePushDiff(configRoles, nil, nil)

	if !reflect.DeepEqual(dec.Configure, []string{"eng2", "eng3"}) {
		t.Errorf("Configure = %v, want eng2,eng3 (empty filtered)", dec.Configure)
	}
}

func TestSetConfigureAgentBuilder(t *testing.T) {
	old := defaultConfigureAgentBuilder
	defer func() { defaultConfigureAgentBuilder = old }()

	called := false
	SetConfigureAgentBuilder(func(roleName string, project *config.Project, remote config.Remote) (ConfigureAgentCmd, error) {
		called = true
		return ConfigureAgentCmd{Name: roleName}, nil
	})

	cmd, err := defaultConfigureAgentBuilder("eng2", nil, config.Remote{})
	if err != nil {
		t.Fatalf("builder error: %v", err)
	}
	if !called {
		t.Error("registered builder should have been called")
	}
	if cmd.Name != "eng2" {
		t.Errorf("Name = %q, want eng2", cmd.Name)
	}
}

func TestSetConfigureAgentBuilder_NilIgnored(t *testing.T) {
	old := defaultConfigureAgentBuilder
	defer func() { defaultConfigureAgentBuilder = old }()

	SetConfigureAgentBuilder(nil) // should be a no-op
	if defaultConfigureAgentBuilder == nil {
		t.Error("nil should not unset the builder")
	}
}
