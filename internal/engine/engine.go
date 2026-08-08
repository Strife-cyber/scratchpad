// Package engine defines the Engine interface that every platform driver must
// satisfy, along with the Kind registry used by the factory.
//
// Concrete drivers (browser, android) register themselves via init() so that
// the sandbox can instantiate them without importing the driver packages directly.
package engine

import (
	"context"
	"fmt"
	"scratchpad/internal/protocol"
)

// Kind identifies which platform driver to instantiate.
type Kind string

const (
	KindChrome  Kind = "chrome"
	KindAndroid Kind = "android"
)

// EventHandler processes raw platform events (CDP events for Chrome, ADB
// events for Android, etc.). Handlers must be non-blocking.
type EventHandler func(event any)

// Engine is the contract every platform driver must satisfy.
// Implementations: browser.ChromeEngine, android.AndroidEngine.
type Engine interface {
	// Navigate loads the given URL inside the engine's context.
	Navigate(url string) error

	// Observe captures the current visual + structural state of the page/screen
	// and returns it as a self-contained ObservationResponse.
	//
	// The optional requests select which parts of the observation to capture
	// (tree, screenshot, tabs, console, page info) and apply budgets (max_nodes,
	// max_depth, max_screenshot_bytes). Passing no requests is equivalent to a
	// full observation. When multiple requests are given they are merged, with
	// later requests overriding earlier ones; a nil entry is ignored.
	Observe(reqs ...*protocol.ObserveRequest) (*protocol.ObservationResponse, error)

	// ExecuteAction dispatches a single user-style action (click, type, scroll, wait).
	// The provided ctx carries the action's cancellation signal: cancelling it must
	// abort the in-flight work (chromedp calls, adb polls, sleep loops) and return a
	// clean, non-fatal result so callers can branch on the cancellation.
	ExecuteAction(ctx context.Context, req protocol.ActionRequest) error

	// AddListener registers a handler that receives raw platform events.
	// Handlers are invoked synchronously inside the event loop; keep them fast.
	AddListener(handler EventHandler)

	// Close shuts down the engine and releases all associated resources.
	Close()
}

// ---------------------------------------------------------------------------
// Registration-based factory (options-aware)
// ---------------------------------------------------------------------------

// driverRegistry maps each Kind to its options-aware constructor.
var driverRegistry = map[Kind]func(Options) (Engine, error){}

// Register makes a driver available under the given Kind.
// It panics on duplicate registration and must be called from an init()
// function inside the driver package.
func Register(kind Kind, fn func(Options) (Engine, error)) {
	if _, dup := driverRegistry[kind]; dup {
		panic(fmt.Sprintf("engine: duplicate registration for kind %q", kind))
	}
	driverRegistry[kind] = fn
}

func newFromRegistry(kind Kind, opts Options) (Engine, error) {
	fn, ok := driverRegistry[kind]
	if !ok {
		return nil, fmt.Errorf("engine: no driver registered for kind %q (did you blank-import the driver package?)", kind)
	}
	return fn(opts)
}
