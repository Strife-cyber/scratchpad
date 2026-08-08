package android

import (
	"sync"
	"time"

	"scratchpad/internal/protocol"
)

// This file implements the cached-spatial-tree layer of improvement-plan item 27.
//
// Every Observe() on Android previously paid the full adb round-trip
// (uiautomator dump + cat + XML parse). The treeCache keeps the most recent
// parsed hierarchy and refreshes it in the background so repeated observes are
// cheap, while still guaranteeing a fresh snapshot right after any action that
// mutates the screen.

const (
	// refreshInterval is how often the background goroutine re-dumps the UI
	// hierarchy while the session is active. 1s keeps the cached tree fresh for
	// typical agent turn-times without saturating the adb connection.
	refreshInterval = 1 * time.Second

	// freshWindow is how recent a cached dump must be for Observe() to serve it
	// without a synchronous re-dump. Wider than refreshInterval so a tree
	// refreshed in the background is served from cache, never re-captured for a
	// read-only observation.
	freshWindow = 2 * time.Second
)

// treeCache holds the most recently parsed spatial tree. It is refreshed in the
// background (startBackgroundRefresh) and invalidated by mutating actions so the
// next Observe re-dumps and reflects the change.
type treeCache struct {
	mu sync.Mutex

	// tree is the last successfully dumped hierarchy.
	tree []protocol.SpatialNode
	// dumpedAt is when tree was captured; combined with freshWindow it decides
	// whether a read-only Observe is served from cache (stale=true).
	dumpedAt time.Time
	// invalid is set by invalidate() after a mutating action, forcing the next
	// Observe to re-dump even if the cache is within freshWindow.
	invalid bool

	// dump captures the current hierarchy from the device. It is the seam tests
	// substitute with a fake.
	dump func() ([]protocol.SpatialNode, error)

	// Background-refresh lifecycle.
	started bool
	stop    chan struct{}
	done    chan struct{}
}

// newTreeCache returns a cache that re-dumps via the given function.
func newTreeCache(dump func() ([]protocol.SpatialNode, error)) *treeCache {
	return &treeCache{dump: dump}
}

// startBackgroundRefresh launches the refresh goroutine. Idempotent: only the
// first call starts it. The goroutine re-dumps every refreshInterval and exits
// on stopBackgroundRefresh, which is called from AndroidEngine.Close.
func (tc *treeCache) startBackgroundRefresh() {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.started {
		return
	}
	tc.started = true
	tc.stop = make(chan struct{})
	tc.done = make(chan struct{})
	stop, done := tc.stop, tc.done
	go func() {
		defer close(done)
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				tc.refresh()
			}
		}
	}()
}

// stopBackgroundRefresh stops the refresh goroutine and waits for it to exit.
// Safe to call when the cache was never started (e.g. Close on an idle engine).
func (tc *treeCache) stopBackgroundRefresh() {
	tc.mu.Lock()
	if !tc.started {
		tc.mu.Unlock()
		return
	}
	tc.started = false
	stop, done := tc.stop, tc.done
	tc.mu.Unlock()

	close(stop)
	<-done
}

// refresh re-dumps the hierarchy and stores it. A failed dump keeps the last
// good tree (stale data is better than a failed Observe); a later read-only
// Observe will re-dump synchronously if the cache is too old.
func (tc *treeCache) refresh() {
	tree, err := tc.dump()
	if err != nil {
		return
	}
	tc.mu.Lock()
	tc.tree = tree
	tc.dumpedAt = time.Now()
	tc.invalid = false
	tc.mu.Unlock()
}

// invalidate marks the cached tree stale so the next Observe re-dumps. Called
// after any action that mutates the screen (click, type, scroll, keyevent, ...).
func (tc *treeCache) invalidate() {
	tc.mu.Lock()
	tc.invalid = true
	tc.mu.Unlock()
}

// treeForObserve returns the spatial tree for an observation and whether it was
// served from cache (stale=true) rather than freshly captured. When the cache is
// fresh and un-invalidated it is served without a synchronous dump; otherwise
// the tree is captured now. A failed fresh capture falls back to the last good
// cached tree when one exists (still flagged stale).
func (tc *treeCache) treeForObserve() (tree []protocol.SpatialNode, stale bool, err error) {
	// Self-start the background refresher on first use (idempotent), so engines
	// that never observe never spawn a goroutine.
	tc.startBackgroundRefresh()

	tc.mu.Lock()
	if !tc.invalid && len(tc.tree) > 0 && time.Since(tc.dumpedAt) <= freshWindow {
		tree := tc.tree
		tc.mu.Unlock()
		return tree, true, nil
	}
	tc.mu.Unlock()

	tree, err = tc.dump()
	if err != nil {
		tc.mu.Lock()
		cached := tc.tree
		tc.mu.Unlock()
		if len(cached) > 0 {
			return cached, true, nil
		}
		return nil, false, err
	}
	tc.mu.Lock()
	tc.tree = tree
	tc.dumpedAt = time.Now()
	tc.invalid = false
	tc.mu.Unlock()
	return tree, false, nil
}
