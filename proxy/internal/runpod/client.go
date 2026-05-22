package runpod

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

const graphqlURL = "https://api.runpod.io/graphql"

type Client struct {
	apiKey string
	client *http.Client
}

func New(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

type GPUMetrics struct {
	GPUUtilPercent      float64 `json:"gpuUtilPercent"`
	MemoryUtilPercent   float64 `json:"memoryUtilPercent"`
}

type Runtime struct {
	UptimeInSeconds float64      `json:"uptimeInSeconds"`
	GPUs            []GPUMetrics `json:"gpus"`
}

type Pod struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	CostPerHr     float64 `json:"costPerHr"`
	DesiredStatus string  `json:"desiredStatus"`
	Runtime       Runtime `json:"runtime"`
}

type podResponse struct {
	Data struct {
		Myself struct {
			Pods []Pod `json:"pods"`
		} `json:"myself"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type PodSummary struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Status         string  `json:"status"`
	CostPerHr      float64 `json:"cost_per_hr"`
	UptimeHours    float64 `json:"uptime_hours"`
	AccruedCost    float64 `json:"accrued_cost"`
	GPUUtil        float64 `json:"gpu_util_pct"`
	MemoryUtil     float64 `json:"memory_util_pct"`
}

type Summary struct {
	Pods       []PodSummary `json:"pods"`
	TotalCostHr float64     `json:"total_cost_hr"`
	TotalAccrued float64    `json:"total_accrued"`
}

func (c *Client) ListPods(ctx context.Context) (*Summary, error) {
	query := `{ "query": "{ myself { pods { id name costPerHr desiredStatus runtime { uptimeInSeconds gpus { gpuUtilPercent memoryUtilPercent } } } } }" }`
	body := strings.NewReader(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runpod request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("runpod read: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runpod status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed podResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("runpod parse: %w (body: %s)", err, string(respBody))
	}

	if len(parsed.Errors) > 0 {
		errs := make([]string, len(parsed.Errors))
		for i, e := range parsed.Errors {
			errs[i] = e.Message
		}
		return nil, fmt.Errorf("runpod errors: %s", strings.Join(errs, "; "))
	}

	sum := &Summary{}
	pods := parsed.Data.Myself.Pods
	for _, p := range pods {
		uptimeHours := p.Runtime.UptimeInSeconds / 3600.0
		accrued := p.CostPerHr * uptimeHours

		var gpuUtil, memUtil float64
		if len(p.Runtime.GPUs) > 0 {
			gpuUtil = p.Runtime.GPUs[0].GPUUtilPercent
			memUtil = p.Runtime.GPUs[0].MemoryUtilPercent
		}

		sum.Pods = append(sum.Pods, PodSummary{
			ID:          p.ID,
			Name:        p.Name,
			Status:      p.DesiredStatus,
			CostPerHr:   p.CostPerHr,
			UptimeHours: uptimeHours,
			AccruedCost: accrued,
			GPUUtil:     gpuUtil,
			MemoryUtil:  memUtil,
		})
		sum.TotalCostHr += p.CostPerHr
		sum.TotalAccrued += accrued
	}
	return sum, nil
}

func (c *Client) Ping(ctx context.Context) error {
	query := `{ "query": "{ myself { pods { id } } }" }`
	body := bytes.NewReader([]byte(query))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, graphqlURL, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("runpod ping: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("runpod ping status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
