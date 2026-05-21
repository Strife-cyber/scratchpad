package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"scratchpad/internal/protocol"

	"github.com/gorilla/websocket"
	mcp "github.com/metoro-io/mcp-golang"
)

type Server struct {
	conn *websocket.Conn
	// sessionID is provided by the engine server as the first WS message.
	sessionID string
}

func NewMcpServer(engineURL string) (*Server, error) {
	conn, _, err := websocket.DefaultDialer.Dial(engineURL, nil)
	if err != nil {
		return nil, err
	}

	// MCP bridge handshake: first message is expected to be {"sessionId":"..."}.
	var handshake struct {
		SessionID string `json:"sessionId"`
	}
	_, message, err := conn.ReadMessage()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: handshake failed: %w", err)
	}
	if err := json.Unmarshal(message, &handshake); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("mcp: handshake parse failed: %w", err)
	}

	if handshake.SessionID != "" {
		log.Printf("mcp: connected to session %s", handshake.SessionID)
	} else {
		log.Printf("mcp: connected (sessionId missing)")
	}

	return &Server{conn: conn, sessionID: handshake.SessionID}, nil
}

// NavigateArgs Defines explicit named structs for the reflection library
type NavigateArgs struct {
	URL string `json:"url"`
}

type ObserveArgs struct {
	// Empty struct is fine, but adding a dummy field prevents rare reflection bugs
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

func (s *Server) RegisterTools(srv *mcp.Server) {
	// 1. Tool: Navigate (Note the added ctx context.Context)
	err := srv.RegisterTool("browser_navigate", "Loads a URL into the browser", func(ctx context.Context, args NavigateArgs) (*mcp.ToolResponse, error) {
		req := protocol.InitializeRequest{
			URL:      args.URL,
			Viewport: protocol.Viewport{},
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register navigate: %v\n", err)
	}

	// 2. Tool: Observe (Note the added ctx context.Context)
	err = srv.RegisterTool("browser_observe", "Captures the current page state", func(ctx context.Context, args ObserveArgs) (*mcp.ToolResponse, error) {
		if err := s.conn.WriteMessage(websocket.TextMessage, []byte("{}")); err != nil {
			return nil, err
		}
		return s.readObservation()
	})
	if err != nil {
		fmt.Printf("Failed to register observe: %v\n", err)
	}

	// 3. Tool: Action (Note the added ctx context.Context)
	err = srv.RegisterTool("browser_action", "Interact with the page", func(ctx context.Context, args protocol.ActionRequest) (*mcp.ToolResponse, error) {
		return s.requestAction(args)
	})
	if err != nil {
		fmt.Printf("Failed to register action: %v\n", err)
	}

	// 4. Tool: Assert
	err = srv.RegisterTool("browser_assert", "Assert page state (selectors/text/attributes/screenshot)", func(ctx context.Context, args AssertArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:    "assert",
			Assertion: &args.Assertion,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register assert tool: %v\n", err)
	}

	// 5. Tool: List Tabs
	err = srv.RegisterTool("browser_list_tabs", "List all open browser tabs", func(ctx context.Context, args ObserveArgs) (*mcp.ToolResponse, error) {
		if err := s.conn.WriteMessage(websocket.TextMessage, []byte("{}")); err != nil {
			return nil, err
		}
		return s.readObservation()
	})
	if err != nil {
		fmt.Printf("Failed to register list_tabs tool: %v\n", err)
	}

	// 6. Tool: Switch Tab
	err = srv.RegisterTool("browser_switch_tab", "Switch to a different browser tab by ID from browser_list_tabs", func(ctx context.Context, args SwitchTabArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action: protocol.ActionSwitchTab,
			TabID:  args.TabID,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register switch_tab tool: %v\n", err)
	}

	// 7. Tool: Close Tab
	err = srv.RegisterTool("browser_close_tab", "Close a browser tab by ID from browser_list_tabs", func(ctx context.Context, args CloseTabArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action: protocol.ActionCloseTab,
			TabID:  args.TabID,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register close_tab tool: %v\n", err)
	}

	// 8. Tool: Dismiss Modal
	err = srv.RegisterTool("browser_dismiss_modal", "Dismiss modal dialogs, popups, cookie banners, or overlays", func(ctx context.Context, args DismissModalArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:        protocol.ActionDismissModal,
			ModalStrategy: args.Strategy,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register dismiss_modal tool: %v\n", err)
	}

	// 9. Tool: Check
	err = srv.RegisterTool("browser_check", "Check a checkbox or radio button", func(ctx context.Context, args CheckArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionCheck,
			Selector: &args.Selector,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register check tool: %v\n", err)
	}

	// 10. Tool: Uncheck
	err = srv.RegisterTool("browser_uncheck", "Uncheck a checkbox or radio button", func(ctx context.Context, args UncheckArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionUncheck,
			Selector: &args.Selector,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register uncheck tool: %v\n", err)
	}

	// 11. Tool: Submit Form
	err = srv.RegisterTool("browser_submit_form", "Submit a form by selector (CSS of form or child element)", func(ctx context.Context, args SubmitFormArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:   protocol.ActionSubmitForm,
			Selector: &args.Selector,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register submit_form tool: %v\n", err)
	}

	// 12. Tool: Fill Form
	err = srv.RegisterTool("browser_fill_form", "Fill multiple form fields at once", func(ctx context.Context, args FillFormArgs) (*mcp.ToolResponse, error) {
		req := protocol.ActionRequest{
			Action:     protocol.ActionFillForm,
			FormFields: args.Fields,
		}
		return s.requestAction(req)
	})
	if err != nil {
		fmt.Printf("Failed to register fill_form tool: %v\n", err)
	}
}

func (s *Server) requestAction(req interface{}) (*mcp.ToolResponse, error) {
	data, _ := json.Marshal(req)
	if err := s.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, err
	}
	return s.readObservation()
}

func (s *Server) readObservation() (*mcp.ToolResponse, error) {
	_, message, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}

	var obs protocol.ObservationResponse
	if err := json.Unmarshal(message, &obs); err != nil {
		return nil, err
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
	displayText := fmt.Sprintf("State: %+v%s\nNodes: %d", obs.SystemState, pageText, len(obs.SpatialTree))

	if b64Images != "" {
		return mcp.NewToolResponse(
			mcp.NewTextContent(displayText),
			mcp.NewTextContent(string(cleanMessage)),
			mcp.NewImageContent(b64Images, "image/jpeg")), nil
	}

	return mcp.NewToolResponse(
		mcp.NewTextContent(displayText),
		mcp.NewTextContent(string(cleanMessage)),
	), nil
}
