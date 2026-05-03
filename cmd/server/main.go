package main

import (
	"log"
	"net/http"

	// Blank imports trigger each driver's init(), registering it with engine.New.
	_ "scratchpad/internal/android"
	_ "scratchpad/internal/browser"

	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"
)

func main() {
	log.Println("Starting Browser Engine Server...")

	mgr := sandbox.NewManager()

	// /ws  — Chrome CDP driver (default for web agents)
	http.HandleFunc("/ws", server.HandleWS(mgr, engine.KindChrome))

	// /ws/android — Android UIAutomator2 driver (stub, ready for the next phase)
	http.HandleFunc("/ws/android", server.HandleWS(mgr, engine.KindAndroid))

	port := ":8080"
	log.Printf("Listening on ws://localhost%s/ws  (chrome)", port)
	log.Printf("Listening on ws://localhost%s/ws/android  (android stub)", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server crashed: %v", err)
	}
}
