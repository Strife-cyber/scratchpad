package server

import "context"

// Concurrency is a server-wide semaphore that caps the number of actions
// executing concurrently across all sessions. It backs the --max-concurrent-actions
// knob on the server. A nil *Concurrency means unlimited concurrency.
//
// The semaphore gates actions only; observe/navigate/session messages are not
// throttled, so control latency is unaffected by a saturated slot pool.
type Concurrency struct {
	slots chan struct{}
}

// NewConcurrency returns a Concurrency limited to max concurrent actions.
// A max <= 0 means unlimited (Acquire always succeeds).
func NewConcurrency(max int) *Concurrency {
	if max <= 0 {
		return &Concurrency{slots: nil}
	}
	return &Concurrency{slots: make(chan struct{}, max)}
}

// Acquire takes one slot, blocking until a slot is free or ctx is cancelled.
// It returns false when ctx is cancelled before a slot becomes available.
func (c *Concurrency) Acquire(ctx context.Context) bool {
	if c.slots == nil {
		return true
	}
	select {
	case c.slots <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

// Release returns one slot. It must be called once per successful Acquire.
func (c *Concurrency) Release() {
	if c.slots == nil {
		return
	}
	<-c.slots
}
