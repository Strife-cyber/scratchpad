package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"scratchpad/internal/android"
	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
)

// -----------------------------------------------------------------------------
// Android device enumeration tool (improvement-plan item 26)
// -----------------------------------------------------------------------------
//
// Unlike the browser tools, this one runs host-side: it shells out to `adb
// devices -l` on the machine running the bridge (via android.ListDevices), so
// it needs no session connection. The serials it returns are the values to pass
// as session_create's "serial" field when launching an android session.

// AndroidListDevicesArgs is empty — enumeration takes no arguments.
type AndroidListDevicesArgs struct{}

// androidToolDefs returns the Android host-side tool descriptors. RegisterTools
// appends these after the emulation tools.
func (s *Server) androidToolDefs() []toolDef {
	return []toolDef{
		{
			name:        "android_list_devices",
			description: "List every connected Android device/emulator visible to adb (serial, state, status, hint, plus product/model/device attributes when available). The serials returned here are what to pass as session_create's \"serial\" field when creating an android session; non-ready states (offline, unauthorized, no permissions) come with a remediation hint.\n\nExample: android_list_devices with {} returns the connected devices as JSON (e.g. {\"devices\":[{\"serial\":\"emulator-5554\",\"state\":\"device\",\"status\":\"ready\"}]}).",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("android_list_devices", "List every connected Android device/emulator visible to adb.", func(_ context.Context, _ AndroidListDevicesArgs) (*mcp.ToolResponse, error) {
					devices, err := android.ListDevices()
					if err != nil {
						// Surface the typed device_unavailable envelope (same code the
						// HTTP layer returns) so agents see the stable code + hint.
						return typedAndroidError(err), nil
					}
					data, err := json.Marshal(map[string]any{"devices": devices})
					if err != nil {
						return nil, fmt.Errorf("mcp: marshal android devices: %w", err)
					}
					return mcp.NewToolResponse(mcp.NewTextContent(string(data))), nil
				})
			},
		},
	}
}

// typedAndroidError renders a host-side adb failure as the typed
// device_unavailable envelope, mirroring typedSessionNotFound. A non-nil error
// that isn't ErrDeviceUnavailable still maps to the same code since it means
// adb itself is unreachable.
func typedAndroidError(err error) *mcp.ToolResponse {
	resp := protocol.ErrorResponseFromError(protocol.ErrDeviceUnavailable, protocol.ErrorLevelAction)
	if err != nil {
		resp.Message = err.Error()
	}
	data, _ := json.Marshal(resp)
	return mcp.NewToolResponse(mcp.NewTextContent(string(data)))
}
