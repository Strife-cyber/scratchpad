package browser

import (
	"sync"
	"testing"

	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/runtime"
)

// ---------------------------------------------------------------------------
// NewConsoleLogger
// ---------------------------------------------------------------------------

func TestNewConsoleLogger_FiltersNonConsoleEvents(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	// Send a non-console event — should be ignored.
	handler("not a console event")

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 logs for non-console event, got %d", count)
	}
}

func TestNewConsoleLogger_LogsConsoleEvent(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	// Send a real console API event.
	handler(&runtime.EventConsoleAPICalled{
		Type: "log",
		Args: []*runtime.RemoteObject{
			{Value: jsonRaw("hello world")},
		},
	})

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 log, got %d", count)
	}

	mu.Lock()
	entry := logs[0]
	mu.Unlock()
	if entry.Level != "log" {
		t.Errorf("expected level 'log', got %q", entry.Level)
	}
}

// jsonRaw returns a json-wrapped string as []byte for RemoteObject.Value.
func jsonRaw(s string) []byte {
	// Produce a JSON string literal: "s"
	quoted := `"` + s + `"`
	return []byte(quoted)
}

func TestNewConsoleLogger_LogsMultipleEvents(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	handler(&runtime.EventConsoleAPICalled{Type: "log", Args: []*runtime.RemoteObject{{Value: jsonRaw("first")}}})
	handler(&runtime.EventConsoleAPICalled{Type: "warn", Args: []*runtime.RemoteObject{{Value: jsonRaw("second")}}})
	handler(&runtime.EventConsoleAPICalled{Type: "error", Args: []*runtime.RemoteObject{{Value: jsonRaw("third")}}})

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != 3 {
		t.Fatalf("expected 3 logs, got %d", count)
	}

	mu.Lock()
	for i, l := range logs {
		if l.Message == "" {
			t.Errorf("log[%d] has empty message", i)
		}
	}
	mu.Unlock()
}

func TestNewConsoleLogger_ConcurrentSafety(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler(&runtime.EventConsoleAPICalled{
				Type: "log",
				Args: []*runtime.RemoteObject{{Value: jsonRaw("concurrent")}},
			})
		}()
	}
	wg.Wait()

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != 10 {
		t.Errorf("expected 10 logs from concurrent calls, got %d", count)
	}
}

func TestNewConsoleLogger_EmptyArgs(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	handler(&runtime.EventConsoleAPICalled{
		Type: "log",
		Args: nil,
	})

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 log even with nil args, got %d", count)
	}

	mu.Lock()
	entry := logs[0]
	mu.Unlock()
	if entry.Message != "" {
		t.Errorf("expected empty message for nil args, got %q", entry.Message)
	}
}

func TestNewConsoleLogger_LogsMultipleEventTypes(t *testing.T) {
	var logs []protocol.ConsoleLog
	var mu sync.Mutex

	handler := NewConsoleLogger(&logs, &mu)

	type typePair struct {
		apiType runtime.APIType
		level   string
	}
	pairs := []typePair{
		{runtime.APITypeLog, "log"},
		{runtime.APITypeWarning, "warning"},
		{runtime.APITypeError, "error"},
		{runtime.APITypeInfo, "info"},
		{runtime.APITypeDebug, "debug"},
	}
	for _, p := range pairs {
		handler(&runtime.EventConsoleAPICalled{
			Type: p.apiType,
			Args: []*runtime.RemoteObject{{Value: jsonRaw("msg")}},
		})
	}

	mu.Lock()
	count := len(logs)
	mu.Unlock()
	if count != len(pairs) {
		t.Fatalf("expected %d logs, got %d", len(pairs), count)
	}
}
