package android

import (
	"context"
	"strings"
	"testing"
	"time"

	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// resolveHoldMS (item 28 long-press window)
// ---------------------------------------------------------------------------

func TestResolveHoldMS_DefaultsTo600ms(t *testing.T) {
	if got := resolveHoldMS(0); got != androidHoldDefault {
		t.Errorf("resolveHoldMS(0) = %v, want %v", got, androidHoldDefault)
	}
}

func TestResolveHoldMS_ClampsToWindow(t *testing.T) {
	cases := []struct {
		in   int
		want time.Duration
	}{
		{100, androidLongPressMin},    // below 500ms clamps up
		{600, 600 * time.Millisecond}, // in range
		{1500, 1500 * time.Millisecond},
		{5000, androidLongPressMax}, // above 2s clamps down
	}
	for _, tc := range cases {
		if got := resolveHoldMS(tc.in); got != tc.want {
			t.Errorf("resolveHoldMS(%d) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// swipeEndpoints / swipeEndpointsAt (pure arithmetic)
// ---------------------------------------------------------------------------

func TestSwipeEndpoints_DirectionMaps(t *testing.T) {
	vp := protocol.Viewport{Width: 1000, Height: 2000}
	cases := []struct {
		dir   string
		wantX int
		wantY int
	}{
		{"up", 500, 0},
		{"down", 500, 2000},
		{"left", 0, 1000},
		{"right", 1000, 1000},
		{"", 500, 0},      // default is up
		{"bogus", 500, 0}, // unknown degrades to up
	}
	for _, tc := range cases {
		sx, sy, ex, ey := swipeEndpoints(vp, tc.dir, 100)
		if sx != 500 || sy != 1000 {
			t.Errorf("swipeEndpoints(%q) start = (%d,%d), want (500,1000)", tc.dir, sx, sy)
		}
		if ex != tc.wantX || ey != tc.wantY {
			t.Errorf("swipeEndpoints(%q) end = (%d,%d), want (%d,%d)", tc.dir, ex, ey, tc.wantX, tc.wantY)
		}
	}
}

func TestSwipeEndpoints_DistancePercent(t *testing.T) {
	vp := protocol.Viewport{Width: 1000, Height: 2000}
	// 60% of the height (2000) = 1200px upward from centre y=1000 → 0 (clamped).
	_, _, ex, ey := swipeEndpoints(vp, "up", 60)
	if ex != 500 || ey != 0 {
		t.Errorf("swipe up 60%% end = (%d,%d), want (500,0)", ex, ey)
	}
	// 25% of the width (1000) = 250px left from centre x=500 → 250.
	_, _, ex, ey = swipeEndpoints(vp, "left", 25)
	if ex != 250 || ey != 1000 {
		t.Errorf("swipe left 25%% end = (%d,%d), want (250,1000)", ex, ey)
	}
}

func TestSwipeEndpoints_ClampsToViewport(t *testing.T) {
	vp := protocol.Viewport{Width: 1000, Height: 2000}
	_, _, ex, ey := swipeEndpoints(vp, "up", 400) // 400% clamped to 100%
	if ey < 0 {
		t.Errorf("swipe end y %d went negative", ey)
	}
	_, _, ex, ey = swipeEndpoints(vp, "right", 300)
	if ex > vp.Width {
		t.Errorf("swipe end x %d exceeded viewport width", ex)
	}
}

func TestSwipeEndpointsAt_StartPointHonored(t *testing.T) {
	vp := protocol.Viewport{Width: 1000, Height: 2000}
	// From a selector-origin at (200, 800), swipe down 50% of height (1000) → y=1800.
	sx, sy, ex, ey := swipeEndpointsAt(200, 800, vp, "down", 50)
	if sx != 200 || sy != 800 {
		t.Errorf("start = (%d,%d), want (200,800)", sx, sy)
	}
	if ex != 200 || ey != 1800 {
		t.Errorf("end = (%d,%d), want (200,1800)", ex, ey)
	}
}

// ---------------------------------------------------------------------------
// clampPercent
// ---------------------------------------------------------------------------

func TestClampPercent(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 50}, {50, 50}, {60, 60}, {120, 100}, {-10, 50},
	}
	for _, tc := range cases {
		if got := clampPercent(tc.in); got != tc.want {
			t.Errorf("clampPercent(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// ExecuteAction — gesture dispatch against a fake adb (no device needed)
// ---------------------------------------------------------------------------

func TestExecuteAction_LongPress_Dispatch(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action: protocol.ActionLongPress,
		X:      100, Y: 200, HoldMS: 1000,
	})
	if err != nil {
		t.Fatalf("long_press: %v", err)
	}

	// Expect: input swipe 100 200 100 200 1000
	got := f.calls
	found := false
	for _, c := range got {
		if strings.Contains(c, "input swipe 100 200 100 200 1000") {
			found = true
		}
	}
	if !found {
		t.Errorf("long_press adb calls = %v, want an input swipe hold of 1000ms", got)
	}
}

func TestExecuteAction_Swipe_DirectionDispatch(t *testing.T) {
	// wm size gives 1080x2400 → centre (540, 1200); up 60% of 2400 = 1440 → y=0.
	f := &fakeADB{out: map[string]string{"shell wm size": "Physical size: 1080x2400\n"}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:          protocol.ActionSwipe,
		Direction:       "up",
		DistancePercent: 60,
	})
	if err != nil {
		t.Fatalf("swipe: %v", err)
	}

	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "input swipe 540 1200 540 0") {
			found = true
		}
	}
	if !found {
		t.Errorf("swipe adb calls = %v, want input swipe 540 1200 540 0", f.calls)
	}
}

func TestExecuteAction_Pinch_MotioneventSequence(t *testing.T) {
	f := &fakeADB{out: map[string]string{"shell wm size": "Physical size: 1080x2400\n"}}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:    protocol.ActionPinch,
		PinchMode: "out",
	})
	if err != nil {
		t.Fatalf("pinch: %v", err)
	}

	seen := map[string]bool{}
	for _, c := range f.calls {
		seen[c] = true
	}
	if !seen["shell input motionevent DOWN 540 1200"] {
		t.Errorf("pinch missing DOWN; calls=%v", f.calls)
	}
	if !seen["shell input motionevent MOVE 1080 2400"] {
		t.Errorf("pinch missing MOVE (50%% of 1080x2400); calls=%v", f.calls)
	}
	if !seen["shell input motionevent UP 1080 2400"] {
		t.Errorf("pinch missing UP; calls=%v", f.calls)
	}
}

func TestExecuteAction_Key_NamedKey(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action: protocol.ActionKey,
		Key:    "home",
	})
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "input keyevent 3") { // KEYCODE_HOME
			found = true
		}
	}
	if !found {
		t.Errorf("key home calls = %v, want input keyevent 3", f.calls)
	}
}

func TestExecuteAction_Key_UnknownKey(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", &fakeADB{}))
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionKey, Key: "not_a_key"})
	if err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

func TestExecuteAction_GoHome(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionGoHome})
	if err != nil {
		t.Fatalf("go_home: %v", err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "input keyevent 3") {
			found = true
		}
	}
	if !found {
		t.Errorf("go_home calls = %v, want input keyevent 3", f.calls)
	}
}

func TestExecuteAction_OpenNotifications(t *testing.T) {
	f := &fakeADB{}
	e := newAndroidEngineWithConn(newADBConn("", f))

	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionOpenNotifications})
	if err != nil {
		t.Fatalf("open_notifications: %v", err)
	}
	// The primary path is cmd statusbar expand-notifications; the keyevent 83
	// fallback is only used when that fails.
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "cmd statusbar expand-notifications") {
			found = true
		}
	}
	if !found {
		t.Errorf("open_notifications calls = %v, want cmd statusbar expand-notifications", f.calls)
	}
}
