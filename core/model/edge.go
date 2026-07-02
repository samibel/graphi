package model

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// EdgeId is the fixed-width 16-char lowercase-hex xxhash64 identity of an Edge,
// derived from its (FromId, ToId, EdgeKind) canonical identity form. Like
// NodeId it is a non-cryptographic content identifier (see package doc).
type EdgeId string

// ConfidenceTier is the closed provenance vocabulary reconciled with
// architecture §7.3. The story's HIGH/MEDIUM/LOW are illustrative only; the
// canonical, persisted set is {heuristic, derived, confirmed}. Any value outside
// this set is rejected on construction and on deserialization.
type ConfidenceTier string

const (
	// TierHeuristic — produced by a heuristic/best-effort signal.
	TierHeuristic ConfidenceTier = "heuristic"
	// TierDerived — derived from structured analysis (e.g. resolved symbols).
	TierDerived ConfidenceTier = "derived"
	// TierConfirmed — confirmed by an authoritative source.
	TierConfirmed ConfidenceTier = "confirmed"
)

// validTiers is the closed set, used for membership checks and stable ordering
// documentation.
var validTiers = map[ConfidenceTier]struct{}{
	TierHeuristic: {},
	TierDerived:   {},
	TierConfirmed: {},
}

// Valid reports whether t is a member of the closed ConfidenceTier enum.
func (t ConfidenceTier) Valid() bool {
	_, ok := validTiers[t]
	return ok
}

// ErrInvalidEdge is the typed sentinel wrapped by NewEdge validation failures,
// including every missing/incomplete-provenance case.
var ErrInvalidEdge = errors.New("model: invalid edge")

// Edge is graphi's canonical, immutable graph edge value type with MANDATORY
// provenance. An Edge cannot be obtained without complete provenance: the only
// constructor, NewEdge, rejects a missing/unknown tier, an out-of-range
// confidence, an empty/whitespace-only reason, or empty evidence. All fields are
// unexported with accessors, so a constructed Edge is immutable and an
// under-provenanced Edge is unrepresentable.
type Edge struct {
	id         EdgeId
	from       NodeId
	to         NodeId
	kind       string
	tier       ConfidenceTier
	confidence float64
	reason     string
	evidence   []string // canonically sorted, defensively copied
}

// EdgeKind identifies the relationship category (e.g. "calls", "imports").
type EdgeKind = string

// NewEdge constructs an immutable, fully-provenanced Edge.
//
// Provenance is mandatory and validated at construction:
//   - tier must be a member of the closed ConfidenceTier enum,
//   - confidence must be within the inclusive range [0, 1],
//   - reason must be non-empty after trimming whitespace,
//   - evidence must contain at least one non-empty (trimmed) reference.
//
// from, to and kind must be non-empty (they form the edge identity). evidence is
// defensively copied and canonically sorted so equal provenance yields identical
// serialized bytes regardless of input order. The EdgeId is derived
// deterministically from (from, to, kind).
func NewEdge(from, to NodeId, kind string, tier ConfidenceTier, confidence float64, reason string, evidence []string) (Edge, error) {
	if strings.TrimSpace(string(from)) == "" {
		return Edge{}, fmt.Errorf("%w: from NodeId must not be empty", ErrInvalidEdge)
	}
	if strings.TrimSpace(string(to)) == "" {
		return Edge{}, fmt.Errorf("%w: to NodeId must not be empty", ErrInvalidEdge)
	}
	if strings.TrimSpace(kind) == "" {
		return Edge{}, fmt.Errorf("%w: kind must not be empty", ErrInvalidEdge)
	}
	if !tier.Valid() {
		return Edge{}, fmt.Errorf("%w: confidence_tier %q is not a member of the closed enum {heuristic,derived,confirmed}", ErrInvalidEdge, tier)
	}
	if confidence < 0 || confidence > 1 {
		return Edge{}, fmt.Errorf("%w: confidence %v is out of range [0,1]", ErrInvalidEdge, confidence)
	}
	if strings.TrimSpace(reason) == "" {
		return Edge{}, fmt.Errorf("%w: reason must not be empty", ErrInvalidEdge)
	}
	cleanedEvidence, err := normalizeEvidence(evidence)
	if err != nil {
		return Edge{}, err
	}
	e := Edge{
		from:       from,
		to:         to,
		kind:       kind,
		tier:       tier,
		confidence: confidence,
		reason:     reason,
		evidence:   cleanedEvidence,
	}
	e.id = EdgeId(deriveID(edgeIDDomainTag, string(from), string(to), kind))
	return e, nil
}

// normalizeEvidence validates, defensively copies, and canonically sorts the
// evidence list. At least one non-empty reference is required; content is
// preserved verbatim (only surrounding membership is validated, not trimmed).
func normalizeEvidence(evidence []string) ([]string, error) {
	if len(evidence) == 0 {
		return nil, fmt.Errorf("%w: evidence must contain at least one reference", ErrInvalidEdge)
	}
	nonEmpty := 0
	out := make([]string, 0, len(evidence))
	for _, ev := range evidence {
		if strings.TrimSpace(ev) != "" {
			nonEmpty++
		}
		out = append(out, ev)
	}
	if nonEmpty == 0 {
		return nil, fmt.Errorf("%w: evidence must contain at least one non-empty reference", ErrInvalidEdge)
	}
	sort.Strings(out)
	return out, nil
}

// ID returns the deterministic EdgeId.
func (e Edge) ID() EdgeId { return e.id }

// From returns the source NodeId.
func (e Edge) From() NodeId { return e.from }

// To returns the destination NodeId.
func (e Edge) To() NodeId { return e.to }

// Kind returns the relationship kind.
func (e Edge) Kind() string { return e.kind }

// Tier returns the closed-enum confidence tier (provenance).
func (e Edge) Tier() ConfidenceTier { return e.tier }

// Confidence returns the numeric confidence in [0,1] (provenance).
func (e Edge) Confidence() float64 { return e.confidence }

// Reason returns the human-readable provenance reason. UNTRUSTED-ORIGIN data.
func (e Edge) Reason() string { return e.reason }

// Evidence returns a defensive copy of the canonically-sorted evidence
// references. UNTRUSTED-ORIGIN data.
func (e Edge) Evidence() []string {
	out := make([]string, len(e.evidence))
	copy(out, e.evidence)
	return out
}
