package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Record struct {
	ID                 int64           `json:"id"`
	SessionID          string          `json:"session_id"`
	PatternID          *string         `json:"pattern_id,omitempty"`
	InputHash          string          `json:"input_hash"`
	InputText          string          `json:"input_text"`
	OutputText         string          `json:"output_text"`
	SystemPromptHash   *string         `json:"system_prompt_hash,omitempty"`
	ModelUsed          *string         `json:"model_used,omitempty"`
	LatencyMs          int64           `json:"latency_ms"`
	TokenCounts        json.RawMessage `json:"token_counts,omitempty"`
	CreatedAt          float64         `json:"created_at"`
	UserMessageID      int64           `json:"user_message_id"`
	AssistantMessageID int64           `json:"assistant_message_id"`
}

type Client struct {
	baseURL    string
	httpClient *http.Client
	batchSize  int
}

func NewClient(observerURL string) *Client {
	return &Client{
		baseURL: observerURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		batchSize: 1000,
	}
}

func (c *Client) FetchAll(ctx context.Context, patternID string) ([]Record, error) {
	var all []Record
	var since float64

	for {
		batch, err := c.fetchBatch(ctx, patternID, since, c.batchSize)
		if err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		all = append(all, batch...)
		if len(batch) < c.batchSize {
			break
		}
		since = batch[len(batch)-1].CreatedAt
	}
	return all, nil
}

func (c *Client) fetchBatch(ctx context.Context, patternID string, since float64, limit int) ([]Record, error) {
	u, err := url.Parse(c.baseURL + "/records")
	if err != nil {
		return nil, fmt.Errorf("parse observer url: %w", err)
	}
	q := u.Query()
	q.Set("pattern", patternID)
	if since > 0 {
		q.Set("since", strconv.FormatFloat(since, 'f', -1, 64))
	}
	q.Set("limit", strconv.Itoa(limit))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch records: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		return nil, fmt.Errorf("observer returned %d: %s", resp.StatusCode, errBody.Error)
	}

	var records []Record
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, fmt.Errorf("decode records: %w", err)
	}
	return records, nil
}
