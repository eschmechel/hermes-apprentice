package dedup

import (
	"strings"
	"unicode"

	"github.com/hermes-apprentice/dataset-builder/internal/fetcher"
)

const DefaultThreshold = 0.85

type Config struct {
	Threshold float64
}

func Filter(records []fetcher.Record, cfg Config) []fetcher.Record {
	if cfg.Threshold <= 0 || cfg.Threshold > 1.0 {
		cfg.Threshold = DefaultThreshold
	}
	if len(records) < 2 {
		return records
	}
	shingles := make([]map[string]struct{}, len(records))
	for i, r := range records {
		shingles[i] = bigramShingles(r.InputText)
	}
	dup := make([]bool, len(records))
	for i := 0; i < len(records); i++ {
		if dup[i] {
			continue
		}
		for j := i + 1; j < len(records); j++ {
			if dup[j] {
				continue
			}
			if jaccard(shingles[i], shingles[j]) >= cfg.Threshold {
				dup[j] = true
			}
		}
	}
	var filtered []fetcher.Record
	for i, r := range records {
		if !dup[i] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func bigramShingles(text string) map[string]struct{} {
	lower := strings.ToLower(strings.TrimSpace(text))
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	set := make(map[string]struct{})
	if len(words) < 2 {
		if len(words) == 1 {
			set[words[0]] = struct{}{}
		}
		return set
	}
	for i := 0; i < len(words)-1; i++ {
		set[words[i]+" "+words[i+1]] = struct{}{}
	}
	return set
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 1.0
	}
	return float64(intersection) / float64(union)
}

