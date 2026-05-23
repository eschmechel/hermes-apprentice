package openailog

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/eschmechel/hermes-apprentice/observer/internal/poller"
)

func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "exchanges.jsonl")
	data := ""
	for _, l := range lines {
		data += l + "\n"
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func drainAll(t *testing.T, path string, sinceID int64) []poller.Message {
	t.Helper()
	var got []poller.Message
	s := New(Config{
		LogPath:     path,
		StartFromID: sinceID,
		Handler: func(_ context.Context, m poller.Message) error {
			got = append(got, m)
			return nil
		},
	})
	if _, err := s.drain(context.Background(), sinceID); err != nil {
		t.Fatalf("drain: %v", err)
	}
	return got
}

func TestDrain_EmitsUserAndAssistant(t *testing.T) {
	path := writeLog(t,
		`{"session_id":"s1","timestamp":1.0,"request":{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"weather?"}]},"response":{"choices":[{"message":{"role":"assistant","content":"Sunny."}}]}}`,
	)
	got := drainAll(t, path, 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != "user" || got[0].Content.String != "weather?" || got[0].ID != 1 {
		t.Errorf("user msg wrong: %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content.String != "Sunny." || got[1].ID != 2 {
		t.Errorf("assistant msg wrong: %+v", got[1])
	}
	if got[0].SessionID != "s1" {
		t.Errorf("session id = %q", got[0].SessionID)
	}
}

func TestDrain_BareStringResponse(t *testing.T) {
	path := writeLog(t,
		`{"session_id":"s2","request":{"messages":[{"role":"user","content":"hi"}]},"response":"hello there"}`,
	)
	got := drainAll(t, path, 0)
	if len(got) != 2 || got[1].Content.String != "hello there" {
		t.Fatalf("expected bare-string assistant content, got %+v", got)
	}
}

func TestDrain_ResumesFromHighWaterMark(t *testing.T) {
	path := writeLog(t,
		`{"session_id":"s1","request":{"messages":[{"role":"user","content":"one"}]},"response":"r1"}`,
		`{"session_id":"s1","request":{"messages":[{"role":"user","content":"two"}]},"response":"r2"}`,
	)
	// sinceID=2 means line 1 (ids 1,2) already processed; only line 2 (ids 3,4).
	got := drainAll(t, path, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages from line 2, got %d", len(got))
	}
	if got[0].Content.String != "two" || got[0].ID != 3 || got[1].ID != 4 {
		t.Errorf("resume wrong: %+v", got)
	}
}

func TestDrain_SkipsUnparseableLines(t *testing.T) {
	path := writeLog(t,
		`not json`,
		`{"session_id":"s1","request":{"messages":[{"role":"user","content":"ok"}]},"response":"fine"}`,
	)
	got := drainAll(t, path, 0)
	// Line 1 unparseable (skipped); line 2 -> ids 3,4.
	if len(got) != 2 || got[0].ID != 3 {
		t.Fatalf("expected to skip bad line and emit line 2, got %+v", got)
	}
}

func TestDrain_MissingFileIsNoop(t *testing.T) {
	got := drainAll(t, filepath.Join(t.TempDir(), "nope.jsonl"), 0)
	if len(got) != 0 {
		t.Fatalf("missing file should yield nothing, got %d", len(got))
	}
}

func TestDrain_MultimodalContentParts(t *testing.T) {
	path := writeLog(t,
		`{"session_id":"s1","request":{"messages":[{"role":"user","content":[{"type":"text","text":"part1"},{"type":"text","text":"part2"}]}]},"response":"ok"}`,
	)
	got := drainAll(t, path, 0)
	if got[0].Content.String != "part1\npart2" {
		t.Errorf("multimodal text join wrong: %q", got[0].Content.String)
	}
}
