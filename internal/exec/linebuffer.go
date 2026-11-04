package exec

import (
	"bytes"
	"sync"
)

// lineBuffer collects a command's stdout and stderr together, safely.
type lineBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lineBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lineBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
