// Package cost provides per-model token pricing data used to estimate
// monetary cost of upstream requests.  Pricing is seeded with compiled-in
// defaults sourced from OpenRouter's current rates and can be overridden
// via ~/.apprentice/pricing.json.
//
// Current default pricing sourced from:
//
//	https://openrouter.ai/docs/models (retrieved 2026-05-19)
package cost

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type ModelPricing struct {
	PromptPerMillionUSD      float64 `json:"prompt_per_million_usd"`
	CompletionPerMillionUSD  float64 `json:"completion_per_million_usd"`
}

type Pricing struct {
	models map[string]ModelPricing
}

func New(models map[string]ModelPricing) *Pricing {
	if models == nil {
		models = Defaults()
	}
	return &Pricing{models: models}
}

// LoadFile reads a JSON file mapping model name → ModelPricing, merges it
// with the compiled-in defaults (file values take precedence), and returns a
// Pricing.  If the file doesn't exist the compiled-in defaults are returned.
func LoadFile(path string) (*Pricing, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return New(nil), nil
		}
		return New(nil), err
	}
	defer f.Close()

	var custom map[string]ModelPricing
	if err := json.NewDecoder(f).Decode(&custom); err != nil {
		return nil, err
	}
	models := Defaults()
	for k, v := range custom {
		models[k] = v
	}
	return New(models), nil
}

func (p *Pricing) Lookup(model string) (ModelPricing, bool) {
	pr, ok := p.models[model]
	return pr, ok
}

// ComputeCost returns the estimated USD cost for the given token counts.
// Returns -1 if the model is unknown (no pricing data).
func (p *Pricing) ComputeCost(model string, promptTokens, completionTokens int) float64 {
	pr, ok := p.Lookup(model)
	if !ok {
		return -1
	}
	promptCost := float64(promptTokens) * pr.PromptPerMillionUSD / 1_000_000
	completionCost := float64(completionTokens) * pr.CompletionPerMillionUSD / 1_000_000
	return promptCost + completionCost
}

// Defaults returns the compiled-in OpenRouter pricing for commonly used
// models.  Specialists (locally-hosted) are not listed here — their cost
// is always 0.
func Defaults() map[string]ModelPricing {
	return map[string]ModelPricing{
		// Anthropic
		"anthropic/claude-3.5-sonnet":        {3.00, 15.00},
		"anthropic/claude-3.5-haiku":         {0.80, 4.00},
		"anthropic/claude-3-opus":            {15.00, 75.00},
		"anthropic/claude-3-sonnet":          {3.00, 15.00},
		"anthropic/claude-3-haiku":           {0.25, 1.25},
		// OpenAI
		"openai/gpt-4o":                      {2.50, 10.00},
		"openai/gpt-4o-mini":                 {0.15, 0.60},
		"openai/gpt-4-turbo":                 {10.00, 30.00},
		"openai/gpt-4":                       {30.00, 60.00},
		"openai/gpt-3.5-turbo":               {0.50, 1.50},
		// Meta
		"meta-llama/llama-3.3-70b-instruct":  {0.23, 0.40},
		"meta-llama/llama-3.1-405b-instruct": {1.79, 1.79},
		"meta-llama/llama-3.1-70b-instruct":  {0.23, 0.40},
		"meta-llama/llama-3.1-8b-instruct":   {0.03, 0.05},
		// Misc free (best-effort for completeness)
		"google/gemini-flash-1.5":            {0, 0},
	}
}
