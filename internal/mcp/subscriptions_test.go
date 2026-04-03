package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestSubscriptionTracker_SubscribeUnsubscribe(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	uri := "initech://agents/eng1/output"

	if st.HasSubscribers(uri) {
		t.Error("should have no subscribers initially")
	}

	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})
	if !st.HasSubscribers(uri) {
		t.Error("should have subscriber after Subscribe")
	}

	st.Unsubscribe(context.Background(), &gomcp.UnsubscribeRequest{
		Params: &gomcp.UnsubscribeParams{URI: uri},
	})
	if st.HasSubscribers(uri) {
		t.Error("should have no subscribers after Unsubscribe")
	}
}

func TestSubscriptionTracker_MultipleSubscribers(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	uri := "initech://status"

	// Two subscriptions.
	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})
	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})

	if !st.HasSubscribers(uri) {
		t.Error("should have subscribers")
	}

	// One unsubscribe: still has one left.
	st.Unsubscribe(context.Background(), &gomcp.UnsubscribeRequest{
		Params: &gomcp.UnsubscribeParams{URI: uri},
	})
	if !st.HasSubscribers(uri) {
		t.Error("should still have subscriber after one Unsubscribe")
	}

	// Second unsubscribe: none left.
	st.Unsubscribe(context.Background(), &gomcp.UnsubscribeRequest{
		Params: &gomcp.UnsubscribeParams{URI: uri},
	})
	if st.HasSubscribers(uri) {
		t.Error("should have no subscribers after both Unsubscribe")
	}
}

func TestSubscriptionTracker_UnsubscribeWithoutSubscribe(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	// Should not panic.
	err := st.Unsubscribe(context.Background(), &gomcp.UnsubscribeRequest{
		Params: &gomcp.UnsubscribeParams{URI: "initech://agents/eng1/output"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSubscriptionTracker_MarkPaneDirty_NoSubscribers(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	// No subscribers: dirty map should stay empty (zero overhead path).
	st.MarkPaneDirty("eng1")

	st.mu.Lock()
	dirty := len(st.dirtyPanes)
	st.mu.Unlock()
	if dirty != 0 {
		t.Errorf("expected 0 dirty panes (no subscribers), got %d", dirty)
	}
}

func TestSubscriptionTracker_MarkPaneDirty_WithSubscriber(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	uri := "initech://agents/eng1/output"
	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})

	st.MarkPaneDirty("eng1")

	st.mu.Lock()
	dirty := st.dirtyPanes["eng1"]
	st.mu.Unlock()
	if !dirty {
		t.Error("expected eng1 to be marked dirty")
	}
}

func TestSubscriptionTracker_FlushDirty(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)

	// Subscribe to eng1 output.
	uri := "initech://agents/eng1/output"
	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})

	// Mark dirty.
	st.MarkPaneDirty("eng1")

	// Flush should clear dirty state.
	st.flushDirty()

	st.mu.Lock()
	dirty := len(st.dirtyPanes)
	st.mu.Unlock()
	if dirty != 0 {
		t.Errorf("expected 0 dirty panes after flush, got %d", dirty)
	}
}

func TestSubscriptionTracker_DebounceGoroutine(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow debounce test in short mode")
	}
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)
	st.StartDebounce()
	defer st.Stop()

	// Subscribe.
	uri := "initech://agents/eng1/output"
	st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
		Params: &gomcp.SubscribeParams{URI: uri},
	})

	// Mark dirty multiple times rapidly.
	for i := 0; i < 10; i++ {
		st.MarkPaneDirty("eng1")
	}

	// Wait for debounce to flush (slightly over 1 tick).
	time.Sleep(1200 * time.Millisecond)

	st.mu.Lock()
	dirty := len(st.dirtyPanes)
	st.mu.Unlock()
	if dirty != 0 {
		t.Errorf("expected dirty panes to be flushed after debounce, got %d", dirty)
	}
}

func TestSubscriptionTracker_StopIsIdempotent(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)
	st.StartDebounce()

	// Double stop should not panic.
	st.Stop()
	st.Stop()
}

func TestSubscriptionTracker_ConcurrentSafety(t *testing.T) {
	mcpServer := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "1.0"}, nil)
	st := newSubscriptionTracker(mcpServer)
	st.StartDebounce()
	defer st.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				st.Subscribe(context.Background(), &gomcp.SubscribeRequest{
					Params: &gomcp.SubscribeParams{URI: "initech://agents/eng1/output"},
				})
				st.MarkPaneDirty("eng1")
				st.NotifyAgentStateChanged("eng1")
				st.Unsubscribe(context.Background(), &gomcp.UnsubscribeRequest{
					Params: &gomcp.UnsubscribeParams{URI: "initech://agents/eng1/output"},
				})
			}
		}()
	}
	wg.Wait() // Must not panic or deadlock.
}
