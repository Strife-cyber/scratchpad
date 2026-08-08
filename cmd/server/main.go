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
	maxConcurrent := flag.Int("max-concurrent-actions", 0,
		"cap on actions executing concurrently across all sessions (0 = unlimited)")
	maxSessions := flag.Int("max-sessions", 0,
		"cap on concurrent live sessions (0 = unlimited); new sessions past the cap get a 429 session_limit_reached")
	maxActionDurationMS := flag.Int("max-action-duration-ms", 0,
		"per-session cap on how long a single action may run (ms; 0 = unlimited)")
	maxTotalSteps := flag.Int("max-total-steps", 0,
		"per-session cap on actions over the session's lifetime (0 = unlimited)")
	maxScreenshotBytes := flag.Int("max-screenshot-bytes", 0,
		"downscale observations' screenshots to this many encoded bytes (0 = unlimited)")
	observeThrottleMS := flag.Int("observe-throttle-ms", 0,
		"minimum spacing between observations on a session (ms; 0 = unlimited)")
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
	if *maxSessions > 0 {
		mgr.SetMaxSessions(*maxSessions)
		logger.Info("Max sessions",
			"max", *maxSessions,
		)
	}
	// Per-session guardrails: env defaults (SCRATCHPAD_*) overridden by any
	// explicit --max-* / --observe-throttle-ms flags.
	limits := mgr.Limits()
	if *maxActionDurationMS > 0 {
		limits.MaxActionDuration = time.Duration(*maxActionDurationMS) * time.Millisecond
	}
	if *maxTotalSteps > 0 {
		limits.MaxTotalSteps = *maxTotalSteps
	}
	if *maxScreenshotBytes > 0 {
		limits.MaxScreenshotBytes = *maxScreenshotBytes
	}
	if *observeThrottleMS > 0 {
		limits.ObserveThrottle = time.Duration(*observeThrottleMS) * time.Millisecond
	}
	mgr.SetLimits(limits)
	if limits.MaxActionDuration > 0 || limits.MaxTotalSteps > 0 || limits.MaxScreenshotBytes > 0 || limits.ObserveThrottle > 0 {
		logger.Info("Per-session limits",
			"max_action_duration_ms", limits.MaxActionDuration.Milliseconds(),
			"max_total_steps", limits.MaxTotalSteps,
			"max_screenshot_bytes", limits.MaxScreenshotBytes,
			"observe_throttle_ms", limits.ObserveThrottle.Milliseconds(),
		)
	}
	apiHandler := api.NewRouter(mgr)

	// Wire session-lifecycle hooks so GET /metrics tracks session churn,
	// including sessions evicted by the idle-cleanup loop.
	mgr.SetSessionCreatedHook(func(string) { server.RecordSessionsCreated() })
	mgr.SetSessionDestroyedHook(func(string) { server.RecordSessionsDestroyed() })

	// Server-wide action concurrency cap (item 33.4). nil means unlimited.
	// Actions queue at the session executor while waiting for a slot; the
	// reader goroutine is never blocked, so cancel/resize stay responsive even
	// when the pool is saturated.
	wsOpts := server.Options{}
	if *maxConcurrent > 0 {
		wsOpts.Concurrency = server.NewConcurrency(*maxConcurrent)
		logger.Info("Max concurrent actions",
			"max", *maxConcurrent,
		)
	}

	mux := http.NewServeMux()

	// /docs — Swagger UI; /swagger.json and /openapi.json — OpenAPI spec
	mux.HandleFunc("/docs", docs.Handler)
	mux.HandleFunc("/swagger.json", docs.Handler)
	mux.HandleFunc("/openapi.json", docs.Handler)

	// /trace_viewer — self-contained .spz trace viewer (improvement-plan item 24)
	mux.HandleFunc("/trace_viewer", docs.TraceViewer)

	// /healthz — JSON readiness probe with per-engine session status.
	mux.HandleFunc("/healthz", healthzHandler(mgr))

	// /metrics — Prometheus text exposition of action/observe/churn/error counts.
	mux.HandleFunc("/metrics", server.MetricsHandler)

	// /version — build info.
	mux.HandleFunc("/version", versionHandler)

	// /api/v1 — Phase 0 HTTP API for session lifecycle and basic actions.
	mux.Handle("/api/v1/", http.StripPrefix("/api/v1", apiHandler))

	// /ws  — Chrome CDP driver (default for web agents)
	mux.HandleFunc("/ws", server.HandleWS(mgr, engine.KindChrome, wsOpts))

	// /ws/android — Android UIAutomator2 driver (stub, ready for the next phase)
	mux.HandleFunc("/ws/android", server.HandleWS(mgr, engine.KindAndroid, wsOpts))

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
