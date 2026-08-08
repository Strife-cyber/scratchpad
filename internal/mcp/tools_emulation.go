package mcp

import (
	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// Browser emulation tools (improvement-plan item 23)
// ---------------------------------------------------------------------------
//
// These map to the protocol session_update_emulation action, which applies
// user-agent / locale / timezone / color-scheme overrides to the ACTIVE session
// mid-flight (the patch semantics: empty fields leave the current override
// unchanged). Proxy overrides are allocator-level and can only be set at session
// creation via session_create.

// SetUserAgentArgs overrides the browser's user agent.
type SetUserAgentArgs struct {
	UserAgent string `json:"user_agent"`
}

// SetEmulationArgs applies locale/timezone/color-scheme overrides. Empty fields
// leave the current override unchanged.
type SetEmulationArgs struct {
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"` // "light", "dark", or "" = system
}

// emulationToolDefs returns the browser emulation tool descriptors. RegisterTools
// appends these to the browser action tools from tools.go.
func (s *Server) emulationToolDefs() []toolDef {
	return []toolDef{
		actionTool(s, "browser_set_user_agent", "Override the browser user-agent for the active session. A/B testing, mobile pages, and bot-detection dodging all care about the UA string.\n\nExample: browser_set_user_agent with {\"user_agent\":\"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1\"} spoofs an iPhone.", func(a SetUserAgentArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action:    protocol.ActionUpdateEmulation,
				Emulation: &protocol.EmulationOptions{UserAgent: a.UserAgent},
			}
		}),
		actionTool(s, "browser_set_emulation", "Apply locale/timezone/color-scheme emulation to the active session for i18n and geo-dependent testing. Empty fields leave the current override unchanged; color_scheme is \"light\", \"dark\", or \"\" (system).\n\nExample: browser_set_emulation with {\"locale\":\"de-DE\",\"timezone\":\"Europe/Berlin\",\"color_scheme\":\"dark\"} emulates a German dark-mode user.", func(a SetEmulationArgs) protocol.ActionRequest {
			return protocol.ActionRequest{
				Action: protocol.ActionUpdateEmulation,
				Emulation: &protocol.EmulationOptions{
					Locale:      a.Locale,
					Timezone:    a.Timezone,
					ColorScheme: a.ColorScheme,
				},
			}
		}),
	}
}
