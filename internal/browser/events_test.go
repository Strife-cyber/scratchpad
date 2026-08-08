package browser

import (
	"encoding/json"
	"sync"
	"testing"

	"scratchpad/internal/protocol"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
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

// ---------------------------------------------------------------------------
// NewEventPublisher (improvement-plan item 34)
// ---------------------------------------------------------------------------

// TestNewEventPublisher_TranslatesRawCDPEvents verifies that the translator
// maps each raw CDP event family to the matching typed bus event.
func TestNewEventPublisher_TranslatesRawCDPEvents(t *testing.T) {
	var got []protocol.Event
	pub := NewEventPublisher(func(ev protocol.Event) { got = append(got, ev) })

	// Navigation: full frame navigation + SPA pushState.
	pub(&page.EventFrameNavigated{Frame: &cdp.Frame{ID: "frame-1", URL: "https://a.com/"}})
	pub(&page.EventNavigatedWithinDocument{FrameID: "frame-1", URL: "https://a.com/#x"})
	// Console.
	pub(&runtime.EventConsoleAPICalled{Type: runtime.APITypeError, Args: []*runtime.RemoteObject{{Value: jsonRaw("boom")}}})
	// Dialog open + close.
	pub(&page.EventJavascriptDialogOpening{Type: page.DialogTypeAlert, Message: "hi"})
	pub(&page.EventJavascriptDialogClosed{Result: true})
	// Target lifecycle.
	pub(&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "t1", Type: "page", URL: "https://a.com/"}})
	pub(&target.EventTargetDestroyed{TargetID: "t1"})
	pub(&target.EventTargetCrashed{TargetID: "t1", Status: "crashed", ErrorCode: 1})
	// Network request + response.
	pub(&network.EventRequestWillBeSent{RequestID: "r1", Request: &network.Request{URL: "https://a.com/x", Method: "GET"}})
	pub(&network.EventResponseReceived{RequestID: "r1", Response: &network.Response{URL: "https://a.com/x", Status: 200}})
	// Download begin + progress.
	pub(&cdpbrowser.EventDownloadWillBegin{GUID: "d1", URL: "https://a.com/f.bin", SuggestedFilename: "f.bin"})
	pub(&cdpbrowser.EventDownloadProgress{GUID: "d1", State: cdpbrowser.DownloadProgressStateCompleted, ReceivedBytes: 10, TotalBytes: 10})
	// Unrelated events are ignored.
	pub("not a cdp event")
	pub(&runtime.EventExceptionThrown{})

	wantTypes := []string{
		protocol.EventNavigation,
		protocol.EventNavigation,
		protocol.EventConsole,
		protocol.EventDialog,
		protocol.EventDialog,
		protocol.EventTargetCreated,
		protocol.EventTargetDestroyed,
		protocol.EventCrash,
		protocol.EventNetworkRequest,
		protocol.EventNetworkResponse,
		protocol.EventDownload,
		protocol.EventDownload,
	}
	if len(got) != len(wantTypes) {
		t.Fatalf("published %d events, want %d", len(got), len(wantTypes))
	}
	for i, want := range wantTypes {
		if got[i].Type != want {
			t.Errorf("event[%d] type = %q, want %q", i, got[i].Type, want)
		}
	}

	// Spot-check a couple of payloads.
	if len(got[0].Data) == 0 {
		t.Error("navigation event has no payload")
	} else {
		var nav struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(got[0].Data, &nav); err != nil {
			t.Fatalf("navigation payload unparseable: %v", err)
		}
		if nav.URL != "https://a.com/" {
			t.Errorf("navigation url = %q, want https://a.com/", nav.URL)
		}
	}
	var resp struct {
		Status int64 `json:"status"`
	}
	if err := json.Unmarshal(got[9].Data, &resp); err != nil || resp.Status != 200 {
		t.Errorf("network_response payload status = %d (err %v), want 200", resp.Status, err)
	}
}

// TestNewEventPublisher_ConcurrentSafety verifies the translator is safe to run
// from concurrent goroutines (the CDP event loop fans out to many listeners).
func TestNewEventPublisher_ConcurrentSafety(t *testing.T) {
	var mu sync.Mutex
	n := 0
	pub := NewEventPublisher(func(ev protocol.Event) {
		mu.Lock()
		n++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pub(&runtime.EventConsoleAPICalled{Type: runtime.APITypeLog, Args: []*runtime.RemoteObject{{Value: jsonRaw("x")}}})
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if n != 10 {
		t.Errorf("published %d events, want 10", n)
	}
}
