package browser

import (
	"context"
	"strings"
	"testing"
)

// The CDP-dependent parts of the handle registry (DOM.resolveNode,
// Runtime.callFunctionOn) need a live browser, so these tests pin the parts that
// are pure engine logic: registration, invalidation, id validation, and stale
// detection after a navigation_id bump.

func TestHandleRegistry_Lifecycle(t *testing.T) {
	e := &ChromeEngine{handles: make(map[string]nodeHandle)}
	if got := e.handleCount(); got != 0 {
		t.Fatalf("expected empty registry, got %d handles", got)
	}
	e.registerHandle("42")
	if got := e.handleCount(); got != 1 {
		t.Fatalf("expected 1 handle after register, got %d", got)
	}
	e.registerHandle("42") // idempotent re-register
	if got := e.handleCount(); got != 1 {
		t.Fatalf("re-register should not duplicate, got %d", got)
	}
	e.registerHandle("7")
	if got := e.handleCount(); got != 2 {
		t.Fatalf("expected 2 handles, got %d", got)
	}
	e.invalidateHandles()
	if got := e.handleCount(); got != 0 {
		t.Fatalf("expected empty after invalidate, got %d", got)
	}
}

func TestResolveHandleNode_RejectsInvalidID(t *testing.T) {
	e := &ChromeEngine{handles: make(map[string]nodeHandle)}
	ctx := context.Background()
	for _, bad := range []string{"", "abc", "12.5", "-3", "0"} {
		if _, err := e.resolveHandleNode(ctx, bad); err == nil {
			t.Errorf("expected error for handle %q, got nil", bad)
		}
	}
}

func TestResolveHandleNode_StaleAfterNavigation(t *testing.T) {
	e := &ChromeEngine{handles: make(map[string]nodeHandle)}
	e.registerHandle("42")

	// Simulate a navigation_id bump without a real browser (the stale check runs
	// before any CDP call, so this never touches Chrome).
	e.navMu.Lock()
	e.navigationID++
	e.navMu.Unlock()

	_, err := e.resolveHandleNode(context.Background(), "42")
	if err == nil || !strings.Contains(err.Error(), "invalidated by navigation") {
		t.Fatalf("expected stale-handle error, got %v", err)
	}
}

func TestRegisterHandle_BindsCurrentNavigation(t *testing.T) {
	e := &ChromeEngine{handles: make(map[string]nodeHandle)}
	e.registerHandle("42")
	if cur := e.currentNavID(); cur != 0 {
		t.Fatalf("expected initial navigation id 0, got %d", cur)
	}
	e.handleMu.Lock()
	h := e.handles["42"]
	e.handleMu.Unlock()
	if h.NavID != 0 {
		t.Fatalf("handle should be bound to nav 0, got %d", h.NavID)
	}
}
