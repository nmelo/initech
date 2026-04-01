package cmd

import "github.com/nmelo/initech/internal/config"

func resolvePaneBehavior(ov config.RoleOverride) (agentType string, noBracketedPaste bool, submitKey string) {
	agentType = config.NormalizeAgentType(ov.AgentType)
	noBracketedPaste = config.DefaultNoBracketedPaste(agentType)
	if ov.NoBracketedPaste {
		noBracketedPaste = true
	}
	submitKey = config.DefaultSubmitKey(agentType)
	if ov.SubmitKey != "" {
		submitKey = ov.SubmitKey
	}
	return agentType, noBracketedPaste, submitKey
}
