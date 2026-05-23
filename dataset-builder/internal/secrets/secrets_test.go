package secrets

import "testing"

func TestRedact_DetectsCommonSecrets(t *testing.T) {
	cases := []struct {
		name, in string
	}{
		{"aws", "use key AKIAIOSFODNN7EXAMPLE here"},
		{"openai", "token sk-abcdefghijklmnopqrstuvwxyz123456"},
		{"github", "ghp_1234567890abcdefghijklmnopqrstuvwxyz"},
		{"bearer", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456"},
		{"assigned", `api_key="supersecretvalue12345"`},
		{"private_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"},
	}
	for _, c := range cases {
		out, n := Redact(c.in)
		if n == 0 {
			t.Errorf("%s: expected a secret to be detected in %q", c.name, c.in)
		}
		if !Found(c.in) {
			t.Errorf("%s: Found should be true", c.name)
		}
		if out == c.in {
			t.Errorf("%s: text should have been redacted", c.name)
		}
	}
}

func TestRedact_PreservesAssignmentFieldName(t *testing.T) {
	out, n := Redact(`api_key=supersecretvalue12345`)
	if n != 1 {
		t.Fatalf("expected 1 secret, got %d", n)
	}
	if want := "api_key=" + Placeholder; out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRedact_LeavesCleanTextAlone(t *testing.T) {
	in := "The weather is nice and the order id is 42."
	out, n := Redact(in)
	if n != 0 || out != in {
		t.Errorf("clean text should be untouched, got %q (n=%d)", out, n)
	}
	if Found(in) {
		t.Error("Found should be false for clean text")
	}
}

func TestRedact_MultipleSecrets(t *testing.T) {
	in := "k1 AKIAIOSFODNN7EXAMPLE and k2 sk-abcdefghijklmnopqrstuvwxyz123456"
	_, n := Redact(in)
	if n < 2 {
		t.Errorf("expected >=2 secrets, got %d", n)
	}
}
