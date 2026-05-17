package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"
)

func (h *handler) GetHAR(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type harGetter interface {
		GetHAR() ([]byte, error)
	}
	hg, ok := sess.Engine.(harGetter)
	if !ok {
		http.Error(w, "HAR not supported for this engine", http.StatusNotImplemented)
		return
	}

	data, err := hg.GetHAR()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to build HAR: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".har.json"))
	_, _ = w.Write(data)
}

func (h *handler) GetDOM(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type domGetter interface {
		GetDOM() (string, error)
	}
	dg, ok := sess.Engine.(domGetter)
	if !ok {
		http.Error(w, "DOM not supported for this engine", http.StatusNotImplemented)
		return
	}

	html, err := dg.GetDOM()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to capture DOM: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".dom.html"))
	_, _ = w.Write([]byte(html))
}

func (h *handler) GetScreenshot(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type screenshotGetter interface {
		CaptureScreenshot(format string, fullPage bool) (mime string, data []byte, err error)
	}
	sg, ok := sess.Engine.(screenshotGetter)
	if !ok {
		http.Error(w, "screenshot not supported for this engine", http.StatusNotImplemented)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "jpeg"
	}
	fullPage := false
	if v := r.URL.Query().Get("fullPage"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			fullPage = b
		}
	}

	mime, data, err := sg.CaptureScreenshot(format, fullPage)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to capture screenshot: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", id+".screenshot."+format))
	_, _ = w.Write(data)
}

func (h *handler) PostScreenshotDiff(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type reqBody struct {
		ExpectedBase64 string `json:"expected_base64"`
		Tolerance      int    `json:"tolerance"`
	}
	var body reqBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if body.ExpectedBase64 == "" {
		http.Error(w, "expected_base64 is required", http.StatusBadRequest)
		return
	}

	// Execute assertion and then observe to fetch assertion_result.
	if err := sess.Engine.ExecuteAction(protocol.ActionRequest{
		Action: "assert",
		Assertion: &protocol.AssertionRequest{
			Type:                "screenshot_matches",
			ScreenshotBase64:   body.ExpectedBase64,
			ScreenshotTolerance: body.Tolerance,
		},
	}); err != nil {
		http.Error(w, fmt.Sprintf("assert failed: %v", err), http.StatusInternalServerError)
		return
	}

	obs, err := sess.Engine.Observe()
	if err != nil {
		http.Error(w, fmt.Sprintf("observe failed: %v", err), http.StatusInternalServerError)
		return
	}

	if obs.AssertionResult == nil {
		http.Error(w, "assertion_result missing", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId":      id,
		"assertionResult": obs.AssertionResult,
	})
}

func (h *handler) PostRecordingStart(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type recorder interface {
		StartRecording(videoDir string) (string, error)
	}
	rec, ok := sess.Engine.(recorder)
	if !ok {
		http.Error(w, "recording not supported for this engine", http.StatusNotImplemented)
		return
	}

	var body struct {
		Dir string `json:"dir,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	videoPath, err := rec.StartRecording(body.Dir)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to start recording: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId": id,
		"outputPath": videoPath,
	})
}

func (h *handler) PostRecordingStop(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type recorder interface {
		StopRecording() ([]byte, string, error)
	}
	rec, ok := sess.Engine.(recorder)
	if !ok {
		http.Error(w, "recording not supported for this engine", http.StatusNotImplemented)
		return
	}

	videoBytes, outputPath, err := rec.StopRecording()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to stop recording: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type tracer interface {
		StartTracing(traceDir string) (string, error)
	}
	tr, ok := sess.Engine.(tracer)
	if !ok {
		http.Error(w, "tracing not supported for this engine", http.StatusNotImplemented)
		return
	}

	var body struct {
		Dir string `json:"dir,omitempty"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	outPath, err := tr.StartTracing(body.Dir)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to start tracing: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId":   id,
		"outputPath":  outPath,
	})
}

func (h *handler) PostTracingStop(w http.ResponseWriter, r *http.Request, id string) {
	sess, ok := h.mgr.GetSession(id)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	type tracer interface {
		StopTracing() ([]byte, string, error)
	}
	tr, ok := sess.Engine.(tracer)
	if !ok {
		http.Error(w, "tracing not supported for this engine", http.StatusNotImplemented)
		return
	}

	traceBytes, outPath, err := tr.StopTracing()
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to stop tracing: %v", err), http.StatusInternalServerError)
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
		http.Error(w, "session not found", http.StatusNotFound)
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

