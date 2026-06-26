package distill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const ArtifactVersion = "distill/v1"

// Distiller turns SessionTrace values into deterministic DistilledSession
// artifacts.
type Distiller struct {
	ledger Ledger
}

// NewDistiller returns a distiller. If ledger is nil, a no-op ledger is used.
func NewDistiller(ledger Ledger) *Distiller {
	if ledger == nil {
		ledger = noopLedger{}
	}
	return &Distiller{ledger: ledger}
}

// Distill compresses trace into a deterministic artifact. The same trace value
// always produces byte-identical output.
func (d *Distiller) Distill(ctx context.Context, trace SessionTrace) (DistilledSession, error) {
	decisions := dedupeSort(trace.Decisions)
	risks := dedupeSort(trace.Risks)
	questions := dedupeSort(trace.OpenQuestions)

	allRefs := make([]string, 0, len(trace.FileReferences))
	allRefs = append(allRefs, trace.FileReferences...)
	for _, t := range trace.Turns {
		allRefs = append(allRefs, t.FilesIn...)
		allRefs = append(allRefs, t.FilesOut...)
	}
	refs := dedupeSort(allRefs)

	touched := touchedFiles(trace.Turns)

	summary := buildSummary(trace.SessionID, len(trace.Turns), len(decisions), len(risks), len(questions), len(refs))

	out := DistilledSession{
		Version:        ArtifactVersion,
		SessionID:      trace.SessionID,
		Summary:        summary,
		Decisions:      decisions,
		Risks:          risks,
		OpenQuestions:  questions,
		FileReferences: refs,
		TouchedFiles:   touched,
	}

	if err := d.ledger.RecordDistill(ctx, int64(len(trace.Turns))*1000); err != nil {
		return out, fmt.Errorf("distill: ledger: %w", err)
	}
	return out, nil
}

// Marshal returns deterministic JSON for the artifact with keys in fixed order.
func Marshal(a DistilledSession) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	writeStringField(&buf, "version", a.Version, true)
	writeStringField(&buf, "session_id", a.SessionID, false)
	writeStringField(&buf, "summary", a.Summary, false)
	writeStringSlice(&buf, "decisions", a.Decisions, false)
	writeStringSlice(&buf, "risks", a.Risks, false)
	writeStringSlice(&buf, "open_questions", a.OpenQuestions, false)
	writeStringSlice(&buf, "file_references", a.FileReferences, false)
	writeStringSlice(&buf, "touched_files", a.TouchedFiles, false)
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Unmarshal parses a deterministic JSON artifact. It accepts any valid JSON
// with the expected fields.
func Unmarshal(data []byte) (DistilledSession, error) {
	var a DistilledSession
	if err := json.Unmarshal(data, &a); err != nil {
		return a, err
	}
	return a, nil
}

func writeStringField(buf *bytes.Buffer, key, value string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	fmt.Fprintf(buf, "%q:%q", key, value)
}

func writeStringSlice(buf *bytes.Buffer, key string, values []string, first bool) {
	if !first {
		buf.WriteByte(',')
	}
	fmt.Fprintf(buf, "%q:", key)
	b, _ := json.Marshal(values)
	buf.Write(b)
}

func dedupeSort(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func touchedFiles(turns []Turn) []string {
	seen := make(map[string]struct{})
	for _, t := range turns {
		for _, f := range t.FilesOut {
			seen[f] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

func buildSummary(sessionID string, turns, decisions, risks, questions, refs int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Distilled session %s: %d turn(s), %d decision(s), %d risk(s), %d open question(s), %d file reference(s).",
		sessionID, turns, decisions, risks, questions, refs)
	return b.String()
}
