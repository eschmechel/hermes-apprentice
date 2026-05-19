package normalizer

import (
	"database/sql"
	"testing"

	"github.com/hermes-apprentice/observer/internal/poller"
)

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestNormalize_TrimsWhitespace(t *testing.T) {
	n := Normalize(poller.Message{Role: "user", Content: nullStr("  hi\n")})
	if n.Content != "hi" {
		t.Fatalf("Content = %q, want %q", n.Content, "hi")
	}
}

func TestNormalize_NullStringsBecomeEmpty(t *testing.T) {
	n := Normalize(poller.Message{Role: "user"})
	if n.Content != "" || n.ToolCalls != "" || n.ToolName != "" {
		t.Fatalf("expected all empties, got %+v", n)
	}
}

// Regression: two assistant turns with empty content but DIFFERENT tool_calls
// must hash distinctly. Empty-content tool-calling turns are common during a
// multi-step agent reply (foundation-06 produced 3 of them in one session).
func TestNormalize_EmptyContentDifferentToolCallsHashesDiffer(t *testing.T) {
	a := Normalize(poller.Message{Role: "assistant", Content: sql.NullString{}, ToolCalls: nullStr(`[{"name":"skill_view"}]`)})
	b := Normalize(poller.Message{Role: "assistant", Content: sql.NullString{}, ToolCalls: nullStr(`[{"name":"terminal"}]`)})
	if a.ContentHash == b.ContentHash {
		t.Fatalf("hashes collided: %s", a.ContentHash)
	}
}

func TestNormalize_RoleParticipatesInHash(t *testing.T) {
	a := Normalize(poller.Message{Role: "user", Content: nullStr("hello")})
	b := Normalize(poller.Message{Role: "assistant", Content: nullStr("hello")})
	if a.ContentHash == b.ContentHash {
		t.Fatalf("user vs assistant hashes collided: %s", a.ContentHash)
	}
}

func TestNormalize_SameRoleAndContentHashesEqual(t *testing.T) {
	a := Normalize(poller.Message{Role: "user", Content: nullStr("hello")})
	b := Normalize(poller.Message{Role: "user", Content: nullStr("hello")})
	if a.ContentHash != b.ContentHash {
		t.Fatalf("identical inputs should hash equal: a=%s b=%s", a.ContentHash, b.ContentHash)
	}
}
