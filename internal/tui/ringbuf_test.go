package tui

import (
	"bytes"
	"testing"
)

func TestRingBuf_Empty(t *testing.T) {
	r := NewRingBuf(64)
	if snap := r.Snapshot(); snap != nil {
		t.Errorf("empty snapshot = %v, want nil", snap)
	}
	if r.Len() != 0 {
		t.Errorf("empty Len = %d, want 0", r.Len())
	}
}

func TestRingBuf_BasicWrite(t *testing.T) {
	r := NewRingBuf(64)
	r.Write([]byte("hello"))
	snap := r.Snapshot()
	if string(snap) != "hello" {
		t.Errorf("snapshot = %q, want hello", snap)
	}
	if r.Len() != 5 {
		t.Errorf("Len = %d, want 5", r.Len())
	}
}

func TestRingBuf_MultipleWrites(t *testing.T) {
	r := NewRingBuf(64)
	r.Write([]byte("aaa"))
	r.Write([]byte("bbb"))
	r.Write([]byte("ccc"))
	snap := r.Snapshot()
	if string(snap) != "aaabbbccc" {
		t.Errorf("snapshot = %q, want aaabbbccc", snap)
	}
}

func TestRingBuf_ExactFill(t *testing.T) {
	r := NewRingBuf(8)
	r.Write([]byte("12345678"))
	snap := r.Snapshot()
	if string(snap) != "12345678" {
		t.Errorf("snapshot = %q, want 12345678", snap)
	}
	if r.Len() != 8 {
		t.Errorf("Len = %d, want 8", r.Len())
	}
}

func TestRingBuf_WrapAround(t *testing.T) {
	r := NewRingBuf(8)
	r.Write([]byte("12345678")) // fills exactly
	r.Write([]byte("AB"))       // overwrites "12" with "AB"
	snap := r.Snapshot()
	// Oldest data is "345678", newest is "AB".
	if string(snap) != "345678AB" {
		t.Errorf("snapshot = %q, want 345678AB", snap)
	}
}

func TestRingBuf_MultipleWraps(t *testing.T) {
	r := NewRingBuf(8)
	for i := 0; i < 100; i++ {
		r.Write([]byte("x"))
	}
	snap := r.Snapshot()
	if len(snap) != 8 {
		t.Fatalf("snapshot len = %d, want 8", len(snap))
	}
	// All bytes should be 'x'.
	for i, b := range snap {
		if b != 'x' {
			t.Errorf("snap[%d] = %c, want x", i, b)
		}
	}
}

func TestRingBuf_OversizedWrite(t *testing.T) {
	r := NewRingBuf(8)
	// Write more than buffer capacity. Only the last 8 bytes survive.
	r.Write([]byte("ABCDEFGHIJKLMNOP")) // 16 bytes, buf is 8
	snap := r.Snapshot()
	if string(snap) != "IJKLMNOP" {
		t.Errorf("snapshot = %q, want IJKLMNOP", snap)
	}
}

func TestRingBuf_SplitWrite(t *testing.T) {
	r := NewRingBuf(8)
	r.Write([]byte("123456")) // w=6, 2 bytes of space
	r.Write([]byte("ABCD"))   // 4 bytes, wraps: "AB" fills [6:8], "CD" fills [0:2]
	snap := r.Snapshot()
	// After: buf = [C D 3 4 5 6 A B], w=2, full=true
	// Snapshot: buf[2:] + buf[:2] = "3456AB" + "CD"
	if string(snap) != "3456ABCD" {
		t.Errorf("snapshot = %q, want 3456ABCD", snap)
	}
}

func TestRingBuf_IOWriterContract(t *testing.T) {
	r := NewRingBuf(64)
	n, err := r.Write([]byte("test"))
	if n != 4 || err != nil {
		t.Errorf("Write returned (%d, %v), want (4, nil)", n, err)
	}
}

func TestRingBuf_SnapshotIsACopy(t *testing.T) {
	r := NewRingBuf(64)
	r.Write([]byte("original"))
	snap := r.Snapshot()
	snap[0] = 'X' // Mutate the snapshot.
	snap2 := r.Snapshot()
	if snap2[0] == 'X' {
		t.Error("Snapshot should return a copy, not a reference to internal buffer")
	}
}

func TestRingBuf_DefaultSize(t *testing.T) {
	r := NewRingBuf(0)
	if len(r.buf) != DefaultRingBufSize {
		t.Errorf("default size = %d, want %d", len(r.buf), DefaultRingBufSize)
	}
}

func TestRingBuf_LargeRealisticWorkload(t *testing.T) {
	// Simulate 1MB of terminal output into a 256KB buffer.
	r := NewRingBuf(DefaultRingBufSize)
	chunk := bytes.Repeat([]byte("terminal output line\r\n"), 100) // ~2200 bytes
	for i := 0; i < 500; i++ {
		r.Write(chunk)
	}
	snap := r.Snapshot()
	if len(snap) != DefaultRingBufSize {
		t.Errorf("snapshot len = %d, want %d", len(snap), DefaultRingBufSize)
	}
	// Verify content is valid (all bytes from the chunk pattern).
	if !bytes.Contains(snap, []byte("terminal output line")) {
		t.Error("snapshot should contain recent terminal output")
	}
}
