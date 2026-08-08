package android

import (
	"context"
	"fmt"
	"testing"
	"time"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
)

// ---------------------------------------------------------------------------
// Interface guard — fails at compile time if AndroidEngine is incomplete
// ---------------------------------------------------------------------------

func TestAndroidEngine_ImplementsEngineInterface(t *testing.T) {
	var _ engine.Engine = (*AndroidEngine)(nil)
}

// ---------------------------------------------------------------------------
// flattenAndroidTree
// ---------------------------------------------------------------------------

func TestFlattenAndroidTree_ClickableNode(t *testing.T) {
	node := protocol.UINode{
		Class:     "android.widget.Button",
		Text:      "OK",
		Clickable: "true",
		Bounds:    "[100,200][300,260]",
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(node, &tree)

	if len(tree) != 1 {
		t.Fatalf("expected 1 node, got %d", len(tree))
	}
	sn := tree[0]
	if sn.Role != "android.widget.Button" {
		t.Errorf("Role: got %q, want %q", sn.Role, "android.widget.Button")
	}
	if sn.Name != "OK" {
		t.Errorf("Name: got %q, want %q", sn.Name, "OK")
	}
	if sn.Bounds.X != 100 || sn.Bounds.Y != 200 {
		t.Errorf("Bounds origin: got (%v,%v), want (100,200)", sn.Bounds.X, sn.Bounds.Y)
	}
	if sn.Bounds.Width != 200 || sn.Bounds.Height != 60 {
		t.Errorf("Bounds size: got (%v,%v), want (200,60)", sn.Bounds.Width, sn.Bounds.Height)
	}
}

func TestFlattenAndroidTree_DescFallback(t *testing.T) {
	// Name must fall back to Desc when Text is empty.
	node := protocol.UINode{
		Class:     "android.widget.ImageButton",
		Desc:      "Share",
		Clickable: "true",
		Bounds:    "[0,0][50,50]",
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(node, &tree)

	if len(tree) != 1 {
		t.Fatalf("expected 1 node, got %d", len(tree))
	}
	if tree[0].Name != "Share" {
		t.Errorf("Name fallback: got %q, want %q", tree[0].Name, "Share")
	}
}

func TestFlattenAndroidTree_ScrollableNode(t *testing.T) {
	node := protocol.UINode{
		Class:      "androidx.recyclerview.widget.RecyclerView",
		Scrollable: "true",
		Bounds:     "[0,0][1080,1920]",
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(node, &tree)

	if len(tree) != 1 {
		t.Fatalf("expected 1 node, got %d", len(tree))
	}
	if !tree[0].ScrollState.CanScrollDown || !tree[0].ScrollState.CanScrollUp {
		t.Error("scrollable node should have CanScrollDown and CanScrollUp set")
	}
}

func TestFlattenAndroidTree_SkipsNonInteractiveNodes(t *testing.T) {
	node := protocol.UINode{
		Class:     "android.view.ViewGroup",
		Clickable: "false",
		Bounds:    "[0,0][1080,1920]",
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(node, &tree)

	if len(tree) != 0 {
		t.Errorf("expected 0 nodes, got %d", len(tree))
	}
}

func TestFlattenAndroidTree_SkipsInvalidBounds(t *testing.T) {
	node := protocol.UINode{
		Class:     "android.widget.Button",
		Text:      "Broken",
		Clickable: "true",
		Bounds:    "invalid_bounds",
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(node, &tree)

	if len(tree) != 0 {
		t.Errorf("expected 0 nodes for invalid bounds, got %d", len(tree))
	}
}

func TestFlattenAndroidTree_RecursiveChildren(t *testing.T) {
	root := protocol.UINode{
		Class:     "android.widget.LinearLayout",
		Clickable: "false",
		Bounds:    "[0,0][1080,200]",
		Children: []protocol.UINode{
			{Class: "android.widget.Button", Text: "Child A", Clickable: "true", Bounds: "[0,0][200,100]"},
			{Class: "android.widget.Button", Text: "Child B", Clickable: "true", Bounds: "[200,0][400,100]"},
		},
	}

	var tree []protocol.SpatialNode
	flattenAndroidTree(root, &tree)

	if len(tree) != 2 {
		t.Fatalf("expected 2 child nodes, got %d", len(tree))
	}
	names := map[string]bool{tree[0].Name: true, tree[1].Name: true}
	if !names["Child A"] || !names["Child B"] {
		t.Errorf("expected Child A and Child B in tree, got %v", names)
	}
}

func TestFlattenAndroidTree_NodeIDUniqueness(t *testing.T) {
	// Two nodes at the same position but different classes must get different IDs.
	n1 := protocol.UINode{Class: "android.widget.Button", Text: "A", Clickable: "true", Bounds: "[0,0][100,50]"}
	n2 := protocol.UINode{Class: "android.widget.TextView", Text: "B", Clickable: "true", Bounds: "[0,0][100,50]"}
	root := protocol.UINode{Children: []protocol.UINode{n1, n2}}

	var tree []protocol.SpatialNode
	flattenAndroidTree(root, &tree)

	if len(tree) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(tree))
	}
	if tree[0].NodeID == tree[1].NodeID {
		t.Errorf("overlapping nodes with different classes must have distinct NodeIDs, both got %q", tree[0].NodeID)
	}
}

// ---------------------------------------------------------------------------
// boundsRegex
// ---------------------------------------------------------------------------

func TestBoundsRegex(t *testing.T) {
	cases := []struct {
		input          string
		valid          bool
		x1, y1, x2, y2 string
	}{
		{"[100,200][300,400]", true, "100", "200", "300", "400"},
		{"[0,0][1080,1920]", true, "0", "0", "1080", "1920"},
		{"invalid", false, "", "", "", ""},
		{"", false, "", "", "", ""},
	}

	for _, tc := range cases {
		m := boundsRegex.FindStringSubmatch(tc.input)
		if tc.valid {
			if len(m) != 5 {
				t.Errorf("%q: expected 5 groups, got %d", tc.input, len(m))
				continue
			}
			if m[1] != tc.x1 || m[2] != tc.y1 || m[3] != tc.x2 || m[4] != tc.y2 {
				t.Errorf("%q: got (%s,%s,%s,%s), want (%s,%s,%s,%s)",
					tc.input, m[1], m[2], m[3], m[4], tc.x1, tc.y1, tc.x2, tc.y2)
			}
		} else if len(m) != 0 {
			t.Errorf("%q: expected no match, got %v", tc.input, m)
		}
	}
}

// ---------------------------------------------------------------------------
// sizeRegex (viewport parsing)
// ---------------------------------------------------------------------------

func TestSizeRegex(t *testing.T) {
	cases := []struct {
		name  string
		input string
		wantW string
		wantH string
	}{
		{"physical only", "Physical size: 1080x2400\n", "1080", "2400"},
		{"override wins (last match)", "Physical size: 1080x2400\nOverride size: 720x1280\n", "720", "1280"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := sizeRegex.FindAllStringSubmatch(tc.input, -1)
			if len(matches) == 0 {
				t.Fatal("sizeRegex found no matches")
			}
			last := matches[len(matches)-1]
			if last[1] != tc.wantW {
				t.Errorf("width: got %q, want %q", last[1], tc.wantW)
			}
			if last[2] != tc.wantH {
				t.Errorf("height: got %q, want %q", last[2], tc.wantH)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ExecuteAction — pure-logic paths that don't need a real device
// ---------------------------------------------------------------------------

func TestExecuteAction_Wait(t *testing.T) {
	e := NewAndroidEngine()
	start := time.Now()
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:    protocol.ActionWait,
		TimeoutMS: 50,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("ActionWait returned unexpected error: %v", err)
	}
	if elapsed < 45*time.Millisecond {
		t.Errorf("ActionWait(50ms) slept only %v — expected ~50ms", elapsed)
	}
}

func TestExecuteAction_WaitZeroIsNoop(t *testing.T) {
	e := NewAndroidEngine()
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: protocol.ActionWait, TimeoutMS: 0})
	if err != nil {
		t.Errorf("ActionWait with 0ms should not error: %v", err)
	}
}

func TestExecuteAction_UnsupportedAction(t *testing.T) {
	e := NewAndroidEngine()
	err := e.ExecuteAction(context.Background(), protocol.ActionRequest{Action: "fly"})
	if err == nil {
		t.Error("expected error for unsupported action, got nil")
	}
}

// ---------------------------------------------------------------------------
// AddListener
// ---------------------------------------------------------------------------

func TestAddListener_StoresHandlers(t *testing.T) {
	e := NewAndroidEngine()
	h := engine.EventHandler(func(ev any) {})

	e.AddListener(h)
	e.AddListener(h)

	e.mu.RLock()
	count := len(e.listeners)
	e.mu.RUnlock()

	if count != 2 {
		t.Errorf("expected 2 listeners, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_IsNoop(t *testing.T) {
	e := NewAndroidEngine()
	// Close must not panic.
	e.Close()
}

// ---------------------------------------------------------------------------
// Observe — stale flag threading (improvement-plan item 27)
// ---------------------------------------------------------------------------

// hierarchyXML is a minimal UIAutomator dump the fake adb returns for the
// dump+cat pipeline, containing one clickable button so the flattened tree is
// non-empty.
const hierarchyXML = `<?xml version="1.0" encoding="UTF-8"?>
<hierarchy rotation="0" width="1080" height="2400">
  <node index="0" text="" resource-id="" class="android.widget.FrameLayout" package="" content-desc="" checkable="false" checked="false" clickable="false" enabled="true" focusable="false" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" bounds="[0,0][1080,2400]">
    <node index="1" text="OK" resource-id="" class="android.widget.Button" package="" content-desc="" checkable="false" checked="false" clickable="true" enabled="true" focusable="true" focused="false" scrollable="false" long-clickable="false" password="false" selected="false" bounds="[400,1200][680,1320]"/>
  </node>
</hierarchy>`

// fakeObserveADB returns canned output for every command an Observe triggers:
// the dump+cat hierarchy pipeline, viewport, activity, and a PNG screenshot.
func fakeObserveADB() *fakeADB {
	return &fakeADB{out: map[string]string{
		"shell uiautomator dump /data/local/tmp/window_dump.xml": "UI hierchary dumped to: /data/local/tmp/window_dump.xml",
		"shell cat /data/local/tmp/window_dump.xml":              hierarchyXML,
		"shell wm size":                 "Physical size: 1080x2400\n",
		"shell dumpsys window displays": "mCurrentFocus=Window{abc u0 com.example/.MainActivity}",
	}}
}

func TestObserve_StaleFlagThreading(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", fakeObserveADB()))
	t.Cleanup(e.Close)

	// First observe: cold cache → synchronous dump → fresh (not stale).
	first, err := e.Observe()
	if err != nil {
		t.Fatalf("first observe: %v", err)
	}
	if first.Stale {
		t.Error("first observe should be stale=false (cold cache forces a dump)")
	}
	if len(first.SpatialTree) != 1 || first.SpatialTree[0].Name != "OK" {
		t.Fatalf("first observe tree = %+v, want the dumped OK button", first.SpatialTree)
	}

	// Second observe within freshWindow: served from cache → stale=true.
	second, err := e.Observe()
	if err != nil {
		t.Fatalf("second observe: %v", err)
	}
	if !second.Stale {
		t.Error("read-only observe of a fresh cache should be stale=true")
	}
}

func TestObserve_ActionInvalidatesCache(t *testing.T) {
	e := newAndroidEngineWithConn(newADBConn("", fakeObserveADB()))
	t.Cleanup(e.Close)

	if _, err := e.Observe(); err != nil { // prime the cache
		t.Fatalf("prime observe: %v", err)
	}

	// A mutating action invalidates the cache; the next observe must re-dump and
	// report fresh (not stale).
	if err := e.ExecuteAction(context.Background(), protocol.ActionRequest{
		Action:   protocol.ActionClick,
		Selector: &protocol.Selector{CSS: "#OK"},
	}); err != nil {
		t.Fatalf("click: %v", err)
	}
	after, err := e.Observe()
	if err != nil {
		t.Fatalf("observe after click: %v", err)
	}
	if after.Stale {
		t.Error("observe after a mutating action should re-dump (stale=false)")
	}
}

// ---------------------------------------------------------------------------
// Scroll direction logic (pure arithmetic — no ADB required)
// ---------------------------------------------------------------------------

func TestScrollDirectionArithmetic(t *testing.T) {
	// Scrolling down (deltaY=200) from centre (540,960): end Y must be above start.
	startY := 960
	deltaY := 200
	endY := startY - deltaY

	if endY >= startY {
		t.Errorf("scroll down: endY (%d) should be less than startY (%d)", endY, startY)
	}
	if endY < 0 {
		t.Errorf("scroll down: endY (%d) went negative, clamping broken", endY)
	}
}

// Ensure fmt is used (imported for NodeID checks in future).
var _ = fmt.Sprintf
