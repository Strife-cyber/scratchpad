package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
)

// Persistent node handles (improvement-plan item 20).
//
// An agent can capture a node_ref (decimal backendNodeId, surfaced on observed
// elements and selector matches) and pass it back as ActionRequest.HandleID to
// reuse the same element across actions. The handle is resolved fresh on every
// use via DOM.resolveNode + Runtime.callFunctionOn — never cached — so a node
// that was re-rendered in place still works, and it is invalidated the moment
// the page navigates (navigation_id changes), because backend node ids do not
// survive a document switch.

// nodeHandle is a registered persistent handle. NodeRef is the decimal
// backendNodeId (also the registry key); NavID is the navigation counter at
// registration time, used to detect handles that outlived a navigation.
type nodeHandle struct {
	NodeRef string
	NavID   int64
}

// registerHandle records a backend node id as a persistent handle bound to the
// current navigation. Re-registering an existing handle refreshes its NavID so
// it stays valid for the current document. Lock order is navMu-then-handleMu,
// never the reverse, to stay consistent with capturePageInfo (which calls
// invalidateHandles while holding navMu).
func (e *ChromeEngine) registerHandle(nodeRef string) {
	navID := e.currentNavID()
	e.handleMu.Lock()
	defer e.handleMu.Unlock()
	e.handles[nodeRef] = nodeHandle{NodeRef: nodeRef, NavID: navID}
}

// invalidateHandles drops every registered handle. Called whenever the
// navigation counter bumps (top-frame navigation, SPA pushState/hash change,
// URL change detected during observe).
func (e *ChromeEngine) invalidateHandles() {
	e.handleMu.Lock()
	defer e.handleMu.Unlock()
	clear(e.handles)
}

// handleCount returns the number of registered handles (test helper).
func (e *ChromeEngine) handleCount() int {
	e.handleMu.Lock()
	defer e.handleMu.Unlock()
	return len(e.handles)
}

// resolveHandleNode resolves a handle_id to a live RemoteObject for the DOM
// element it refers to. It validates the id, rejects handles invalidated by a
// navigation, resolves the backend node fresh, and re-registers the handle so
// it stays tracked. The caller must releaseHandleNode the returned object.
func (e *ChromeEngine) resolveHandleNode(ctx context.Context, handleID string) (*runtime.RemoteObject, error) {
	backendID, err := strconv.ParseInt(handleID, 10, 64)
	if err != nil || backendID <= 0 {
		return nil, fmt.Errorf("handle %q is not a valid node_ref (decimal backendNodeId)", handleID)
	}

	// Reject handles that were registered under an older navigation. (Normally
	// invalidateHandles clears them, but this is a belt-and-suspenders check for
	// the window before the navigation event handler runs.)
	e.handleMu.Lock()
	h, ok := e.handles[handleID]
	e.handleMu.Unlock()
	if ok {
		if cur := e.currentNavID(); h.NavID != cur {
			return nil, fmt.Errorf("handle %q invalidated by navigation (registered at nav %d, current nav %d)",
				handleID, h.NavID, cur)
		}
	}

	obj, err := dom.ResolveNode().WithBackendNodeID(cdp.BackendNodeID(backendID)).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve handle %q: %w", handleID, err)
	}
	if obj == nil || obj.ObjectID == "" {
		return nil, fmt.Errorf("handle %q does not resolve to a live element", handleID)
	}

	// Now tracked for invalidation, even if the agent never saw it via
	// findElementsOnce (e.g. it came from a SpatialNode observation).
	e.registerHandle(handleID)
	return obj, nil
}

// releaseHandleNode releases a RemoteObject obtained from resolveHandleNode.
// Best-effort: a release failure is not an action failure.
func (e *ChromeEngine) releaseHandleNode(obj *runtime.RemoteObject) {
	if obj != nil && obj.ObjectID != "" {
		_ = runtime.ReleaseObject(obj.ObjectID).Do(e.ctx)
	}
}

// handleCenter computes the current centre coordinates of a resolved handle,
// fresh on every call (the node may have moved since it was observed). It
// returns an error when the element has no box (removed or hidden).
func handleCenter(ctx context.Context, obj *runtime.RemoteObject) (cx, cy float64, err error) {
	const fn = `function() {
		const r = this.getBoundingClientRect();
		if (r.width <= 0 || r.height <= 0) return null;
		return {x: r.left + r.width / 2, y: r.top + r.height / 2};
	}`
	res, ex, err := runtime.CallFunctionOn(fn).
		WithObjectID(obj.ObjectID).
		WithReturnByValue(true).
		Do(ctx)
	if err != nil {
		return 0, 0, err
	}
	if ex != nil {
		return 0, 0, fmt.Errorf("handle center JS exception: %s", ex.Text)
	}
	if len(res.Value) == 0 || string(res.Value) == "null" {
		return 0, 0, fmt.Errorf("handle element is not visible (no box)")
	}
	var pt struct{ X, Y float64 }
	if err := json.Unmarshal(res.Value, &pt); err != nil {
		return 0, 0, fmt.Errorf("decode handle center: %w", err)
	}
	return pt.X, pt.Y, nil
}

// runHandleAction runs actionBody with `el` bound to the resolved element. The
// body is a sequence of JS statements ending in a boolean return (the same
// bodies used by buildPierceActionJS, so handle-driven and selector-driven
// actions share logic). Returns true when the action succeeded.
func (e *ChromeEngine) runHandleAction(ctx context.Context, obj *runtime.RemoteObject, actionBody string) (bool, error) {
	fn := "function() { let el = this;\n" + actionBody + "\n}"
	res, ex, err := runtime.CallFunctionOn(fn).
		WithObjectID(obj.ObjectID).
		WithReturnByValue(true).
		Do(ctx)
	if err != nil {
		return false, err
	}
	if ex != nil {
		return false, fmt.Errorf("JS exception: %s", ex.Text)
	}
	if res == nil {
		return false, nil
	}
	return string(res.Value) == "true", nil
}

// resolveHandlePoint resolves a handle to its current centre coordinates.
// Used by the coordinate-driven actions (click, hover, type, ...).
func (e *ChromeEngine) resolveHandlePoint(ctx context.Context, handleID string) (cx, cy float64, err error) {
	obj, err := e.resolveHandleNode(ctx, handleID)
	if err != nil {
		return 0, 0, err
	}
	defer e.releaseHandleNode(obj)
	return handleCenter(ctx, obj)
}

// runRetryHandleAction is the persistent-handle counterpart of
// runRetryJSAction: it re-resolves the handle on every attempt (fresh on each
// use) and re-runs the action body until it succeeds, the timeout elapses, or
// the handle is invalidated by a navigation. A handle that no longer resolves
// is retried like a stale element so a just-navigated page can recover.
func (e *ChromeEngine) runRetryHandleAction(ctx context.Context, name string, timeout time.Duration, handleID, actionBody string) error {
	deadline := time.Now().Add(timeout)
	for {
		obj, err := e.resolveHandleNode(ctx, handleID)
		if err != nil {
			if time.Now().After(deadline) {
				return fmt.Errorf("%s: %w", name, err)
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%s: %w", name, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		ok, err := e.runHandleAction(ctx, obj, actionBody)
		e.releaseHandleNode(obj)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return errJSActionTimeout(name, timeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s: %w", name, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// nodeRefForPoint resolves the backend node id at a viewport point via
// DOM.getNodeForLocation, returning its decimal string form or "" on failure.
// Used by findElementsOnce to give selector matches a stable node_ref.
func nodeRefForPoint(ctx context.Context, x, y float64) string {
	bid, _, _, err := dom.GetNodeForLocation(int64(x), int64(y)).Do(ctx)
	if err != nil || bid == 0 {
		return ""
	}
	return backendNodeRef(bid)
}
