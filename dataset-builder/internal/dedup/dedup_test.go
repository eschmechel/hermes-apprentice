package dedup

import (
	"testing"

	"github.com/eschmechel/hermes-apprentice/dataset-builder/internal/fetcher"
)

func TestFilter_Empty(t *testing.T) {
	got := Filter(nil, Config{Threshold: DefaultThreshold})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d records", len(got))
	}
}

func TestFilter_SingleRecord(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
	}
	got := Filter(records, Config{Threshold: DefaultThreshold})
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
}

func TestFilter_IdenticalText(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
		{ID: 2, InputText: "extract name from customer email"},
	}
	got := Filter(records, Config{Threshold: DefaultThreshold})
	if len(got) != 1 {
		t.Fatalf("expected 1 record (duplicate dropped), got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Fatalf("expected first occurrence kept (id=1), got id=%d", got[0].ID)
	}
}

func TestFilter_DifferentTexts(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
		{ID: 2, InputText: "summarize this article about climate change"},
		{ID: 3, InputText: "translate text from French to English"},
	}
	got := Filter(records, Config{Threshold: 0.85})
	if len(got) != 3 {
		t.Fatalf("expected all 3 kept (different texts), got %d", len(got))
	}
}

func TestFilter_NearDuplicate(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email for me please"},
		{ID: 2, InputText: "extract name from customer email for me please thanks"},
	}
	got := Filter(records, Config{Threshold: 0.85})
	if len(got) != 1 {
		t.Fatalf("expected 1 record (near-dup dropped), got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Fatalf("expected first occurrence kept, got id=%d", got[0].ID)
	}
}

func TestFilter_ChainDedup(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
		{ID: 2, InputText: "extract name from customer email"},
		{ID: 3, InputText: "extract name from customer email"},
	}
	got := Filter(records, Config{Threshold: 0.85})
	if len(got) != 1 {
		t.Fatalf("expected 1 record (chain deduped), got %d", len(got))
	}
}

func TestFilter_CustomThreshold(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
		{ID: 2, InputText: "extract email from customer message"},
	}
	got := Filter(records, Config{Threshold: 0.99})
	if len(got) != 2 {
		t.Fatalf("high threshold should keep both, got %d", len(got))
	}
}

func TestFilter_DefaultThresholdApplied(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, InputText: "extract name from customer email"},
		{ID: 2, InputText: "extract name from customer email"},
	}
	got := Filter(records, Config{})
	if len(got) != 1 {
		t.Fatalf("default threshold should drop identical, got %d", len(got))
	}
}
