// Package normalizer cleans raw Hermes messages into a stable form for the
// dedup window and the downstream store. We keep the surface tiny here: full
// (user, assistant) pairing into the apprentice's training-pair schema is
// observer-04's job once the local SQLite store exists.
package normalizer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/hermes-apprentice/observer/internal/poller"
)

// Normalized is a poller.Message with NULL strings flattened to "" and a
// stable content hash attached. Equal content + session means equal hash.
type Normalized struct {
	ID          int64
	SessionID   string
	Role        string
	Content     string
	ToolCalls   string
	ToolName    string
	Timestamp   float64
	ContentHash string // sha256 of normalized content (lowercase hex)
	TokenCount  int64  // 0 if Hermes hadn't recorded a count for this row
	HasTokens   bool   // distinguishes "unknown" from "zero"
}

// Normalize collapses NULLs, trims surrounding whitespace, and computes a
// content hash that uniquely identifies the *semantic event*.
//
// The hash includes role + content + tool_calls, joined with a NUL separator
// so component boundaries can't be smuggled across. Why not content alone:
// assistant turns inside a tool-using session frequently carry empty content
// + non-empty tool_calls — keying on content alone would dedupe two distinct
// tool-call turns just because they both have empty content.
func Normalize(m poller.Message) Normalized {
	content := ""
	if m.Content.Valid {
		content = strings.TrimSpace(m.Content.String)
	}
	toolCalls := ""
	if m.ToolCalls.Valid {
		toolCalls = strings.TrimSpace(m.ToolCalls.String)
	}
	toolName := ""
	if m.ToolName.Valid {
		toolName = strings.TrimSpace(m.ToolName.String)
	}

	h := sha256.New()
	h.Write([]byte(m.Role))
	h.Write([]byte{0})
	h.Write([]byte(content))
	h.Write([]byte{0})
	h.Write([]byte(toolCalls))

	var tokens int64
	hasTokens := false
	if m.TokenCount.Valid {
		tokens = m.TokenCount.Int64
		hasTokens = true
	}

	return Normalized{
		ID:          m.ID,
		SessionID:   m.SessionID,
		Role:        m.Role,
		Content:     content,
		ToolCalls:   toolCalls,
		ToolName:    toolName,
		Timestamp:   m.Timestamp,
		ContentHash: hex.EncodeToString(h.Sum(nil)),
		TokenCount:  tokens,
		HasTokens:   hasTokens,
	}
}
