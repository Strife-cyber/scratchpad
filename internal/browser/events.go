package browser

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
)

// NewConsoleLogger returns an EventHandler that collects Chrome console messages
// into the provided slice. The mutex guards concurrent writes from the CDP
// event loop.
func NewConsoleLogger(logs *[]protocol.ConsoleLog, mu *sync.Mutex) engine.EventHandler {
	return func(ev any) {
		e, ok := ev.(*runtime.EventConsoleAPICalled)
		if !ok {
			return
		}
		msg := ""
		if len(e.Args) > 0 {
			msg = fmt.Sprintf("%v", e.Args[0].Value)
		}
		mu.Lock()
		defer mu.Unlock()
		*logs = append(*logs, protocol.ConsoleLog{
			Level:     string(e.Type),
			Message:   msg,
			Timestamp: time.Now().Unix(),
		})
	}
}

// NewEventPublisher returns an EventHandler that translates raw CDP events into
// typed protocol.Events and forwards them to pub (improvement-plan item 34).
// The sandbox wires it onto the engine via AddListener, exactly like
// NewConsoleLogger; events that do not map to a typed bus event (network/target
// noise, input events, ...) are ignored. pub must be non-blocking (the bus
// fan-out drops on overflow), because this runs synchronously in the CDP event
// loop.
func NewEventPublisher(pub func(protocol.Event)) engine.EventHandler {
	return func(ev any) {
		switch e := ev.(type) {
		case *page.EventFrameNavigated:
			if e.Frame != nil {
				pub(protocol.Event{Type: protocol.EventNavigation, Data: payload(map[string]any{
					"url":      e.Frame.URL,
					"frame_id": e.Frame.ID.String(),
				})})
			}
		case *page.EventNavigatedWithinDocument:
			pub(protocol.Event{Type: protocol.EventNavigation, Data: payload(map[string]any{
				"url":      e.URL,
				"frame_id": e.FrameID.String(),
			})})
		case *runtime.EventConsoleAPICalled:
			msg := ""
			if len(e.Args) > 0 {
				msg = fmt.Sprintf("%v", e.Args[0].Value)
			}
			pub(protocol.Event{Type: protocol.EventConsole, Data: payload(map[string]any{
				"level":   string(e.Type),
				"message": msg,
			})})
		case *page.EventJavascriptDialogOpening:
			pub(protocol.Event{Type: protocol.EventDialog, Data: payload(map[string]any{
				"state":   "opened",
				"type":    string(e.Type),
				"message": e.Message,
			})})
		case *page.EventJavascriptDialogClosed:
			pub(protocol.Event{Type: protocol.EventDialog, Data: payload(map[string]any{
				"state":  "closed",
				"result": e.Result,
			})})
		case *target.EventTargetCreated:
			if e.TargetInfo != nil {
				pub(protocol.Event{Type: protocol.EventTargetCreated, Data: payload(map[string]any{
					"target_id": e.TargetInfo.TargetID.String(),
					"type":      e.TargetInfo.Type,
					"url":       e.TargetInfo.URL,
				})})
			}
		case *target.EventTargetDestroyed:
			pub(protocol.Event{Type: protocol.EventTargetDestroyed, Data: payload(map[string]any{
				"target_id": e.TargetID.String(),
			})})
		case *target.EventTargetCrashed:
			pub(protocol.Event{Type: protocol.EventCrash, Data: payload(map[string]any{
				"target_id":  e.TargetID.String(),
				"status":     e.Status,
				"error_code": e.ErrorCode,
			})})
		case *network.EventRequestWillBeSent:
			if e.Request != nil {
				pub(protocol.Event{Type: protocol.EventNetworkRequest, Data: payload(map[string]any{
					"request_id": e.RequestID.String(),
					"url":        e.Request.URL,
					"method":     e.Request.Method,
				})})
			}
		case *network.EventResponseReceived:
			if e.Response != nil {
				pub(protocol.Event{Type: protocol.EventNetworkResponse, Data: payload(map[string]any{
					"request_id": e.RequestID.String(),
					"url":        e.Response.URL,
					"status":     e.Response.Status,
				})})
			}
		case *cdpbrowser.EventDownloadWillBegin:
			pub(protocol.Event{Type: protocol.EventDownload, Data: payload(map[string]any{
				"id":       e.GUID,
				"url":      e.URL,
				"filename": e.SuggestedFilename,
				"state":    "begin",
			})})
		case *cdpbrowser.EventDownloadProgress:
			pub(protocol.Event{Type: protocol.EventDownload, Data: payload(map[string]any{
				"id":             e.GUID,
				"state":          string(e.State),
				"received_bytes": int64(e.ReceivedBytes),
				"total_bytes":    int64(e.TotalBytes),
			})})
		}
	}
}

// payload marshals a map into a json.RawMessage event payload, returning nil
// when marshaling fails (should not happen with plain values).
func payload(m map[string]any) json.RawMessage {
	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return data
}
