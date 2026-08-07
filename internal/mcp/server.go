package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"scratchpad/internal/protocol"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	mcp "github.com/metoro-io/mcp-golang"
)

// Server bridges MCP tool calls to the Scratchpad engine over a single
// WebSocket connection. Tool calls are serialised via a mutex so concurrent
// invocations don't interleave on the single WS connection.
type Server struct {
	conn      *websocket.Conn
	sessionID string
	engineURL string

	mu sync.Mutex // serializes write-then-read cycles for concurrent tool calls
}

// NewMcpServer connects to the Scratchpad engine at engineURL and performs
// the session handshake. Returns once the session is ready.
func NewMcpServer(engineURL string) (*Server, error) {
	s := &Server{engineURL: engineURL}
	if err := s.connect(); err != nil {
		return nil, err
	}
	return s, nil
}

// connect dials the engine and performs the session-ID handshake.
func (s *Server) connect() error {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, _, err := dialer.Dial(s.engineURL, nil)
	if err != nil {
		return fmt.Errorf("mcp: dial failed: %w", err)
	}

	// MCP bridge handshake: first message is expected to be {"sessionId":"..."}.
	var handshake struct {
		SessionID string `json:"sessionId"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, message, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mcp: handshake failed: %w", err)
	}
	if err := json.Unmarshal(message, &handshake); err != nil {
		_ = conn.Close()
		return fmt.Errorf("mcp: handshake parse failed: %w", err)
	}

	s.conn = conn
	s.sessionID = handshake.SessionID

	if s.sessionID != "" {
		slog.Info("mcp: connected", "session_id", s.sessionID)
	} else {
		slog.Info("mcp: connected", "session_id", "", "note", "sessionId missing — legacy engine")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Tool argument types
// ---------------------------------------------------------------------------

type NavigateArgs struct {
	URL string `json:"url"`
}

type ObserveArgs struct {
	_ string `json:"-"`
}

type AssertArgs struct {
	Assertion protocol.AssertionRequest `json:"assertion"`
}

type SwitchTabArgs struct {
	TabID string `json:"tab_id"`
}

type CloseTabArgs struct {
	TabID string `json:"tab_id"`
}

type DismissModalArgs struct {
	Strategy string `json:"strategy,omitempty"`
}

type CheckArgs struct {
	Selector protocol.Selector `json:"selector"`
}

type UncheckArgs struct {
	Selector protocol.Selector `json:"selector"`
}

type SubmitFormArgs struct {
	Selector protocol.Selector `json:"selector"`
}

type FillFormArgs struct {
	Fields []protocol.FormField `json:"fields"`
}

// ---------------------------------------------------------------------------
// Tool registration
// ---------------------------------------------------------------------------

func (s *Server) RegisterTools(srv *mcp.Server) {
	// 1. browser_navigate
	err := srv.RegisterTool("browser_navigate", "Load a URL into the browser", func(ctx context.Context, args NavigateArgs) (*mcp.ToolResponse, error) {
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeNavigate,
			Data: mustJSON(protocol.InitializeRequest{URL: args.URL}),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register navigate: %v\n", err)
	}

	// 2. browser_observe
	err = srv.RegisterTool("browser_observe", "Capture the current page state (screenshot + spatial tree + page info)", func(ctx context.Context, args ObserveArgs) (*mcp.ToolResponse, error) {
		return s.sendEnvelope(protocol.Envelope{Type: protocol.MsgTypeObserve})
	})
	if err != nil {
		fmt.Printf("Failed to register observe: %v\n", err)
	}

	// 3. browser_action
	err = srv.RegisterTool("browser_action", "Interact with the page", func(ctx context.Context, args protocol.ActionRequest) (*mcp.ToolResponse, error) {
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(args),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register action: %v\n", err)
	}

	// 4. browser_assert
	err = srv.RegisterTool("browser_assert", "Assert page state (selectors/text/attributes/screenshot)", func(ctx context.Context, args AssertArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:    protocol.ActionAssert,
			Assertion: &args.Assertion,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register assert tool: %v\n", err)
	}

	// 5. browser_list_tabs
	err = srv.RegisterTool("browser_list_tabs", "List all open browser tabs", func(ctx context.Context, args ObserveArgs) (*mcp.ToolResponse, error) {
		return s.sendEnvelope(protocol.Envelope{Type: protocol.MsgTypeObserve})
	})
	if err != nil {
		fmt.Printf("Failed to register list_tabs tool: %v\n", err)
	}

	// 6. browser_switch_tab
	err = srv.RegisterTool("browser_switch_tab", "Switch to a different browser tab by ID from browser_list_tabs", func(ctx context.Context, args SwitchTabArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action: protocol.ActionSwitchTab,
			TabID:  args.TabID,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register switch_tab tool: %v\n", err)
	}

	// 7. browser_close_tab
	err = srv.RegisterTool("browser_close_tab", "Close a browser tab by ID from browser_list_tabs", func(ctx context.Context, args CloseTabArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action: protocol.ActionCloseTab,
			TabID:  args.TabID,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register close_tab tool: %v\n", err)
	}

	// 8. browser_dismiss_modal
	err = srv.RegisterTool("browser_dismiss_modal", "Dismiss modal dialogs, popups, cookie banners, or overlays", func(ctx context.Context, args DismissModalArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:        protocol.ActionDismissModal,
			ModalStrategy: args.Strategy,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register dismiss_modal tool: %v\n", err)
	}

	// 9. browser_check
	err = srv.RegisterTool("browser_check", "Check a checkbox or radio button", func(ctx context.Context, args CheckArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionCheck,
			Selector: &args.Selector,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register check tool: %v\n", err)
	}

	// 10. browser_uncheck
	err = srv.RegisterTool("browser_uncheck", "Uncheck a checkbox or radio button", func(ctx context.Context, args UncheckArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionUncheck,
			Selector: &args.Selector,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register uncheck tool: %v\n", err)
	}

	// 11. browser_submit_form
	err = srv.RegisterTool("browser_submit_form", "Submit a form by selector (CSS of form or child element)", func(ctx context.Context, args SubmitFormArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionSubmitForm,
			Selector: &args.Selector,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register submit_form tool: %v\n", err)
	}

	// 12. browser_fill_form
	err = srv.RegisterTool("browser_fill_form", "Fill multiple form fields at once", func(ctx context.Context, args FillFormArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:     protocol.ActionFillForm,
			FormFields: args.Fields,
		}
		return s.sendEnvelope(protocol.Envelope{
			Type: protocol.MsgTypeAction,
			Data: mustJSON(req),
		})
	})
	if err != nil {
		fmt.Printf("Failed to register fill_form tool: %v\n", err)
	}
}

// ---------------------------------------------------------------------------
// Core IO: envelope send + response parse
// ---------------------------------------------------------------------------

// sendEnvelope wraps the write-then-read cycle in a mutex so concurrent
// MCP tool calls are serialised over the single WS connection.
func (s *Server) sendEnvelope(env protocol.Envelope) (*mcp.ToolResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, _ := json.Marshal(env)
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("mcp: write error: %w", err)
	}

	return s.readResponse()
}

// readResponse reads one message from the engine and parses it as either
// an ErrorResponse or an ObservationResponse. Errors are returned as
// descriptive text so the AI agent gets helpful feedback.
func (s *Server) readResponse() (*mcp.ToolResponse, error) {
	s.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	_, message, err := s.conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("mcp: read error: %w", err)
	}

	// Try ErrorResponse first — the engine always sends this on failure. The
	// envelope is passed through VERBATIM (preserving code, hint, request_id,
	// selector and screenshot) so the AI sees the same stable error grammar as
	// the HTTP and WS transports, instead of a reformatted summary that drops
	// the machine code. The screenshot, when present, is also attached as an
	// image so it stays viewable.
	var errResp protocol.ErrorResponse
	if err := json.Unmarshal(message, &errResp); err == nil && errResp.Type != "" && errResp.Message != "" {
		data, _ := json.Marshal(errResp)
		contents := []*mcp.Content{mcp.NewTextContent(string(data))}
		if errResp.Screenshot != "" {
			contents = append(contents, mcp.NewImageContent(errResp.Screenshot, "image/jpeg"))
		}
		return mcp.NewToolResponse(contents...), nil
	}

	// Fall back to ObservationResponse (success path).
	var obs protocol.ObservationResponse
	if err := json.Unmarshal(message, &obs); err != nil {
		return nil, fmt.Errorf("mcp: unexpected response: %s", string(message))
	}

	b64Images := obs.Visual
	obs.Visual = ""
	cleanMessage, _ := json.Marshal(obs)

	pageText := ""
	if obs.PageInfo != nil {
		pageText = fmt.Sprintf("\nPage: %s | %s", obs.PageInfo.URL, obs.PageInfo.Platform)
		if obs.PageInfo.Title != "" {
			pageText = fmt.Sprintf("\nPage: %s | %s | %s", obs.PageInfo.URL, obs.PageInfo.Title, obs.PageInfo.Platform)
		}
	}

	actionResult := ""
	if obs.ActionResult != nil {
		status := "✅"
		if !obs.ActionResult.Success {
			status = "❌"
		}
		actionResult = fmt.Sprintf("\nAction: %s %s", status, obs.ActionResult.Action)
		if obs.ActionResult.Error != "" {
			actionResult += " — " + obs.ActionResult.Error
		}
		if obs.ActionResult.ElapsedMS > 0 {
			actionResult += fmt.Sprintf(" (%dms)", obs.ActionResult.ElapsedMS)
		}
	}

	displayText := fmt.Sprintf("State: %+v%s%s\nNodes: %d", obs.SystemState, pageText, actionResult, len(obs.SpatialTree))

	contents := []*mcp.Content{
		mcp.NewTextContent(displayText),
		mcp.NewTextContent(string(cleanMessage)),
	}

	if b64Images != "" {
		contents = append(contents, mcp.NewImageContent(b64Images, "image/jpeg"))
	}

	// Also attach the element highlight screenshot if present.
	if obs.ActionResult != nil && obs.ActionResult.ElementHighlight != "" {
		contents = append(contents, mcp.NewImageContent(obs.ActionResult.ElementHighlight, "image/png"))
	}

	return mcp.NewToolResponse(contents...), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustJSON marshals v to json.RawMessage. Panics on error (should never
// happen with our well-known types).
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("mcp: mustJSON failed: %v", err))
	}
	return json.RawMessage(data)
}
