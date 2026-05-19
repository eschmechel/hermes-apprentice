package describer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_EmptyAPIKey(t *testing.T) {
	_, err := New(Config{APIKey: ""})
	if err == nil || !strings.Contains(err.Error(), "APIKey") {
		t.Fatalf("expected APIKey error, got %v", err)
	}
}

func TestDescribe_EmptySamples(t *testing.T) {
	d := &Describer{cfg: Config{APIKey: "sk-test", Provider: ProviderOpenRouter}, client: http.DefaultClient}
	if _, err := d.Describe(context.Background(), nil, 25); err == nil {
		t.Fatal("expected empty-samples error")
	}
}

// ---------- OpenAI-compatible (OpenRouter) ----------

func TestDescribe_OpenRouter_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("bad auth: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(openAIResponse{
			Choices: []struct {
				Message openAIMessage `json:"message"`
			}{{Message: openAIMessage{Content: "Extract fields from emails"}}},
		})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-test", Provider: ProviderOpenRouter, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	desc, err := d.Describe(context.Background(),
		[]string{"extract name from email", "get address from message"}, 25)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Extract fields from emails" {
		t.Fatalf("desc = %q", desc)
	}
}

func TestDescribe_OpenRouter_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-bad", Provider: ProviderOpenRouter, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"hello"}, 10)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

// ---------- OpenAI ----------

func TestDescribe_OpenAI_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-openai-test" {
			t.Errorf("bad auth: %s", r.Header.Get("Authorization"))
		}
		json.NewEncoder(w).Encode(openAIResponse{
			Choices: []struct {
				Message openAIMessage `json:"message"`
			}{{Message: openAIMessage{Content: "Summarise articles"}}},
		})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-openai-test", Provider: ProviderOpenAI, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	desc, err := d.Describe(context.Background(),
		[]string{"summarize this post", "tl;dr this blog"}, 20)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Summarise articles" {
		t.Fatalf("desc = %q", desc)
	}
}

// ---------- Anthropic ----------

func TestDescribe_Anthropic_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "sk-ant-test" {
			t.Errorf("bad api key header: %s", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") == "" {
			t.Error("missing anthropic-version header")
		}

		// Verify the request body uses Anthropic format.
		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode anthropic request: %v", err)
		}
		if req.System == "" {
			t.Error("anthropic system prompt missing")
		}
		if len(req.Messages) == 0 {
			t.Error("anthropic messages empty")
		}

		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			}{{Text: "Rewrite text professionally", Type: "text"}},
		})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-ant-test", Provider: ProviderAnthropic, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	desc, err := d.Describe(context.Background(),
		[]string{"make this sound more formal", "rewrite professionally"}, 30)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if desc != "Rewrite text professionally" {
		t.Fatalf("desc = %q", desc)
	}
}

func TestDescribe_Anthropic_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-bad", Provider: ProviderAnthropic, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"hello"}, 10)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 error, got %v", err)
	}
}

// ---------- Shared error cases ----------

func TestDescribe_NoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIResponse{})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"help"}, 10)
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("expected no-choices error, got %v", err)
	}
}

func TestDescribe_EmptyContent_OpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(openAIResponse{
			Choices: []struct {
				Message openAIMessage `json:"message"`
			}{{Message: openAIMessage{Content: ""}}},
		})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"hello"}, 10)
	if err == nil || !strings.Contains(err.Error(), "empty description") {
		t.Fatalf("expected empty-description error, got %v", err)
	}
}

func TestDescribe_EmptyContent_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(anthropicResponse{
			Content: []struct {
				Text string `json:"text"`
				Type string `json:"type"`
			}{{Text: "", Type: "text"}},
		})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-test", Provider: ProviderAnthropic, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"hello"}, 10)
	if err == nil || !strings.Contains(err.Error(), "empty description") {
		t.Fatalf("expected empty-description error, got %v", err)
	}
}

// ---------- Utility tests ----------

func TestTrimSamples_Noop(t *testing.T) {
	in := []string{"a", "b", "c"}
	out := trimSamples(in, 10)
	if len(out) != 3 {
		t.Fatalf("trimSamples shrunk small input: %d", len(out))
	}
}

func TestTrimSamples_EvenSpacing(t *testing.T) {
	in := make([]string, 100)
	for i := range in {
		in[i] = "x"
	}
	out := trimSamples(in, 5)
	if len(out) != 5 {
		t.Fatalf("len = %d, want 5", len(out))
	}
	if out[0] != in[0] || out[4] != in[99] {
		t.Fatal("first / last not preserved")
	}
}

func TestDefaultConfig_ProviderDefaults(t *testing.T) {
	tests := []struct {
		provider Provider
		wantModelPrefix string
	}{
		{ProviderOpenRouter, "google/"},
		{ProviderOpenAI, "gpt-"},
		{ProviderAnthropic, "claude-"},
	}
	for _, tt := range tests {
		cfg := DefaultConfig(tt.provider)
		if cfg.BaseURL == "" {
			t.Errorf("%s: BaseURL empty", tt.provider)
		}
		if !strings.HasPrefix(cfg.Model, tt.wantModelPrefix) {
			t.Errorf("%s: Model = %q, want prefix %q", tt.provider, cfg.Model, tt.wantModelPrefix)
		}
	}
}

func TestAnthropic_NoContentBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(anthropicResponse{Content: nil})
	}))
	defer srv.Close()

	d, err := New(Config{APIKey: "sk-test", Provider: ProviderAnthropic, BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = d.Describe(context.Background(), []string{"hello"}, 10)
	if err == nil || !strings.Contains(err.Error(), "no content blocks") {
		t.Fatalf("expected no-content-blocks error, got %v", err)
	}
}
