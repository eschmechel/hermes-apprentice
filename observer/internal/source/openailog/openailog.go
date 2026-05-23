// Package openailog is a generic SessionSource (W2): it tails a JSONL file of
// OpenAI chat-completion exchanges and emits the same poller.Message stream the
// Hermes DB poller does, so a non-Hermes agent can feed Apprentice just by
// logging its req/resp pairs. Each line is one exchange:
//
//	{"session_id":"s1","timestamp":1716200000.0,
//	 "request":{"messages":[{"role":"user","content":"weather?"}]},
//	 "response":{"choices":[{"message":{"role":"assistant","content":"Sunny."}}]}}
//
// `response` may also be a bare string. The last user turn of the request and
// the assistant turn of the response become two messages, mirroring how the
// pairer consumes a Hermes user->assistant pair.
package openailog

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/eschmechel/hermes-apprentice/observer/internal/poller"
)

type Config struct {
	LogPath            string
	Interval           time.Duration
	Logger             *slog.Logger
	Handler            func(context.Context, poller.Message) error
	StartFromID        int64
	StartFromBeginning bool
}

type Source struct {
	cfg    Config
	logger *slog.Logger
}

func New(cfg Config) *Source {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Second
	}
	return &Source{cfg: cfg, logger: cfg.Logger}
}

// Run tails the log until ctx is cancelled. IDs are synthetic and monotonic:
// line N (1-based) yields user id 2N-1 and assistant id 2N, so the observer's
// existing high-water-mark resume logic works unchanged.
func (s *Source) Run(ctx context.Context) error {
	lastID := s.cfg.StartFromID
	if s.cfg.StartFromBeginning {
		lastID = 0
	}
	s.logger.Info("openai-log source starting", "log_path", s.cfg.LogPath,
		"interval", s.cfg.Interval, "start_at_id", lastID)

	tick := time.NewTicker(s.cfg.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			newLast, err := s.drain(ctx, lastID)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				s.logger.Warn("openai-log drain error, will retry", "err", err)
				continue
			}
			lastID = newLast
		}
	}
}

func (s *Source) drain(ctx context.Context, sinceID int64) (int64, error) {
	f, err := os.Open(s.cfg.LogPath)
	if err != nil {
		if os.IsNotExist(err) {
			return sinceID, nil // log not created yet; try again next tick
		}
		return sinceID, fmt.Errorf("open %s: %w", s.cfg.LogPath, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // tolerate long lines
	high := sinceID
	var line int64
	for sc.Scan() {
		line++
		assistantID := 2 * line
		if assistantID <= sinceID {
			continue // whole exchange already processed
		}
		raw := sc.Bytes()
		if len(raw) == 0 {
			continue
		}
		ex, perr := parseExchange(raw)
		if perr != nil {
			s.logger.Warn("skipping unparseable exchange", "line", line, "err", perr)
			continue
		}
		for _, m := range ex.messages(line) {
			if m.ID <= sinceID {
				continue
			}
			if s.cfg.Handler != nil {
				if err := s.cfg.Handler(ctx, m); err != nil {
					return high, fmt.Errorf("handler: %w", err)
				}
			}
			high = m.ID
		}
	}
	if err := sc.Err(); err != nil {
		return high, fmt.Errorf("scan: %w", err)
	}
	return high, nil
}

// ── exchange parsing ─────────────────────────────────────────────────────────

type exchange struct {
	SessionID string  `json:"session_id"`
	Timestamp float64 `json:"timestamp"`
	Request   struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	} `json:"request"`
	Response json.RawMessage `json:"response"`
}

func parseExchange(raw []byte) (*exchange, error) {
	var ex exchange
	if err := json.Unmarshal(raw, &ex); err != nil {
		return nil, err
	}
	if ex.SessionID == "" {
		ex.SessionID = "openai-log"
	}
	return &ex, nil
}

// messages turns one exchange into the user + assistant messages the pipeline
// expects. IDs: user = 2*line-1, assistant = 2*line.
func (ex *exchange) messages(line int64) []poller.Message {
	out := make([]poller.Message, 0, 2)
	if user := lastUserContent(ex); user != "" {
		out = append(out, poller.Message{
			ID:        2*line - 1,
			SessionID: ex.SessionID,
			Role:      "user",
			Content:   sql.NullString{String: user, Valid: true},
			Timestamp: ex.Timestamp,
		})
	}
	if asst := assistantContent(ex.Response); asst != "" {
		out = append(out, poller.Message{
			ID:        2 * line,
			SessionID: ex.SessionID,
			Role:      "assistant",
			Content:   sql.NullString{String: asst, Valid: true},
			Timestamp: ex.Timestamp,
		})
	}
	return out
}

func lastUserContent(ex *exchange) string {
	for i := len(ex.Request.Messages) - 1; i >= 0; i-- {
		if ex.Request.Messages[i].Role == "user" {
			return contentText(ex.Request.Messages[i].Content)
		}
	}
	return ""
}

// assistantContent accepts either a full chat-completion response object or a
// bare string.
func assistantContent(resp json.RawMessage) string {
	if len(resp) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(resp, &s); err == nil {
		return s
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(resp, &parsed); err != nil || len(parsed.Choices) == 0 {
		return ""
	}
	return contentText(parsed.Choices[0].Message.Content)
}

// contentText handles both string content and the array-of-parts (multimodal)
// form, concatenating text parts.
func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	out := ""
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			if out != "" {
				out += "\n"
			}
			out += p.Text
		}
	}
	return out
}
