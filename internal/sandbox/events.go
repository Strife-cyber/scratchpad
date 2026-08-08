package sandbox

import (
	"sync"
	"time"

	"scratchpad/internal/protocol"
)

// EventBus is the per-session publish/subscribe hub for typed engine events
// (improvement-plan item 34). Engines publish via the existing AddListener hook
// (see browser.NewEventPublisher); the WS and SSE transports subscribe so late
// subscribers can replay the small ring buffer instead of missing history.
//
// The bus is explicitly non-blocking for publishers: Publish fans out to every
// subscriber's buffered channel and drops the event when a channel is full
// (drop-on-overflow). A slow or absent consumer therefore can never stall the
// engine's CDP event loop.
type EventBus struct {
	mu sync.Mutex

	// seq is the monotonic event-id counter; ring is a small bounded history
	// of recent events replayed to late subscribers via Recent.
	seq      int64
	ring     []protocol.Event
	ringSize int

	// subs maps subscriber ids to their buffered channels.
	subs   map[int64]chan protocol.Event
	nextID int64
}

// defaultEventRingSize bounds how many recent events a session's bus retains
// for replay to late subscribers (SSE reconnect, wait_for_event predicates).
const defaultEventRingSize = 100

// NewEventBus returns an empty EventBus retaining up to ringSize recent events.
func NewEventBus(ringSize int) *EventBus {
	if ringSize <= 0 {
		ringSize = defaultEventRingSize
	}
	return &EventBus{
		ring:     make([]protocol.Event, 0, ringSize),
		ringSize: ringSize,
		subs:     make(map[int64]chan protocol.Event),
	}
}

// Publish appends ev to the ring buffer (stamping a monotonic ID and timestamp
// when absent) and fans it out to every subscriber without ever blocking. A
// subscriber whose buffer is full has the event dropped.
func (b *EventBus) Publish(ev protocol.Event) {
	b.mu.Lock()
	if ev.ID == 0 {
		b.seq++
		ev.ID = b.seq
	}
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now()
	}
	b.ring = append(b.ring, ev)
	if len(b.ring) > b.ringSize {
		b.ring = b.ring[len(b.ring)-b.ringSize:]
	}
	subs := make([]chan protocol.Event, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default: // drop-on-overflow: a slow consumer never blocks the engine.
		}
	}
}

// Subscription is a live view of a session's event stream. C yields published
// events; Cancel removes the subscription from the bus. Cancel does not close C
// (a concurrent Publish could already hold the channel), so consumers should
// stop via their own termination signal and call Cancel to release the slot.
type Subscription struct {
	C      <-chan protocol.Event
	cancel func()
}

// Cancel detaches the subscription from the bus. Safe to call multiple times.
func (s *Subscription) Cancel() {
	s.cancel()
}

// Subscribe registers a new subscriber with a buffered channel of the given
// size (clamped to at least 1). Events published after Subscribe are delivered;
// recent history is available separately via Recent.
func (b *EventBus) Subscribe(buffer int) *Subscription {
	if buffer <= 0 {
		buffer = 1
	}
	ch := make(chan protocol.Event, buffer)
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = ch
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, id)
			b.mu.Unlock()
		})
	}
	return &Subscription{C: ch, cancel: cancel}
}

// Recent returns up to n of the most recent events from the ring buffer, in
// chronological order. n <= 0 returns every retained event. The returned slice
// is a copy, so callers may mutate it freely.
func (b *EventBus) Recent(n int) []protocol.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.ring) {
		n = len(b.ring)
	}
	out := make([]protocol.Event, n)
	copy(out, b.ring[len(b.ring)-n:])
	return out
}

// Len returns the number of events currently retained in the ring buffer.
// Primarily useful in tests and for metrics.
func (b *EventBus) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.ring)
}

// SubscriberCount returns the number of live subscribers. Primarily useful in
// tests (e.g. to wait until an SSE/WS consumer has attached) and metrics.
func (b *EventBus) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
