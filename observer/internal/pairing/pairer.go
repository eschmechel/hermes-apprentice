// Package pairing turns a stream of Hermes messages into (user, assistant)
// records persisted to the observer store. The state machine is per-session:
//
//   - user message → set as pending input for that session (overwrites any
//     previous pending; a user who asks twice in a row before getting a reply
//     loses the first question, which is what they meant by re-asking).
//   - assistant message with non-empty content → close the pair, write a
//     Record, clear pending.
//   - assistant message with empty content (tool-call only) → ignore; this is
//     an intermediate turn in a multi-step agent reply.
//   - tool message → ignore for v1 pairing; the tool trace is implicit between
//     the user and final assistant turn.
//
// Sessions table fields (model, system_prompt) are looked up against the
// Hermes DB once per session and cached in-memory.
package pairing

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/eschmechel/hermes-apprentice/observer/internal/normalizer"
	"github.com/eschmechel/hermes-apprentice/observer/internal/store"
)

type Pairer struct {
	store    *store.Store
	hermes   *sql.DB
	logger   *slog.Logger
	mu       sync.Mutex
	pending  map[string]pendingInput
	sessions map[string]sessionMeta
}

type pendingInput struct {
	UserMessageID int64
	InputText     string
	InputHash     string
	Timestamp     float64
	HasTokens     bool
	TokenCount    int64
}

type sessionMeta struct {
	model            sql.NullString
	systemPromptHash sql.NullString
	loaded           bool // we already tried to enrich; don't re-query on every pair
}

func New(s *store.Store, hermes *sql.DB, logger *slog.Logger) *Pairer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pairer{
		store:    s,
		hermes:   hermes,
		logger:   logger,
		pending:  make(map[string]pendingInput),
		sessions: make(map[string]sessionMeta),
	}
}

// Observe processes one normalized message. Returns ErrSkip-classified
// failures only via the logger; the public error is reserved for fatal
// problems (e.g. store write failure other than ErrDuplicate).
func (p *Pairer) Observe(ctx context.Context, n normalizer.Normalized) error {
	switch n.Role {
	case "user":
		if n.Content == "" {
			// Empty user message is odd; nothing to pair against. Skip.
			return nil
		}
		p.mu.Lock()
		p.pending[n.SessionID] = pendingInput{
			UserMessageID: n.ID,
			InputText:     n.Content,
			InputHash:     n.ContentHash,
			Timestamp:     n.Timestamp,
			HasTokens:     n.HasTokens,
			TokenCount:    n.TokenCount,
		}
		p.mu.Unlock()
		return nil

	case "assistant":
		if n.Content == "" {
			// Tool-call-only assistant turn — keep waiting for a content turn.
			return nil
		}
		p.mu.Lock()
		pi, ok := p.pending[n.SessionID]
		if ok {
			delete(p.pending, n.SessionID)
		}
		p.mu.Unlock()
		if !ok {
			// Assistant without a preceding user — system bootstrap, ignore.
			return nil
		}
		return p.commitPair(ctx, pi, n)

	default:
		// tool / system / other roles: not used by v1 pairing
		return nil
	}
}

func (p *Pairer) commitPair(ctx context.Context, pi pendingInput, ar normalizer.Normalized) error {
	model, sysHash, err := p.sessionEnrichment(ctx, ar.SessionID)
	if err != nil {
		// Enrichment failure shouldn't lose the pair — record it without it.
		p.logger.Warn("session enrichment failed",
			"session_id", ar.SessionID, "err", err)
	}

	latencyMs := int64((ar.Timestamp - pi.Timestamp) * 1000)
	if latencyMs < 0 {
		latencyMs = 0
	}

	tokenCounts := buildTokenCounts(pi, ar)

	rec := store.Record{
		SessionID:          ar.SessionID,
		InputHash:          pi.InputHash,
		InputText:          pi.InputText,
		OutputText:         ar.Content,
		LatencyMs:          latencyMs,
		CreatedAt:          float64(time.Now().Unix()),
		UserMessageID:      pi.UserMessageID,
		AssistantMessageID: ar.ID,
		TokenCounts:        tokenCounts,
	}
	if model.Valid {
		rec.ModelUsed = &model.String
	}
	if sysHash.Valid {
		rec.SystemPromptHash = &sysHash.String
	}

	id, err := p.store.InsertRecord(ctx, rec)
	if errors.Is(err, store.ErrDuplicate) {
		// Restart replayed a message past the dedup window. The UNIQUE
		// constraint caught it — that's the design.
		p.logger.Debug("duplicate record absorbed by UNIQUE constraint",
			"session_id", rec.SessionID,
			"user_msg_id", rec.UserMessageID,
			"assistant_msg_id", rec.AssistantMessageID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("insert record: %w", err)
	}
	p.logger.Info("paired record",
		"record_id", id,
		"session_id", rec.SessionID,
		"input_len", len(rec.InputText),
		"output_len", len(rec.OutputText),
		"latency_ms", rec.LatencyMs,
		"model", nullStr(model),
	)
	return nil
}

func (p *Pairer) sessionEnrichment(ctx context.Context, sessionID string) (sql.NullString, sql.NullString, error) {
	p.mu.Lock()
	cached, ok := p.sessions[sessionID]
	p.mu.Unlock()
	if ok && cached.loaded {
		return cached.model, cached.systemPromptHash, nil
	}

	// Source-agnostic (W2): non-Hermes sources (e.g. the openai-log adapter)
	// have no sessions table to enrich from — just skip enrichment.
	if p.hermes == nil {
		meta := sessionMeta{loaded: true}
		p.mu.Lock()
		p.sessions[sessionID] = meta
		p.mu.Unlock()
		return meta.model, meta.systemPromptHash, nil
	}

	const q = `SELECT model, system_prompt FROM sessions WHERE id = ?`
	var model, sysPrompt sql.NullString
	err := p.hermes.QueryRowContext(ctx, q, sessionID).Scan(&model, &sysPrompt)

	meta := sessionMeta{model: model, loaded: true}
	if sysPrompt.Valid {
		sum := sha256.Sum256([]byte(sysPrompt.String))
		meta.systemPromptHash = sql.NullString{String: hex.EncodeToString(sum[:]), Valid: true}
	}

	p.mu.Lock()
	p.sessions[sessionID] = meta
	p.mu.Unlock()

	if errors.Is(err, sql.ErrNoRows) {
		// Session row missing — not fatal, just no enrichment.
		return meta.model, meta.systemPromptHash, nil
	}
	if err != nil {
		return meta.model, meta.systemPromptHash, err
	}
	return meta.model, meta.systemPromptHash, nil
}

func buildTokenCounts(pi pendingInput, ar normalizer.Normalized) json.RawMessage {
	if !pi.HasTokens && !ar.HasTokens {
		return nil
	}
	payload := map[string]int64{}
	if pi.HasTokens {
		payload["input"] = pi.TokenCount
	}
	if ar.HasTokens {
		payload["output"] = ar.TokenCount
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

func nullStr(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return s.String
}
