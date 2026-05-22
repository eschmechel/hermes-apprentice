package embedder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func vocabPath(t *testing.T) string {
	t.Helper()
	tokens := make([]string, 30000)
	tokens[0] = "[PAD]"
	tokens[1] = "[UNK]"
	tokens[2] = "[CLS]"
	tokens[3] = "[SEP]"
	tokens[4] = "[MASK]"
	tokens[5] = "hello"
	tokens[6] = "world"
	tokens[7] = "##tion"
	tokens[8] = "test"
	tokens[9] = "a"
	tokens[10] = "b"
	tokens[11] = "##c"
	tokens[12] = "the"
	tokens[13] = "is"
	tokens[14] = "at"
	tokens[15] = "to"
	tokens[16] = "we"
	tokens[17] = "are"
	tokens[18] = "nation"
	tokens[19] = "you"
	for i := 20; i < 30000; i++ {
		tokens[i] = string(rune(i))
	}
	data, err := json.Marshal(tokens)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "vocab.json")
	os.WriteFile(path, data, 0o644)
	return path
}

func TestLoadTokenizer(t *testing.T) {
	path := vocabPath(t)
	tok, err := LoadTokenizer(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok == nil {
		t.Fatal("expected non-nil tokenizer")
	}
	if tok.clsID != 2 {
		t.Errorf("expected clsID=2, got %d", tok.clsID)
	}
	if tok.sepID != 3 {
		t.Errorf("expected sepID=3, got %d", tok.sepID)
	}
	if tok.unkID != 1 {
		t.Errorf("expected unkID=1, got %d", tok.unkID)
	}
	if tok.padID != 0 {
		t.Errorf("expected padID=0, got %d", tok.padID)
	}
}

func TestLoadTokenizerFileNotFound(t *testing.T) {
	_, err := LoadTokenizer("/nonexistent/vocab.json")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadTokenizerInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(path, []byte("not json"), 0o644)
	_, err := LoadTokenizer(path)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEncodeBasic(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	ids, mask, typeIDs := tok.Encode("hello world")

	if ids[0] != int64(tok.clsID) {
		t.Errorf("expected CLS at position 0, got %d", ids[0])
	}
	if mask[0] != 1 {
		t.Errorf("expected attention_mask=1 for CLS")
	}
	if typeIDs[0] != 0 {
		t.Errorf("expected token_type_ids=0")
	}
	if n := len(ids); n != maxSeqLen {
		t.Errorf("expected length %d, got %d", maxSeqLen, n)
	}
	if n := len(mask); n != maxSeqLen {
		t.Errorf("expected attention_mask length %d, got %d", maxSeqLen, n)
	}
	if ids[1] != int64(tok.tokenToID["hello"]) {
		t.Errorf("expected 'hello' token at pos 1, got %d", ids[1])
	}
	if ids[2] != int64(tok.tokenToID["world"]) {
		t.Errorf("expected 'world' token at pos 2, got %d", ids[2])
	}
	if ids[3] != int64(tok.sepID) {
		t.Errorf("expected SEP token at pos 3, got %d", ids[3])
	}
	for i := 4; i < maxSeqLen; i++ {
		if ids[i] != int64(tok.padID) {
			t.Errorf("expected padding at position %d, got %d", i, ids[i])
		}
		if mask[i] != 0 {
			t.Errorf("expected mask[%d]=0, got %d", i, mask[i])
		}
	}
}

func TestEncodeUnknownToken(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	ids, _, _ := tok.Encode("\u8000")

	found := false
	for _, id := range ids {
		if id == int64(tok.unkID) {
			found = true
			break
		}
		if id == int64(tok.padID) {
			break
		}
	}
	if !found {
		t.Fatal("expected [UNK] token for unknown word")
	}
}

func TestEncodeEmptyText(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	ids, mask, _ := tok.Encode("")
	if ids[0] != int64(tok.clsID) {
		t.Errorf("expected CLS, got %d", ids[0])
	}
	if ids[1] != int64(tok.sepID) {
		t.Errorf("expected SEP, got %d", ids[1])
	}
	if mask[0] != 1 || mask[1] != 1 {
		t.Errorf("expected masks=1 for CLS+SEP")
	}
}

func TestEncodeVeryLongTruncation(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	text := ""
	for i := 0; i < 600; i++ {
		text += "test "
	}
	ids, _, _ := tok.Encode(text)
	if ids[maxSeqLen-1] != int64(tok.padID) {
		t.Errorf("expected padding at last position due to truncation")
	}
}

func TestWordpieceKnownWord(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	subwords := tok.wordpiece("hello")
	if len(subwords) != 1 || subwords[0] != "hello" {
		t.Errorf("expected ['hello'], got %v", subwords)
	}
}

func TestWordpieceNation(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	subwords := tok.wordpiece("nation")
	if len(subwords) != 1 || subwords[0] != "nation" {
		t.Errorf("expected ['nation'], got %v", subwords)
	}
}

func TestWordpieceUnknownWithPrefix(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	subwords := tok.wordpiece("inc")
	if len(subwords) < 1 {
		t.Error("expected non-empty subwords")
	}
	if subwords[0] == unkToken {
		t.Errorf("expected decomposition, got [UNK]")
	}
}

func TestWordpieceFullyUnknown(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	subwords := tok.wordpiece("\u8000")
	if len(subwords) != 1 || subwords[0] != unkToken {
		t.Errorf("expected [UNK], got %v", subwords)
	}
}

func TestTruncateUnderLimit(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	tokens := []string{"a", "b", "c"}
	result := tok.truncate(tokens)
	if len(result) != 3 {
		t.Errorf("expected 3 tokens, got %d", len(result))
	}
}

func TestTruncateOverLimit(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	tokens := make([]string, maxSeqLen+10)
	result := tok.truncate(tokens)
	if len(result) != maxSeqLen-1 {
		t.Errorf("expected %d tokens, got %d", maxSeqLen-1, len(result))
	}
}

func TestCleanTextRemovesPunctuation(t *testing.T) {
	result := cleanText("hello, world!")
	if result == "hello, world!" {
		t.Error("expected punctuation cleanup")
	}
}

func TestCleanTextSymbols(t *testing.T) {
	result := cleanText("test@example")
	if result == "test@example" {
		t.Error("expected symbol cleanup")
	}
}

func TestL2Norm(t *testing.T) {
	v := []float32{3, 4}
	l2Norm(v)
	expected0 := float32(3.0 / 5.0)
	expected1 := float32(4.0 / 5.0)
	if v[0] != expected0 || v[1] != expected1 {
		t.Errorf("expected [%f, %f], got %v", expected0, expected1, v)
	}
}

func TestL2NormZero(t *testing.T) {
	v := []float32{0, 0, 0}
	l2Norm(v)
	for i := range v {
		if v[i] != 0 {
			t.Errorf("expected all zeros, got %v", v)
		}
	}
}

func TestEncodeKnownWords(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	ids, _, _ := tok.Encode("test a b the")

	knownIDs := map[int64]bool{
		int64(tok.tokenToID["test"]): true,
		int64(tok.tokenToID["a"]):    true,
		int64(tok.tokenToID["b"]):    true,
		int64(tok.tokenToID["the"]):  true,
	}
	for _, id := range ids {
		if id == int64(tok.padID) {
			break
		}
		if id == int64(tok.clsID) || id == int64(tok.sepID) {
			continue
		}
		if !knownIDs[id] {
			t.Errorf("unexpected token ID %d in output", id)
		}
	}
}

func TestEncodeReturnsCorrectShapes(t *testing.T) {
	tok, _ := LoadTokenizer(vocabPath(t))
	ids, mask, typeIDs := tok.Encode("test")
	if len(ids) != maxSeqLen || len(mask) != maxSeqLen || len(typeIDs) != maxSeqLen {
		t.Fatalf("expected all arrays to be %d long", maxSeqLen)
	}
}
