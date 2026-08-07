package main

import (
	"log"
	"net/http"

	// Blank imports trigger each driver's init(), registering it with engine.New.
	_ "scratchpad/internal/android"
	_ "scratchpad/internal/browser"

	"scratchpad/internal/api"
	"scratchpad/internal/docs"
	"scratchpad/internal/engine"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"
)

func main() {
	log.Println("Starting Browser Engine Server...")

	mgr := sandbox.NewManager()
	apiHandler := api.NewRouter(mgr)

	// /docs — Swagger UI, /swagger.json — OpenAPI spec
	http.HandleFunc("/docs", docs.Handler)
	http.HandleFunc("/swagger.json", docs.Handler)

	// /healthz — lightweight readiness probe
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	// /api/v1 — Phase 0 HTTP API for session lifecycle and basic actions.
	http.Handle("/api/v1/", http.StripPrefix("/api/v1", apiHandler))

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
