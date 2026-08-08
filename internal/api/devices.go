package api

import (
	"encoding/json"
	"net/http"

	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
)

// GetDevices returns the built-in device-emulation presets (improvement-plan
// item 13): name, viewport size, pixel ratio, mobile/touch flags. The same
// presets power session_create's "device" field (and the WebSocket devices
// message), so clients can pick a name here and apply it at session creation.
func (h *handler) GetDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(protocol.DeviceListResponse{Devices: browser.DevicePresets()})
}
