// Package httpapi implements the Apprentice Proxy HTTP server.  The proxy
// exposes:
//
//	GET  /healthz                  liveness check
//	GET  /stats                    latency percentiles (p50/p99)
//	GET  /metrics                  Prometheus metrics
// POST /v1/chat/completions      OpenAI-compatible chat-completions endpoint
//                                that routes to a specialist when the last
//                                user message embeds close to a registered
//                                pattern, and otherwise proxies to upstream.
//                                Supports canary %-ramp for progressive
//                                specialist rollout.
// GET  /canary/state             list all canary ramp states.
// GET  /canary/state/{id}        get canary state for one pattern.
// POST /canary/advance           record agreement score and advance ramp.
// POST /canary/set-state         set canary state directly.
// POST /canary/compare           compare two response bodies for agreement.
//	POST /patterns                 register or replace a pattern (called by
//	                                the detector after operator approval).
//	GET  /patterns                 list registered patterns.
//	GET  /api/cost/roi             ROI summary for all patterns.
//	GET  /api/cost/roi/{pattern_id} ROI summary for one pattern.
//	GET  /api/cost/usage           usage-over-time buckets (query: pattern_id, bucket).
//	GET  /api/cost/latency         latency stats for specialist vs upstream.
//	GET  /api/cost/runpod          live RunPod pod costs (requires --runpod-api-key).
//	GET  /dashboard                cost/ROI dashboard (Vue.js + Chart.js).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/eschmechel/hermes-apprentice/proxy/internal/alias"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/canary"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/patterns"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/ratelimit"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/runpod"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/tenant"
)

// Embedder is the minimal surface the proxy needs from the BGE-small
// embedder.  Defined here (consumer side) so tests can inject a fake without
// pulling in ONNX runtime.
type Embedder interface {
	Embed(text string) ([]float32, error)
}

// CostEstimator provides per-model cost estimates from token counts.
// The proxy is unaware of pricing specifics — it only calls ComputeCost.
type CostEstimator interface {
	ComputeCost(model string, promptTokens, completionTokens int) float64
}

type Config struct {
	Addr   string
	Logger *slog.Logger

	UpstreamURL    string
	Embedder       Embedder
	PatternStore   *patterns.Store
	MatchThreshold float32
	ShadowRate     float64

	// CanaryManager drives the canary %-ramp state machine for progressive
	// specialist rollout. When nil, canary is disabled and all matched patterns
	// route at 100%.
	CanaryManager *canary.Manager

	// Breaker is a per-pattern circuit breaker on the specialist path (W7).
	// After BreakerThreshold consecutive failures (errors, bad responses, or
	// failed output validation), matched requests skip the specialist and
	// fall through to upstream for BreakerCooldown. Nil disables it.
	Breaker          *CircuitBreaker
	BreakerThreshold int           // ignored when Breaker is set
	BreakerCooldown  time.Duration // ignored when Breaker is set

	// ServeURL, when set, enables multi-LoRA routing: a matched request is
	// routed by adapter name (the pattern id) to this single warm vLLM server
	// instead of each pattern's specialist_url. ResidencyURL is the residency
	// control plane the proxy calls to ensure the adapter is resident first.
	ServeURL     string
	ResidencyURL string

	// HTTPClient is optional; if nil, a default client with a long timeout is
	// constructed.  Tests inject a client pointing at httptest.Server.
	HTTPClient *http.Client

	// LatencyTracker tracks p50/p99 for specialist vs upstream.
	// When nil, latency stats and /stats endpoint are disabled.
	LatencyTracker *LatencyTracker

	// Pricing provides per-model cost estimates.  When nil, cost fields
	// are omitted from request log lines.
	Pricing CostEstimator

	// Metrics provides an optional Prometheus metrics recorder.
	// When nil, metrics are not recorded.
	Metrics *Metrics

	// StateDir is the proxy state directory (used by cost API to locate
	// ledger.jsonl and proxy.log). When empty, cost endpoints are disabled.
	StateDir string

	// CostDir is the directory containing ledger.jsonl.
	// Defaults to filepath.Dir(StateDir)/cost when empty.
	CostDir string

	// RegistryRoot is the Apprentice model registry directory.
	// Defaults to filepath.Dir(StateDir)/registry when empty.
	RegistryRoot string

	// RunPodClient provides live RunPod pod cost data for /api/cost/runpod.
	// When nil, the endpoint returns 503 (not configured).
	RunPodClient *runpod.Client

	// AliasStore resolves merged-pattern aliases. When nil, alias resolution
	// is skipped and all matched patterns route by their own ID.
	AliasStore *alias.Store

	// TenantStore validates X-Apprentice-Tenant + X-Apprentice-Key headers.
	// When nil, auth is disabled and all requests are treated as "global".
	TenantStore *tenant.Store

	// RateLimiter enforces per-tenant request rate limits.
	// When nil, rate limiting is disabled.
	RateLimiter *ratelimit.Limiter
}

type Server struct {
	cfg    Config
	srv    *http.Server
	logger *slog.Logger
}

func New(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 120 * time.Second}
	}
	if cfg.MatchThreshold == 0 {
		cfg.MatchThreshold = 0.78
	}
	if cfg.CostDir == "" && cfg.StateDir != "" {
		cfg.CostDir = filepath.Dir(cfg.StateDir) + "/cost"
	}
	if cfg.RegistryRoot == "" && cfg.StateDir != "" {
		cfg.RegistryRoot = filepath.Dir(cfg.StateDir) + "/registry"
	}

	mux := http.NewServeMux()
	s := &Server{cfg: cfg, logger: cfg.Logger}

	// Routes that do NOT require tenant auth.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Build auth-wrapped handler for authenticated routes.
	var handler http.Handler = mux
	if cfg.TenantStore != nil {
		ah := newAuthHandler(cfg.TenantStore, cfg.Logger)
		handler = ah.wrap(mux)
	}

	stats := newStatsHandler(cfg.LatencyTracker)
	mux.HandleFunc("GET /stats", stats.handleStats)

	// Build the per-pattern circuit breaker if the caller didn't supply one.
	if cfg.Breaker == nil && cfg.BreakerThreshold > 0 {
		cd := cfg.BreakerCooldown
		if cd <= 0 {
			cd = 30 * time.Second
		}
		cfg.Breaker = NewCircuitBreaker(cfg.BreakerThreshold, cd)
	}

	ph := newProxyHandler(cfg)
	mux.HandleFunc("POST /v1/chat/completions", ph.handleChatCompletions)

	pat := newPatternsHandler(cfg.PatternStore, cfg.Logger)
	mux.HandleFunc("POST /patterns", pat.handleRegister)
	mux.HandleFunc("GET /patterns", pat.handleList)

	if cfg.AliasStore != nil {
		ah := alias.NewHandler(cfg.AliasStore)
		ah.RegisterRoutes(mux)
	}

	if cfg.CanaryManager != nil {
		ch := canary.NewHandler(cfg.CanaryManager)
		ch.RegisterRoutes(mux)
	}

	if cfg.StateDir != "" {
		ch := newCostHandler(cfg.StateDir, cfg.CostDir, cfg.Logger, cfg.RunPodClient)
		mux.HandleFunc("GET /api/cost/roi", ch.handleROI)
		mux.HandleFunc("GET /api/cost/roi/{pattern_id}", ch.handleROI)
		mux.HandleFunc("GET /api/cost/usage", ch.handleUsage)
		mux.HandleFunc("GET /api/cost/latency", ch.handleLatency)
		mux.HandleFunc("GET /api/cost/runpod", ch.handleRunPod)
		mux.HandleFunc("GET /dashboard", s.handleDashboard)

		rh := newRegistryHandler(cfg.RegistryRoot, cfg.Logger)
		mux.HandleFunc("GET /api/registry/{pattern_id}/latest", rh.handleLatest)
	}

	if cfg.Metrics != nil {
		mux.HandleFunc("GET /metrics", cfg.Metrics.Handler().ServeHTTP)
	}

	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	s.logger.Info("http listening", "addr", s.cfg.Addr)

	go func() {
		<-ctx.Done()
		s.logger.Info("shutting down http server")
		_ = s.Shutdown(context.Background())
	}()

	err := s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(shutdownCtx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
