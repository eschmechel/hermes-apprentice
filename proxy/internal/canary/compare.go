package canary

import (
	"encoding/json"
	"math"
	"strings"
)

func CompareResponses(specialist, upstream []byte) float64 {
	specContent := ExtractContent(specialist)
	upContent := ExtractContent(upstream)
	if specContent == "" && upContent == "" {
		return 1.0
	}
	specTokens := tokenize(specContent)
	upTokens := tokenize(upContent)
	if len(specTokens) == 0 && len(upTokens) == 0 {
		return 1.0
	}
	if len(specTokens) == 0 || len(upTokens) == 0 {
		return 0.0
	}
	intersection := 0
	set := make(map[string]int, len(specTokens))
	for _, t := range specTokens {
		set[t]++
	}
	for _, t := range upTokens {
		if set[t] > 0 {
			intersection++
			set[t]--
		}
	}
	union := len(specTokens) + len(upTokens) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

func ExtractContent(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if len(parsed.Choices) == 0 {
		return ""
	}
	return parsed.Choices[0].Message.Content
}

func tokenize(s string) []string {
	words := strings.Fields(strings.ToLower(s))
	if len(words) == 0 {
		return nil
	}
	out := make([]string, 0, len(words))
	for _, w := range words {
		cleaned := strings.Trim(w, ".,!?;:\"'()[]{}-")
		if cleaned != "" {
			out = append(out, cleaned)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func AgreementRatio(score float64, threshold float64) float64 {
	if threshold <= 0 {
		return 1.0
	}
	return math.Min(1.0, score/threshold)
}
