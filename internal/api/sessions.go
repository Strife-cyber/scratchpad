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

// createSessionReq is the POST /api/v1/sessions body. Besides the established
// headless/platform/kind/device knobs it carries the persistent-profile,
// attach-to-running-Chrome, and emulation/proxy options for improvement-plan
// items 22/23 (see toOptions).
type createSessionReq struct {
	Headless    *bool  `json:"headless,omitempty"`
	Platform    string `json:"platform,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Device      string `json:"device,omitempty"`
	ProfileDir  string `json:"profile_dir,omitempty"`
	AttachPort  int    `json:"attach_port,omitempty"`
	Persistent  bool   `json:"session_persist,omitempty"`
	UserAgent   string `json:"user_agent,omitempty"`
	Locale      string `json:"locale,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	ProxyAuth   string `json:"proxy_auth,omitempty"`
}

func (h *handler) CreateSession(w http.ResponseWriter, r *http.Request) {
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

	opts := reqBody.toOptions()

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

// toOptions maps the HTTP create-session body onto engine.Options, threading the
// profile/attach/persistent (item 22) and emulation/proxy (item 23) knobs
// through to engine creation.
func (r createSessionReq) toOptions() engine.Options {
	return engine.Options{
		Headless:    r.Headless,
		Device:      r.Device,
		ProfileDir:  r.ProfileDir,
		AttachPort:  r.AttachPort,
		Persistent:  r.Persistent,
		UserAgent:   r.UserAgent,
		Locale:      r.Locale,
		Timezone:    r.Timezone,
		ColorScheme: r.ColorScheme,
		ProxyURL:    r.ProxyURL,
		ProxyAuth:   r.ProxyAuth,
	}
}

func (h *handler) DeleteSession(w http.ResponseWriter, r *http.Request, id string) {
	if err := h.mgr.DeleteSession(id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
