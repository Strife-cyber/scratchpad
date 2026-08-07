package api

import (
	"encoding/json"
	"net/http"

	"scratchpad/internal/middleware"
	"scratchpad/internal/protocol"
)

// writeError writes err as a typed protocol.ErrorResponse JSON envelope,
// deriving code, hint, error level, and HTTP status from the error catalog and
// the request_id from the request-id middleware. This replaces bare
// http.Error calls so every HTTP error carries the same typed shape as
// WebSocket and MCP errors.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	class := protocol.Classify(err)
	writeEnvelope(w, r, class.Status, class.Level, err.Error(), class.Code, class.Hint)
}

// writeErrorStatus is writeError with an explicit HTTP status, for the cases
// where a handler knows the right status regardless of the catalog (e.g. a
// client-sent unknown action name is a 400, not the catalog's 501). The code,
// hint, and level still come from the catalog; 4xx responses are always
// Warning-level so a fatal classification doesn't contradict a client error.
func writeErrorStatus(w http.ResponseWriter, r *http.Request, status int, err error) {
	class := protocol.Classify(err)
	if status < 500 {
		class.Level = protocol.ErrorLevelWarning
	}
	writeEnvelope(w, r, status, class.Level, err.Error(), class.Code, class.Hint)
}

// writeEnvelope serializes the ErrorResponse and writes it with the given
// status. Errors here are best-effort: once the status header is written there
// is nothing sensible to do if encoding fails.
func writeEnvelope(w http.ResponseWriter, r *http.Request, status int, level protocol.ErrorLevel, msg, code, hint string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{
		RequestID: middleware.FromRequest(r),
		Code:      code,
		Type:      level,
		Message:   msg,
		Hint:      hint,
	})
}
