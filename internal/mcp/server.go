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

	displayText := fmt.Sprintf("State: %+v\nNodes: %d", obs.SystemState, len(obs.SpatialTree))

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
