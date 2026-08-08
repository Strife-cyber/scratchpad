package android

import (
	"fmt"
	"strings"

	"scratchpad/internal/protocol"
)

// This file adds device enumeration and selection (improvement-plan item 26).
// `adb devices -l` is host-side (not bound to one serial), so these functions
// take an explicit runner and default to the real one in the exported surface.

// DeviceInfo describes one connected Android device/emulator as reported by
// `adb devices -l`. Status is a friendly one-word summary of State and Hint is a
// concrete remediation for non-ready states, so agents (and humans) know what to
// do with an offline/unauthorized device.
type DeviceInfo struct {
	Serial string `json:"serial"`
	State  string `json:"state"`

	// Status/Hint are derived from State via describeDeviceState.
	Status string `json:"status"`
	Hint   string `json:"hint"`

	// -l attributes.
	Product     string `json:"product,omitempty"`
	Model       string `json:"model,omitempty"`
	Device      string `json:"device,omitempty"`
	TransportID string `json:"transport_id,omitempty"`
	USB         string `json:"usb,omitempty"`
}

// ListDevices enumerates every connected Android device/emulator via
// `adb devices -l`. Exposed as GET /api/v1/devices/android and the MCP
// android_list_devices tool.
func ListDevices() ([]DeviceInfo, error) {
	return listDevices(realRunner{})
}

// listDevices runs `adb devices -l` through the given runner and parses it. The
// runner is passed in so session-creation validation and tests share the code.
func listDevices(runner commandRunner) ([]DeviceInfo, error) {
	out, err := runner.run("", "devices", "-l")
	if err != nil {
		// adb missing or the server down means no device is reachable; surface the
		// typed device_unavailable so transports (HTTP/MCP/WS) classify it cleanly.
		return nil, fmt.Errorf("%w: adb devices failed: %v", protocol.ErrDeviceUnavailable, err)
	}
	return parseDevices(out), nil
}

// parseDevices parses `adb devices -l` output. Each data line is
// "<serial> <state> [product:x model:y device:z transport_id:n usb:w]"; the
// header and adb's "* daemon started" lines are skipped.
func parseDevices(output string) []DeviceInfo {
	var devices []DeviceInfo
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := DeviceInfo{Serial: fields[0], State: fields[1]}
		for _, prop := range fields[2:] {
			kv := strings.SplitN(prop, ":", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "product":
				d.Product = kv[1]
			case "model":
				d.Model = kv[1]
			case "device":
				d.Device = kv[1]
			case "transport_id":
				d.TransportID = kv[1]
			case "usb":
				d.USB = kv[1]
			}
		}
		d.Status, d.Hint = describeDeviceState(d.State)
		devices = append(devices, d)
	}
	return devices
}

// describeDeviceState maps adb's device-state vocabulary onto a friendly status
// word and a concrete remediation hint (improvement-plan item 26). Unknown
// states keep the raw state string with a generic hint.
func describeDeviceState(state string) (status, hint string) {
	switch strings.TrimSpace(state) {
	case "device":
		return "ready", "The device is online and usable."
	case "unauthorized":
		return "unauthorized", "Approve the USB debugging prompt on the device (check 'Allow USB debugging'), then re-run 'adb reconnect'."
	case "offline":
		return "offline", "The device is detected but unresponsive. Reconnect the USB cable, restart the emulator, or run 'adb kill-server && adb start-server'."
	case "no permissions":
		return "no_permissions", "adb cannot access the device. On Linux add a udev rule for the vendor; on other platforms reinstall the device driver."
	case "connecting":
		return "connecting", "The device is still connecting. Wait a moment and retry."
	case "authorizing":
		return "authorizing", "The device has not accepted the RSA key. Accept the debugging prompt on the device."
	case "bootloader":
		return "bootloader", "The device is in bootloader/fastboot mode and cannot run Android. Boot it to the OS first."
	case "recovery":
		return "recovery", "The device is in recovery mode and cannot run apps. Reboot it to the OS first."
	default:
		if state == "" {
			return "unknown", "The device is in an unexpected state. Reconnect it and check 'adb devices'."
		}
		return state, "Unexpected device state. Reconnect the device and check 'adb devices'."
	}
}

// validateDevice checks that the session's serial is connected and usable,
// returning protocol.ErrDeviceUnavailable with a concrete hint otherwise. Called
// at Android session creation so a bad serial fails fast instead of sending the
// agent into a session that cannot reach its device.
func validateDevice(conn *adbConn) error {
	devices, err := listDevices(conn.runner)
	if err != nil {
		return fmt.Errorf("%w: could not query adb devices: %v", protocol.ErrDeviceUnavailable, err)
	}
	for _, d := range devices {
		if d.Serial == conn.serial {
			if d.State == "device" {
				return nil
			}
			return fmt.Errorf("%w: device %s is %s — %s", protocol.ErrDeviceUnavailable, conn.serial, d.Status, d.Hint)
		}
	}
	return fmt.Errorf("%w: no Android device with serial %q is connected (see 'adb devices -l')", protocol.ErrDeviceUnavailable, conn.serial)
}
