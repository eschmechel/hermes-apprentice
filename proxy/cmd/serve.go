package cmd

import (
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/eschmechel/hermes-apprentice/proxy/internal/alias"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/canary"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/cost"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/embedder"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/httpapi"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/patterns"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/ratelimit"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/runpod"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/tenant"
	"github.com/spf13/cobra"
)

func serveCmd() *cobra.Command {
	var (
		listenAddr     string
		upstreamURL    string
		serveURL       string
		residencyURL   string
		stateDir       string
		modelDir       string
		ortLibPath     string
		matchThreshold float64
		shadowRate     float64
		logFile             string
		runpodAPIKey        string
		canaryStateDir      string
		canaryRampStart     int
		canaryRampStep      int
		canaryStepRequests  int
		canaryAgreeThresh   float64
		tenantRoot          string
		globalAPIKey        string
		tenantRPM           int
	)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the proxy server.",
		Long: `Start the Apprentice Proxy HTTP server.

The proxy embeds the last user message of every POST /v1/chat/completions request
and cosine-matches it against patterns registered via POST /patterns.  Matches
above --match-threshold route to the pattern's specialist_url; non-matches and
specialist failures fall through to --upstream-url.

Hermes integration: configure your Hermes profile's model_url to the proxy's
listen address (e.g. http://localhost:8083/v1).  The proxy speaks the OpenAI
chat-completions schema, so request and response shapes are unchanged.`,
		RunE: func(c *cobra.Command, _ []string) error {
			var logWriter io.Writer = os.Stderr
			if logFile != "" {
				fh, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
				if err != nil {
					return err
				}
				defer fh.Close()
				logWriter = io.MultiWriter(os.Stderr, fh)
			}
			logger := slog.New(slog.NewJSONHandler(logWriter, nil))
			logger.Info("proxy starting",
				"listen", listenAddr,
				"upstream_url", upstreamURL,
				"state_dir", stateDir,
				"model_dir", modelDir,
				"match_threshold", matchThreshold,
				"shadow_rate", shadowRate,
				"version", Version,
			)
			ctx, stop := signal.NotifyContext(c.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			store, err := patterns.Open(filepath.Join(stateDir, "patterns.json"))
			if err != nil {
				return err
			}

			var emb *embedder.Embedder
			modelPath := filepath.Join(modelDir, "model.onnx")
			vocabPath := filepath.Join(modelDir, "vocab.json")
			if _, statErr := os.Stat(modelPath); statErr == nil {
				if ortLibPath != "" {
					ort.SetSharedLibraryPath(ortLibPath)
				}
				if initErr := ort.InitializeEnvironment(); initErr != nil {
					logger.Warn("onnx environment init failed; embedding disabled", "err", initErr)
				} else {
					emb, err = embedder.New(modelPath, vocabPath)
					if err != nil {
						logger.Warn("embedder init failed; embedding disabled", "err", err)
						emb = nil
					}
				}
			} else {
				logger.Warn("BGE-small model not found; all requests pass through to upstream",
					"expected_path", modelPath)
			}
			if emb != nil {
				defer emb.Close()
			}

			latencyTracker := httpapi.NewLatencyTracker()

			pricing, loadErr := cost.LoadFile(
				filepath.Join(stateDir, "pricing.json"),
			)
			if loadErr != nil {
				logger.Warn("pricing config load failed; using defaults", "err", loadErr)
				pricing = cost.New(nil)
			}

			metrics := httpapi.NewMetrics()

			var cm *canary.Manager
			if canaryStateDir != "" {
				canaryCfg := canary.Config{
					StartPct:        canaryRampStart,
					StepPct:         canaryRampStep,
					StepRequests:    canaryStepRequests,
					AgreementThresh: canaryAgreeThresh,
				}
				cm, err = canary.New(filepath.Join(canaryStateDir, "canary.json"), canaryCfg)
				if err != nil {
					logger.Warn("canary manager init failed; canary disabled", "err", err)
					cm = nil
				} else {
					logger.Info("canary manager loaded",
						"start_pct", canaryRampStart,
						"step_pct", canaryRampStep,
						"step_requests", canaryStepRequests,
						"agreement_threshold", canaryAgreeThresh,
					)
				}
			}

			var rpClient *runpod.Client
			if runpodAPIKey != "" {
				rpClient = runpod.New(runpodAPIKey)
				if err := rpClient.Ping(c.Context()); err != nil {
					logger.Warn("RunPod API key set but ping failed; /api/cost/runpod may error",
						"err", err)
				} else {
					logger.Info("RunPod client connected")
				}
			}

			aliasStore, err := alias.Open(filepath.Join(stateDir, "aliases.json"))
			if err != nil {
				logger.Warn("alias store init failed; alias resolution disabled", "err", err)
				aliasStore = nil
			}

			var ts *tenant.Store
			if tenantRoot != "" {
				ts, err = tenant.Open(tenant.Config{TenantRoot: tenantRoot, GlobalKey: globalAPIKey})
				if err != nil {
					logger.Warn("tenant store init failed; auth disabled", "err", err)
					ts = nil
				} else {
					logger.Info("tenant auth enabled", "tenant_root", tenantRoot)
				}
			}

			var rl *ratelimit.Limiter
			if tenantRPM > 0 {
				rl = ratelimit.New(tenantRPM)
				logger.Info("rate limiting enabled", "rpm", tenantRPM)
			}

			srv := httpapi.New(httpapi.Config{
				Addr:           listenAddr,
				Logger:         logger,
				UpstreamURL:    upstreamURL,
				ServeURL:       serveURL,
				ResidencyURL:   residencyURL,
				Embedder:       emb,
				PatternStore:   store,
				MatchThreshold: float32(matchThreshold),
				ShadowRate:     shadowRate,
				AliasStore:     aliasStore,
				CanaryManager:  cm,
				LatencyTracker: latencyTracker,
				Pricing:        pricing,
				Metrics:        metrics,
				StateDir:       stateDir,
				RunPodClient:   rpClient,
				TenantStore:    ts,
				RateLimiter:    rl,
			})

			return srv.ListenAndServe(ctx)
		},
	}
	cmd.Flags().StringVar(&listenAddr, "listen", ":8083", "HTTP listen address")
	cmd.Flags().StringVar(&upstreamURL, "upstream-url", "https://openrouter.ai/api/v1", "Upstream OpenAI-compatible base URL (used for fallback and non-matching requests)")
	cmd.Flags().StringVar(&serveURL, "serve-url", "", "Multi-LoRA: single warm vLLM base URL; matches route here by adapter name (empty = legacy per-pattern specialist_url)")
	cmd.Flags().StringVar(&residencyURL, "residency-url", "", "Residency control plane URL (apprentice-serve-control); proxy ensures the adapter is resident before routing")
	cmd.Flags().StringVar(&stateDir, "state-dir", os.ExpandEnv("$HOME/.apprentice/proxy"), "Proxy state directory (patterns.json, pricing.json)")
	cmd.Flags().StringVar(&modelDir, "model-dir", os.ExpandEnv("$HOME/.apprentice/models/bge-small-onnx"), "Directory containing BGE-small ONNX model.onnx + vocab.json")
	cmd.Flags().StringVar(&ortLibPath, "onnxruntime-lib", "/usr/lib/libonnxruntime.so", "Path to libonnxruntime.so (empty = use ORT default search)")
	cmd.Flags().Float64Var(&matchThreshold, "match-threshold", 0.78, "Minimum cosine similarity to consider a pattern matched")
	cmd.Flags().Float64Var(&shadowRate, "shadow-rate", 0.05, "Fraction of matched requests to also send to upstream for shadow comparison (0..1)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Write JSON request log to file in addition to stderr (default: no file)")
	cmd.Flags().StringVar(&runpodAPIKey, "runpod-api-key", "", "RunPod API key for live pod cost tracking (enables /api/cost/runpod)")
	cmd.Flags().StringVar(&canaryStateDir, "canary-state-dir", stateDir, "Canary state directory (defaults to --state-dir)")
	cmd.Flags().IntVar(&canaryRampStart, "canary-ramp-start", 5, "Canary ramp starting percentage (1-50)")
	cmd.Flags().IntVar(&canaryRampStep, "canary-ramp-step", 10, "Canary ramp step percentage increment")
	cmd.Flags().IntVar(&canaryStepRequests, "canary-ramp-step-requests", 50, "Requests per canary step before evaluating")
	cmd.Flags().Float64Var(&canaryAgreeThresh, "canary-agreement-threshold", 0.8, "Minimum agreement ratio for canary advancement (0..1)")
	cmd.Flags().StringVar(&tenantRoot, "tenant-root", "", "Tenant root directory (~/.apprentice/tenants); empty = auth disabled")
	cmd.Flags().StringVar(&globalAPIKey, "global-api-key", "", "Admin API key for global pattern management")
	cmd.Flags().IntVar(&tenantRPM, "tenant-ratelimit-rpm", 0, "Per-tenant rate limit in requests/minute (0 = disabled)")
	return cmd
}
