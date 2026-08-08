package engine

import (
	"encoding/json"
	"testing"
)

// TestWithEngines verifies that the WithEngines constructor option populates
// Options.Engines keyed by context name, so the sandbox can build a hybrid
// session from pre-built engines without going through the platform registry
// (improvement-plan item 31).
func TestWithEngines(t *testing.T) {
	web := NewMemoryEngine(t)
	android := NewMemoryEngine(t)

	opts := Options{}
	WithEngines(map[string]Engine{"web": web, "android": android})(&opts)

	if len(opts.Engines) != 2 {
		t.Fatalf("Engines: want 2 entries, got %d", len(opts.Engines))
	}
	if opts.Engines["web"] != web {
		t.Error("Engines[\"web\"]: want the web engine")
	}
	if opts.Engines["android"] != android {
		t.Error("Engines[\"android\"]: want the android engine")
	}
	if len(opts.Platforms) != 0 {
		t.Errorf("Platforms: want empty (mutually exclusive with Engines), got %v", opts.Platforms)
	}
}

// TestOptionsEnginesJSONExcluded verifies live engines never leak into JSON
// serialization (Options carries no wire format for them), while the Platforms
// hint round-trips for the transports that pass it over the HTTP body.
func TestOptionsEnginesJSONExcluded(t *testing.T) {
	web := NewMemoryEngine(t)
	opts := Options{
		Engines:   map[string]Engine{"web": web},
		Platforms: []string{"web", "android"},
	}

	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)
	if len(s) == 0 || s[0] != '{' {
		t.Fatalf("marshal: want JSON object, got %q", s)
	}
	// Platforms is the only wire hint; Engines must not serialize.
	var decoded Options
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(decoded.Engines) != 0 {
		t.Errorf("Engines: want empty after round-trip (excluded from wire), got %d", len(decoded.Engines))
	}
	if len(decoded.Platforms) != 2 || decoded.Platforms[0] != "web" {
		t.Errorf("Platforms: want [web android] after round-trip, got %v", decoded.Platforms)
	}
}
