package augment

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/fetcher"
)

func makeRecords(n int) []fetcher.Record {
	out := make([]fetcher.Record, n)
	for i := 0; i < n; i++ {
		out[i] = fetcher.Record{
			ID:         int64(i + 1),
			SessionID:  "sess-1",
			InputText:  "extract the customer name from this email",
			OutputText: "John Doe",
		}
	}
	return out
}

func startMock(t *testing.T, statusCode int, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		if content != "" {
			resp := openAIResponse{
				Choices: []struct {
					Message openAIMessage `json:"message"`
				}{
					{Message: openAIMessage{Role: "assistant", Content: content}},
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
}

func TestAugment_AlreadyAboveMinTarget(t *testing.T) {
	srv := startMock(t, 200, "1) get the client name\n2) pull customer name")
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MinTarget: 5})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recs := makeRecords(10)
	out, err := a.Augment(context.Background(), recs)
	if err != nil {
		t.Fatalf("Augment: %v", err)
	}
	if len(out) != 10 {
		t.Fatalf("len = %d, want 10 (unchanged)", len(out))
	}
}

func TestAugment_ExpandsBelowMinTarget(t *testing.T) {
	content := "1) pull the customer name from this email\n2) extract the client's name\n3) get customer full name from email"
	srv := startMock(t, 200, content)
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MinTarget: 5})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recs := makeRecords(2)
	out, err := a.Augment(context.Background(), recs)
	if err != nil {
		t.Fatalf("Augment: %v", err)
	}
	// 2 original + at least some paraphrases added
	if len(out) <= 2 {
		t.Fatalf("len = %d, want > 2 (should have added paraphrases)", len(out))
	}
	// Original records should still be present with untouched InputText.
	if out[0].InputText != "extract the customer name from this email" {
		t.Fatalf("original input modified: %q", out[0].InputText)
	}
	// OutputText is preserved on paraphrases.
	for i, r := range out {
		if r.OutputText != "John Doe" {
			t.Fatalf("record %d: OutputText = %q, want %q", i, r.OutputText, "John Doe")
		}
	}
}

func TestAugment_EmptyInput(t *testing.T) {
	a, err := New(Config{APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = a.Augment(context.Background(), nil)
	if err == nil {
		t.Fatalf("expected error for empty records")
	}
}

func TestAugment_MaxParaphrasesCap(t *testing.T) {
	content := "1) a\n2) b\n3) c\n4) d\n5) e\n6) f\n7) g\n8) h\n9) i\n10) j\n11) k\n12) l"
	srv := startMock(t, 200, content)
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MinTarget: 20, MaxParaphrases: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recs := makeRecords(5)
	out, err := a.Augment(context.Background(), recs)
	if err != nil {
		t.Fatalf("Augment: %v", err)
	}
	// 5 original + at most 5*3 = 15 paraphrases = max 20.
	if len(out) > 20 {
		t.Fatalf("len = %d, want <= 20 (15 paraphrases cap + 5 original)", len(out))
	}
}

func TestAugment_APIKeyRequired(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatalf("expected error for missing API key")
	}
}

func TestAugment_APIError(t *testing.T) {
	srv := startMock(t, 401, `{"error":"unauthorized"}`)
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MinTarget: 5})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recs := makeRecords(2)
	out, err := a.Augment(context.Background(), recs)
	if err != nil {
		t.Fatalf("API error should not abort augmentation: %v", err)
	}
	// API call failed for both records → output equals input (unchanged).
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (unchanged on API error)", len(out))
	}
}

func TestAugment_ParaphrasePreservesOutput(t *testing.T) {
	content := "1) get the name from email\n2) pull customer name out"
	srv := startMock(t, 200, content)
	defer srv.Close()

	a, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL, MinTarget: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	recs := makeRecords(1)
	out, err := a.Augment(context.Background(), recs)
	if err != nil {
		t.Fatalf("Augment: %v", err)
	}
	for i := 1; i < len(out); i++ {
		if out[i].InputText == out[0].InputText {
			t.Fatalf("paraphrase %d has same InputText as original", i)
		}
		if out[i].OutputText != out[0].OutputText {
			t.Fatalf("paraphrase %d: OutputText = %q, want %q (preserved)", i, out[i].OutputText, out[0].OutputText)
		}
	}
}

func TestParseNumbered(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
		wantLen  int
	}{
		{"standard", "1) first\n2) second\n3) third", 3, 3},
		{"dot prefix", "1. first\n2. second", 2, 2},
		{"dash prefix", "1 - first\n2 - second", 2, 2},
		{"mixed", "1) good\njunk line\n2) better", 2, 2},
		{"truncated", "1) a\n2) b\n3) c\n4) d", 2, 2},
		{"no numbers", "plain text", 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := parseNumbered(tt.input, tt.expected)
			if len(out) != tt.wantLen {
				t.Fatalf("parseNumbered = %d items, want %d: %v", len(out), tt.wantLen, out)
			}
		})
	}
}
