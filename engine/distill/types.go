// Package distill provides deterministic, non-LLM session compression for
// graphi. It converts a SessionTrace into a compact DistilledSession artifact
// that can be reloaded later to restore context without replaying the full
// trace.
package distill

import (
	"context"
)

// Ledger records avoided token spend when distillation is performed.
type Ledger interface {
	RecordDistill(ctx context.Context, savedTokens int64) error
}

type noopLedger struct{}

func (noopLedger) RecordDistill(ctx context.Context, savedTokens int64) error { return nil }

// Turn captures one agent turn in the session.
type Turn struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	FilesIn  []string `json:"files_in"`
	FilesOut []string `json:"files_out"`
}

// SessionTrace is the uncompressed input to distillation.
type SessionTrace struct {
	SessionID      string   `json:"session_id"`
	Turns          []Turn   `json:"turns"`
	Decisions      []string `json:"decisions"`
	Risks          []string `json:"risks"`
	OpenQuestions  []string `json:"open_questions"`
	FileReferences []string `json:"file_references"`
}

// DistilledSession is the compressed, reusable artifact.
type DistilledSession struct {
	Version        string   `json:"version"`
	SessionID      string   `json:"session_id"`
	Summary        string   `json:"summary"`
	Decisions      []string `json:"decisions"`
	Risks          []string `json:"risks"`
	OpenQuestions  []string `json:"open_questions"`
	FileReferences []string `json:"file_references"`
	TouchedFiles   []string `json:"touched_files"`
}
