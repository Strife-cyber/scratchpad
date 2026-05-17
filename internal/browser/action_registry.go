package browser

import (
	"context"
	"sync"

	"scratchpad/internal/protocol"
)

// ActionHandler is a pluggable action implementation.
// It can be used by third-party packages to extend browser actions without
// modifying the core switch statement.
type ActionHandler func(e *ChromeEngine, ctx context.Context, req protocol.ActionRequest) error

var (
	actionRegistryMu sync.RWMutex
	actionRegistry   = map[string]ActionHandler{}
)

// RegisterAction registers an action handler under a stable action name.
// It panics on duplicate registration.
func RegisterAction(name string, h ActionHandler) {
	actionRegistryMu.Lock()
	defer actionRegistryMu.Unlock()
	if _, dup := actionRegistry[name]; dup {
		panic("browser: duplicate action registration: " + name)
	}
	actionRegistry[name] = h
}

func getRegisteredAction(name string) (ActionHandler, bool) {
	actionRegistryMu.RLock()
	defer actionRegistryMu.RUnlock()
	h, ok := actionRegistry[name]
	return h, ok
}

