package cmd

import "testing"

// ini-e7y AC1 reachability: every command's entry point, Execute, runs the
// stable-copy refresh, before the command itself.
func TestExecute_RefreshesTheStableCopyFirst(t *testing.T) {
	var order []string
	old := refreshStableCopy
	t.Cleanup(func() { refreshStableCopy = old })
	refreshStableCopy = func() { order = append(order, "refresh") }
	defer stubExecuteRoot(t, func() error { order = append(order, "command"); return nil })()
	Execute()
	if len(order) != 2 || order[0] != "refresh" || order[1] != "command" {
		t.Errorf("Execute ran %v, want the refresh then the command", order)
	}
}
