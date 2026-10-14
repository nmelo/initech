package roles

import "testing"

func TestRoleSummary_FamiliesShareALineAndUnknownRolesReadCustom(t *testing.T) {
	if RoleSummary("eng7") != RoleSummary("eng") || RoleSummary("qa3") != RoleSummary("qa") {
		t.Error("numbered variants must share their family's summary")
	}
	for _, r := range []string{"datasci", "engineer", "qa-lead"} {
		if got := RoleSummary(r); got != "Custom role" {
			t.Errorf("RoleSummary(%q) = %q, want Custom role", r, got)
		}
	}
}
