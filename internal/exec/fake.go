package exec

import "strings"

// FakeRunner records commands for testing downstream packages.
// It implements Runner by recording each call and returning configured
// Output and Err values.
//
// Not used in production. Exists so packages like git, scaffold, and
// tmuxinator can test their command invocations without shelling out.
type FakeRunner struct {
	// Calls records each invocation as "dir|name arg1 arg2".
	// Dir is empty string when Run (not RunInDir) was used.
	Calls []string

	// Output is returned for every call. Set per-test.
	Output string

	// Err is returned for every call. Set per-test.
	Err error
}

// Run records the call and returns the configured output and error.
func (f *FakeRunner) Run(name string, args ...string) (string, error) {
	return f.RunInDir("", name, args...)
}

// RunInDir records the call with directory context and returns configured output and error.
func (f *FakeRunner) RunInDir(dir, name string, args ...string) (string, error) {
	call := dir + "|" + name
	if len(args) > 0 {
		call += " " + strings.Join(args, " ")
	}
	f.Calls = append(f.Calls, call)
	return f.Output, f.Err
}
