package mcp

import (
	"context"
	"encoding/json"
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
// sessions. viewport and proxy are recorded in the session URL for upcoming
// configure work and ignored by older engines.
type SessionCreateArgs struct {
	Platform string             `json:"platform,omitempty"`
	Headless *bool              `json:"headless,omitempty"`
	Viewport *protocol.Viewport `json:"viewport,omitempty"`
	Proxy    string             `json:"proxy,omitempty"`
}

type SessionListArgs struct{}

type SessionAttachArgs struct {
	SessionID string `json:"session_id"`
}

type SessionCloseArgs struct {
	SessionID string `json:"session_id"`
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
					return s.parseResponse(msg)
				})
			},
		},
		{
			name:        "session_create",
			description: "Create a new isolated engine session and make it the active session for subsequent browser_* tools. Use when a task needs a second, independent page context.\n\nExample: session_create with {\"platform\":\"web\",\"headless\":true} creates a fresh headless browser session and returns its session_id.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_create", "Create a new isolated engine session and make it the active session.", func(_ context.Context, args SessionCreateArgs) (*mcp.ToolResponse, error) {
					sc, err := s.createSessionConn(args.Platform, args.Headless, args.Viewport, args.Proxy)
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
			description: "Close a session by id (defaults to the active session), tearing down its engine server-side and dropping the bridge's connection to it. The session is removed from session_list. Use when a session's work is finished.\n\nExample: session_close with {\"session_id\":\"abc-123\"} closes that session.",
			register: func(srv *mcp.Server) error {
				return srv.RegisterTool("session_close", "Close a session by id (defaults to the active session).", func(_ context.Context, args SessionCloseArgs) (*mcp.ToolResponse, error) {
					id := args.SessionID
					if id == "" {
						id = s.SessionID()
					}
					sc, err := s.getConn(id)
					if err != nil {
						return nil, err
					}
					env := protocol.Envelope{
						Type: protocol.MsgTypeCloseSession,
						Data: mustJSON(protocol.CloseSessionRequest{SessionID: id}),
					}
					// Best-effort ack read; drop the connection regardless so a
					// post-close transport failure doesn't surface as an error.
					msg, rerr := sc.roundTrip(env)
					s.dropSession(id)
					if rerr == nil {
						var ack protocol.Envelope
						if json.Unmarshal(msg, &ack) != nil || ack.Type != protocol.MsgTypeCloseSession {
							rerr = fmt.Errorf("unexpected close ack")
						}
					}
					if rerr != nil {
						slog.Warn("mcp: session_close ack not received (session may still be closed)",
							"session_id", id, "err", rerr)
					}
					return mcp.NewToolResponse(mcp.NewTextContent(fmt.Sprintf("Session %q closed.", id))), nil
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
					base, err := s.parseResponse(msg)
					if err != nil {
						return nil, err
					}
					meta, _ := json.Marshal(sc.sessionInfo())
					contents := append([]*mcp.Content{mcp.NewTextContent("Session snapshot: " + string(meta))}, base.Content...)
					return mcp.NewToolResponse(contents...), nil
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
