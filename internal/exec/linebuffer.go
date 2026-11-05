package exec

import (
	"bytes"
	"sync"
)

// lineOutputCap is how much of a line's output RunLine keeps. A chatty command
// cannot grow memory past it: the rest is read and dropped, never refused, so
// the line is not killed by a broken pipe.
const lineOutputCap = 64 * 1024

// lineBuffer collects a command's stdout and stderr together, safely, up to
// lineOutputCap.
type lineBuffer struct {
	mu      sync.Mutex
	b       bytes.Buffer
	dropped int
}

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := min(len(p), lineOutputCap-l.b.Len())
	if keep > 0 {
		l.b.Write(p[:keep])
	}
	l.dropped += len(p) - max(keep, 0)
	return len(p), nil
}

func (l *lineBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
