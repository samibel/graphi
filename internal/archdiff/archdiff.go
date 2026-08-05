// Package archdiff is the ARCH-P0 differential harness for the P2 architecture
// refactor.
//
// The refactor's central promise is that behaviour does not change: use-case
// logic moves out of surfaces/client into an application layer, and every
// surface must keep producing the same bytes. The PRD therefore requires that a
// legacy and a new code path can be driven with identical input and compared,
// before any production code moves.
//
// This package is that harness. It drives use cases through the
// surfaces/client.Client seam — the one boundary both the legacy client and a
// future application-backed client satisfy — so a later phase supplies a second
// implementation and calls Compare with no change here.
//
// In Phase 0 only the legacy side exists, so the harness does two things:
// it records the legacy outcomes as a checked-in baseline, and it proves the
// recording is reproducible (two independently built environments must produce
// identical results). A harness that could not reproduce its own output would be
// worthless as evidence later, so that proof comes first.
//
// What is recorded is a digest per use case, not the bytes: the byte-level
// goldens already exist in the characterization suite, and duplicating them here
// would create a second thing to update on every intentional change. What this
// baseline adds is BREADTH — every bounded context, including the fail-closed
// paths — at a size that stays reviewable.
//
// Like internal/bench and internal/perfbaseline, this is UNRANKED CI tooling.
package archdiff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/surfaces/client"
)

// SchemaVersion versions the artifact envelope.
const SchemaVersion = 1

// Outcome kinds recorded for a use case.
const (
	// OutcomeOK means the call returned bytes.
	OutcomeOK = "ok"
	// OutcomeSentinel means the call refused with a known typed sentinel. The
	// sentinel's identity IS the recorded contract: an unwired capability must
	// stay fail-closed across the refactor, never quietly become a success.
	OutcomeSentinel = "sentinel"
	// OutcomeError means the call failed with an error that is not a known
	// sentinel. Recorded rather than hidden, so it cannot be mistaken for a pass.
	OutcomeError = "error"
)

// Entry is the recorded result of one use case.
type Entry struct {
	// Context is the bounded context this use case belongs to.
	Context string `json:"context"`
	// Outcome is one of the Outcome* constants.
	Outcome string `json:"outcome"`
	// Sentinel names the typed error when Outcome is OutcomeSentinel.
	Sentinel string `json:"sentinel,omitempty"`
	// Error is the message when Outcome is OutcomeError.
	Error string `json:"error,omitempty"`
	// SHA256 digests the normalized response bytes when Outcome is OutcomeOK.
	SHA256 string `json:"sha256,omitempty"`
	// Bytes is the normalized response length, a cheap corroboration of the
	// digest that makes an accidental empty-body regression obvious to a reader.
	Bytes int `json:"bytes,omitempty"`
}

// Baseline is the recorded artifact.
type Baseline struct {
	SchemaVersion int              `json:"schema_version"`
	Commit        string           `json:"commit"`
	Fixture       string           `json:"fixture"`
	Note          string           `json:"note"`
	Cases         map[string]Entry `json:"cases"`
}

const baselineNote = "Legacy (surfaces/client.Direct) outcomes per use case, recorded through the " +
	"Client seam. Digests are over path-normalized response bytes. A later phase records the " +
	"application-backed path the same way and compares; an unexplained diff stops that phase."

// namedSentinels maps every typed refusal the harness can observe to a stable
// name. Recording the name rather than the message means a reworded error does
// not read as a behaviour change, while a genuinely different refusal does.
var namedSentinels = []struct {
	name string
	err  error
}{
	{"ErrSearchUnavailable", client.ErrSearchUnavailable},
	{"ErrSavingsUnavailable", client.ErrSavingsUnavailable},
	{"ErrAnalysisUnavailable", client.ErrAnalysisUnavailable},
	{"ErrEditUnavailable", client.ErrEditUnavailable},
	{"ErrReviewUnavailable", client.ErrReviewUnavailable},
	{"ErrCompareUnavailable", client.ErrCompareUnavailable},
	{"ErrDiagnosticUnavailable", client.ErrDiagnosticUnavailable},
	{"ErrReviewFetchUnavailable", client.ErrReviewFetchUnavailable},
	{"ErrForgeUnavailable", client.ErrForgeUnavailable},
	{"ErrMemoryUnavailable", client.ErrMemoryUnavailable},
	{"ErrExportPathRejected", client.ErrExportPathRejected},
	{"ErrDistillUnavailable", client.ErrDistillUnavailable},
	{"ErrSkillGenUnavailable", client.ErrSkillGenUnavailable},
	{"ErrBriefUnavailable", client.ErrBriefUnavailable},
	{"ErrTrustUnavailable", client.ErrTrustUnavailable},
	{"ErrAgentToolsUnavailable", client.ErrAgentToolsUnavailable},
}

// classify turns a call result into a recorded entry.
func classify(env *Env, useCase UseCase, payload []byte, err error) Entry {
	entry := Entry{Context: useCase.Context}
	if err != nil {
		for _, known := range namedSentinels {
			if errors.Is(err, known.err) {
				entry.Outcome = OutcomeSentinel
				entry.Sentinel = known.name
				return entry
			}
		}
		entry.Outcome = OutcomeError
		entry.Error = env.Normalize(err.Error())
		return entry
	}
	normalized := env.NormalizeBytes(payload)
	sum := sha256.Sum256(normalized)
	entry.Outcome = OutcomeOK
	entry.SHA256 = hex.EncodeToString(sum[:])
	entry.Bytes = len(normalized)
	return entry
}

// Record drives every use case against c and returns the recorded outcomes.
func Record(ctx context.Context, c client.Client, env *Env, cases []UseCase) (map[string]Entry, error) {
	out := make(map[string]Entry, len(cases))
	for _, useCase := range cases {
		if _, dup := out[useCase.ID]; dup {
			return nil, fmt.Errorf("archdiff: duplicate use case id %q", useCase.ID)
		}
		payload, err := useCase.Invoke(ctx, c, env)
		out[useCase.ID] = classify(env, useCase, payload, err)
	}
	return out, nil
}

// CaseDiff is one use case where two implementations disagreed.
type CaseDiff struct {
	ID string
	A  Entry
	B  Entry
}

// String renders the disagreement for a failure message.
func (d CaseDiff) String() string {
	return fmt.Sprintf("%s: legacy=%s new=%s", d.ID, describe(d.A), describe(d.B))
}

func describe(e Entry) string {
	switch e.Outcome {
	case OutcomeOK:
		return fmt.Sprintf("ok(sha=%s, %d bytes)", shortDigest(e.SHA256), e.Bytes)
	case OutcomeSentinel:
		return "sentinel(" + e.Sentinel + ")"
	case OutcomeError:
		return "error(" + e.Error + ")"
	}
	return "missing"
}

func shortDigest(d string) string {
	if len(d) > 12 {
		return d[:12]
	}
	return d
}

// Compare drives the same use cases against two implementations in the same
// environment and returns every disagreement.
//
// This is the function the migration phases call: `a` is the compatibility
// facade, `b` is the application-backed path, and a non-empty result means the
// refactor changed behaviour. Running both in ONE environment is deliberate —
// it removes normalization from the equation, so a reported diff is a real
// behavioural difference rather than an artefact of two different temp dirs.
func Compare(ctx context.Context, a, b client.Client, env *Env, cases []UseCase) ([]CaseDiff, error) {
	recordedA, err := Record(ctx, a, env, cases)
	if err != nil {
		return nil, err
	}
	recordedB, err := Record(ctx, b, env, cases)
	if err != nil {
		return nil, err
	}
	return DiffRecorded(recordedA, recordedB), nil
}

// DiffRecorded compares two recorded result sets.
func DiffRecorded(a, b map[string]Entry) []CaseDiff {
	ids := map[string]bool{}
	for id := range a {
		ids[id] = true
	}
	for id := range b {
		ids[id] = true
	}
	names := make([]string, 0, len(ids))
	for id := range ids {
		names = append(names, id)
	}
	sort.Strings(names)

	var diffs []CaseDiff
	for _, id := range names {
		entryA, okA := a[id]
		entryB, okB := b[id]
		if !okA || !okB || entryA != entryB {
			diffs = append(diffs, CaseDiff{ID: id, A: entryA, B: entryB})
		}
	}
	return diffs
}

// Render encodes the baseline as canonical, reviewable JSON.
func Render(b Baseline) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return nil, fmt.Errorf("archdiff: encode baseline: %w", err)
	}
	return buf.Bytes(), nil
}

// Parse decodes a recorded baseline.
func Parse(raw []byte) (Baseline, error) {
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return Baseline{}, fmt.Errorf("archdiff: decode baseline: %w", err)
	}
	return b, nil
}

// NewBaseline assembles a baseline artifact from recorded outcomes.
func NewBaseline(commit, fixture string, cases map[string]Entry) Baseline {
	return Baseline{
		SchemaVersion: SchemaVersion,
		Commit:        commit,
		Fixture:       fixture,
		Note:          baselineNote,
		Cases:         cases,
	}
}

// Summary renders a short digest of what was recorded, grouped by outcome, so a
// reader can see at a glance how much of the surface is actually exercised.
func Summary(cases map[string]Entry) string {
	byOutcome := map[string]int{}
	byContext := map[string]int{}
	for _, entry := range cases {
		byOutcome[entry.Outcome]++
		byContext[entry.Context]++
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d use cases recorded\n", len(cases))
	for _, outcome := range []string{OutcomeOK, OutcomeSentinel, OutcomeError} {
		if byOutcome[outcome] > 0 {
			fmt.Fprintf(&b, "  %-10s %d\n", outcome, byOutcome[outcome])
		}
	}
	contexts := make([]string, 0, len(byContext))
	for ctx := range byContext {
		contexts = append(contexts, ctx)
	}
	sort.Strings(contexts)
	b.WriteString("by bounded context:\n")
	for _, ctx := range contexts {
		fmt.Fprintf(&b, "  %-12s %d\n", ctx, byContext[ctx])
	}
	return b.String()
}
