package redact

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRedact_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call analyze for empty text")
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.Redact(context.Background(), "")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestRedact_NoPII(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.Redact(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestRedact_ReplacePerson(t *testing.T) {
	input := "Hello Alice, nice to meet you."
	// "Alice," is bytes 6-12 (end exclusive)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]recognizerResult{
			{EntityType: "PERSON", Start: 6, End: 11, Score: 0.9},
		})
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.Redact(context.Background(), input)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	want := "Hello <PERSON>, nice to meet you."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedact_MultipleEntities(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]recognizerResult{
			{EntityType: "PERSON", Start: 0, End: 10, Score: 0.9},
			{EntityType: "EMAIL_ADDRESS", Start: 29, End: 47, Score: 0.95},
			{EntityType: "PHONE_NUMBER", Start: 53, End: 65, Score: 0.88},
		})
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.Redact(context.Background(), "John Smith sent a message to jsmith@example.com from 555-123-4567.")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	want := "<PERSON> sent a message to <EMAIL_ADDRESS> from <PHONE_NUMBER>."
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRedact_PresidioError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "down"})
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	_, err := c.Redact(context.Background(), "some text")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestRedact_OverlappingSpans(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]recognizerResult{
			{EntityType: "PHONE_NUMBER", Start: 10, End: 22, Score: 0.9},
			{EntityType: "CREDIT_CARD", Start: 10, End: 26, Score: 0.85},
		})
	}))
	defer ts.Close()

	c := NewClient(ts.URL)
	got, err := c.Redact(context.Background(), "text here 555-123-4567-8900 end")
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}
	if got == "text here 555-123-4567-8900 end" {
		t.Fatalf("expected PII to be redacted, got unchanged")
	}
}
