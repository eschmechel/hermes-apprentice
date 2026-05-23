package httpapi

import "testing"

func TestValidateSpecialistOutput(t *testing.T) {
	good := `{"choices":[{"message":{"content":"hello"}}]}`
	empty := `{"choices":[{"message":{"content":"   "}}]}`
	noChoices := `{"choices":[]}`
	jsonContent := `{"choices":[{"message":{"content":"{\"sku\":\"X\"}"}}]}`
	badJSON := `{"choices":[{"message":{"content":"not json"}}]}`

	plainReq := `{"model":"m","messages":[]}`
	jsonReq := `{"model":"m","response_format":{"type":"json_object"},"messages":[]}`

	cases := []struct {
		name, req, resp, want string
	}{
		{"ok", plainReq, good, ""},
		{"empty", plainReq, empty, "empty_completion"},
		{"no_choices", plainReq, noChoices, "no_assistant_content"},
		{"json_ok", jsonReq, jsonContent, ""},
		{"json_invalid", jsonReq, badJSON, "expected_json_invalid"},
		{"json_not_required", plainReq, badJSON, ""}, // plain text fine when JSON not asked
	}
	for _, c := range cases {
		if got := validateSpecialistOutput([]byte(c.req), []byte(c.resp)); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
