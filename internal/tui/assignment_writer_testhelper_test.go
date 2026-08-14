package tui

import "testing"

// mustAssignWriter obtains the assignment mutation capability, failing the
// test if this store's process is not the authority (ini-la97). Tests act as
// window 1 unless they are explicitly exercising a viewer, so a failure here
// means the test's window identity is not what it thinks it is.
func mustAssignWriter(t *testing.T, a *WindowAssignment) *AssignmentWriter {
	t.Helper()
	w, ok := a.Writer()
	if !ok {
		t.Fatalf("no assignment writer: this store was loaded by a non-authority window, " +
			"or is a read-only fallback")
	}
	return w
}
