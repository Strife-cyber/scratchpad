package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Android screen-recording & logcat tools (improvement-plan item 30)
// -----------------------------------------------------------------------------

// AndroidScreenRecordStartArgs configures a device screen recording.
type AndroidScreenRecordStartArgs struct {
	// Dir is the local output directory for the pulled video. Empty resolves to
	// SCRATCHPAD_VIDEO_DIR, then "videos".
	Dir string `json:"dir,omitempty"`
	// DurationSec caps the recording (--time-limit). 0 uses the engine default
	// (180s).
	DurationSec int `json:"duration_sec,omitempty"`
}

// AndroidScreenRecordStopArgs is empty — stopping takes no arguments (the
// engine pulls the active recording).
type AndroidScreenRecordStopArgs struct{}

// AndroidLogcatStartArgs configures a logcat capture.
type AndroidLogcatStartArgs struct {
	// Path is the local output file. Empty resolves to
	// traces/<session>/logcat.txt.
	Path string `json:"path,omitempty"`
	// Package filters the capture to that app's pid (resolved via `pidof`).
	Package string `json:"package,omitempty"`
	// Filter is the logcat priority filter, e.g. "*:E". Empty uses "*:V".
	Filter string `json:"filter,omitempty"`
	// Clear clears the logcat buffer before capture starts (-c).
	Clear bool `json:"clear,omitempty"`
}

// AndroidLogcatStopArgs is empty — stopping takes no arguments.
type AndroidLogcatStopArgs struct{}

func (s *Server) androidRecordingToolDefs() []toolDef {
	return []toolDef{
		tool(s, "android_screenrecord_start", "Start screen recording on the Android device (`screenrecord`). dir sets the local output directory for the pulled video (default SCRATCHPAD_VIDEO_DIR, then videos); duration_sec caps the clip (0 = engine default 180s).\n\nExample: android_screenrecord_start with {\"dir\":\"/tmp/videos\",\"duration_sec\":60} starts a 60s recording.", func(a AndroidScreenRecordStartArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action: protocol.ActionStartRecording,
				Record: &protocol.RecordOptions{Dir: a.Dir, DurationSec: a.DurationSec},
			})
		}),

		tool(s, "android_screenrecord_stop", "Stop the active Android screen recording and pull the video locally. Returns the local path and byte size.\n\nExample: android_screenrecord_stop with {} stops the recording and saves the video.", func(a AndroidScreenRecordStopArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionStopRecording})
		}),

		tool(s, "android_logcat", "Start capturing Android logcat to traces/<session>/logcat.txt. package filters the capture to that app's pid; filter sets the logcat priority filter (e.g. \"*:E\"; empty = \"*:V\"); clear empties the buffer first; path overrides the output file. Stop with android_logcat_stop to pull the capture and get its tail.\n\nExample: android_logcat with {\"package\":\"com.example.app\",\"filter\":\"*:E\"} captures errors for the app.", func(a AndroidLogcatStartArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action: protocol.ActionStartLogcat,
				Record: &protocol.RecordOptions{Path: a.Path, Package: a.Package, Filter: a.Filter, Clear: a.Clear},
			})
		}),

		tool(s, "android_logcat_stop", "Stop the active Android logcat capture, pull it locally, and return its path plus the last few lines as a tail.\n\nExample: android_logcat_stop with {} stops the capture and returns the logcat tail.", func(a AndroidLogcatStopArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionStopLogcat})
		}),
	}
}
