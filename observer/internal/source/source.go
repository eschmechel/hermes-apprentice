// Package source defines the SessionSource seam (W2): the observer's downstream
// pipeline (normalize -> dedup -> pair -> store -> HTTP) is agnostic to *where*
// conversations come from. Today there are two sources:
//
//   - hermes: tails the Hermes SQLite session DB (internal/poller.Poller)
//   - openai-log: tails a generic JSONL of OpenAI chat req/resp exchanges
//     (internal/source/openailog.Source)
//
// Both feed the same poller.Message handler, so "point any OpenAI-compatible
// agent at Apprentice and it grows local specialists" is a configuration choice,
// not a fork. A SessionSource is anything that streams messages until ctx ends.
package source

import "context"

// SessionSource streams conversation messages to a handler (configured on the
// concrete source) until ctx is cancelled.
type SessionSource interface {
	Run(ctx context.Context) error
}
