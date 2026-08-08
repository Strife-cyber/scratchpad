package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

func (h *handler) GetHAR(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type harGetter interface {
		GetHAR() ([]byte, error)
	}
	hg, ok := sess.Engine.(harGetter)
	if !ok {
		writeError(w, r, fmt.Errorf("HAR not supported for this engine"))
		return
	}

	data, err := hg.GetHAR()
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to build HAR: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".har.json"))
	_, _ = w.Write(data)
}

func (h *handler) GetDOM(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type domGetter interface {
		GetDOM() (string, error)
	}
	dg, ok := sess.Engine.(domGetter)
	if !ok {
		writeError(w, r, fmt.Errorf("DOM not supported for this engine"))
		return
	}

	html, err := dg.GetDOM()
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to capture DOM: %w", err))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".dom.html"))
	_, _ = w.Write([]byte(html))
}

func (h *handler) GetScreenshot(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	// Prefer the options-aware interface (item 18: full_page, element crop,
	// format, quality); fall back to the legacy (format, fullPage) method.
	type optionsScreenshotGetter interface {
		CaptureScreenshotOptions(opts protocol.ScreenshotOptions) (mime string, data []byte, err error)
	}
	type screenshotGetter interface {
		CaptureScreenshot(format string, fullPage bool) (mime string, data []byte, err error)
	}

	q := r.URL.Query()
	opts := protocol.ScreenshotOptions{}
	if format := q.Get("format"); format != "" {
		opts.Format = format
	}
	if v := q.Get("fullPage"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			opts.FullPage = b
		}
	}
	if sel := q.Get("element"); sel != "" {
		opts.ElementSelector = &protocol.Selector{CSS: sel}
	}
	if v := q.Get("quality"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Quality = &n
		}
	}

	var (
		mime string
		data []byte
		err  error
	)
	if osg, ok := sess.Engine.(optionsScreenshotGetter); ok {
		mime, data, err = osg.CaptureScreenshotOptions(opts)
	} else if sg, ok := sess.Engine.(screenshotGetter); ok {
		mime, data, err = sg.CaptureScreenshot(opts.FormatOr("jpeg"), opts.FullPage)
	} else {
		writeError(w, r, fmt.Errorf("screenshot not supported for this engine"))
		return
	}
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to capture screenshot: %w", err))
		return
	}

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".screenshot."+opts.FormatOr("jpeg")))
	_, _ = w.Write(data)
}

// GetArtifact serves a binary artifact (e.g. a capture_pdf PDF) by its base
// name via GET /sessions/{id}/artifacts/{name} (improvement-plan item 18). The
// path comes from the engine's artifact table, so only files the engine itself
// produced are reachable.
func (h *handler) GetArtifact(w http.ResponseWriter, r *http.Request, id, name string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type artifactGetter interface {
		ArtifactPath(name string) (string, bool)
	}
	ag, ok := sess.Engine.(artifactGetter)
	if !ok {
		writeError(w, r, fmt.Errorf("artifacts not supported for this engine"))
		return
	}

	path, ok := ag.ArtifactPath(name)
	if !ok {
		writeErrorStatus(w, r, http.StatusNotFound, fmt.Errorf("artifact %q not found", name))
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to read artifact: %w", err))
		return
	}

	ctype := "application/pdf"
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		ctype = "image/png"
	case ".jpg", ".jpeg":
		ctype = "image/jpeg"
	case ".webp":
		ctype = "image/webp"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(name)))
	_, _ = w.Write(data)
}

func (h *handler) PostScreenshotDiff(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type reqBody struct {
		ExpectedBase64 string `json:"expected_base64"`
		Tolerance      int    `json:"tolerance"`
	}
	var body reqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, fmt.Errorf("bad request body"))
		return
	}
	if body.ExpectedBase64 == "" {
		writeError(w, r, fmt.Errorf("expected_base64 is required"))
		return
	}

	// Execute assertion and then observe to fetch assertion_result.
	if !h.runAction(w, r, sess, protocol.ActionRequest{
		Action: "assert",
		Assertion: &protocol.AssertionRequest{
			Type:                "screenshot_matches",
			ScreenshotBase64:    body.ExpectedBase64,
			ScreenshotTolerance: body.Tolerance,
		},
	}) {
		return
	}

	obs, err := sess.Engine.Observe()
	if err != nil {
		writeError(w, r, fmt.Errorf("observe failed: %w", err))
		return
	}

	if obs.AssertionResult == nil {
		writeError(w, r, fmt.Errorf("assertion_result missing"))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId":       id,
		"assertionResult": obs.AssertionResult,
	})
}

func (h *handler) PostRecordingStart(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type recorder interface {
		StartRecording(videoDir string) (string, error)
	}
	rec, ok := sess.Engine.(recorder)
	if !ok {
		writeError(w, r, fmt.Errorf("recording not supported for this engine"))
		return
	}

	var body struct {
		Dir string `json:"dir,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	videoPath, err := rec.StartRecording(body.Dir)
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to start recording: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId":  id,
		"outputPath": videoPath,
	})
}

func (h *handler) PostRecordingStop(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type recorder interface {
		StopRecording() ([]byte, string, error)
	}
	rec, ok := sess.Engine.(recorder)
	if !ok {
		writeError(w, r, fmt.Errorf("recording not supported for this engine"))
		return
	}

	videoBytes, outputPath, err := rec.StopRecording()
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to stop recording: %w", err))
		return
	}

	w.Header().Set("Content-Type", "video/webm")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".webm"))
	w.Header().Set("X-Output-Path", outputPath)
	_, _ = w.Write(videoBytes)
}

func (h *handler) PostTracingStart(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type tracer interface {
		StartTracing(traceDir string) (string, error)
	}
	tr, ok := sess.Engine.(tracer)
	if !ok {
		writeError(w, r, fmt.Errorf("tracing not supported for this engine"))
		return
	}

	var body struct {
		Dir string `json:"dir,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	outPath, err := tr.StartTracing(body.Dir)
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to start tracing: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId":  id,
		"outputPath": outPath,
	})
}

func (h *handler) PostTracingStop(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	type tracer interface {
		StopTracing() ([]byte, string, error)
	}
	tr, ok := sess.Engine.(tracer)
	if !ok {
		writeError(w, r, fmt.Errorf("tracing not supported for this engine"))
		return
	}

	traceBytes, outPath, err := tr.StopTracing()
	if err != nil {
		writeError(w, r, fmt.Errorf("failed to stop tracing: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".trace.json.gz"))
	w.Header().Set("X-Output-Path", outPath)
	_, _ = w.Write(traceBytes)
}

func (h *handler) GetConsole(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		writeError(w, r, protocol.ErrSessionNotFound)
		return
	}

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			limit = n
		}
	}
	if limit == 0 {
		limit = 0
	}

	sess.LogMu.Lock()
	defer sess.LogMu.Unlock()
	logs := sess.ConsoleRing
	if limit > 0 && len(logs) > limit {
		logs = logs[len(logs)-limit:]
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"sessionId": id,
		"logs":      logs,
	}); err != nil {
		// best-effort; headers may already be sent
	}
}

// Keep file referenced.
var _ = sandbox.Session{}
