package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"scratchpad/internal/protocol"

	mcp "github.com/metoro-io/mcp-golang"
)

// ---------------------------------------------------------------------------
// Session lifecycle tool argument types (item 6.1)
// ---------------------------------------------------------------------------
//
// These tools manage the bridge's session pool and the server-side sandbox
// lifecycle. They are appended to the descriptor table in toolDefs() so
// RegisterTools stays a simple loop (file-ownership rule 3).

// SessionCreateArgs creates a new isolated engine session.
// platform is "web" (default) or "android". headless only matters for browser
// sessions. device applies a built-in device-emulation preset by name (see
// browser_list_devices, e.g. "iPhone 14") at session start. viewport is recorded
// in the session URL for upcoming configure work and ignored by older engines.
// The remaining fields (improvement-plan items 22/23) are threaded through the
// WS query string: profile_dir reuses a Chrome user-data-dir as a persistent
// profile, attach_port connects to a running Chrome on 127.0.0.1:<port>, and
// user_agent/locale/timezone/color_scheme/proxy/proxy_auth set emulation
// overrides at session creation.
type SessionCreateArgs struct {
	Platform    string             `json:"platform,omitempty"`
	Headless    *bool              `json:"headless,omitempty"`
	Viewport    *protocol.Viewport `json:"viewport,omitempty"`
	Proxy       string             `json:"proxy,omitempty"`
	Device      string             `json:"device,omitempty"`
	ProfileDir  string             `json:"profile_dir,omitempty"`
	AttachPort  int                `json:"attach_port,omitempty"`
	Persistent  bool               `json:"session_persist,omitempty"`
	UserAgent   string             `json:"user_agent,omitempty"`
	Locale      string             `json:"locale,omitempty"`
	Timezone    string             `json:"timezone,omitempty"`
	ColorScheme string             `json:"color_scheme,omitempty"`
	ProxyAuth   string             `json:"proxy_auth,omitempty"`
	// Serial targets a specific connected Android device/emulator (from
	// android_list_devices) when platform is "android"; empty means adb picks
	// its default (ANDROID_SERIAL env var or the single connected device).
	Serial string `json:"serial,omitempty"`
	// Platforms creates a hybrid session owning one engine per context
	// (improvement-plan item 31), e.g. ["web","android"]. Subsequent
	// browser_* tools act on the session's active context until
	// session_switch_context flips it. Mutually exclusive with platform.
	Platforms []string `json:"platforms,omitempty"`
}

type SessionListArgs struct{}

type SessionAttachArgs struct {
	SessionID string `json:"session_id"`
}

type SessionCloseArgs struct {
	SessionID string `json:"session_id"`
}

// SwitchContextArgs flips the active context of a hybrid session (item 31).
// Context names one of the contexts the session owns, e.g. "web" or "android".
type SwitchContextArgs struct {
	Context string `json:"context"`
}

// SessionSnapshotArgs observes a session (optionally the active one) and
// attaches its metadata. session_id defaults to the active session.
type SessionSnapshotArgs struct {
	SessionID string `json:"session_id,omitempty"`
}

// CancelArgs cancels an in-flight action. When ActionID is empty the currently
// running action on the session is cancelled.
type CancelArgs struct {
	ActionID string `json:"action_id,omitempty"`
}

// sessionToolDefs returns the session lifecycle tool descriptors. RegisterTools
// appends these to the browser action tools from tools.go.
func (s *Server) sessionToolDefs() []toolDef {
	return []toolDef{
		{
			name:        "browser_cancel",
			description: "Cancel the in-flight action on the active session (or the action whose action_id is given), e.g. a long-running browser_wait or browser_scroll. The engine returns a clean, non-fatal 'cancelled' result instead of an error.\n\nExample: browser_cancel with {\"action_id\":\"abc\"} cancels that action; browser_cancel with {} cancels whatever is running.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("browser_cancel", "Cancel the in-flight action on the active session.", func(_ context.Context, args CancelArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					env := protocol.Envelope{Type: protocol.MsgTypeCancel}
					if args.ActionID != "" {
						env.Data = mustJSON(protocol.CancelRequest{ActionID: args.ActionID})
					}
					msg, err := sc.roundTrip(env)
					if err != nil {
						return nil, err
					}
					// A cancel ack is {"type":"cancel","data":{"ok":true,...}}. The
					// engine then emits one more message — the observation carrying
					// the cancelled action's non-fatal result. Drain it so it
					// doesn't bleed into the next tool call. Any ErrorResponse
					// (unknown action_id, nothing in flight) is surfaced verbatim.
					var ack protocol.Envelope
					if json.Unmarshal(msg, &ack) == nil && ack.Type == protocol.MsgTypeCancel {
						sc.mu.Lock()
						drainPending(sc)
						sc.mu.Unlock()
						return mcp.NewToolResponse(mcp.NewTextContent("Action cancelled.")), nil
					}
					return s.parseResponse(sc, msg, nil)
				})
			},
		},
		{
			name:        "session_create",
			description: "Create a new isolated engine session and make it the active session for subsequent browser_* tools. Use when a task needs a second, independent page context.\n\nExample: session_create with {\"platform\":\"web\",\"headless\":true} creates a fresh headless browser session and returns its session_id.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_create", "Create a new isolated engine session and make it the active session.", func(_ context.Context, args SessionCreateArgs) (*mcp.ToolResponse, error) {
					sc, err := s.createSessionConn(sessionOptions{
						Platform:    args.Platform,
						Headless:    args.Headless,
						Viewport:    args.Viewport,
						ProxyURL:    args.Proxy,
						Device:      args.Device,
						ProfileDir:  args.ProfileDir,
						AttachPort:  args.AttachPort,
						Persistent:  args.Persistent,
						UserAgent:   args.UserAgent,
						Locale:      args.Locale,
						Timezone:    args.Timezone,
						ColorScheme: args.ColorScheme,
						ProxyAuth:   args.ProxyAuth,
						Serial:      args.Serial,
						Platforms:   args.Platforms,
					})
					if err != nil {
						return nil, err
					}
					s.mu.Lock()
					s.conns[sc.id] = sc
					s.activeSessionID = sc.id
					s.mu.Unlock()
					return sessionInfoContent(sc.sessionInfo())
				})
			},
		},
		{
			name:        "session_list",
			description: "List every live session (id, kind, created/last-activity timestamps), including sessions created by other clients. Call this before session_attach to find a session to reuse.\n\nExample: session_list with {} returns the sessions as JSON.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_list", "List every live session.", func(_ context.Context, _ SessionListArgs) (*mcp.ToolResponse, error) {
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeListSessions})
					if err != nil {
						return nil, err
					}
					resp, err := parseSessionList(msg)
					if err != nil {
						return nil, err
					}
					data, _ := json.Marshal(resp)
					return mcp.NewToolResponse(mcp.NewTextContent(string(data))), nil
				})
			},
		},
		{
			name:        "session_attach",
			description: "Attach the bridge to an existing session by id and make it the active session. Attaching bumps the session's keep-alive lease, so it is not reaped for being idle. Returns the attached session's metadata.\n\nExample: session_attach with {\"session_id\":\"abc-123\"} resumes that session.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_attach", "Attach the bridge to an existing session by id.", func(_ context.Context, args SessionAttachArgs) (*mcp.ToolResponse, error) {
					if args.SessionID == "" {
						return nil, fmt.Errorf("mcp: session_attach requires session_id")
					}
					sc, err := s.attachSessionConn(args.SessionID)
					if err != nil {
						return nil, err
					}
					return sessionInfoContent(sc.sessionInfo())
				})
			},
		},
		{
			name:        "session_close",
			description: "Close a session by id (defaults to the active session), tearing down its engine server-side and dropping the bridge's connection to it. The session is removed from session_list. Works even when this process holds no connection to the session (e.g. a reconnected or fresh host closing an older session). Use when a session's work is finished.\n\nExample: session_close with {\"session_id\":\"abc-123\"} closes that session.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_close", "Close a session by id (defaults to the active session).", func(_ context.Context, args SessionCloseArgs) (*mcp.ToolResponse, error) {
					id := args.SessionID
					if id == "" {
						id = s.SessionID()
					}
					return s.closeSession(id)
				})
			},
		},
		{
			name:        "session_snapshot",
			description: "Observe a session (defaults to the active one) and attach its metadata (id, kind, platform). The payload is the same observation envelope browser_observe returns, prefixed with the session descriptor.\n\nExample: session_snapshot with {\"session_id\":\"abc-123\"} captures that session's current page state.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_snapshot", "Observe a session and attach its metadata.", func(_ context.Context, args SessionSnapshotArgs) (*mcp.ToolResponse, error) {
					id := args.SessionID
					if id == "" {
						id = s.SessionID()
					}
					sc, err := s.getConn(id)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(protocol.Envelope{Type: protocol.MsgTypeObserve})
					if err != nil {
						return nil, err
					}
					base, err := s.parseResponse(sc, msg, nil)
					if err != nil {
						return nil, err
					}
					meta, _ := json.Marshal(sc.sessionInfo())
					contents := append([]*mcp.Content{mcp.NewTextContent("Session snapshot: " + string(meta))}, base.Content...)
					return mcp.NewToolResponse(contents...), nil
				})
			},
		},
		{
			name:        "session_switch_context",
			description: "Switch the active context of a hybrid session (created with platforms [\"web\",\"android\"]) to the named context, so subsequent browser_* tools act on that engine until the next switch. Use when a task alternates between the browser and the Android app.\n\nExample: session_switch_context with {\"context\":\"android\"} makes the Android engine the active context and returns an observation of it.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_switch_context", "Switch the active context of a hybrid session.", func(_ context.Context, args SwitchContextArgs) (*mcp.ToolResponse, error) {
					if args.Context == "" {
						return nil, fmt.Errorf("mcp: session_switch_context requires context")
					}
					env := protocol.Envelope{
						Type: protocol.MsgTypeSetContext,
						Data: mustJSON(protocol.SetContextRequest{Context: args.Context}),
					}
					sc, err := s.getConn(s.activeSessionID)
					if err != nil {
						return nil, err
					}
					msg, err := sc.roundTrip(env)
					if err != nil {
						return nil, err
					}
					return s.parseResponse(sc, msg, nil)
				})
			},
		},
	}
}

// sessionInfoContent renders a SessionInfo as a single text content block.
func sessionInfoContent(info protocol.SessionInfo) (*mcp.ToolResponse, error) {
	data, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal session info: %w", err)
	}
	return mcp.NewToolResponse(mcp.NewTextContent(string(data))), nil
}

// drainPending consumes follow-up messages the engine sent after the last
// response (e.g. the observation emitted when a cancelled action finishes), so
// they don't bleed into the next tool call. It reads with a short per-read
// deadline and returns at the first timeout or after a small number of messages.
// Callers must hold sc.mu.
func drainPending(sc *sessionConn) {
	for i := 0; i < 5; i++ {
		_ = sc.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, _, err := sc.conn.ReadMessage()
		if err != nil {
			return
		}
	}
}

// parseSessionList extracts a SessionListResponse from a session_list envelope.
func parseSessionList(msg []byte) (protocol.SessionListResponse, error) {
	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		return protocol.SessionListResponse{}, fmt.Errorf("mcp: session_list: %w", err)
	}
	var resp protocol.SessionListResponse
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &resp); err != nil {
			return protocol.SessionListResponse{}, fmt.Errorf("mcp: session_list data: %w", err)
		}
	}
	return resp, nil
}

// ---------------------------------------------------------------------------
// session_close
// ---------------------------------------------------------------------------

// closeSession closes the session with the given id. When the bridge already
// holds a live connection to that session, the close is forwarded on it (fast
// path) and the local connection is dropped. When it holds no connection — the
// common case for a reconnected or fresh stdio host closing a session that
// outlives its own process — a throwaway connection is opened, attached to the
// session by id, and used to forward the close (slow path). A session that no
// longer exists server-side surfaces as the typed session_not_found instead of
// the local "no connection" error.
func (s *Server) closeSession(id string) (*mcp.ToolResponse, error) {
	if id == "" {
		return nil, fmt.Errorf("mcp: session_close requires a session_id")
	}
	env := protocol.Envelope{
		Type: protocol.MsgTypeCloseSession,
		Data: mustJSON(protocol.CloseSessionRequest{SessionID: id}),
	}

	// Fast path: forward the close on the session's live connection, if any.
	if sc, err := s.getConn(id); err == nil {
		msg, rerr := sc.roundTrip(env)
		s.dropSession(id)
		if rerr != nil {
			slog.Warn("mcp: session_close ack not received (session may still be closed)",
				"session_id", id, "err", rerr)
			return closeAck(id), nil
		}
		var ack protocol.Envelope
		if json.Unmarshal(msg, &ack) == nil && ack.Type == protocol.MsgTypeCloseSession {
			return closeAck(id), nil
		}
		return s.parseResponse(sc, msg, nil)
	}

	// Slow path: no local connection for this session.
	resp, err := s.closeSessionRemote(id, env)
	if err == nil {
		s.mu.Lock()
		if s.activeSessionID == id {
			s.activeSessionID = ""
		}
		s.mu.Unlock()
	}
	return resp, err
}

// closeSessionRemote forwards a close for id on a throwaway connection: it
// dials the engine, attaches to the session by id (releasing the throwaway
// fresh session when the target exists), sends the close, and closes the
// connection. A missing session — refused at attach or by the server's close
// handler — surfaces as the typed session_not_found.
func (s *Server) closeSessionRemote(id string, env protocol.Envelope) (*mcp.ToolResponse, error) {
	sc, err := dial(s.engineURL, id)
	if err != nil {
		if errors.Is(err, protocol.ErrSessionNotFound) {
			return typedSessionNotFound(id), nil
		}
		return nil, fmt.Errorf("mcp: close session %q: %w", id, err)
	}
	defer sc.closeConn()

	msg, rerr := sc.roundTrip(env)
	if rerr != nil {
		slog.Warn("mcp: session_close ack not received (session may still be closed)",
			"session_id", id, "err", rerr)
		return closeAck(id), nil
	}
	var ack protocol.Envelope
	if json.Unmarshal(msg, &ack) == nil && ack.Type == protocol.MsgTypeCloseSession {
		return closeAck(id), nil
	}
	// The server refused the close (e.g. session_not_found): surface verbatim.
	return s.parseResponse(sc, msg, nil)
}

// closeAck builds the success ToolResponse for a closed session.
func closeAck(id string) *mcp.ToolResponse {
	return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Session %q closed.", id)))
}

// typedSessionNotFound builds a ToolResponse carrying the typed
// session_not_found envelope, so agents see the stable code and hint instead of
// a local "no connection for session" error.
func typedSessionNotFound(id string) *mcp.ToolResponse {
	resp := protocol.ErrorResponseFromError(protocol.ErrSessionNotFound, protocol.ErrorLevelAction)
	resp.Message = fmt.Sprintf("close session: no session with id %q", id)
	data, _ := json.Marshal(resp)
	return mcp.NewToolResponse(mcp.NewTextContent(string(data)))
}
