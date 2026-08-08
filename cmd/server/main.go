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
	bindFlag := flag.String("bind", "",
		"listen address host:port (default "+defaultBind+"; overrides SCRATCHPAD_BIND; non-loopback requires a token or --allow-shared-sessions)")
	tokenFlag := flag.String("token", "",
		"bearer token required on all /api, /ws, /docs, /trace_viewer routes (overrides SCRATCHPAD_TOKEN; empty = auth off)")
	corsFlag := flag.String("cors", "",
		"comma-separated list of allowed CORS origins for browser-based UIs (overrides SCRATCHPAD_CORS_ORIGINS)")
	certFile := flag.String("cert", "", "TLS certificate (PEM); requires --key to enable https")
	keyFile := flag.String("key", "", "TLS private key (PEM); requires --cert to enable https")
	allowShared := flag.Bool("allow-shared-sessions", false,
		"allow sessions to be shared across all authenticated clients and permit non-loopback binds without a token (opt-out of per-session capability isolation)")
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

	// ---- Auth, binding, and hardening (improvement-plan item 35) ----------
	token := resolveToken(*tokenFlag, os.Getenv("SCRATCHPAD_TOKEN"))

	bindAddr, err := resolveBind(*bindFlag, os.Getenv(bindEnv))
	if err != nil {
		logger.Error("Invalid bind address", "err", err)
		os.Exit(1)
	}
	if warn, err := validateBind(bindAddr, token, *allowShared); err != nil {
		logger.Error("Refusing to start", "err", err)
		os.Exit(1)
	} else if warn != "" {
		logger.Warn("Network exposure", "warning", warn)
	}

	// Per-session capability isolation: when a token is configured and sessions
	// are not explicitly shared, each session gets an owner secret returned in
	// the WS handshake / HTTP create response, and re-attach + the SSE stream
	// require it — only the creator may drive a session.
	if token != "" && !*allowShared {
		mgr.SetRequireSessionCapability(true)
		logger.Info("Per-session capability isolation enabled",
			"note", "sessions are owned by their creator; re-attach and the SSE stream require the session secret")
	} else if token != "" {
		logger.Warn("Shared sessions",
			"note", "--allow-shared-sessions set: any authenticated client may drive any session")
	}

	// Operational probes stay public so liveness/readiness and metrics scraping
	// need no credentials; everything else (docs, api, ws, trace viewer) sits
	// behind auth. CORS runs before auth so preflight OPTIONS from allowed
	// origins short-circuit without carrying a token.
	outer := http.NewServeMux()
	outer.HandleFunc("/healthz", healthzHandler(mgr))
	outer.HandleFunc("/metrics", server.MetricsHandler)
	outer.HandleFunc("/version", versionHandler)

	protected := http.NewServeMux()
	protected.HandleFunc("/docs", docs.Handler)
	protected.HandleFunc("/swagger.json", docs.Handler)
	protected.HandleFunc("/openapi.json", docs.Handler)
	protected.HandleFunc("/trace_viewer", docs.TraceViewer)
	protected.Handle("/api/v1/", http.StripPrefix("/api/v1", apiHandler))
	protected.HandleFunc("/ws", server.HandleWS(mgr, engine.KindChrome, wsOpts))
	protected.HandleFunc("/ws/android", server.HandleWS(mgr, engine.KindAndroid, wsOpts))

	// ServeMux prefers the longest matching pattern, so the three probe paths
	// resolve to their handlers and every other path falls through to the
	// authenticated, CORS-aware protected mux.
	outer.Handle("/", corsMiddleware(corsOrigins(*corsFlag, os.Getenv("SCRATCHPAD_CORS_ORIGINS")))(middleware.Auth(token, protected)))

	// Request-ID stamps every request (HTTP + WS upgrade) with a correlation id
	// echoed in X-Request-ID and threaded onto error envelopes.
	httpHandler := middleware.RequestID(outer)

	// http.Server hygiene (item 35): bounded read/idle timeouts and a header cap.
	// WriteTimeout is 0 because long-lived SSE event streams must not be killed
	// by a fixed write deadline.
	srv := &http.Server{
		Addr:           bindAddr,
		Handler:        httpHandler,
		ReadTimeout:    15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	logger.Info("Listening",
		"addr", bindAddr,
		"docs", "http://localhost/docs",
		"ws", "ws://localhost/ws",
		"ws_android", "ws://localhost/ws/android",
		"metrics", "http://localhost/metrics",
		"auth", token != "",
	)

	// TLS mode requires both --cert and --key; otherwise plain HTTP.
	if *certFile != "" || *keyFile != "" {
		if *certFile == "" || *keyFile == "" {
			logger.Error("TLS requires both --cert and --key")
			os.Exit(1)
		}
		logger.Info("TLS enabled", "cert", *certFile)
		if err := srv.ListenAndServeTLS(*certFile, *keyFile); err != nil {
			logger.Error("Server crashed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := srv.ListenAndServe(); err != nil {
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
