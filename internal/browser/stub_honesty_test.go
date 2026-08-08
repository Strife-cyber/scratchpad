package browser

import (
	"context"
	"errors"
	"testing"

	"scratchpad/internal/protocol"
)

// TestStubActions_ReturnTypedUnsupported verifies the actions that remain stubs
// this wave fail loudly with the typed ErrUnsupported sentinel (which the error
// catalog maps to code "unsupported" + a hint) instead of reporting fake success.
func TestStubActions_ReturnTypedUnsupported(t *testing.T) {
	e := &ChromeEngine{ctx: context.Background()}

	cases := []protocol.ActionRequest{
		{Action: protocol.ActionPressKeyCombo, KeyChord: protocol.KeyChord{Key: "s", Ctrl: true}},
		{Action: protocol.ActionMockNetworkResp},
	}
	for _, req := range cases {
		t.Run(req.Action, func(t *testing.T) {
			err := e.ExecuteAction(context.Background(), req)
			if err == nil {
				t.Fatalf("%s returned nil error, want typed ErrUnsupported", req.Action)
			}
			if !errors.Is(err, protocol.ErrUnsupported) {
				t.Errorf("%s error = %v, want errors.Is(err, protocol.ErrUnsupported)", req.Action, err)
			}
			if got := protocol.Classify(err).Code; got != protocol.CodeUnsupported {
				t.Errorf("%s classified code = %q, want %q", req.Action, got, protocol.CodeUnsupported)
			}
		})
	}
}
