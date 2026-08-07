package browser

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestJSResultMetadata(t *testing.T) {
	if got := jsResultMetadata(nil); got != nil {
		t.Errorf("jsResultMetadata(nil) = %v, want nil", got)
	}

	cases := []struct {
		name   string
		result any
		want   any
	}{
		{"string", "hello", "hello"},
		{"number", float64(42), float64(42)},
		{"bool", true, true},
		{"slice", []any{"a", "b"}, []any{"a", "b"}},
		{"map", map[string]any{"k": "v"}, map[string]any{"k": "v"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsResultMetadata(tc.result)
			if got == nil {
				t.Fatalf("jsResultMetadata(%v) returned nil map", tc.result)
			}
			if !reflect.DeepEqual(got["result"], tc.want) {
				t.Errorf("got result=%v, want %v", got["result"], tc.want)
			}
		})
	}
}

// TestJSResultMetadataSerializes ensures the metadata marshals to JSON cleanly
// (numbers as JSON numbers, not strings), which is what the MCP layer surfaces
// for browser_eval / execute_js.
func TestJSResultMetadataSerializes(t *testing.T) {
	meta := jsResultMetadata(map[string]any{"price": 19.99, "ok": true})
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if !strings.Contains(string(data), `"price":19.99`) {
		t.Errorf("expected price as a JSON number, got %s", data)
	}
	if !strings.Contains(string(data), `"ok":true`) {
		t.Errorf("expected ok as a JSON boolean, got %s", data)
	}
}
