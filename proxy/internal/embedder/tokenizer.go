// Package embedder converts text to BGE-small ONNX embeddings.
// The tokenizer implements a minimal BERT WordPiece tokenizer matching the
// bert-base-uncased vocabulary used by BAAI/bge-small-en-v1.5.
package embedder

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
)

const (
	clsToken    = "[CLS]"
	sepToken    = "[SEP]"
	unkToken    = "[UNK]"
	padToken    = "[PAD]"
	maxSeqLen   = 512 // BGE-small max sequence length
	embeddingDim = 384
)

// Tokenizer holds the BERT vocabulary and special token IDs.
type Tokenizer struct {
	tokenToID map[string]int
	clsID     int
	sepID     int
	unkID     int
	padID     int
}

// LoadTokenizer reads a JSON array of tokens (ordered by ID) from path and
// builds a lookup map. The file format matches HuggingFace tokenizer.json
// vocab exported as a plain JSON array.
func LoadTokenizer(path string) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vocab: %w", err)
	}
	var tokens []string
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse vocab: %w", err)
	}

	tok := &Tokenizer{tokenToID: make(map[string]int, len(tokens))}
	for id, token := range tokens {
		tok.tokenToID[token] = id
	}

	tok.clsID = tok.tokenToID[clsToken]
	tok.sepID = tok.tokenToID[sepToken]
	tok.unkID = tok.tokenToID[unkToken]
	tok.padID = tok.tokenToID[padToken]
	return tok, nil
}

// Encode tokenizes text and returns input_ids, attention_mask, token_type_ids
// suitable for ONNX inference. Outputs are padded to maxSeqLen.
func (t *Tokenizer) Encode(text string) (inputIDs, attentionMask, tokenTypeIDs []int64) {
	tokens := t.tokenize(text)
	tokens = t.truncate(tokens)

	n := len(tokens)
	inputIDs = make([]int64, maxSeqLen)
	attentionMask = make([]int64, maxSeqLen)
	tokenTypeIDs = make([]int64, maxSeqLen)

	for i, tok := range tokens {
		if id, ok := t.tokenToID[tok]; ok {
			inputIDs[i] = int64(id)
		} else {
			inputIDs[i] = int64(t.unkID)
		}
		attentionMask[i] = 1
	}
	for i := n; i < maxSeqLen; i++ {
		inputIDs[i] = int64(t.padID)
	}

	return inputIDs, attentionMask, tokenTypeIDs
}

func (t *Tokenizer) tokenize(text string) []string {
	text = strings.ToLower(text)
	text = cleanText(text)

	words := strings.Fields(text)
	toks := []string{clsToken}
	for _, word := range words {
		subwords := t.wordpiece(word)
		toks = append(toks, subwords...)
	}
	toks = append(toks, sepToken)
	return toks
}

func (t *Tokenizer) truncate(tokens []string) []string {
	if len(tokens) > maxSeqLen-1 {
		return tokens[:maxSeqLen-1]
	}
	return tokens
}

// wordpiece applies BERT WordPiece tokenization to a single whitespace-delimited token.
// Tries longest-match-first, falling back to subwords with ## prefix.
func (t *Tokenizer) wordpiece(word string) []string {
	if _, ok := t.tokenToID[word]; ok {
		return []string{word}
	}

	var subwords []string
	runes := []rune(word)
	start := 0
	for start < len(runes) {
		end := len(runes)
		found := false
		for start < end {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if _, ok := t.tokenToID[sub]; ok {
				subwords = append(subwords, sub)
				start = end
				found = true
				break
			}
			end--
		}
		if !found {
			subwords = append(subwords, unkToken)
			break
		}
	}
	return subwords
}

// cleanText splits on punctuation per BERT basic tokenization rules.
func cleanText(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
