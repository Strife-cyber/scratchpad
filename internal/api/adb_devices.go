package api

import (
	"encoding/json"
	"net/http"

	"scratchpad/internal/android"
)

// listAndroidDevices is an injectable seam over android.ListDevices so the
// handler's HTTP behavior is unit-testable without adb on the machine.
var listAndroidDevices = android.ListDevices

// GetAndroidDevices lists every Android device/emulator visible to adb via
// `adb devices -l` (improvement-plan item 26): serial, state, product/model, and
// a friendly status + hint for non-ready states. Served at
// GET /api/v1/devices/android — distinct from the browser device-emulation
// presets at GET /api/v1/devices (item 13). The serials returned here are the
// values to pass as the "serial" field of POST /api/v1/sessions for platform
// "android".
func (h *handler) GetAndroidDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := listAndroidDevices()
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"devices": devices})
}
