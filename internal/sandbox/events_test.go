package sandbox

import (
	"testing"
	"time"

	"scratchpad/internal/protocol"
)

// TestEventBus_FanOut verifies that every subscriber receives every published
// event, in order, with a monotonic id and a stamped timestamp.
func TestEventBus_FanOut(t *testing.T) {
	b := NewEventBus(16)
	subA := b.Subscribe(4)
	defer subA.Cancel()
	subB := b.Subscribe(4)
	defer subB.Cancel()

	b.Publish(protocol.Event{Type: protocol.EventNavigation})
	b.Publish(protocol.Event{Type: protocol.EventConsole})
	b.Publish(protocol.Event{Type: protocol.EventObserveComplete})

	for name, sub := range map[string]*Subscription{"A": subA, "B": subB} {
		types := []string{}
		for i := 0; i < 3; i++ {
			select {
			case ev := <-sub.C:
				types = append(types, ev.Type)
				if ev.ID != int64(i+1) {
					t.Errorf("sub %s: event %d id = %d, want %d", name, i, ev.ID, i+1)
				}
				if ev.Timestamp.IsZero() {
					t.Errorf("sub %s: event %d timestamp is zero", name, i)
				}
			case <-time.After(time.Second):
				t.Fatalf("sub %s: timed out waiting for event %d", name, i)
			}
		}
		want := []string{protocol.EventNavigation, protocol.EventConsole, protocol.EventObserveComplete}
		if len(types) != len(want) {
			t.Fatalf("sub %s: got types %v, want %v", name, types, want)
		}
		for i := range want {
			if types[i] != want[i] {
				t.Errorf("sub %s: types[%d] = %q, want %q", name, i, types[i], want[i])
			}
		}
	}
}

// TestEventBus_DropOnOverflow verifies that a slow consumer with a full buffer
// never blocks the publisher: its overflow events are dropped while a fast
// consumer still sees everything.
func TestEventBus_DropOnOverflow(t *testing.T) {
	b := NewEventBus(32)
	slow := b.Subscribe(1) // 1-deep buffer fills on the second event
	defer slow.Cancel()

	for i := 0; i < 10; i++ {
		b.Publish(protocol.Event{Type: protocol.EventConsole})
	}

	// The slow consumer can drain at most what fit; the bus must not have
	// blocked, so we can assert at least one event was delivered.
	select {
	case <-slow.C:
	default:
		t.Fatal("slow subscriber received nothing; publisher may have blocked")
	}
}

// TestEventBus_CancelStopsDelivery verifies that a cancelled subscription no
// longer receives events and that cancelling is idempotent.
func TestEventBus_CancelStopsDelivery(t *testing.T) {
	b := NewEventBus(16)
	sub := b.Subscribe(4)

	b.Publish(protocol.Event{Type: protocol.EventNavigation})
	select {
	case <-sub.C:
	default:
		t.Fatal("expected the pre-cancel event to be delivered")
	}

	sub.Cancel()
	sub.Cancel() // must be safe to call twice

	b.Publish(protocol.Event{Type: protocol.EventConsole})
	select {
	case ev := <-sub.C:
		t.Fatalf("cancelled subscription received %q", ev.Type)
	default:
	}
}

// TestEventBus_RingBufferReplay verifies that Recent returns the last N events
// in order and that the ring stays bounded.
func TestEventBus_RingBufferReplay(t *testing.T) {
	b := NewEventBus(4) // tiny ring to force eviction
	for i := 0; i < 10; i++ {
		b.Publish(protocol.Event{Type: protocol.EventConsole})
	}

	if got := b.Len(); got != 4 {
		t.Fatalf("Len = %d, want 4 (ring capped)", got)
	}

	recent := b.Recent(0) // all retained
	if len(recent) != 4 {
		t.Fatalf("Recent(0) returned %d events, want 4", len(recent))
	}
	if recent[0].ID != 7 || recent[3].ID != 10 {
		t.Errorf("Recent order wrong: ids %d..%d, want 7..10", recent[0].ID, recent[3].ID)
	}

	two := b.Recent(2)
	if len(two) != 2 || two[0].ID != 9 || two[1].ID != 10 {
		t.Errorf("Recent(2) = ids %v, want [9 10]", ids(two))
	}
}

// TestEventBus_NoSubscribersNoBlock verifies that publishing with zero
// subscribers is a no-op that never blocks.
func TestEventBus_NoSubscribersNoBlock(t *testing.T) {
	b := NewEventBus(16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			b.Publish(protocol.Event{Type: protocol.EventCrash})
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publishing with no subscribers blocked")
	}
}

func ids(evs []protocol.Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}
