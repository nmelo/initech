package tui

import "testing"

// BenchmarkCurrentGoroutineID documents the cost of runOnMain's reentrancy
// check (ini-jesh), since a runtime.Stack-based goroutine-ID parse is not
// free and the choice to use it was deliberate: ~1.3µs/op on Apple M5 Pro
// (BenchmarkCurrentGoroutineID-15  ~1.3µs/op), called only at IPC-request
// rate and a 10s resource-monitor tick — never from render() or the PTY
// read loop, neither of which calls runOnMain at all.
func BenchmarkCurrentGoroutineID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = currentGoroutineID()
	}
}
