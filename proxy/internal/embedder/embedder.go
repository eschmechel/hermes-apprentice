package embedder

import (
	"fmt"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Embedder loads the BGE-small ONNX model and returns normalized sentence
// embeddings. Thread-safe for concurrent inference on a shared session.
type Embedder struct {
	mu        sync.Mutex
	session   *ort.DynamicAdvancedSession
	tokenizer *Tokenizer
}

// New creates an Embedder. Caller must have already called
// ort.InitializeEnvironment() and should call ort.DestroyEnvironment()
// after all embedders are closed.
func New(modelPath, vocabPath string) (*Embedder, error) {
	tok, err := LoadTokenizer(vocabPath)
	if err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}

	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("session options: %w", err)
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		opts,
	)
	if err != nil {
		opts.Destroy()
		return nil, fmt.Errorf("load model %s: %w", modelPath, err)
	}

	return &Embedder{session: session, tokenizer: tok}, nil
}

// Embed returns a normalized 384-dim embedding for text. Safe for concurrent use.
func (e *Embedder) Embed(text string) ([]float32, error) {
	inputIDs, attentionMask, tokenTypeIDs := e.tokenizer.Encode(text)

	inputTensor, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), inputIDs)
	if err != nil {
		return nil, fmt.Errorf("input_ids tensor: %w", err)
	}
	defer inputTensor.Destroy()

	attnTensor, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), attentionMask)
	if err != nil {
		return nil, fmt.Errorf("attention_mask tensor: %w", err)
	}
	defer attnTensor.Destroy()

	typeIDsTensor, err := ort.NewTensor(ort.NewShape(1, maxSeqLen), tokenTypeIDs)
	if err != nil {
		return nil, fmt.Errorf("token_type_ids tensor: %w", err)
	}
	defer typeIDsTensor.Destroy()

	// nil output → auto-allocated by Run()
	outputs := []ort.Value{nil}

	e.mu.Lock()
	err = e.session.Run(
		[]ort.Value{inputTensor, attnTensor, typeIDsTensor},
		outputs,
	)
	e.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("inference: %w", err)
	}
	output := outputs[0]
	defer output.Destroy()

	// last_hidden_state: [1, seq_len, 384] → take CLS token [0, 0, :]
	tensor, ok := output.(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output type %T", output)
	}
	hidden := tensor.GetData()
	clsStart := 0
	clsEnd := embeddingDim
	vec := make([]float32, embeddingDim)
	copy(vec, hidden[clsStart:clsEnd])

	l2Norm(vec)
	return vec, nil
}

// Close releases ONNX session resources.
func (e *Embedder) Close() error {
	if e == nil || e.session == nil {
		return nil
	}
	return e.session.Destroy()
}

func l2Norm(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return
	}
	for i := range v {
		v[i] /= norm
	}
}
