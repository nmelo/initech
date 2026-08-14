package tui

// i7fr_trace.go is TEMPORARY INVESTIGATION INSTRUMENTATION for ini-i7fr.
// It exists on branch investigate/i7fr only and must never merge to main:
// the bead's deliverable is a root-cause model, not a code change.
//
// Every line it emits is prefixed [i7fr] so a run can be filtered out of an
// otherwise noisy log, and carries the emitting process's identity (pid +
// window identity) so the two windows' streams can be interleaved and told
// apart -- which is the whole point, since the question is which PROCESS
// derived or wrote what.

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
)

// i7frCallers returns a compact caller chain, skipping this file's own frames.
// A logged write names its writer; inference from reading code does not close
// the bead's write-path questions.
func i7frCallers(skip int) string {
	pcs := make([]uintptr, 8)
	n := runtime.Callers(skip+2, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var out []string
	for i := 0; i < 6; i++ {
		f, more := frames.Next()
		if f.Function == "" {
			break
		}
		name := f.Function
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		out = append(out, fmt.Sprintf("%s:%d", name, f.Line))
		if !more {
			break
		}
	}
	return strings.Join(out, " <- ")
}

// i7frWho identifies the emitting process. INITECH_I7FR_WHO is set by the rig
// so each window is labelled even before it knows its own window identity.
func i7frWho() string {
	who := os.Getenv("INITECH_I7FR_WHO")
	if who == "" {
		who = "unlabelled"
	}
	return fmt.Sprintf("%s/pid%d", who, os.Getpid())
}

// i7frLog emits one instrumentation line.
func i7frLog(event string, kv ...any) {
	args := append([]any{"who", i7frWho()}, kv...)
	LogInfo("i7fr", event, args...)
}

// i7frLogWithStack emits one instrumentation line including the caller chain.
func i7frLogWithStack(event string, kv ...any) {
	args := append([]any{"who", i7frWho(), "callers", i7frCallers(1)}, kv...)
	LogInfo("i7fr", event, args...)
}

// i7frKeys renders a key list compactly for logging.
func i7frKeys(ss []string) string {
	if len(ss) == 0 {
		return "(none)"
	}
	return strings.Join(ss, ",")
}

// i7frMap renders a map deterministically for logging.
func i7frMap(m map[string]string) string {
	if len(m) == 0 {
		return "(empty)"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%s=%s", k, m[k])
	}
	return b.String()
}

// i7frDroppedLabels reports labels that members still reference but that the
// derived group universe does not contain -- the asymmetry this investigation
// is chasing: Groups is rebuilt only from the persisted groups: list, so a
// label reachable via group_of but missing from groups: cannot rejoin.
func i7frDroppedLabels(fileGroups, keptGroups []string, memberCount map[string]int) string {
	kept := make(map[string]bool, len(keptGroups))
	for _, g := range keptGroups {
		kept[g] = true
	}
	inFile := make(map[string]bool, len(fileGroups))
	for _, g := range fileGroups {
		inFile[g] = true
	}
	var out []string
	for label, n := range memberCount {
		if !kept[label] {
			reason := "pruned-empty"
			if !inFile[label] {
				reason = "ABSENT-FROM-groups-field"
			}
			out = append(out, fmt.Sprintf("%s(members=%d,%s)", label, n, reason))
		}
	}
	sort.Strings(out)
	return i7frKeys(out)
}
