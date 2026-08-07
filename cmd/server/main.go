package main

import (
	"encoding/json"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	// Blank imports trigger each driver's init(), registering it with engine.New.
	_ "scratchpad/internal/android"
	_ "scratchpad/internal/browser"

	"scratchpad/internal/api"
	"scratchpad/internal/docs"
	"scratchpad/internal/engine"
	"scratchpad/internal/middleware"
	"scratchpad/internal/sandbox"
	"scratchpad/internal/server"
)

// version is the build version, overridable via
// -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	logFormat := flag.String("log-format", "text", "log output format: json or text")
	flag.Parse()

	// Configure the process-wide structured logger.
	var logHandler slog.Handler
	if *logFormat == "json" {
		logHandler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		logHandler = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(logHandler))
	logger := slog.Default()
	logger.Info("Starting Browser Engine Server",
		"version", version,
		"go", runtime.Version(),
		"log_format", *logFormat,
	)

	mgr := sandbox.NewManager()
	apiHandler := api.NewRouter(mgr)

	// Wire session-lifecycle hooks so GET /metrics tracks session churn,
	// including sessions evicted by the idle-cleanup loop.
	mgr.SetSessionCreatedHook(func(string) { server.RecordSessionsCreated() })
	mgr.SetSessionDestroyedHook(func(string) { server.RecordSessionsDestroyed() })

	mux := http.NewServeMux()

	// /docs — Swagger UI, /swagger.json — OpenAPI spec
	mux.HandleFunc("/docs", docs.Handler)
	mux.HandleFunc("/swagger.json", docs.Handler)

	// /healthz — JSON readiness probe with per-engine session status.
	mux.HandleFunc("/healthz", healthzHandler(mgr))

	// /metrics — Prometheus text exposition of action/observe/churn/error counts.
	mux.HandleFunc("/metrics", server.MetricsHandler)

	// /version — build info.
	mux.HandleFunc("/version", versionHandler)

	// /api/v1 — Phase 0 HTTP API for session lifecycle and basic actions.
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", apiHandler))

	// /ws  — Chrome CDP driver (default for web agents)
	mux.HandleFunc("/ws", server.HandleWS(mgr, engine.KindChrome))

	// /ws/android — Android UIAutomator2 driver (stub, ready for the next phase)
	mux.HandleFunc("/ws/android", server.HandleWS(mgr, engine.KindAndroid))

	// Request-ID middleware stamps every request (HTTP + WS upgrade) with a
	// correlation id echoed in X-Request-ID and threaded onto error envelopes.
	httpHandler := middleware.RequestID(mux)

	port := ":8080"
	logger.Info("Listening",
		"port", port,
		"docs", "http://localhost"+port+"/docs",
		"ws", "ws://localhost"+port+"/ws",
		"ws_android", "ws://localhost"+port+"/ws/android",
		"metrics", "http://localhost"+port+"/metrics",
	)

	if err := http.ListenAndServe(port, httpHandler); err != nil {
		logger.Error("Server crashed", "err", err)
		os.Exit(1)
	}
}

// healthzHandler reports readiness as JSON with per-engine session counts and
// process uptime, replacing the previous bare "ok" body.
func healthzHandler(mgr *sandbox.Manager) http.HandlerFunc {
	start := time.Now()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         "ok",
			"uptime_seconds": int(time.Since(start).Seconds()),
			"version":        version,
			"sessions": map[string]any{
				"active":  mgr.ActiveCount(),
				"by_kind": mgr.ActiveCountByKind(),
			},
		})
	}
}

// versionHandler reports build version, module, and Go runtime version.
func versionHandler(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{
		"version": version,
		"go":      runtime.Version(),
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info["module"] = bi.Main.Path
		if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			info["module_version"] = bi.Main.Version
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}
