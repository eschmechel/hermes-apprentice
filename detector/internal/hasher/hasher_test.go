package hasher

import "testing"

func TestNormalize_LowersAndCollapses(t *testing.T) {
	got := Normalize("  Extract Email   from\tCustomer\n MESSAGE  ")
	want := "extract email from customer message"
	if got != want {
		t.Fatalf("Normalize = %q, want %q", got, want)
	}
}

func TestHash_StableAcrossWhitespaceVariants(t *testing.T) {
	a := Hash("extract  email FROM customer message")
	b := Hash("Extract email from customer\tmessage\n")
	if a != b {
		t.Fatalf("hashes diverge across whitespace: %s vs %s", a, b)
	}
}

func TestHash_DifferentInputsHashDifferently(t *testing.T) {
	a := Hash("extract email from customer message")
	b := Hash("summarize this article")
	if a == b {
		t.Fatalf("distinct inputs collided: %s", a)
	}
}

func TestHash_FixedLength(t *testing.T) {
	h := Hash("anything")
	if len(h) != 64 {
		t.Fatalf("len = %d, want 64 (hex sha256)", len(h))
	}
}
