package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadWithWarn(t *testing.T, line string) (*Project, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "initech.yaml")
	body := "project: demo\nroot: /tmp/demo\nroles: [eng1]\n" + line
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

// ini-qn9b AC3: absent or 0 is off; a whole number up to 100 is the setting.
func TestDispatchContextWarnPercent_AbsentZeroAndAWholeNumber_Load(t *testing.T) {
	for line, want := range map[string]WarnPercent{
		"":                                     0,
		"dispatch_context_warn_percent: 0\n":   0,
		"dispatch_context_warn_percent: 80\n":  80,
		"dispatch_context_warn_percent: 100\n": 100,
	} {
		p, err := loadWithWarn(t, line)
		if err != nil {
			t.Errorf("%q: %v", line, err)
			continue
		}
		if p.DispatchContextWarnPercent != want {
			t.Errorf("%q loaded %d, want %d", line, p.DispatchContextWarnPercent, want)
		}
	}
}

// AC7: anything else fails the load with one line naming the key and value.
func TestDispatchContextWarnPercent_ABadValue_FailsTheLoad_NamingKeyAndValue(t *testing.T) {
	for _, v := range []string{"150", "101", "-1", "80.5", "high", `"80"`, "[80]", "true"} {
		_, err := loadWithWarn(t, "dispatch_context_warn_percent: "+v+"\n")
		if err == nil {
			t.Errorf("%s loaded", v)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "dispatch_context_warn_percent") || strings.Count(msg, "\n") != 0 {
			t.Errorf("%s: error %q is not one line naming the key", v, msg)
		}
	}
}
