package browser

import (
	"context"
	"errors"
	"testing"

	"scratchpad/internal/protocol"
)

// A pointer action with no selector, handle or coordinates is rejected before
// touching the browser instead of clicking the page's top-left corner.
func TestExecuteAction_PointerActionNeedsTarget(t *testing.T) {
	e := &ChromeEngine{}
	for _, action := range []string{protocol.ActionClick, protocol.ActionDoubleClick, protocol.ActionRightClick, protocol.ActionHover} {
		for _, sel := range []*protocol.Selector{nil, {}} {
			err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: action, Selector: sel})
			if !errors.Is(err, protocol.ErrInvalidRequest) {
				t.Errorf("%s with selector %+v: err = %v, want ErrInvalidRequest", action, sel, err)
			}
		}
	}
}
