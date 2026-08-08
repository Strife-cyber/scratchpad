package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

// fakeKind is an engine kind backed by a MemoryEngine that also implements the
// item-18 methods the HTTP handlers type-assert on (ArtifactPath /
// CaptureScreenshotOptions), without ever launching a browser.
const fakeKind engine.Kind = "artifact-test-fake"

var (
	registerOnce sync.Once
	// fakeArtifacts and fakeScreenshot let tests drive what the fake engine
	// returns. Mutating them is only safe while no handler runs concurrently;
	// these tests are single-threaded.
	fakeArtifacts      map[string]string
	fakeScreenshot     func(opts protocol.ScreenshotOptions) (string, []byte, error)
	fakeScreenshotOpts protocol.ScreenshotOptions
)

func registerFakeEngine(t *testing.T) {
	t.Helper()
	registerOnce.Do(func() {
		engine.Register(fakeKind, func(engine.Options) (engine.Engine, error) {
			return &fakeArtifactEngine{MemoryEngine: engine.NewMemoryEngine(t)}, nil
		})
	})
}

// fakeArtifactEngine wraps engine.MemoryEngine (the shared test fake) and adds
// the item-18 methods. It does not need a browser.
type fakeArtifactEngine struct {
	*engine.MemoryEngine
}

func (f *fakeArtifactEngine) ArtifactPath(name string) (string, bool) {
	p, ok := fakeArtifacts[name]
	return p, ok
}

func (f *fakeArtifactEngine) CaptureScreenshotOptions(opts protocol.ScreenshotOptions) (string, []byte, error) {
	fakeScreenshotOpts = opts
	if fakeScreenshot != nil {
		return fakeScreenshot(opts)
	}
	return "image/jpeg", []byte("screenshot-bytes"), nil
}

// newTestSession builds a manager holding one fake session, bypassing any real
// Chrome launch, and returns the manager plus the session ID.
func newTestSession(t *testing.T) (*sandbox.Manager, string) {
	t.Helper()
	registerFakeEngine(t)
	mgr := sandbox.NewManager()
	sess, err := mgr.CreateSession(fakeKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return mgr, sess.ID
}

func TestGetArtifact_ServesRegisteredFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "receipt.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4 fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeArtifacts = map[string]string{"receipt.pdf": path}

	mgr, sid := newTestSession(t)
	h := &handler{mgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/artifacts/receipt.pdf", nil)
	rr := httptest.NewRecorder()
	h.GetArtifact(rr, req, sid, "receipt.pdf")

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("Content-Type: want application/pdf, got %q", ct)
	}
	body, _ := io.ReadAll(rr.Result().Body)
	if string(body) != "%PDF-1.4 fake" {
		t.Errorf("body: want file bytes, got %q", string(body))
	}
}

func TestGetArtifact_UnknownReturns404(t *testing.T) {
	fakeArtifacts = map[string]string{}
	mgr, sid := newTestSession(t)
	h := &handler{mgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sid+"/artifacts/missing.pdf", nil)
	rr := httptest.NewRecorder()
	h.GetArtifact(rr, req, sid, "missing.pdf")

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rr.Code)
	}
}

func TestGetScreenshot_PassesOptionsToEngine(t *testing.T) {
	fakeArtifacts = map[string]string{}
	quality := 60
	fakeScreenshot = func(opts protocol.ScreenshotOptions) (string, []byte, error) {
		return "image/png", []byte("png"), nil
	}
	defer func() { fakeScreenshot = nil }()

	mgr, sid := newTestSession(t)
	h := &handler{mgr: mgr}
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/sessions/"+sid+"/screenshot?format=png&fullPage=true&element=#table&quality=60", nil)
	rr := httptest.NewRecorder()
	h.GetScreenshot(rr, req, sid)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !fakeScreenshotOpts.FullPage {
		t.Error("FullPage: want true from query param")
	}
	if fakeScreenshotOpts.Format != "png" {
		t.Errorf("Format: want png, got %q", fakeScreenshotOpts.Format)
	}
	if fakeScreenshotOpts.ElementSelector == nil || fakeScreenshotOpts.ElementSelector.CSS != "#table" {
		t.Errorf("ElementSelector: want css=#table, got %+v", fakeScreenshotOpts.ElementSelector)
	}
	if fakeScreenshotOpts.Quality == nil || *fakeScreenshotOpts.Quality != quality {
		t.Errorf("Quality: want 60, got %v", fakeScreenshotOpts.Quality)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: want image/png, got %q", ct)
	}
}
