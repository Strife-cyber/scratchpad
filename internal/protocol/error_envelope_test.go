package protocol_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"scratchpad/internal/protocol"
)

// =============================================================================
// Contract tests for the typed error envelope (RequestID + Code)
//
// These define Step 1 of improvement-plan item 1: every error from every
// transport carries a stable machine-readable `code` and a `request_id`
// correlation id. A dumb AI learns the `code` vocabulary once and can
// self-correct without parsing English.
// =============================================================================

// TestErrorResponseEnvelopeGolden pins the exact serialized shape of a
// fully-populated error envelope so future schema changes are reviewed.
func TestErrorResponseEnvelopeGolden(t *testing.T) {
	input := protocol.ErrorResponse{
		RequestID: "req-abc123",
		Code:      protocol.CodeSelectorNoMatch,
		Type:      protocol.ErrorLevelAction,
		Message:   "element not found",
		Action:    "click",
		Selector: &protocol.Selector{
			CSS: ".btn",
		},
		Hint: "No element matched css=.btn. Try scroll_into_view then retry.",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	golden := filepath.Join("testdata", "error_response_envelope.golden.json")
	if *update {
		os.WriteFile(golden, data, 0644)
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := string(data); diff != string(expected) {
		t.Errorf("mismatch\ngot:  %s\nwant: %s", string(data), string(expected))
	}
}

// TestErrorResponseNewFieldsOmitted asserts the omitempty discipline: when
// RequestID and Code are empty, they must NOT appear in the JSON at all. This
// keeps the envelope small and keeps legacy goldens stable.
func TestErrorResponseNewFieldsOmitted(t *testing.T) {
	input := protocol.ErrorResponse{
		Type:    protocol.ErrorLevelFatal,
		Message: "test error",
	}

	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, ok := raw["request_id"]; ok {
		t.Error("request_id should be omitted when empty")
	}
	if _, ok := raw["code"]; ok {
		t.Error("code should be omitted when empty")
	}
}

// TestErrorResponseNewFieldsRoundtrip asserts the fields survive a
// marshal/unmarshal cycle intact.
func TestErrorResponseNewFieldsRoundtrip(t *testing.T) {
	original := protocol.ErrorResponse{
		RequestID: "req-xyz789",
		Code:      protocol.CodeTimeout,
		Type:      protocol.ErrorLevelAction,
		Message:   "timed out waiting for selector",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var decoded protocol.ErrorResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	if decoded.RequestID != original.RequestID {
		t.Errorf("RequestID: want %q, got %q", original.RequestID, decoded.RequestID)
	}
	if decoded.Code != original.Code {
		t.Errorf("Code: want %q, got %q", original.Code, decoded.Code)
	}
}
