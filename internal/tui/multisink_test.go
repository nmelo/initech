package tui

import (
	"errors"
	"sync"
	"testing"
)

type testWriter struct {
	mu   sync.Mutex
	data []byte
}

func (tw *testWriter) Write(p []byte) (int, error) {
	tw.mu.Lock()
	tw.data = append(tw.data, p...)
	tw.mu.Unlock()
	return len(p), nil
}

func (tw *testWriter) String() string {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	return string(tw.data)
}

type failWriter struct{}

func (fw *failWriter) Write(p []byte) (int, error) {
	return 0, errors.New("dead")
}

func TestMultiSink_FanOut(t *testing.T) {
	ms := NewMultiSink()
	w1 := &testWriter{}
	w2 := &testWriter{}
	ms.Add(w1)
	ms.Add(w2)

	ms.Write([]byte("hello"))

	if w1.String() != "hello" {
		t.Errorf("w1 = %q, want hello", w1.String())
	}
	if w2.String() != "hello" {
		t.Errorf("w2 = %q, want hello", w2.String())
	}
}

func TestMultiSink_DeadWriterRemoved(t *testing.T) {
	ms := NewMultiSink()
	good := &testWriter{}
	bad := &failWriter{}
	ms.Add(good)
	ms.Add(bad)

	if ms.Len() != 2 {
		t.Fatalf("before write: len = %d, want 2", ms.Len())
	}

	ms.Write([]byte("test"))

	if ms.Len() != 1 {
		t.Errorf("after write: len = %d, want 1 (dead writer removed)", ms.Len())
	}
	if good.String() != "test" {
		t.Errorf("good writer = %q, want test", good.String())
	}
}

func TestMultiSink_Remove(t *testing.T) {
	ms := NewMultiSink()
	w1 := &testWriter{}
	w2 := &testWriter{}
	ms.Add(w1)
	ms.Add(w2)
	ms.Remove(w1)

	ms.Write([]byte("data"))

	if w1.String() != "" {
		t.Errorf("removed writer should not receive data, got %q", w1.String())
	}
	if w2.String() != "data" {
		t.Errorf("w2 = %q, want data", w2.String())
	}
}

func TestMultiSink_Empty(t *testing.T) {
	ms := NewMultiSink()
	n, err := ms.Write([]byte("noop"))
	if n != 4 || err != nil {
		t.Errorf("empty Write = (%d, %v), want (4, nil)", n, err)
	}
}

func TestMultiSink_ConcurrentWrites(t *testing.T) {
	ms := NewMultiSink()
	w := &testWriter{}
	ms.Add(w)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ms.Write([]byte("x"))
		}()
	}
	wg.Wait()

	if len(w.String()) != 100 {
		t.Errorf("len = %d, want 100", len(w.String()))
	}
}
