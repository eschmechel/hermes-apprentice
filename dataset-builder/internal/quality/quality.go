package quality

import (
	"sort"
	"strings"

	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/fetcher"
)

var reaskPrefixes = []string{
	"could you clarify",
	"i need more",
	"i need additional",
	"can you provide more",
	"could you elaborate",
	"could you provide more",
	"can you clarify",
	"i don't understand",
	"i'm not sure",
	"let me explain",
	"i don't know",
	"can you rephrase",
	"i'm confused",
	"that doesn't answer",
	"that's not what",
	"it looks like you",
	"it seems you",
	"i think you misunderstood",
	"i was asking",
	"no, that's not",
	"no, i meant",
	"no, i need",
	"no, i want",
	"not exactly",
	"that's incorrect",
	"that's wrong",
	"try again",
	"rephrase",
	"redo",
	"restart",
}

var correctionPatterns = []string{
	"no, ",
	"nope,",
	"wrong,",
	"incorrect,",
	"that's not right",
	"that's wrong",
	"that's incorrect",
	"please fix",
	"you made a mistake",
	"can you correct",
	"fix the output",
	"redo",
	"try again",
	"that didn't work",
	"not what i asked",
	"you're wrong",
	"you are wrong",
	"you misunderstood",
	"that's not what i",
	"i said",
	"i meant",
	"no i said",
	"actually",
}

type indexed struct {
	fetcher.Record
	origIdx int
}

func Filter(records []fetcher.Record) []fetcher.Record {
	if len(records) == 0 {
		return records
	}
	bySession := make(map[string][]indexed)
	for i, r := range records {
		bySession[r.SessionID] = append(bySession[r.SessionID], indexed{r, i})
	}

	drop := make(map[int]bool)

	for sid, entries := range bySession {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].CreatedAt < entries[j].CreatedAt
		})
		for i, e := range entries {
			if hasReaskOutput(e.OutputText) {
				drop[e.origIdx] = true
				continue
			}
			if isCorrection(e, entries, i) {
				drop[e.origIdx] = true
			}
		}
		_ = sid
	}

	var filtered []fetcher.Record
	for i, r := range records {
		if !drop[i] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func hasReaskOutput(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	for _, p := range reaskPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func isCorrection(entry indexed, session []indexed, pos int) bool {
	lower := strings.ToLower(strings.TrimSpace(entry.InputText))
	for _, p := range correctionPatterns {
		if strings.Contains(lower, p) {
			for j := pos - 1; j >= 0 && pos-j <= 3; j-- {
				if session[j].OutputText != "" {
					return true
				}
			}
			return false
		}
	}
	return false
}
