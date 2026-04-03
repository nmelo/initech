package mcp

import (
	"context"
	"fmt"
	"sync"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const outputDebounceInterval = 1 * time.Second

// subscriptionTracker tracks which resource URIs have active subscribers and
// manages debounced notifications for terminal output changes.
type subscriptionTracker struct {
	mu         sync.Mutex
	subs       map[string]int    // URI -> subscriber count
	dirtyPanes map[string]bool   // agent name -> needs output notification
	mcpServer  *gomcp.Server
	stopCh     chan struct{}
	stopped    bool
}

func newSubscriptionTracker(mcpServer *gomcp.Server) *subscriptionTracker {
	return &subscriptionTracker{
		subs:       make(map[string]int),
		dirtyPanes: make(map[string]bool),
		mcpServer:  mcpServer,
		stopCh:     make(chan struct{}),
	}
}

// Subscribe is called by the SDK when a client subscribes to a resource URI.
func (st *subscriptionTracker) Subscribe(_ context.Context, req *gomcp.SubscribeRequest) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.subs[req.Params.URI]++
	return nil
}

// Unsubscribe is called by the SDK when a client unsubscribes from a resource URI.
func (st *subscriptionTracker) Unsubscribe(_ context.Context, req *gomcp.UnsubscribeRequest) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.subs[req.Params.URI] > 0 {
		st.subs[req.Params.URI]--
		if st.subs[req.Params.URI] == 0 {
			delete(st.subs, req.Params.URI)
		}
	}
	return nil
}

// HasSubscribers returns true if any client is subscribed to the given URI.
func (st *subscriptionTracker) HasSubscribers(uri string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.subs[uri] > 0
}

// NotifyResourceUpdated sends a resource updated notification if there are
// subscribers for the URI.
func (st *subscriptionTracker) NotifyResourceUpdated(uri string) {
	if !st.HasSubscribers(uri) {
		return
	}
	st.mcpServer.ResourceUpdated(context.Background(), &gomcp.ResourceUpdatedNotificationParams{
		URI: uri,
	})
}

// MarkPaneDirty marks a pane's terminal output as changed. The debounce
// goroutine will fire the notification within 1 second.
func (st *subscriptionTracker) MarkPaneDirty(name string) {
	uri := fmt.Sprintf("initech://agents/%s/output", name)
	if !st.HasSubscribers(uri) {
		return
	}
	st.mu.Lock()
	st.dirtyPanes[name] = true
	st.mu.Unlock()
}

// NotifyAgentStateChanged fires notifications for both the fleet status
// and the per-agent status resource.
func (st *subscriptionTracker) NotifyAgentStateChanged(name string) {
	st.NotifyResourceUpdated(statusResourceURI)
	st.NotifyResourceUpdated(fmt.Sprintf("initech://agents/%s/status", name))
}

// StartDebounce starts the background goroutine that flushes dirty pane
// notifications every second. Call Stop to clean up.
func (st *subscriptionTracker) StartDebounce() {
	go func() {
		ticker := time.NewTicker(outputDebounceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				st.flushDirty()
			case <-st.stopCh:
				return
			}
		}
	}()
}

// Stop shuts down the debounce goroutine.
func (st *subscriptionTracker) Stop() {
	st.mu.Lock()
	if !st.stopped {
		st.stopped = true
		close(st.stopCh)
	}
	st.mu.Unlock()
}

func (st *subscriptionTracker) flushDirty() {
	st.mu.Lock()
	dirty := st.dirtyPanes
	st.dirtyPanes = make(map[string]bool)
	st.mu.Unlock()

	for name := range dirty {
		uri := fmt.Sprintf("initech://agents/%s/output", name)
		st.NotifyResourceUpdated(uri)
	}
}
