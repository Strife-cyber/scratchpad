package android

import (
	"errors"
	"testing"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// parseDevices
// ---------------------------------------------------------------------------

func TestParseDevices(t *testing.T) {
	out := `List of devices attached
emulator-5554          device product:sdk_gphone64_x86_64 model:sdk_gphone64_x86_64 device:emu64xa transport_id:1
R5CT1ABC123           offline usb:1-1 product:shiba model:Pixel_8 device:shiba transport_id:2
`

	devs := parseDevices(out)
	if len(devs) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devs))
	}

	em := devs[0]
	if em.Serial != "emulator-5554" || em.State != "device" {
		t.Errorf("emulator: serial/state = %q/%q", em.Serial, em.State)
	}
	if em.Status != "ready" {
		t.Errorf("emulator status = %q, want ready", em.Status)
	}
	if em.Product != "sdk_gphone64_x86_64" || em.Model != "sdk_gphone64_x86_64" || em.Device != "emu64xa" {
		t.Errorf("emulator -l props = %q/%q/%q", em.Product, em.Model, em.Device)
	}
	if em.TransportID != "1" {
		t.Errorf("emulator transport_id = %q, want 1", em.TransportID)
	}

	off := devs[1]
	if off.Serial != "R5CT1ABC123" || off.State != "offline" {
		t.Errorf("offline device: serial/state = %q/%q", off.Serial, off.State)
	}
	if off.Status != "offline" {
		t.Errorf("offline status = %q, want offline", off.Status)
	}
	if off.Hint == "" {
		t.Error("offline device should carry a remediation hint")
	}
	if off.USB != "1-1" {
		t.Errorf("offline device usb = %q, want 1-1", off.USB)
	}
}

func TestParseDevices_IgnoresHeaderAndServerLines(t *testing.T) {
	out := `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
emulator-5554          device product:sdk_gphone64_x86_64 model:sdk_gphone64_x86_64 device:emu64xa transport_id:1
`
	devs := parseDevices(out)
	if len(devs) != 1 {
		t.Fatalf("expected 1 device, got %d (%v)", len(devs), devs)
	}
	if devs[0].Serial != "emulator-5554" {
		t.Errorf("serial = %q, want emulator-5554", devs[0].Serial)
	}
}

func TestParseDevices_Empty(t *testing.T) {
	if devs := parseDevices(""); len(devs) != 0 {
		t.Errorf("expected 0 devices for empty output, got %d", len(devs))
	}
}

// ---------------------------------------------------------------------------
// describeDeviceState
// ---------------------------------------------------------------------------

func TestDescribeDeviceState(t *testing.T) {
	cases := map[string]string{
		"device":         "ready",
		"unauthorized":   "unauthorized",
		"offline":        "offline",
		"no permissions": "no_permissions",
		"connecting":     "connecting",
		"authorizing":    "authorizing",
		"bootloader":     "bootloader",
		"recovery":       "recovery",
	}
	for state, wantStatus := range cases {
		status, hint := describeDeviceState(state)
		if status != wantStatus {
			t.Errorf("describeDeviceState(%q).status = %q, want %q", state, status, wantStatus)
		}
		if hint == "" {
			t.Errorf("describeDeviceState(%q) returned an empty hint", state)
		}
	}
}

func TestDescribeDeviceState_UnknownAndEmpty(t *testing.T) {
	if status, _ := describeDeviceState(""); status != "unknown" {
		t.Errorf("empty state status = %q, want unknown", status)
	}
	if status, _ := describeDeviceState("weird-state"); status != "weird-state" {
		t.Errorf("unknown state status = %q, want passthrough", status)
	}
}

// ---------------------------------------------------------------------------
// validateDevice (session-creation device check)
// ---------------------------------------------------------------------------

func TestValidateDevice_Ready(t *testing.T) {
	conn := newADBConn("emulator-5554", &fakeADB{out: map[string]string{
		"devices -l": "List of devices attached\nemulator-5554 device model:Pixel_8\n",
	}})
	if err := validateDevice(conn); err != nil {
		t.Errorf("validateDevice(ready) = %v, want nil", err)
	}
}

func TestValidateDevice_SerialNotFound(t *testing.T) {
	conn := newADBConn("nope", &fakeADB{out: map[string]string{
		"devices -l": "List of devices attached\nemulator-5554 device\n",
	}})
	err := validateDevice(conn)
	if !errors.Is(err, protocol.ErrDeviceUnavailable) {
		t.Fatalf("validateDevice(missing) = %v, want ErrDeviceUnavailable", err)
	}
}

func TestValidateDevice_OfflineRejected(t *testing.T) {
	conn := newADBConn("emulator-5554", &fakeADB{out: map[string]string{
		"devices -l": "List of devices attached\nemulator-5554 offline\n",
	}})
	err := validateDevice(conn)
	if !errors.Is(err, protocol.ErrDeviceUnavailable) {
		t.Fatalf("validateDevice(offline) = %v, want ErrDeviceUnavailable", err)
	}
}

func TestValidateDevice_ADBFailure(t *testing.T) {
	conn := newADBConn("emulator-5554", &fakeADB{out: map[string]string{
		"devices -l": "error: no devices/emulators found",
	}})
	// "no devices/emulators found" parses to zero devices → serial not found.
	err := validateDevice(conn)
	if !errors.Is(err, protocol.ErrDeviceUnavailable) {
		t.Fatalf("validateDevice(no devices) = %v, want ErrDeviceUnavailable", err)
	}
}
