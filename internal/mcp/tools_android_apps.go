package mcp

import (
	"scratchpad/internal/protocol"
)

// -----------------------------------------------------------------------------
// Android app-management & deep-link tools (improvement-plan item 29)
// -----------------------------------------------------------------------------

// AndroidNavigateArgs loads a URL or launches an app on the Android device.
// Intent carries extra key/value pairs for deep links
// (`am start -a android.intent.action.VIEW -d <url> -e k v ... -W`).
type AndroidNavigateArgs struct {
	URL string `json:"url"`
	// Intent holds deep-link extras, e.g. {"token":"abc"}. Ignored when empty.
	Intent map[string]string `json:"intent,omitempty"`
}

// AndroidAppPackageArgs names an app by package name.
type AndroidAppPackageArgs struct {
	Package string `json:"package"`
}

// AndroidAppInstallArgs is the APK to install (local path or http(s) URL).
type AndroidAppInstallArgs struct {
	Path string `json:"path"`
}

// AndroidWaitAppArgs waits for an app to come to the foreground.
type AndroidWaitAppArgs struct {
	Package   string `json:"package"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// AndroidAppListArgs is empty — listing installed apps takes no arguments.
type AndroidAppListArgs struct{}

func (s *Server) androidAppsToolDefs() []toolDef {
	return []toolDef{
		tool(s, "android_navigate", "Load a URL or launch an app on the Android device. A bare package name launches the app; a URL opens the default browser. intent supplies extra key/value pairs for deep-link navigation (turns the launch into `am start -a android.intent.action.VIEW -d <url> -e k v`).\n\nExample: android_navigate with {\"url\":\"myapp://open\",\"intent\":{\"token\":\"abc\"}} deep-links into the app.", func(a AndroidNavigateArgs) protocol.Envelope {
			return protocol.Envelope{
				Type: protocol.MsgTypeNavigate,
				Data: mustJSON(protocol.InitializeRequest{URL: a.URL, Intent: a.Intent}),
			}
		}),

		tool(s, "android_app_launch", "Launch an installed Android app by package name (routes through the engine's navigate path, which starts the package's launcher activity).\n\nExample: android_app_launch with {\"package\":\"com.example.app\"} launches the app.", func(a AndroidAppPackageArgs) protocol.Envelope {
			return protocol.Envelope{
				Type: protocol.MsgTypeNavigate,
				Data: mustJSON(protocol.InitializeRequest{URL: a.Package}),
			}
		}),

		tool(s, "android_app_install", "Install an Android app from a local APK path or an http(s) URL (pushed to the device, then `pm install`).\n\nExample: android_app_install with {\"path\":\"/tmp/app.apk\"} installs the APK.", func(a AndroidAppInstallArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action: protocol.ActionAppInstall,
				Path:   a.Path,
			})
		}),

		tool(s, "android_app_uninstall", "Uninstall an Android app by package name.\n\nExample: android_app_uninstall with {\"package\":\"com.example.app\"} removes the app.", func(a AndroidAppPackageArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:  protocol.ActionAppUninstall,
				Package: a.Package,
			})
		}),

		tool(s, "android_app_clear", "Clear an Android app's data (back to its first-launch state, like Settings > Clear data).\n\nExample: android_app_clear with {\"package\":\"com.example.app\"} wipes the app's data.", func(a AndroidAppPackageArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:  protocol.ActionAppClearData,
				Package: a.Package,
			})
		}),

		tool(s, "android_app_force_stop", "Force-stop an Android app by package name.\n\nExample: android_app_force_stop with {\"package\":\"com.example.app\"} kills the app.", func(a AndroidAppPackageArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:  protocol.ActionAppForceStop,
				Package: a.Package,
			})
		}),

		tool(s, "android_app_list", "List installed packages on the Android device (parsed from `pm list packages`).\n\nExample: android_app_list with {} returns the installed packages.", func(a AndroidAppListArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{Action: protocol.ActionAppList})
		}),

		tool(s, "android_wait_app", "Wait until an Android app package is the foreground activity, polling `dumpsys window` (reuses getCurrentActivity).\n\nExample: android_wait_app with {\"package\":\"com.example.app\",\"timeout_ms\":15000} waits up to 15s for the app to come to the foreground.", func(a AndroidWaitAppArgs) protocol.Envelope {
			return androidActionEnvelope(protocol.ActionRequest{
				Action:    protocol.ActionWaitApp,
				Package:   a.Package,
				TimeoutMS: a.TimeoutMS,
			})
		}),
	}
}
