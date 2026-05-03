package main

import (
	"log"
	"net/http"

	// Blank imports trigger each driver's init(), registering it with engine.New.
	_ "scratchpad/internal/android"
	_ "scratchpad/internal/browser"

	"scratchpad/internal/docs"
	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"
)

func main() {
	log.Println("Starting Browser Engine Server...")

	mgr := sandbox.NewManager()

	// /docs — Swagger UI, /swagger.json — OpenAPI spec
	http.HandleFunc("/docs", docs.Handler)
	http.HandleFunc("/swagger.json", docs.Handler)

	// /ws  — Chrome CDP driver (default for web agents)
	http.HandleFunc("/ws", server.HandleWS(mgr, engine.KindChrome))

	// /ws/android — Android UIAutomator2 driver (stub, ready for the next phase)
	http.HandleFunc("/ws/android", server.HandleWS(mgr, engine.KindAndroid))

	port := ":8080"
	log.Printf("Listening on http://localhost%s/docs  (docs)", port)
	log.Printf("Listening on ws://localhost%s/ws  (chrome)", port)
	log.Printf("Listening on ws://localhost%s/ws/android  (android)", port)

	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatalf("Server crashed: %v", err)
	}
}
