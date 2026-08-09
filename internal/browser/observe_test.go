package browser

import (
	"testing"

	"scratchpad/internal/protocol"
)

// TestSystemDocumentStatus covers the pure mapping from PageInfo.LoadStatus
// into SystemState.DocumentStatus: the truthful readyState when available, and
// the historical "interactive" fallback when page info was not captured.
func TestSystemDocumentStatus(t *testing.T) {
	cases := []struct {
		name string
		pi   *protocol.PageInfo
		want string
	}{
		{"nil page info falls back to interactive", nil, "interactive"},
		{"empty load status falls back to interactive", &protocol.PageInfo{LoadStatus: ""}, "interactive"},
		{"whitespace load status falls back to interactive", &protocol.PageInfo{LoadStatus: "   "}, "interactive"},
		{"loading is preserved", &protocol.PageInfo{LoadStatus: "loading"}, "loading"},
		{"interactive is preserved", &protocol.PageInfo{LoadStatus: "interactive"}, "interactive"},
		{"complete is preserved", &protocol.PageInfo{LoadStatus: "complete"}, "complete"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := systemDocumentStatus(tc.pi); got != tc.want {
				t.Errorf("systemDocumentStatus() = %q, want %q", got, tc.want)
			}
		})
	}
}
