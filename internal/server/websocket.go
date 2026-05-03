package server

import (
	"encoding/json"
	"log"
	"net/http"

	"scratchpad/internal/browser"
	"scratchpad/internal/engine"
	"scratchpad/internal/protocol"
	"scratchpad/internal/sandbox"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// HandleWS returns an http.HandlerFunc that upgrades the connection to
// WebSocket, creates a dedicated engine session, and drives the agent loop.
func HandleWS(mgr *sandbox.Manager, kind engine.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("websocket: upgrade failed:", err)
			return
		}
		defer conn.Close()

		// Each WebSocket connection gets its own isolated engine session.
		session, err := mgr.CreateSession(kind)
		if err != nil {
			log.Println("websocket: failed to create session:", err)
			return
		}
		defer mgr.DeleteSession(session.ID)

		// Attach the Chrome console-log collector only for Chrome sessions.
		// On Android, AddListener is a no-op for CDP-specific event types,
		// so registering it is harmless but we skip it to keep intent clear.
		if kind == engine.KindChrome {
			session.Engine.AddListener(
				browser.NewConsoleLogger(&session.SessionLogs, &session.LogMu),
			)
		}

		log.Printf("websocket: agent connected — session=%s kind=%s", session.ID, session.Kind)

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				log.Printf("websocket: agent disconnected — session=%s err=%v", session.ID, err)
				break
			}

			// Try to parse as an InitializeRequest first.
			var initReq protocol.InitializeRequest
			if err := json.Unmarshal(msg, &initReq); err == nil && initReq.URL != "" {
				log.Printf("websocket: navigate — session=%s url=%s", session.ID, initReq.URL)
				if err := session.Engine.Navigate(initReq.URL); err != nil {
					log.Printf("websocket: navigate failed — session=%s err=%v", session.ID, err)
					continue
				}
				sendObservation(conn, session)
				continue
			}

			// Then try an ActionRequest.
			var actionReq protocol.ActionRequest
			if err := json.Unmarshal(msg, &actionReq); err == nil && actionReq.Action != "" {
				log.Printf("websocket: action — session=%s action=%s x=%d y=%d",
					session.ID, actionReq.Action, actionReq.X, actionReq.Y)
				if err := session.Engine.ExecuteAction(actionReq); err != nil {
					log.Printf("websocket: action failed — session=%s err=%v", session.ID, err)
					continue
				}
				sendObservation(conn, session)
				continue
			}

			log.Printf("websocket: unknown payload — session=%s", session.ID)
		}
	}
}

// sendObservation captures the current engine state and pushes it over the
// WebSocket. It sends a delta when the diff is smaller than the full tree,
// saving bandwidth for both Chrome and Android sessions.
func sendObservation(conn *websocket.Conn, session *sandbox.Session) {
	obs, err := session.Engine.Observe()
	if err != nil {
		log.Printf("websocket: observe failed — session=%s err=%v", session.ID, err)
		return
	}

	// Send a diff when it would be smaller than the full tree.
	if len(session.LastTree) > 0 {
		delta := engine.ComputeDiff(session.LastTree, obs.SpatialTree)
		if len(delta.Added)+len(delta.Updated)+len(delta.Removed) < len(obs.SpatialTree) {
			obs.Type = "delta"
			obs.Delta = delta
			obs.SpatialTree = nil
		}
	}
	session.LastTree = obs.SpatialTree

	// Drain and attach any buffered console logs (Chrome only; nil for Android).
	session.LogMu.Lock()
	obs.Logs = session.SessionLogs
	session.SessionLogs = nil
	session.LogMu.Unlock()

	if err := conn.WriteJSON(obs); err != nil {
		log.Printf("websocket: write failed — session=%s err=%v", session.ID, err)
	}
}
