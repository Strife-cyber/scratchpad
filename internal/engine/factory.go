package engine

import (
	"os"
	"strings"
)

// Options configures how engines should be created.
type Options struct {
	// Headless controls whether the Chrome browser runs headless.
	// When nil, the default is resolved from SCRATCHPAD_HEADLESS.
	Headless *bool `json:"headless,omitempty"`
}

// New creates a new Engine of the requested kind.
//
// Phase 0 semantics:
// - For Chrome, Headless defaults to SCRATCHPAD_HEADLESS (when unset).
// - For other kinds, options are currently ignored.
func New(kind Kind, opts Options) (Engine, error) {
	resolved := opts

	// Resolve Chrome headless default here so the Chrome driver doesn't need
	// to read environment variables.
	if kind == KindChrome {
		headless := true
		if opts.Headless != nil {
			headless = *opts.Headless
		} else {
			// SCRATCHPAD_HEADLESS=false -> visible (headless=false)
			headless = !strings.EqualFold(os.Getenv("SCRATCHPAD_HEADLESS"), "false")
		}
		resolved.Headless = &headless
	}

	return newFromRegistry(kind, resolved)
}

