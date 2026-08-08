package android

import (
	"testing"
)

// Benchmarks for per-observe latency (improvement-plan item 27 step 7). They run
// against the fake adb, so they measure the Go-side overhead of Observe — the
// cache-hit path vs. a fresh-dump path — not device I/O, and need no live adb.

// BenchmarkObserve_Cached measures a read-only Observe served entirely from the
// cached spatial tree (stale=true): no adb round-trip, only the observe budget
// application and JSON assembly.
func BenchmarkObserve_Cached(b *testing.B) {
	e := newAndroidEngineWithConn(newADBConn("", fakeObserveADB()))
	defer e.Close()

	// Prime the cache so the timed loop hits the warm path.
	if _, err := e.Observe(); err != nil {
		b.Fatalf("prime observe: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Observe(); err != nil {
			b.Fatalf("observe: %v", err)
		}
	}
}

// BenchmarkObserve_Cold measures a full observation that must re-dump the
// hierarchy: the cache is invalidated each iteration (as a mutating action
// would), so every observe pays the dump+parse path.
func BenchmarkObserve_Cold(b *testing.B) {
	e := newAndroidEngineWithConn(newADBConn("", fakeObserveADB()))
	defer e.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.treeCache.invalidate()
		if _, err := e.Observe(); err != nil {
			b.Fatalf("observe: %v", err)
		}
	}
}
