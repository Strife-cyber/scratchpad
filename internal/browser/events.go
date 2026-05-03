package browser

import (
	"fmt"
	"sync"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"

	"github.com/chromedp/cdproto/runtime"
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
