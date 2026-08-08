package android

import (
	"context"
	"strings"
	"testing"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// matchesTarget
// ---------------------------------------------------------------------------

func TestMatchesTarget_BarePackage(t *testing.T) {
	cases := []struct {
		current, target string
		want            bool
	}{
		{"com.example.app/MainActivity", "com.example.app", true},
		{"com.example.app/.Login", "com.example.app", true},
		{"com.other.app/Main", "com.example.app", false},
		{"com.example.app/Main", "com.example.app/Main", true},
		{"com.example.app/Login", "com.example.app/Main", false},
	}
	for _, tc := range cases {
		if got := matchesTarget(tc.current, tc.target); got != tc.want {
			t.Errorf("matchesTarget(%q, %q) = %v, want %v", tc.current, tc.target, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// listInstalledApps (pm list packages parsing)
// ---------------------------------------------------------------------------

func TestListInstalledApps_ParsesPackageLines(t *testing.T) {
	f := &fakeADB{out: map[string]string{
		"shell pm list packages": "package:com.zebra.app\npackage:com.alpha.app\nbogus line\npackage:com.mid.app\n",
	}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	pkgs, err := e.listInstalledApps()
	if err != nil {
		t.Fatalf("listInstalledApps: %v", err)
	}
	want := []string{"com.alpha.app", "com.mid.app", "com.zebra.app"}
	if len(pkgs) != len(want) {
		t.Fatalf("got %d packages %v, want %d %v", len(pkgs), pkgs, len(want), want)
	}
	for i := range want {
		if pkgs[i] != want[i] {
			t.Errorf("pkgs[%d] = %q, want %q (sorted)", i, pkgs[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// NavigateWithIntent (deep-link extras, item 29)
// ---------------------------------------------------------------------------

func TestNavigateWithIntent_BuildsAmStartCommand(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.NavigateWithIntent("myapp://open?x=1", map[string]string{"b": "two", "a": "one"})
	if err != nil {
		t.Fatalf("NavigateWithIntent: %v", err)
	}

	// Extras are sorted (a before b) so the command is deterministic.
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "shell am start -a android.intent.action.VIEW -d myapp://open?x=1 -e a one -e b two -W") {
			found = true
		}
	}
	if !found {
		t.Errorf("NavigateWithIntent calls = %v, want the sorted -e extras with -W", f.calls)
	}
}

// ---------------------------------------------------------------------------
// ExecuteAction dispatch — app management against the fake adb
// ---------------------------------------------------------------------------

func TestExecuteAction_AppInstall(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action: protocol.ActionAppInstall,
		Path:   "/tmp/app.apk",
	})
	if err != nil {
		t.Fatalf("app_install: %v", err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "install /tmp/app.apk") {
			found = true
		}
	}
	if !found {
		t.Errorf("app_install calls = %v, want adb install /tmp/app.apk", f.calls)
	}
}

func TestExecuteAction_AppInstallRequiresPath(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionAppInstall})
	if err == nil {
		t.Error("app_install without path should error")
	}
}

func TestExecuteAction_AppClearData(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:  protocol.ActionAppClearData,
		Package: "com.example.app",
	})
	if err != nil {
		t.Fatalf("app_clear_data: %v", err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "shell pm clear com.example.app") {
			found = true
		}
	}
	if !found {
		t.Errorf("app_clear_data calls = %v, want pm clear com.example.app", f.calls)
	}
}

func TestExecuteAction_AppForceStop(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:  protocol.ActionAppForceStop,
		Package: "com.example.app",
	})
	if err != nil {
		t.Fatalf("app_force_stop: %v", err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "shell am force-stop com.example.app") {
			found = true
		}
	}
	if !found {
		t.Errorf("app_force_stop calls = %v, want am force-stop com.example.app", f.calls)
	}
}

func TestExecuteAction_AppList_SurfacesMetadata(t *testing.T) {
	f := &fakeADB{out: map[string]string{
		"shell pm list packages": "package:com.one.app\npackage:com.two.app\n",
	}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionAppList})
	if err != nil {
		t.Fatalf("app_list: %v", err)
	}
	if e.lastActionResult == nil || !e.lastActionResult.Success {
		t.Fatal("app_list should succeed")
	}
	count, _ := e.lastActionResult.ActionMetadata["count"].(int)
	if count != 2 {
		t.Errorf("app_list count = %d, want 2", count)
	}
}

func TestExecuteAction_WaitApp_Timeout(t *testing.T) {
	// getCurrentActivity never matches → timeout after ~20ms.
	f := &fakeADB{out: map[string]string{
		"shell dumpsys window displays": "mCurrentFocus=Window{abc u0 com.other/.Main}",
	}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	// Override the default 10s with a short timeout.
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:    protocol.ActionWaitApp,
		Package:   "com.target.app",
		TimeoutMS: 20,
	})
	if err == nil {
		t.Fatal("wait_app should time out when the target never comes to foreground")
	}
	if e.lastActionResult == nil || e.lastActionResult.Success {
		t.Error("wait_app timeout should set Success=false")
	}
}
