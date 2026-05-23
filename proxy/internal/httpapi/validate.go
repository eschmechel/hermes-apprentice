package httpapi

import (
	"bytes"
	"encoding/json"
)

// validateSpecialistOutput is the output-guard (W7): beyond HTTP-level health
// (specialistResponseOK), it inspects the *content* the specialist produced and
// rejects responses that would degrade the caller — empty completions, and
// non-JSON bodies when the request asked for response_format json. A rejection
// makes the proxy fall through to upstream (and trips the breaker), so a
// quietly-broken specialist degrades to the teacher rather than to the user.
//
// Returns "" when the output is acceptable, else a short reason for the log.
func validateSpecialistOutput(reqBody, respBody []byte) string {
	content, ok := firstChoiceContent(respBody)
	if !ok {
		return "no_assistant_content"
	}
	if len(bytes.TrimSpace([]byte(content))) == 0 {
		return "empty_completion"
	}
	if requestWantsJSON(reqBody) && !json.Valid([]byte(content)) {
		return "expected_json_invalid"
	}
	return ""
}

// firstChoiceContent extracts choices[0].message.content as a string.
func firstChoiceContent(respBody []byte) (string, bool) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", false
	}
	if len(parsed.Choices) == 0 {
		return "", false
	}
	return parsed.Choices[0].Message.Content, true
}

// requestWantsJSON reports whether the request set response_format to a JSON
// type (json_object / json_schema), the OpenAI structured-output convention.
func requestWantsJSON(reqBody []byte) bool {
	var parsed struct {
		ResponseFormat *struct {
			Type string `json:"type"`
		} `json:"response_format"`
	}
	if err := json.Unmarshal(reqBody, &parsed); err != nil || parsed.ResponseFormat == nil {
		return false
	}
	switch parsed.ResponseFormat.Type {
	case "json_object", "json_schema":
		return true
	}
	return false
}
