package browser

import (
	"fmt"
	"scratchpad/internal/protocol"
	"sync"
	"time"

	"github.com/chromedp/cdproto/runtime"
)

// EventHandler is a function that processes CDP events.
type EventHandler func(event interface{})

// NewConsoleLogger returns a handler that pushes logs into a provided slice.
// This decouples the storage from the engine.
func NewConsoleLogger(sessionLogs *[]protocol.ConsoleLog, mu *sync.Mutex) EventHandler {
	return func(event interface{}) {
		if e, ok := event.(*runtime.EventConsoleAPICalled); ok {
			mu.Lock()
			defer mu.Unlock()

			msg := ""

			if len(e.Args) > 0 {
				// Simply grab the first argument for the MVP
				msg = fmt.Sprintf("%v", e.Args[0].Value)
			}

			*sessionLogs = append(*sessionLogs, protocol.ConsoleLog{
				Level:     string(e.Type),
				Message:   msg,
				Timestamp: time.Now().Unix(),
			})
		}
	}
}
