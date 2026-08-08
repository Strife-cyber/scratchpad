package android

import (
	"errors"
	"sync"
	"testing"
	"time"

	"scratchpad/internal/protocol"
)

// fakeTreeDump returns a canned spatial tree and records how many times it was
// called, letting tests assert the cache avoids redundant dumps.
type fakeTreeDump struct {
	mu    sync.Mutex
	count int
	fail  bool
}

func (f *fakeTreeDump) call() ([]protocol.SpatialNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.count++
	if f.fail {
		return nil, errors.New("boom")
	}
	return []protocol.SpatialNode{{NodeID: "n1", Name: "node"}}, nil
}

func (f *fakeTreeDump) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count
}

func TestTreeCache_FirstObserveDumpsSynchronously(t *testing.T) {
	dump := &fakeTreeDump{}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)

	tree, stale, err := tc.treeForObserve()
	if err != nil {
		t.Fatalf("treeForObserve: %v", err)
	}
	if stale {
		t.Error("first observe should be a fresh dump, not stale")
	}
	if len(tree) != 1 || tree[0].Name != "node" {
		t.Errorf("tree = %+v, want the dumped node", tree)
	}
}

func TestTreeCache_ServesFreshCacheWithoutRedump(t *testing.T) {
	dump := &fakeTreeDump{}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)

	// Prime the cache with a synchronous dump.
	if _, _, err := tc.treeForObserve(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// A second read-only observe within freshWindow must be served from cache
	// (stale=true) without calling dump again.
	tree, stale, err := tc.treeForObserve()
	if err != nil {
		t.Fatalf("cached observe: %v", err)
	}
	if !stale {
		t.Error("read-only observe of a fresh cache should be flagged stale")
	}
	if len(tree) != 1 {
		t.Errorf("cached tree = %+v, want the cached node", tree)
	}
	if n := dump.calls(); n != 1 {
		t.Errorf("dump called %d times, want 1 (second observe served from cache)", n)
	}
}

func TestTreeCache_InvalidateForcesRedump(t *testing.T) {
	dump := &fakeTreeDump{}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)
	if _, _, err := tc.treeForObserve(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// A mutating action invalidates the cache; the next observe must re-dump and
	// report fresh (not stale) even though the cache was young.
	tc.invalidate()
	_, stale, err := tc.treeForObserve()
	if err != nil {
		t.Fatalf("observe after invalidate: %v", err)
	}
	if stale {
		t.Error("observe after invalidate() should re-dump (stale=false)")
	}
	if n := dump.calls(); n != 2 {
		t.Errorf("dump called %d times, want 2 (invalidate forced a re-dump)", n)
	}
}

func TestTreeCache_FailedFreshDumpFallsBackToCache(t *testing.T) {
	dump := &fakeTreeDump{}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)
	if _, _, err := tc.treeForObserve(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// Force the next dump to fail, but keep the cached tree within freshWindow.
	dump.fail = true
	tc.invalidate()
	tree, stale, err := tc.treeForObserve()
	if err != nil {
		t.Fatalf("observe with failing dump: %v", err)
	}
	if len(tree) != 1 {
		t.Errorf("fallback tree = %+v, want last good cached tree", tree)
	}
	_ = stale // fallback is best-effort; either flag is acceptable
}

func TestTreeCache_FailedDumpNoCacheReturnsError(t *testing.T) {
	dump := &fakeTreeDump{fail: true}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)

	_, _, err := tc.treeForObserve()
	if err == nil {
		t.Fatal("expected error when the first dump fails and there is no cache")
	}
}

func TestTreeCache_BackgroundRefreshUpdatesCache(t *testing.T) {
	dump := &fakeTreeDump{}
	tc := newTreeCache(dump.call)
	t.Cleanup(tc.stopBackgroundRefresh)
	if _, _, err := tc.treeForObserve(); err != nil {
		t.Fatalf("prime: %v", err)
	}

	// treeForObserve already self-started the refresher; let one tick run.
	time.Sleep(refreshInterval + 150*time.Millisecond)

	// A read-only observe is served from cache (stale) and the background goroutine
	// has re-dumped at least once since the prime.
	if n := dump.calls(); n < 2 {
		t.Errorf("dump called %d times, want >= 2 (background refresh re-dumped)", n)
	}
	tc.stopBackgroundRefresh()
}

func TestTreeCache_StopBeforeStartIsSafe(t *testing.T) {
	tc := newTreeCache(func() ([]protocol.SpatialNode, error) {
		return nil, errors.New("boom")
	})
	// Must not panic, hang, or deadlock.
	tc.stopBackgroundRefresh()
}

func TestTreeCache_StartIsIdempotent(t *testing.T) {
	tc := newTreeCache(func() ([]protocol.SpatialNode, error) {
		return []protocol.SpatialNode{{NodeID: "n"}}, nil
	})
	tc.startBackgroundRefresh()
	tc.startBackgroundRefresh() // second start is a no-op
	time.Sleep(refreshInterval + 150*time.Millisecond)
	tc.stopBackgroundRefresh() // must return promptly (single goroutine to stop)
}
