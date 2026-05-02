package main

import (
	"log"
	"net/http"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"
)

func main() {
	log.Println("Starting Browser Engine Server...")

	// 1. Boot up the headless browser
	manager := sandbox.NewManager()

	// 2. Register the WebSocket route
	http.HandleFunc("/ws", server.HandleWS(manager))

	// 3. Start the server
	port := ":8080"
	log.Printf("Listening for Agent connections on ws://localhost%s/ws", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server crashed: %v", err)
	}
}
