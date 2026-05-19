// Package describer sends a cluster of user-input samples to an LLM and
// returns a concise pattern description ("Extract structured fields from
// customer emails").  It supports OpenRouter, OpenAI, and Anthropic
// providers through a single API.  Satisfies detector-04.
package describer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Provider identifies the LLM API backend.
type Provider string

const (
	ProviderOpenRouter Provider = "openrouter"
	ProviderOpenAI     Provider = "openai"
	ProviderAnthropic  Provider = "anthropic"
)

// Config holds the connection parameters.
type Config struct {
	// Provider selects the API backend.  Must be one of "openrouter",
	// "openai", or "anthropic".  Defaults to "openrouter".
	Provider Provider

	// APIKey is required.  For OpenRouter/OpenAI this goes in the
	// Authorization: Bearer header; for Anthropic it becomes x-api-key.
	APIKey string

	// Model is the provider-specific model ID.  Defaults vary by provider:
	//   openrouter → "google/gemini-2.5-flash-preview"
	//   openai     → "gpt-4.1-mini"
	//   anthropic  → "claude-3-haiku-20240307"
	Model string

	// BaseURL overrides the provider default.  Useful for proxies or
	// self-hosted OpenAI-compatible servers.
	BaseURL string

	// HTTPClient is optional; a sensible default with a 30 s timeout is
	// used when nil.
	HTTPClient *http.Client
}

// DefaultConfig returns sane defaults for the selected (or default) provider.
func DefaultConfig(provider Provider) Config {
	c := Config{Provider: provider}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	if c.Provider == "" {
		c.Provider = ProviderOpenRouter
	}
	if c.Model == "" {
		switch c.Provider {
		case ProviderOpenAI:
			c.Model = "gpt-4.1-mini"
		case ProviderAnthropic:
			c.Model = "claude-3-haiku-20240307"
		default:
			c.Model = "google/gemini-2.5-flash-preview"
		}
	}
	if c.BaseURL == "" {
		switch c.Provider {
		case ProviderOpenAI:
			c.BaseURL = "https://api.openai.com/v1"
		case ProviderAnthropic:
			c.BaseURL = "https://api.anthropic.com/v1"
		default:
			c.BaseURL = "https://openrouter.ai/api/v1"
		}
	}
}

// Describer wraps an LLM API client specialised for pattern naming.
type Describer struct {
	cfg    Config
	client *http.Client
}

// New returns a ready-to-use Describer.  APIKey must be non-empty.
func New(cfg Config) (*Describer, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("describer: APIKey is required")
	}
	cfg.applyDefaults()
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &Describer{cfg: cfg, client: client}, nil
}

// Describe sends up to maxSamples cluster members to the LLM and returns a
// concise plain-language description of the common task / pattern.
func (d *Describer) Describe(ctx context.Context, samples []string, clusterSize int) (string, error) {
	if len(samples) == 0 {
		return "", fmt.Errorf("describer: at least one sample is required")
	}
	trimmed := trimSamples(samples, 10)
	userPrompt := buildPrompt(trimmed, clusterSize)

	switch d.cfg.Provider {
	case ProviderAnthropic:
		return d.describeAnthropic(ctx, userPrompt)
	default:
		return d.describeOpenAI(ctx, userPrompt)
	}
}

// ---------- OpenAI-compatible path (OpenRouter, OpenAI, any /v1/chat/completions) ----------

func (d *Describer) describeOpenAI(ctx context.Context, userPrompt string) (string, error) {
	body := openAIRequest{
		Model: d.cfg.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.3,
		MaxTokens:   256,
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	url := strings.TrimRight(d.cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return "", fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+d.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s call: %w", d.cfg.Provider, err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s returned %d: %s", d.cfg.Provider, resp.StatusCode, string(respBytes))
	}
	var cr openAIResponse
	if err := json.Unmarshal(respBytes, &cr); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", d.cfg.Provider)
	}
	desc := strings.TrimSpace(cr.Choices[0].Message.Content)
	if desc == "" {
		return "", fmt.Errorf("%s returned empty description", d.cfg.Provider)
	}
	return desc, nil
}

// ---------- Anthropic Messages API path ----------

func (d *Describer) describeAnthropic(ctx context.Context, userPrompt string) (string, error) {
	body := anthropicRequest{
		Model:       d.cfg.Model,
		MaxTokens:   256,
		Temperature: 0.3,
		System:      systemPrompt,
		Messages: []anthropicMessage{
			{Role: "user", Content: userPrompt},
		},
	}
	reqBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal anthropic request: %w", err)
	}
	url := strings.TrimRight(d.cfg.BaseURL, "/") + "/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBytes))
	if err != nil {
		return "", fmt.Errorf("new anthropic request: %w", err)
	}
	req.Header.Set("x-api-key", d.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := d.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic call: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read anthropic response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, string(respBytes))
	}
	var ar anthropicResponse
	if err := json.Unmarshal(respBytes, &ar); err != nil {
		return "", fmt.Errorf("unmarshal anthropic response: %w", err)
	}
	if len(ar.Content) == 0 {
		return "", fmt.Errorf("anthropic returned no content blocks")
	}
	desc := strings.TrimSpace(ar.Content[0].Text)
	if desc == "" {
		return "", fmt.Errorf("anthropic returned empty description")
	}
	return desc, nil
}

// ---------- prompt building ----------

const systemPrompt = `You are a pattern-discovery assistant for an autonomous agent training system.
Your job is to look at a batch of user prompts sent to an AI assistant and describe the common task
or framework they represent.  Write a single sentence (under 140 characters) that captures the essence,
such as:
  "Extract structured contact fields from customer emails"
  "Summarise a technical article into three bullet points"
  "Translate a JSON object from English to French"
  "Generate a REST API endpoint from a database schema"
  "Rewrite a paragraph in a more professional tone"

Do NOT include quotation marks, markdown, or any preamble — just the sentence.`

func buildPrompt(samples []string, clusterSize int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A cluster of %d similar user prompts was detected. Here are %d examples:\n\n",
		clusterSize, len(samples))
	for i, s := range samples {
		fmt.Fprintf(&b, "%d. %s\n", i+1, s)
	}
	b.WriteString("\nWhat is the common task these prompts describe?")
	return b.String()
}

func trimSamples(samples []string, max int) []string {
	if len(samples) <= max {
		return samples
	}
	step := float64(len(samples)-1) / float64(max-1)
	out := make([]string, max)
	for i := 0; i < max; i++ {
		out[i] = samples[int(float64(i)*step)]
	}
	return out
}

// ---------- OpenAI / OpenRouter JSON types ----------

type openAIRequest struct {
	Model       string         `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64        `json:"temperature"`
	MaxTokens   int            `json:"max_tokens"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
}

// ---------- Anthropic JSON types ----------

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
	System      string             `json:"system"`
	Messages    []anthropicMessage `json:"messages"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Text string `json:"text"`
		Type string `json:"type"`
	} `json:"content"`
}
