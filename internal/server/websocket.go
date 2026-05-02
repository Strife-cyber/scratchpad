package server

import (
	"encoding/json"
	"log"
	"net/http"
	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"

	"github.com/gorilla/websocket"
)

// Upgrader upgrades HTTP connections to WebSockets
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandleWS creates a closure that holds our engine instance and handle WS traffic
func HandleWS(mgr *sandbox.Manager) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		conn, err := upgrader.Upgrade(writer, request, nil)

		if err != nil {
			log.Println("Websocket upgrade failed:", err)
			return
		}
		defer func(conn *websocket.Conn) {
			err := conn.Close()
			if err != nil {
				log.Println("Websocket close failed:", err)
			}
		}(conn)

		// Create a dedicated session for this connection
		session, err := mgr.CreateSession()
		if err != nil {
			log.Println("Failed to create session:", err)
			return
		}
		defer mgr.DeleteSession(session.ID)

		// Tell the engine to send events to this session's log collector
		logHandler := browser.NewConsoleLogger(&session.SessionLogs, &session.LogMu)
		session.Engine.AddListener(logHandler)

		// Start the engine's dispatcher if not already started
		session.Engine.SetupEventDispatcher()

		log.Printf("Agent connected. Assigned Session: %s\n", session.ID)

		// The infinite message loop
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("Agent disconnected or read error:", err)
				break
			}

			// 1. Check if the message is an InitializeRequest
			var initReq protocol.InitializeRequest
			if err := json.Unmarshal(message, &initReq); err == nil && initReq.URL != "" {
				log.Printf("Initializing session for URL: %s", initReq.URL)

				if err := session.Engine.Navigate(initReq.URL); err != nil {
					log.Printf("Navigation failed: %v", err)
					continue
				}

				sendObservation(conn, session)
				continue
			}

			// 2. Check if the message is an ActionRequest
			var actionReq protocol.ActionRequest
			if err := json.Unmarshal(message, &actionReq); err == nil && actionReq.Action != "" {
				log.Printf("Executing AI Action: %s at X: %d, Y: %d", actionReq.Action, actionReq.X, actionReq.Y)

				if err := session.Engine.ExecuteAction(actionReq); err != nil {
					log.Printf("Action execution failed: %v", err)
					continue
				}

				// Send the new state back after the action completes
				sendObservation(conn, session)
				continue
			}

			log.Println("Received unknown or malformed payload.")
		}
	}
}

// sendObservation is a helper to grab the engine state and push it over the socket.
func sendObservation(conn *websocket.Conn, session *sandbox.Session) {
	newObs, err := session.Engine.Observe()
	if err != nil {
		log.Printf("Failed to observe state: %v", err)
		return
	}

	if len(session.LastTree) > 0 {
		delta := browser.ComputeDiff(session.LastTree, newObs.SpatialTree)

		// If the delta is significantly smaller than the full tree
		if len(delta.Added)+len(delta.Updated)+len(delta.Removed) < len(newObs.SpatialTree) {
			newObs.Type = "delta"
			newObs.Delta = delta
			newObs.SpatialTree = nil // Wipe the full tree to save tokens/bandwidth
		}
	}

	session.LastTree = newObs.SpatialTree

	session.LogMu.Lock()
	newObs.Logs = session.SessionLogs
	session.SessionLogs = []protocol.ConsoleLog{}
	session.LogMu.Unlock()

	if err := conn.WriteJSON(newObs); err != nil {
		log.Printf("Failed to send observation to the agent: %v", err)
	}
}
