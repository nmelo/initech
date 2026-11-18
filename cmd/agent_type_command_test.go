package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nmelo/initech/internal/config"
)

func TestCommandOnlyAgentUsesItsExecutable(t *testing.T) {
	for _, tc := range []struct{ program, explicit, want string }{
		{"codex", "", "codex"}, {"agy", "", "generic"}, {"custom", "", "claude-code"},
		{"agy", "opencode", "opencode"},
	} {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, "seat"), 0700); err != nil {
			t.Fatal(err)
		}
		project := &config.Project{Root: root, RoleOverrides: map[string]config.RoleOverride{"seat": {Command: []string{filepath.Join("bin", tc.program)}, AgentType: tc.explicit}}}
		cfg, err := buildAgentPaneConfig("seat", project)
		if err != nil || cfg.AgentType != tc.want {
			t.Errorf("%s (%s): type %q error %v; want %q", tc.program, tc.explicit, cfg.AgentType, err, tc.want)
		}
	}
}
