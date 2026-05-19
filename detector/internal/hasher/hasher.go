// Package hasher computes stable, normalized hashes of observer record
// inputs so the dedup store can recognize "we already embedded this" without
// holding the full text in the lookup index.
//
// We deliberately re-do the normalization step (lowercase, collapse internal
// whitespace, trim) here even though the observer also hashed the message:
// observer.records.input_hash is keyed on (role, content, tool_calls). The
// detector cares about *semantic input identity* and ignores trailing
// punctuation noise, so the two hashes are not interchangeable.
package hasher

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// Hash returns a hex-encoded SHA-256 of the normalized input.
func Hash(input string) string {
	n := Normalize(input)
	sum := sha256.Sum256([]byte(n))
	return hex.EncodeToString(sum[:])
}

// Normalize lowercases, collapses runs of whitespace to single spaces, and
// trims. Exported so tests + dedup-window debugging can reuse it.
func Normalize(input string) string {
	lower := strings.ToLower(input)
	var b strings.Builder
	b.Grow(len(lower))
	prevSpace := true
	for _, r := range lower {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
