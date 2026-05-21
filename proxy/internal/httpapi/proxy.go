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

	"github.com/hermes-apprentice/proxy/internal/patterns"
)

type proxyHandler struct {
	upstreamURL    string
	serveURL       string // warm multi-LoRA vLLM; empty => legacy per-pattern SpecialistURL
	residencyURL   string // residency control plane (ensure adapter resident)
	client         *http.Client
	embedder       Embedder
	store          *patterns.Store
	matchThreshold float32
	shadowRate     float64
	logger         *slog.Logger

	latencyTracker *LatencyTracker
	pricing        CostEstimator
	metrics        *Metrics

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
		logger:         cfg.Logger,
		latencyTracker: cfg.LatencyTracker,
		pricing:        cfg.Pricing,
		metrics:        cfg.Metrics,
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
	match, ok := h.store.BestMatch(emb, h.matchThreshold)
	if !ok {
		return false, "no_match"
	}

	// Resolve the specialist target. Multi-LoRA mode (serveURL set): ensure the
	// adapter (the pattern id) is resident on the warm server, then route by
	// adapter name to that single server. Legacy mode: per-pattern SpecialistURL.
	routeBody := body
	destURL := strings.TrimRight(match.Pattern.SpecialistURL, "/") + "/v1/chat/completions"
	if h.serveURL != "" {
		if err := h.ensureAdapter(r.Context(), match.Pattern.ID); err != nil {
			h.logger.Warn("residency ensure failed; falling back to upstream",
				"pattern_id", match.Pattern.ID, "err", err)
			return false, "specialist_ensure_failed"
		}
		if rb, rerr := rewriteModel(body, match.Pattern.ID); rerr == nil {
			routeBody = rb
		} else {
			h.logger.Warn("model rewrite failed; routing original body",
				"pattern_id", match.Pattern.ID, "err", rerr)
		}
		destURL = h.serveURL + "/v1/chat/completions"
	}

	var shadow *shadowJob
	if h.shadowRate > 0 && h.rand() < h.shadowRate {
		shadow = h.startShadow(r, body, match.Pattern.ID, hashInput(lastUser))
	}

	specStart := time.Now()
	resp, err := h.doRequest(r.Context(), r, routeBody, destURL)
	specLatency := time.Since(specStart)
	if err != nil {
		h.logger.Warn("specialist call failed; falling back to upstream",
			"pattern_id", match.Pattern.ID,
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
			"pattern_id", match.Pattern.ID, "err", readErr)
		if shadow != nil {
			shadow.discard()
		}
		return false, "specialist_body_error"
	}

	if !specialistResponseOK(resp.StatusCode, specBody) {
		h.logger.Warn("specialist returned bad response; falling back to upstream",
			"pattern_id", match.Pattern.ID,
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
		"pattern_id", match.Pattern.ID,
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
		go shadow.finish(specBody, specLatency)
	}
	return true, "specialist"
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
