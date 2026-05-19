package redact

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(presidioURL string) *Client {
	return &Client{
		baseURL: presidioURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type analyzeRequest struct {
	Text     string `json:"text"`
	Language string `json:"language"`
}

type recognizerResult struct {
	EntityType string  `json:"entity_type"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
}

func (c *Client) Redact(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}
	results, err := c.analyze(ctx, text)
	if err != nil {
		return "", err
	}
	return replaceSpans(text, results), nil
}

func (c *Client) analyze(ctx context.Context, text string) ([]recognizerResult, error) {
	body, err := json.Marshal(analyzeRequest{
		Text:     text,
		Language: "en",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal analyze request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build analyze request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("presidio returned %d: %s", resp.StatusCode, errBody.Error)
	}

	var results []recognizerResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode analyze response: %w", err)
	}
	return results, nil
}

func replaceSpans(text string, spans []recognizerResult) string {
	if len(spans) == 0 {
		return text
	}
	sort.Slice(spans, func(i, j int) bool {
		return spans[i].Start > spans[j].Start
	})
	for _, s := range spans {
		if s.Start >= len(text) || s.End > len(text) || s.Start >= s.End {
			continue
		}
		tag := "<" + s.EntityType + ">"
		text = text[:s.Start] + tag + text[s.End:]
	}
	return text
}
