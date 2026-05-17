package api

import (
	"encoding/json"
	"io"
	"net/http"

	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
)

type handler struct {
	mgr *sandbox.Manager
}

func (h *handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var opts engine.Options
	// Optional request body: {"headless": false}
	// If the body is empty, Decoder returns EOF which is fine.
	if err := func() error {
		// Decode into opts to allow partial inputs; ignore EOF for empty bodies.
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		return nil
	}(); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}

	sess, err := h.mgr.CreateSession(engine.KindChrome, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"sessionId": sess.ID,
	})
}

func (h *handler) DeleteSession(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.mgr.DeleteSession(id); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

