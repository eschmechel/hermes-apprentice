// Package secrets is a regex secrets scanner that runs alongside Presidio PII
// redaction (W8). Presidio catches names/emails/phone numbers; this catches the
// machine secrets that leak into agent transcripts — API keys, tokens, private
// keys — which would otherwise be baked into a specialist's weights.
package secrets

import "regexp"

// Placeholder is what a detected secret is replaced with.
const Placeholder = "[REDACTED_SECRET]"

// rule is one detector. Patterns are intentionally conservative (anchored on
// recognizable prefixes / structure) to keep the false-positive rate low.
type rule struct {
	name string
	re   *regexp.Regexp
}

var rules = []rule{
	{"private_key_block", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{"aws_access_key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b`)},
	{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}\b`)},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{"bearer_token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/-]{20,}=*`)},
	// key=value / key: value assignments for obvious secret-named fields.
	{"assigned_secret", regexp.MustCompile(`(?i)(api[_-]?key|secret|token|passwd|password)\s*[:=]\s*["']?[A-Za-z0-9._\-/+]{12,}["']?`)},
}

// Redact replaces every detected secret in text with Placeholder and returns the
// redacted text plus the number of secrets found.
func Redact(text string) (string, int) {
	count := 0
	out := text
	for _, r := range rules {
		out = r.re.ReplaceAllStringFunc(out, func(match string) string {
			count++
			// For assignment forms, keep the field name so the data is still
			// shaped naturally ("api_key=[REDACTED_SECRET]").
			if r.name == "assigned_secret" {
				if loc := assignSplit.FindStringSubmatchIndex(match); loc != nil {
					return match[:loc[3]] + Placeholder
				}
			}
			return Placeholder
		})
	}
	return out, count
}

// assignSplit captures "<field><sep>" so we can preserve the field name.
var assignSplit = regexp.MustCompile(`(?i)^((api[_-]?key|secret|token|passwd|password)\s*[:=]\s*["']?)`)

// Found reports whether text contains any detectable secret (no allocation of a
// redacted copy).
func Found(text string) bool {
	for _, r := range rules {
		if r.re.MatchString(text) {
			return true
		}
	}
	return false
}
