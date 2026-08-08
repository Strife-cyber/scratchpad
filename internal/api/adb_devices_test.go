package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"scratchpad/internal/android"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

// TestGetAndroidDevices_Success verifies GET /devices/android returns the
// enumerated devices as JSON. The android.ListDevices seam is swapped for a fake
// so no adb is required on the test machine.
func TestGetAndroidDevices_Success(t *testing.T) {
	orig := listAndroidDevices
	listAndroidDevices = func() ([]android.DeviceInfo, error) {
		return []android.DeviceInfo{
			{Serial: "emulator-5554", State: "device", Status: "ready"},
			{Serial: "R5CT1ABC123", State: "offline", Status: "offline"},
		}, nil
	}
	t.Cleanup(func() { listAndroidDevices = orig })

	mgr := sandbox.NewManager()
	req := httptest.NewRequest(http.MethodGet, "/devices/android", nil)
	rec := httptest.NewRecorder()
	NewRouter(mgr).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		Devices []android.DeviceInfo `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	if len(body.Devices) != 2 || body.Devices[0].Serial != "emulator-5554" {
		t.Errorf("devices = %+v, want 2 entries starting with emulator-5554", body.Devices)
	}
}

// TestGetAndroidDevices_Error verifies the handler surfaces adb failures as the
// typed device_unavailable envelope (502) instead of a bare error.
func TestGetAndroidDevices_Error(t *testing.T) {
	orig := listAndroidDevices
	listAndroidDevices = func() ([]android.DeviceInfo, error) {
		return nil, protocol.ErrDeviceUnavailable
	}
	t.Cleanup(func() { listAndroidDevices = orig })

	mgr := sandbox.NewManager()
	req := httptest.NewRequest(http.MethodGet, "/devices/android", nil)
	rec := httptest.NewRecorder()
	NewRouter(mgr).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	var env protocol.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("error body is not an ErrorResponse envelope: %v", err)
	}
	if env.Code != protocol.CodeDeviceUnavailable {
		t.Errorf("code = %q, want %q", env.Code, protocol.CodeDeviceUnavailable)
	}
}

// TestRouter_AndroidDevicesOnlyOnGET ensures the route is GET-only; other
// methods 404.
func TestRouter_AndroidDevicesOnlyOnGET(t *testing.T) {
	mgr := sandbox.NewManager()
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPut} {
		req := httptest.NewRequest(method, "/devices/android", nil)
		rec := httptest.NewRecorder()
		NewRouter(mgr).ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s /devices/android = %d, want 404", method, rec.Code)
		}
	}
}
