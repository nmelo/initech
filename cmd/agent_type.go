package cmd

import (
	"path/filepath"

	"github.com/nmelo/initech/internal/config"
)

func resolvePaneBehavior(ov config.RoleOverride) (agentType string, noBracketedPaste bool, submitKey string) {
	agentType = config.NormalizeAgentType(ov.AgentType)
	if ov.AgentType == "" && len(ov.Command) > 0 {
		switch filepath.Base(ov.Command[0]) {
		case "codex", "codex.exe":
			agentType = config.AgentTypeCodex
		case "agy", "agy.exe":
			agentType = config.AgentTypeGeneric
		}
	}
	noBracketedPaste = config.DefaultNoBracketedPaste(agentType) || ov.NoBracketedPaste
	submitKey = config.DefaultSubmitKey(agentType)
	if ov.SubmitKey != "" {
		submitKey = ov.SubmitKey
	}
	return agentType, noBracketedPaste, submitKey
}
