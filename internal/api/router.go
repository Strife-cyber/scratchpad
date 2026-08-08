package api

import (
	"net/http"
	"strings"

	"scratchpad/internal/sandbox"
)

// NewRouter builds the Phase 0 HTTP API for session lifecycle and basic actions.
//
// The server mounts this handler under `/api/v1/` with `http.StripPrefix`,
// so r.URL.Path inside handlers typically starts with `/sessions...`.
func NewRouter(mgr *sandbox.Manager) http.Handler {
	h := &handler{mgr: mgr}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/") // e.g. "sessions/123/actions"
		if path == "" {
			http.NotFound(w, r)
			return
		}

		parts := strings.Split(path, "/")

		// Device-emulation presets (item 13).
		if len(parts) == 1 && parts[0] == "devices" && r.Method == http.MethodGet {
			h.GetDevices(w, r)
			return
		}

		// Connected Android devices/emulators via `adb devices -l` (item 26).
		// Distinct from the browser device-emulation presets at /devices.
		if len(parts) == 2 && parts[0] == "devices" && parts[1] == "android" && r.Method == http.MethodGet {
			h.GetAndroidDevices(w, r)
			return
		}

		if len(parts) >= 1 && parts[0] == "sessions" {
			switch {
			case len(parts) == 1 && r.Method == http.MethodPost:
				h.CreateSession(w, r)
				return
			case len(parts) == 2 && r.Method == http.MethodDelete:
				h.DeleteSession(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodGet && parts[2] == "har":
				h.GetHAR(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodGet && parts[2] == "dom":
				h.GetDOM(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodGet && parts[2] == "console":
				h.GetConsole(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodGet && parts[2] == "timeline":
				h.GetTimeline(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodGet && parts[2] == "trace":
				h.GetTrace(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodGet && parts[2] == "screenshot":
				h.GetScreenshot(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodGet && parts[2] == "artifacts":
				h.GetArtifact(w, r, parts[1], parts[3])
				return
			case len(parts) == 5 && r.Method == http.MethodPost && parts[2] == "screenshot" && parts[3] == "diff":
				h.PostScreenshotDiff(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodPost && parts[2] == "recording" && parts[3] == "start":
				h.PostRecordingStart(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodPost && parts[2] == "recording" && parts[3] == "stop":
				h.PostRecordingStop(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodPost && parts[2] == "tracing" && parts[3] == "start":
				h.PostTracingStart(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodPost && parts[2] == "tracing" && parts[3] == "stop":
				h.PostTracingStop(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "actions":
				h.Actions(w, r, parts[1])
				return
			case len(parts) == 3 && r.Method == http.MethodPost && parts[2] == "network":
				h.PostNetwork(w, r, parts[1])
				return
			case len(parts) == 4 && r.Method == http.MethodGet && parts[2] == "network" && parts[3] == "requests":
				h.GetNetworkRequests(w, r, parts[1])
				return
			}
		}

		http.NotFound(w, r)
	})
}
