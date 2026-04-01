package cmd

import "github.com/nmelo/initech/internal/config"

func resolvePaneBehavior(ov config.RoleOverride) (agentType string, autoApprove bool, noBracketedPaste bool, submitKey string) {
	agentType = config.NormalizeAgentType(ov.AgentType)
	autoApprove = config.DefaultAutoApprove(agentType)
	if ov.AutoApprove != nil {
		autoApprove = *ov.AutoApprove
	}
	noBracketedPaste = config.DefaultNoBracketedPaste(agentType)
	if ov.NoBracketedPaste {
		noBracketedPaste = true
	}
	submitKey = config.DefaultSubmitKey(agentType)
	if ov.SubmitKey != "" {
		submitKey = ov.SubmitKey
	}
	return agentType, autoApprove, noBracketedPaste, submitKey
}
