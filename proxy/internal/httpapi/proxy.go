package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/eschmechel/hermes-apprentice/proxy/internal/alias"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/canary"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/patterns"
	"github.com/eschmechel/hermes-apprentice/proxy/internal/ratelimit"
)

type proxyHandler struct {
	upstreamURL    string
	serveURL       string
	residencyURL   string
	client         *http.Client
	embedder       Embedder
	store          *patterns.Store
	matchThreshold float32
	shadowRate     float64
	logger         *slog.Logger

	aliasStore     *alias.Store
	canaryManager  *canary.Manager
	latencyTracker *LatencyTracker
	pricing        CostEstimator
	metrics        *Metrics
	rateLimiter    *ratelimit.Limiter

	rand func() float64
}

func newProxyHandler(cfg Config) *proxyHandler {
	return &proxyHandler{
		upstreamURL:    strings.TrimRight(cfg.UpstreamURL, "/"),
		serveURL:       strings.TrimRight(cfg.ServeURL, "/"),
		residencyURL:   strings.TrimRight(cfg.ResidencyURL, "/"),
		client:         cfg.HTTPClient,
		embedder:       cfg.Embedder,
		store:          cfg.PatternStore,
		matchThreshold: cfg.MatchThreshold,
		shadowRate:     cfg.ShadowRate,
		aliasStore:     cfg.AliasStore,
		canaryManager:  cfg.CanaryManager,
		logger:         cfg.Logger,
		latencyTracker: cfg.LatencyTracker,
		pricing:        cfg.Pricing,
		metrics:        cfg.Metrics,
		rateLimiter:    cfg.RateLimiter,
		rand:           rand.Float64,
	}
}

type chatRequestPeek struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

type chatResponseUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (h *proxyHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	// Enforce per-tenant rate limit.
	if h.rateLimiter != nil {
		tenantID := TenantFromContext(r.Context())
		if !h.rateLimiter.Allow(tenantID) {
			h.logger.Warn("rate limit exceeded", "tenant", tenantID)
			writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
	}

	peek, peekErr := peekChat(body)
	if peekErr != nil {
		h.forwardToUpstream(w, r, body, "peek_failed", "", false)
		return
	}

	lastUser := lastUserText(peek)
	routed, reason := h.tryRouteToSpecialist(w, r, body, peek, lastUser)
	if !routed {
		isFallback := strings.HasPrefix(reason, "specialist_")
		h.forwardToUpstream(w, r, body, reason, peek.Model, isFallback)
	}
}

func (h *proxyHandler) tryRouteToSpecialist(w http.ResponseWriter, r *http.Request, body []byte, peek chatRequestPeek, lastUser string) (bool, string) {
	if h.embedder == nil || h.store == nil || lastUser == "" {
		return false, "no_embedder_or_store"
	}
	emb, err := h.embedder.Embed(lastUser)
	if err != nil {
		h.logger.Warn("embed failed; falling back to upstream", "err", err)
		return false, "embed_failed"
	}
	tenantID := TenantFromContext(r.Context())
	match, ok := h.store.BestMatchTenant(emb, h.matchThreshold, tenantID)
	if !ok {
		return false, "no_match"
	}

	// Resolve aliases — if the matched pattern has been merged into a
	// consolidated pattern, route to the merged target instead.
	patternID := match.Pattern.ID
	if h.aliasStore != nil {
		if targetID, ok := h.aliasStore.Resolve(match.Pattern.ID); ok {
			patternID = targetID
			h.logger.Debug("alias resolved",
				"from", match.Pattern.ID, "to", targetID)
		}
	}

	// Check canary decision for this pattern.
	canaryRoute, canaryManaged := false, false
	if h.canaryManager != nil {
		canaryRoute, canaryManaged = h.canaryManager.Decision(patternID)
		if canaryManaged && !canaryRoute {
			h.logger.Debug("canary: routing to upstream",
				"pattern_id", patternID)
			return false, "canary_upstream"
		}
	}

	// Resolve the specialist target. Multi-LoRA mode (serveURL set): ensure the
	// adapter (the pattern id) is resident on the warm server, then route by
	// adapter name to that single server. Legacy mode: per-pattern SpecialistURL.
	routeBody := body
	destURL := strings.TrimRight(match.Pattern.SpecialistURL, "/") + "/v1/chat/completions"
	if h.serveURL != "" {
		if err := h.ensureAdapter(r.Context(), patternID); err != nil {
			h.logger.Warn("residency ensure failed; falling back to upstream",
				"pattern_id", patternID, "err", err)
			return false, "specialist_ensure_failed"
		}
		if rb, rerr := rewriteModel(body, patternID); rerr == nil {
			routeBody = rb
		} else {
			h.logger.Warn("model rewrite failed; routing original body",
				"pattern_id", patternID, "err", rerr)
		}
		destURL = h.serveURL + "/v1/chat/completions"
	}

	// For canary-managed patterns, always shadow to upstream so we can
	// compute agreement scores. Otherwise use the configured shadow rate.
	shadowRate := h.shadowRate
	if canaryManaged {
		shadowRate = 1.0
	}
	var shadow *shadowJob
	if shadowRate > 0 && h.rand() < shadowRate {
		shadow = h.startShadow(r, body, patternID, hashInput(lastUser))
	}

	specStart := time.Now()
	resp, err := h.doRequest(r.Context(), r, routeBody, destURL)
	specLatency := time.Since(specStart)
	if err != nil {
		h.logger.Warn("specialist call failed; falling back to upstream",
			"pattern_id", patternID,
			"specialist_url", match.Pattern.SpecialistURL,
			"err", err,
		)
		if shadow != nil {
			shadow.discard()
		}
		return false, "specialist_error"
	}

	specBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		h.logger.Warn("specialist body read failed; falling back to upstream",
			"pattern_id", patternID, "err", readErr)
		if shadow != nil {
			shadow.discard()
		}
		return false, "specialist_body_error"
	}

	if !specialistResponseOK(resp.StatusCode, specBody) {
		h.logger.Warn("specialist returned bad response; falling back to upstream",
			"pattern_id", patternID,
			"status", resp.StatusCode,
		)
		if shadow != nil {
			shadow.discard()
		}
		return false, "specialist_bad_response"
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(specBody)

	h.logger.Info("specialist served request",
		"pattern_id", patternID,
		"similarity", match.Similarity,
		"latency_ms", specLatency.Milliseconds(),
	)

	if h.latencyTracker != nil {
		h.latencyTracker.RecordSpecialist(specLatency)
	}

	promptTokens, completionTokens := extractUsage(specBody)
	var costSavedUSD float64
	if h.pricing != nil {
		costSavedUSD = h.pricing.ComputeCost(peek.Model, promptTokens, completionTokens)
	}

	h.logRequest(peek.Model, "specialist", match.Pattern.ID, resp.StatusCode,
		promptTokens, completionTokens, specLatency, 0, costSavedUSD)

	if h.metrics != nil {
		h.metrics.Observe("specialist", match.Pattern.ID, peek.Model, resp.StatusCode, specLatency, 0)
	}

	if shadow != nil {
		if canaryManaged && h.canaryManager != nil {
			go h.finishCanaryShadow(shadow, match.Pattern.ID, specBody, specLatency)
		} else {
			go shadow.finish(specBody, specLatency)
		}
	}
	return true, "specialist"
}

func (h *proxyHandler) finishCanaryShadow(shadow *shadowJob, patternID string, specialistBody []byte, specialistLatency time.Duration) {
	<-shadow.done
	shadow.cancel()

	specContent := canary.ExtractContent(specialistBody)
	upContent := canary.ExtractContent(shadow.body)
	score := canary.CompareResponses(specialistBody, shadow.body)

	// Log agreement data for external analysis.
	h.logger.Info("canary_agreement",
		"pattern_id", patternID,
		"agreement", score,
		"input_hash", shadow.inputHash,
		"specialist_latency_ms", specialistLatency.Milliseconds(),
		"upstream_latency_ms", shadow.latency.Milliseconds(),
		"specialist_output_length", len(specContent),
		"upstream_output_length", len(upContent),
	)

	state, transitioned, err := h.canaryManager.Advance(patternID, score)
	if err != nil {
		h.logger.Warn("canary advance failed", "pattern_id", patternID, "err", err)
		return
	}
	if transitioned {
		if state == canary.StateBroken {
			alert := h.canaryManager.SendAlert(patternID)
			h.logger.Warn("canary auto-demoted", "pattern_id", patternID, "alert", alert)
		} else if state == canary.StateLive {
			h.logger.Info("canary promoted to live", "pattern_id", patternID)
		} else {
			r, _ := h.canaryManager.State(patternID)
			h.logger.Info("canary advanced", "pattern_id", patternID, "pct", r.Pct)
		}
	}
}

func (h *proxyHandler) forwardToUpstream(w http.ResponseWriter, r *http.Request, body []byte, reason string, model string, isFallback bool) {
	start := time.Now()
	resp, err := h.doRequest(r.Context(), r, body, h.upstreamURL+"/chat/completions")
	var statusCode int
	var promptTokens, completionTokens int
	var costUSD float64

	if err != nil {
		h.logger.Warn("upstream call failed", "err", err, "reason", reason)
		writeError(w, http.StatusBadGateway, "upstream call failed: "+err.Error())
		statusCode = http.StatusBadGateway
		if h.latencyTracker != nil {
			h.latencyTracker.RecordUpstream(time.Since(start))
		}
		routeDecision := "upstream"
		if isFallback {
			routeDecision = "fallback"
		}
		h.logRequest(model, routeDecision, "", statusCode, 0, 0, time.Since(start), -1, 0)
		if h.metrics != nil {
			h.metrics.Observe(routeDecision, "", model, statusCode, time.Since(start), -1)
		}
		return
	}
	defer resp.Body.Close()
	statusCode = resp.StatusCode

	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		respBody = nil
	}

	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	if respBody != nil {
		_, _ = w.Write(respBody)
	}

	latency := time.Since(start)

	if h.latencyTracker != nil {
		h.latencyTracker.RecordUpstream(latency)
	}

	promptTokens, completionTokens = extractUsage(respBody)

	if h.pricing != nil {
		costUSD = h.pricing.ComputeCost(model, promptTokens, completionTokens)
	}

	routeDecision := "upstream"
	if isFallback {
		routeDecision = "fallback"
	}

	h.logRequest(model, routeDecision, "", statusCode, promptTokens, completionTokens, latency, costUSD, 0)

	if h.metrics != nil {
		h.metrics.Observe(routeDecision, "", model, statusCode, latency, costUSD)
	}

	h.logger.Info("upstream served request", "reason", reason, "status", statusCode)
}

func (h *proxyHandler) logRequest(model, routeDecision, patternID string, statusCode, promptTokens, completionTokens int, latency time.Duration, costUSD, costSavedUSD float64) {
	args := []any{
		"method", "POST",
		"route_decision", routeDecision,
		"model", model,
		"status", statusCode,
		"latency_ms", latency.Milliseconds(),
	}
	if patternID != "" {
		args = append(args, "pattern_id", patternID)
	}
	args = append(args, "prompt_tokens", promptTokens)
	args = append(args, "completion_tokens", completionTokens)
	if costUSD >= 0 {
		args = append(args, "estimated_cost_usd", costUSD)
	}
	args = append(args, "cost_saved_usd", costSavedUSD)

	h.logger.Info("request", args...)
}

func extractUsage(body []byte) (promptTokens, completionTokens int) {
	if body == nil {
		return 0, 0
	}
	var peeked struct {
		Usage *chatResponseUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &peeked); err != nil || peeked.Usage == nil {
		return 0, 0
	}
	return peeked.Usage.PromptTokens, peeked.Usage.CompletionTokens
}

func (h *proxyHandler) doRequest(ctx context.Context, in *http.Request, body []byte, destURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range in.Header {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return h.client.Do(req)
}

// ensureAdapter asks the residency control plane to make the adapter resident
// on the warm server before routing. No-op when no residency URL is configured
// (caller assumes the adapter is preloaded/pinned).
func (h *proxyHandler) ensureAdapter(ctx context.Context, adapterID string) error {
	if h.residencyURL == "" {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"adapter_id": adapterID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		h.residencyURL+"/residency/ensure", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("residency ensure returned %d", resp.StatusCode)
	}
	return nil
}

// rewriteModel returns body with its top-level "model" field set to model,
// preserving all other fields byte-for-byte. Routes by adapter name (multi-LoRA).
func rewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	mv, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = mv
	return json.Marshal(m)
}

func specialistResponseOK(status int, body []byte) bool {
	if status < 200 || status >= 300 {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	if !bytes.HasPrefix(trimmed, []byte("{")) {
		return true
	}
	var parsed struct {
		Choices []json.RawMessage `json:"choices"`
		Error   json.RawMessage   `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &parsed); err != nil {
		return false
	}
	if len(parsed.Error) > 0 {
		return false
	}
	return len(parsed.Choices) > 0
}

func peekChat(body []byte) (chatRequestPeek, error) {
	var p chatRequestPeek
	if err := json.Unmarshal(body, &p); err != nil {
		return p, err
	}
	return p, nil
}

func lastUserText(p chatRequestPeek) string {
	for i := len(p.Messages) - 1; i >= 0; i-- {
		m := p.Messages[i]
		if m.Role != "user" {
			continue
		}
		raw := bytes.TrimSpace(m.Content)
		if len(raw) == 0 {
			continue
		}
		if raw[0] == '"' {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				return s
			}
			continue
		}
		if raw[0] == '[' {
			var parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &parts); err != nil {
				continue
			}
			var sb strings.Builder
			for _, part := range parts {
				if part.Type == "text" && part.Text != "" {
					if sb.Len() > 0 {
						sb.WriteByte('\n')
					}
					sb.WriteString(part.Text)
				}
			}
			if sb.Len() > 0 {
				return sb.String()
			}
		}
	}
	return ""
}

func hashInput(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}

func isHopHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "Connection",
		"Proxy-Connection",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"Host":
		return true
	}
	return false
}

func copyHeaders(dst http.Header, src http.Header) {
	for k, vs := range src {
		if isHopHeader(k) {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

type shadowJob struct {
	cancel    context.CancelFunc
	done      chan struct{}
	body      []byte
	status    int
	err       error
	latency   time.Duration
	patternID string
	inputHash string
	logger    *slog.Logger
}

func (h *proxyHandler) startShadow(r *http.Request, body []byte, patternID, inputHash string) *shadowJob {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	job := &shadowJob{
		cancel:    cancel,
		done:      make(chan struct{}),
		patternID: patternID,
		inputHash: inputHash,
		logger:    h.logger,
	}
	go func() {
		defer close(job.done)
		start := time.Now()
		resp, err := h.doRequest(ctx, r, body, h.upstreamURL+"/chat/completions")
		job.latency = time.Since(start)
		if err != nil {
			job.err = err
			return
		}
		defer resp.Body.Close()
		bodyBytes, readErr := io.ReadAll(resp.Body)
		job.status = resp.StatusCode
		if readErr != nil {
			job.err = readErr
			return
		}
		job.body = bodyBytes
	}()
	return job
}

func (j *shadowJob) finish(specialistBody []byte, specialistLatency time.Duration) {
	<-j.done
	j.cancel()
	args := []any{
		"pattern_id", j.patternID,
		"input_hash", j.inputHash,
		"specialist_latency_ms", specialistLatency.Milliseconds(),
		"specialist_output", string(specialistBody),
	}
	if j.err != nil {
		args = append(args, "upstream_error", j.err.Error())
	} else {
		args = append(args,
			"upstream_status", j.status,
			"upstream_latency_ms", j.latency.Milliseconds(),
			"upstream_output", string(j.body),
		)
	}
	j.logger.Info("shadow_sample", args...)
}

func (j *shadowJob) discard() {
	j.cancel()
	go func() { <-j.done }()
}
