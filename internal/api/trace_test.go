package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
)

// traceFakeKind is a fake engine kind whose engine implements TraceBundlePath,
// so GetTrace can be exercised without launching a browser. It has its own
// registration once (the artifacts test's fakeKind is already registered).
const traceFakeKind engine.Kind = "trace-test-fake"

var (
	traceRegisterOnce sync.Once
	// fakeTraceBundlePath drives what the fake engine reports.
	fakeTraceBundlePath string
)

type fakeTraceEngine struct {
	*engine.MemoryEngine
}

func (f *fakeTraceEngine) TraceBundlePath() string {
	return fakeTraceBundlePath
}

func registerTraceFakeEngine(t *testing.T) {
	t.Helper()
	traceRegisterOnce.Do(func() {
		engine.Register(traceFakeKind, func(engine.Options) (engine.Engine, error) {
			return &fakeTraceEngine{MemoryEngine: engine.NewMemoryEngine(t)}, nil
		})
	})
}

func TestGetTrace_ServesBundle(t *testing.T) {
	registerTraceFakeEngine(t)

	dir := t.TempDir()
	bundle := filepath.Join(dir, "sess-1.spz")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("summary.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(`{"steps":1}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeTraceBundlePath = bundle
	defer func() { fakeTraceBundlePath = "" }()

	mgr := sandbox.NewManager()
	sess, err := mgr.CreateSession(traceFakeKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	h := &handler{mgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/trace", nil)
	rr := httptest.NewRecorder()
	h.GetTrace(rr, req, sess.ID)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type: want application/zip, got %q", ct)
	}
	body, _ := io.ReadAll(rr.Result().Body)
	if !bytes.Equal(body, buf.Bytes()) {
		t.Error("body: want the .spz file bytes")
	}
}

func TestGetTrace_NoBundleReturns404(t *testing.T) {
	registerTraceFakeEngine(t)
	fakeTraceBundlePath = ""
	defer func() { fakeTraceBundlePath = "" }()

	mgr := sandbox.NewManager()
	sess, err := mgr.CreateSession(traceFakeKind, engine.Options{})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	h := &handler{mgr: mgr}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/trace", nil)
	rr := httptest.NewRecorder()
	h.GetTrace(rr, req, sess.ID)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rr.Code)
	}
}
