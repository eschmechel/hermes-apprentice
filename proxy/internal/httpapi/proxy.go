package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	client         *http.Client
	embedder       Embedder
	store          *patterns.Store
	matchThreshold float32
	shadowRate     float64
	logger         *slog.Logger

	// rand is the source for shadow-sampling decisions.  Defaulting nil
	// uses math/rand/v2's global source; tests can override.
	rand func() float64
}

func newProxyHandler(cfg Config) *proxyHandler {
	return &proxyHandler{
		upstreamURL:    strings.TrimRight(cfg.UpstreamURL, "/"),
		client:         cfg.HTTPClient,
		embedder:       cfg.Embedder,
		store:          cfg.PatternStore,
		matchThreshold: cfg.MatchThreshold,
		shadowRate:     cfg.ShadowRate,
		logger:         cfg.Logger,
		rand:           rand.Float64,
	}
}

// chatRequestPeek is the minimal slice of the OpenAI chat-completions request
// we need to inspect for routing.  We use json.RawMessage for content because
// OpenAI allows it to be either a string or an array of parts.
type chatRequestPeek struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream"`
}

func (h *proxyHandler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	peek, peekErr := peekChat(body)
	if peekErr != nil {
		// Forward malformed requests verbatim so upstream's error surface
		// reaches the client — we don't claim to be a stricter validator
		// than OpenAI.
		h.forwardToUpstream(w, r, body, "peek_failed")
		return
	}

	lastUser := lastUserText(peek)
	routed := h.tryRouteToSpecialist(w, r, body, peek, lastUser)
	if routed {
		return
	}
	h.forwardToUpstream(w, r, body, "no_match")
}

// tryRouteToSpecialist embeds the last user message, picks the best matching
// pattern (if any), forwards to its specialist_url, and returns true when the
// specialist served the response.  Returns false when no match, no embedder,
// or the specialist failed and the caller should fall back to upstream.
func (h *proxyHandler) tryRouteToSpecialist(w http.ResponseWriter, r *http.Request, body []byte, peek chatRequestPeek, lastUser string) bool {
	if h.embedder == nil || h.store == nil || lastUser == "" {
		return false
	}
	emb, err := h.embedder.Embed(lastUser)
	if err != nil {
		h.logger.Warn("embed failed; falling back to upstream", "err", err)
		return false
	}
	match, ok := h.store.BestMatch(emb, h.matchThreshold)
	if !ok {
		return false
	}

	// Shadow sample: launch upstream call concurrently for offline diff.
	// Specialist response is still the one we return to the client.
	var shadow *shadowJob
	if h.shadowRate > 0 && h.rand() < h.shadowRate {
		shadow = h.startShadow(r, body, match.Pattern.ID, hashInput(lastUser))
	}

	specStart := time.Now()
	resp, err := h.doRequest(r.Context(), r, body, match.Pattern.SpecialistURL+"/v1/chat/completions")
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
		return false
	}

	specBody, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		h.logger.Warn("specialist body read failed; falling back to upstream",
			"pattern_id", match.Pattern.ID, "err", readErr)
		if shadow != nil {
			shadow.discard()
		}
		return false
	}

	if !specialistResponseOK(resp.StatusCode, specBody) {
		h.logger.Warn("specialist returned bad response; falling back to upstream",
			"pattern_id", match.Pattern.ID,
			"status", resp.StatusCode,
		)
		if shadow != nil {
			shadow.discard()
		}
		return false
	}

	// Stream specialist response back to caller.
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(specBody)

	h.logger.Info("specialist served request",
		"pattern_id", match.Pattern.ID,
		"similarity", match.Similarity,
		"latency_ms", specLatency.Milliseconds(),
	)

	if shadow != nil {
		go shadow.finish(specBody, specLatency)
	}
	return true
}

// forwardToUpstream sends body to upstreamURL/chat/completions and streams the
// response back to w.  reason is logged for observability.
func (h *proxyHandler) forwardToUpstream(w http.ResponseWriter, r *http.Request, body []byte, reason string) {
	resp, err := h.doRequest(r.Context(), r, body, h.upstreamURL+"/chat/completions")
	if err != nil {
		h.logger.Warn("upstream call failed", "err", err, "reason", reason)
		writeError(w, http.StatusBadGateway, "upstream call failed: "+err.Error())
		return
	}
	defer resp.Body.Close()
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	h.logger.Info("upstream served request", "reason", reason, "status", resp.StatusCode)
}

// doRequest builds an outbound POST mirroring the inbound request's body and
// content headers, targeting destURL.  Hop-by-hop headers and Host are
// stripped.
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

// specialistResponseOK accepts a response only if it is HTTP 2xx and the JSON
// body parses with a non-empty "choices" array (the OpenAI chat-completions
// success shape).  Streamed responses (SSE) are accepted on status alone — we
// don't parse a stream here; if the specialist is a real vLLM it almost
// certainly speaks JSON on non-streaming calls.
func specialistResponseOK(status int, body []byte) bool {
	if status < 200 || status >= 300 {
		return false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	// SSE / chunked stream — trust the status.
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

// lastUserText returns the text content of the most recent user message, or
// "" if none.  OpenAI allows content to be either a string or an array of
// parts ([{type:"text", text:"..."}, ...]); both shapes are supported.
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
		// String form
		if raw[0] == '"' {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				return s
			}
			continue
		}
		// Array of parts form
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

// shadowJob runs the upstream call for shadow comparison in parallel with the
// specialist call.  finish() logs the diff once both have completed; discard()
// cancels the upstream call when the specialist already failed and we'd never
// want to compare.
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
	// Decouple from the request context so a fast specialist response that
	// finishes the inbound request doesn't kill the shadow upstream call
	// mid-flight.
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
	// Drain the goroutine so it doesn't leak.
	go func() { <-j.done }()
}

