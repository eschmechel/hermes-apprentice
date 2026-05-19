package splitter

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hermes-apprentice/dataset-builder/internal/fetcher"
)

func makeRecs(n int) []fetcher.Record {
	out := make([]fetcher.Record, n)
	for i := 0; i < n; i++ {
		out[i] = fetcher.Record{
			ID:         int64(i + 1),
			SessionID:  "sess-1",
			InputText:  "extract name from email",
			OutputText: "John Doe",
		}
	}
	return out
}

func ids(recs []fetcher.Record) []int64 {
	out := make([]int64, len(recs))
	for i, r := range recs {
		out[i] = r.ID
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestSplit_Ratios(t *testing.T) {
	recs := makeRecs(100)
	sr := Split(recs, 42)

	if len(sr.Train) != 80 {
		t.Fatalf("train = %d, want 80", len(sr.Train))
	}
	if len(sr.Val) != 10 {
		t.Fatalf("val = %d, want 10", len(sr.Val))
	}
	if len(sr.Test) != 10 {
		t.Fatalf("test = %d, want 10", len(sr.Test))
	}
}

func TestSplit_Deterministic(t *testing.T) {
	recs := makeRecs(100)
	a := Split(recs, 42)
	b := Split(recs, 42)

	// Compare IDs in each split — same seed should produce same ordering.
	aTrain := ids(a.Train)
	bTrain := ids(b.Train)
	for i := range aTrain {
		if aTrain[i] != bTrain[i] {
			t.Fatalf("train[%d] = %d vs %d", i, aTrain[i], bTrain[i])
		}
	}

	// Different seed should produce different ordering.
	c := Split(recs, 99)
	cTrain := ids(c.Train)
	sameOrder := true
	for i := range aTrain {
		if aTrain[i] != cTrain[i] {
			sameOrder = false
			break
		}
	}
	if sameOrder {
		t.Fatalf("different seeds produced identical ordering")
	}
}

func TestSplit_SmallN(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{"n=0", 0},
		{"n=1", 1},
		{"n=2", 2},
		{"n=3", 3},
		{"n=5", 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recs := makeRecs(tt.n)
			sr := Split(recs, 42)
			total := len(sr.Train) + len(sr.Val) + len(sr.Test)
			if total != tt.n {
				t.Fatalf("total across splits = %d, want %d", total, tt.n)
			}
		})
	}
}

func TestSplit_AllRecordsAccountedFor(t *testing.T) {
	recs := makeRecs(100)
	sr := Split(recs, 42)

	seen := make(map[int64]bool)
	for _, r := range sr.Train {
		seen[r.ID] = true
	}
	for _, r := range sr.Val {
		seen[r.ID] = true
	}
	for _, r := range sr.Test {
		seen[r.ID] = true
	}
	if len(seen) != 100 {
		t.Fatalf("seen = %d unique IDs, want 100", len(seen))
	}
}

func TestWrite_RoundTrip(t *testing.T) {
	recs := makeRecs(5)
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl.gz")

	if err := Write(path, recs, "You are a helpful assistant."); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Read back and verify.
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	dec := json.NewDecoder(gr)
	var count int
	for {
		var ex hermesExample
		if err := dec.Decode(&ex); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode line %d: %v", count, err)
		}
		if len(ex.Messages) != 3 {
			t.Fatalf("line %d: expected 3 messages, got %d", count, len(ex.Messages))
		}
		if ex.Messages[0].Role != "system" {
			t.Fatalf("line %d: msg[0].role = %q, want system", count, ex.Messages[0].Role)
		}
		if ex.Messages[1].Role != "user" {
			t.Fatalf("line %d: msg[1].role = %q, want user", count, ex.Messages[1].Role)
		}
		if ex.Messages[2].Role != "assistant" {
			t.Fatalf("line %d: msg[2].role = %q, want assistant", count, ex.Messages[2].Role)
		}
		if ex.Messages[1].Content != "extract name from email" {
			t.Fatalf("line %d: user content = %q", count, ex.Messages[1].Content)
		}
		if ex.Messages[2].Content != "John Doe" {
			t.Fatalf("line %d: assistant content = %q", count, ex.Messages[2].Content)
		}
		count++
	}
	if count != 5 {
		t.Fatalf("read %d lines, want 5", count)
	}
}

func TestWriteSplits(t *testing.T) {
	recs := makeRecs(100)
	dir := t.TempDir()

	train, val, test, err := WriteSplits(dir, "You are helpful.", recs, 42)
	if err != nil {
		t.Fatalf("WriteSplits: %v", err)
	}

	for _, p := range []string{train, val, test} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		f, _ := os.Open(p)
		gr, _ := gzip.NewReader(f)
		defer gr.Close()
		data, _ := io.ReadAll(gr)
		f.Close()
		if len(data) == 0 {
			t.Fatalf("%s is empty", filepath.Base(p))
		}
	}

	// Verify filenames.
	if filepath.Base(train) != "train.jsonl.gz" {
		t.Fatalf("train path = %q", train)
	}
	if filepath.Base(val) != "val.jsonl.gz" {
		t.Fatalf("val path = %q", val)
	}
	if filepath.Base(test) != "test.jsonl.gz" {
		t.Fatalf("test path = %q", test)
	}
}
