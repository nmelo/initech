package cmd

import (
	"fmt"

	"github.com/nmelo/initech/internal/config"
	"github.com/nmelo/initech/internal/tui"
)

type paneConfigFactory func(roleName string, proj *config.Project) (tui.PaneConfig, error)

func buildReloadingPaneConfigBuilder(cfgPath string, factory paneConfigFactory) func(name string) (tui.PaneConfig, error) {
	return func(name string) (tui.PaneConfig, error) {
		proj, err := config.Load(cfgPath)
		if err != nil {
			return tui.PaneConfig{}, fmt.Errorf("reload config %s: %w", cfgPath, err)
		}
		return factory(name, proj)
	}
}
