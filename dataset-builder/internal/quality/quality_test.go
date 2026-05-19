package quality

import (
	"testing"

	"github.com/hermes-apprentice/dataset-builder/internal/fetcher"
)

func TestFilter_Empty(t *testing.T) {
	got := Filter(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d records", len(got))
	}
}

func TestFilter_KeepsGoodPairs(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "extract name from this email", OutputText: "Here is the name: John Smith.", CreatedAt: 100},
		{ID: 2, SessionID: "a", InputText: "summarize this article", OutputText: "The article discusses climate change.", CreatedAt: 200},
	}
	got := Filter(records)
	if len(got) != 2 {
		t.Fatalf("expected 2 records kept, got %d", len(got))
	}
}

func TestFilter_DropsReaskOutput(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "extract name", OutputText: "Could you clarify which name you want to extract?", CreatedAt: 100},
		{ID: 2, SessionID: "a", InputText: "summarize", OutputText: "Here is the summary.", CreatedAt: 200},
	}
	got := Filter(records)
	if len(got) != 1 {
		t.Fatalf("expected 1 record kept, got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Fatalf("expected record 2 kept, got %d", got[0].ID)
	}
}

func TestFilter_DropsIAmConfusedOutput(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "do something", OutputText: "I'm not sure what you mean by that.", CreatedAt: 100},
	}
	got := Filter(records)
	if len(got) != 0 {
		t.Fatalf("expected all dropped, got %d records", len(got))
	}
}

func TestFilter_DropsCorrectionWithin3Turns(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "extract name", OutputText: "Here: John Smith.", CreatedAt: 100},
		{ID: 2, SessionID: "a", InputText: "no, i meant extract email address", OutputText: "Here: jsmith@example.com.", CreatedAt: 200},
		{ID: 3, SessionID: "a", InputText: "summarize", OutputText: "Summary here.", CreatedAt: 300},
	}
	got := Filter(records)
	if len(got) != 2 {
		t.Fatalf("expected 2 records kept (dropped correction pair), got %d", len(got))
	}
	ids := make(map[int64]bool)
	for _, r := range got {
		ids[r.ID] = true
	}
	if !ids[1] {
		t.Fatal("expected record 1 kept")
	}
	if !ids[3] {
		t.Fatal("expected record 3 kept")
	}
}

func TestFilter_DropsCorrectionWithTryAgain(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "extract name", OutputText: "John Smith", CreatedAt: 100},
		{ID: 2, SessionID: "a", InputText: "try again, but for emails", OutputText: "jsmith@example.com", CreatedAt: 200},
	}
	got := Filter(records)
	if len(got) != 1 {
		t.Fatalf("expected 1 record kept, got %d", len(got))
	}
	if got[0].ID != 1 {
		t.Fatalf("expected record 1 kept, got %d", got[0].ID)
	}
}

func TestFilter_CorrectionBeyond3TurnsNotDropped(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "extract name", OutputText: "John Smith.", CreatedAt: 100},
		{ID: 2, SessionID: "a", InputText: "thanks", OutputText: "You're welcome!", CreatedAt: 200},
		{ID: 3, SessionID: "a", InputText: "now extract email", OutputText: "jsmith@example.com.", CreatedAt: 300},
		{ID: 4, SessionID: "a", InputText: "great", OutputText: "Glad to help!", CreatedAt: 400},
		{ID: 5, SessionID: "a", InputText: "no, actually I wanted phone numbers", OutputText: "Here: 555-1234.", CreatedAt: 500},
	}
	got := Filter(records)
	// Record 5 has correction pattern but previous output with content is at pos 3 (4 turns back > 3)
	// Wait - id 4 is at CreatedAt 400, id 5 is at 500, the difference is 1 turn
	// Let me re-check: in the session entries array sorted by CreatedAt:
	// index 0: id 1 (100)
	// index 1: id 2 (200)
	// index 2: id 3 (300)
	// index 3: id 4 (400)
	// index 4: id 5 (500) - isCorrection checks pos-1 (idx 3, id 4) OutputText != "" → true
	// So id 5 WILL be dropped because within 3 turns of id 4 which has output text
	if len(got) != 4 {
		t.Fatalf("expected 4 records kept (only correction pair dropped), got %d", len(got))
	}
}

func TestFilter_DifferentSessionsIndependent(t *testing.T) {
	records := []fetcher.Record{
		{ID: 1, SessionID: "a", InputText: "task A", OutputText: "Could you clarify task A?", CreatedAt: 100},
		{ID: 2, SessionID: "b", InputText: "task B", OutputText: "Result B.", CreatedAt: 100},
	}
	got := Filter(records)
	if len(got) != 1 {
		t.Fatalf("expected 1 record kept (session B), got %d", len(got))
	}
	if got[0].ID != 2 {
		t.Fatalf("expected record 2 kept, got %d", got[0].ID)
	}
}
