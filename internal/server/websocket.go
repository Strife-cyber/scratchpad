package server

import (
	"encoding/json"
	"log"
	"net/http"
	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"

	"github.com/gorilla/websocket"
)

// Upgrader upgrades HTTP connections to WebSockets
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandleWS creates a closure that holds our engine instance and handle WS traffic
func HandleWS(engine *browser.Engine) http.HandlerFunc {
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
		log.Println("Agent connected to WebSocket!")

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

				if err := engine.Navigate(initReq.URL); err != nil {
					log.Printf("Navigation failed: %v", err)
					continue
				}

				sendObservation(conn, engine)
				continue
			}

			// 2. Check if the message is an ActionRequest
			var actionReq protocol.ActionRequest
			if err := json.Unmarshal(message, &actionReq); err == nil && actionReq.Action != "" {
				log.Printf("Executing AI Action: %s at X: %d, Y: %d", actionReq.Action, actionReq.X, actionReq.Y)

				if err := engine.ExecuteAction(actionReq); err != nil {
					log.Printf("Action execution failed: %v", err)
					continue
				}

				// Send the new state back after the action completes
				sendObservation(conn, engine)
				continue
			}

			log.Println("Received unknown or malformed payload.")
		}
	}
}

// sendObservation is a helper to grab the engine state and push it over the socket.
func sendObservation(conn *websocket.Conn, engine *browser.Engine) {
	obs, err := engine.Observe()
	if err != nil {
		log.Printf("Failed to observe state: %v", err)
		return
	}

	if err := conn.WriteJSON(obs); err != nil {
		log.Printf("Failed to send observation to the agent: %v", err)
	}
}
