package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
)

type handler struct {
	mgr *sandbox.Manager
}

func (h *handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	// Optional request body:
	// {
	//   "headless": false,
	//   "platform": "web"|"android",
	//   "kind": "chrome"|"android"
	// }
	type createSessionReq struct {
		Headless *bool  `json:"headless,omitempty"`
		Platform string `json:"platform,omitempty"`
		Kind     string `json:"kind,omitempty"`
	}

	var reqBody createSessionReq
	// If the body is empty, Decoder returns EOF which is fine.
	if err := func() error {
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		return nil
	}(); err != nil {
		writeError(w, r, fmt.Errorf("bad request body"))
		return
	}

	platform := reqBody.Platform
	kindStr := reqBody.Kind

	opts := engine.Options{
		Headless: reqBody.Headless,
	}

	kind := engine.KindChrome
	switch platform {
	case "android":
		kind = engine.KindAndroid
	}
	switch kindStr {
	case "android":
		kind = engine.KindAndroid
	case "chrome":
		kind = engine.KindChrome
	}

	sess, err := h.mgr.CreateSession(kind, opts)
	if err != nil {
		writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId": sess.ID,
	})
}

func (h *handler) DeleteSession(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.mgr.DeleteSession(id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
