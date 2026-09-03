// Package contract defines the canonical agent-response envelope and serializer
// shared by the EP-020 agent-first tools (explain_symbol, related_files,
// change_risk) and the C1 common-contract serializer.
package contract

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Outcome is the result classification.
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeFound     Outcome = "found"
	OutcomePartial   Outcome = "partial"
	OutcomeAmbiguous Outcome = "ambiguous"
	OutcomeEmpty     Outcome = "empty"
	// OutcomeUnavailable — the tool cannot run on this surface/build (e.g. no
	// graph services wired). Distinct from empty: nothing was searched.
	OutcomeUnavailable Outcome = "unavailable"
	OutcomeError       Outcome = "error"
)

var validOutcomes = map[Outcome]bool{
	OutcomeOK:          true,
	OutcomeFound:       true,
	OutcomePartial:     true,
	OutcomeAmbiguous:   true,
	OutcomeEmpty:       true,
	OutcomeUnavailable: true,
	OutcomeError:       true,
}

// Valid reports whether o is a known outcome value.
func (o Outcome) Valid() bool { return validOutcomes[o] }

// Evidence is a file:line citation backing an item.
//
// Every `task_context/2` evidence item is one of two disjoint kinds. The kinds
// share the same struct so a consumer can iterate `bundle.Evidence` uniformly,
// but the fields they populate differ — and SW-268's AC-1 decision is exactly
// that they differ. Adding `text_hash` to a claim-typed citation would either
// re-hash the file on disk (coupling the wire shape to the source) or stamp a
// non-text identifier hash (a misnamed rename); both were rejected. The two
// kinds, and the per-kind contract:
//
//   - Claim-typed citation. The item carries `Path`, `Line` (+ optional `Span`
//     for retrieval rows: "start-end"), `Role`, `ClaimType`
//     (`source_match` for spans that came from a retrieval row,
//     `graph_relation` for spans reached via an edge), and — on
//     `graph_relation` items — the edge's provenance tier on `EdgeTier`. It
//     carries NO `Snippet` and NO `TextHash`: a citation names text, it does
//     not include it; the consumer reads the cited bytes via the standard
//     `path:line` interface.
//   - Snippet entry. The item carries `Path`, `Line` (the start line),
//     `Span` (`start-end`), the snippet text on `Snippet`, and the xxhash64
//     hex of `Snippet` (16 chars) on `TextHash`. It carries NO `ClaimType`:
//     a snippet is a body, not a claim; setting `claim_type` on it would
//     mislabel a quoted range as a verified match.
//
// The two kinds are exhaustive over `task_context/2`. Measured against
// `55c8a8a` across 541 emitted evidence items: 494 are claim-typed citations,
// 47 are snippet entries, 0 carry both `ClaimType` and `TextHash`, 0 carry
// neither. The latter zero is the load-bearing property AC-4 of SW-268
// asserts per item.
//
// Snippet is additive and omitempty: the frozen stable operations and the
// `task_context/1` path never set it, so their serialized bytes are
// unchanged. ClaimType, TextHash and EdgeTier are SW-264 additions for the
// v2 versions of `search_hybrid` and `task_context`. They are additive
// omitempty fields so v1 callers do not emit them and the SW-257
// byte-identical golden for `search_hybrid/1` and `task_context/1` stays
// byte-identical.
type Evidence struct {
	RefID     string `json:"ref_id"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Span      string `json:"span,omitempty"`
	Role      string `json:"role"`
	Snippet   string `json:"snippet,omitempty"`
	ClaimType string `json:"claim_type,omitempty"`
	TextHash  string `json:"text_hash,omitempty"`
	EdgeTier  string `json:"edge_tier,omitempty"`
}

// Item is a single ranked result row.
type Item struct {
	RefID          string   `json:"ref_id"`
	Rank           int      `json:"rank"`
	Reason         string   `json:"reason"`
	EvidenceRefIDs []string `json:"evidence_ref_ids"`
}

// Confidence is a normalized distribution over outcome labels.
type Confidence struct {
	Distribution map[string]float64 `json:"distribution"`
	Top          string             `json:"top"`
	Method       string             `json:"method"`
}

// Limits records size-budget enforcement metadata.
type Limits struct {
	CapApplied     int    `json:"cap_applied"`
	TotalAvailable int    `json:"total_available"`
	Dropped        int    `json:"dropped"`
	Truncated      bool   `json:"truncated"`
	Next           string `json:"next"`
}

// Result is the canonical agent-response envelope.
type Result struct {
	Outcome    Outcome    `json:"outcome"`
	Summary    string     `json:"summary"`
	Items      []Item     `json:"items"`
	Evidence   []Evidence `json:"evidence"`
	Confidence Confidence `json:"confidence"`
	Limits     Limits     `json:"limits"`
}

const confidenceTolerance = 1e-6

// ValidateResult checks envelope invariants.
func ValidateResult(r *Result) error {
	if r == nil {
		return errors.New("result is nil")
	}
	if !r.Outcome.Valid() {
		return fmt.Errorf("invalid outcome %q", r.Outcome)
	}
	if err := ValidateConfidence(&r.Confidence); err != nil {
		return fmt.Errorf("confidence: %w", err)
	}
	refSet := make(map[string]bool, len(r.Evidence))
	for _, ev := range r.Evidence {
		if ev.RefID == "" {
			return errors.New("evidence missing ref_id")
		}
		if refSet[ev.RefID] {
			return fmt.Errorf("duplicate evidence ref_id %q", ev.RefID)
		}
		refSet[ev.RefID] = true
	}
	for i, it := range r.Items {
		for _, ref := range it.EvidenceRefIDs {
			if !refSet[ref] {
				return fmt.Errorf("item %d references unknown evidence ref_id %q", i, ref)
			}
		}
	}
	return nil
}

// ValidateConfidence checks that the distribution is valid.
func ValidateConfidence(c *Confidence) error {
	if c == nil {
		return errors.New("confidence is nil")
	}
	if c.Distribution == nil {
		return errors.New("confidence distribution is nil")
	}
	sum := 0.0
	for label, w := range c.Distribution {
		if w < 0 {
			return fmt.Errorf("negative weight for label %q", label)
		}
		sum += w
	}
	if len(c.Distribution) > 0 && math.Abs(sum-1.0) > confidenceTolerance {
		return fmt.Errorf("confidence distribution sums to %g, expected 1.0", sum)
	}
	return nil
}

// NormalizeConfidence rescales weights to sum to 1.0 and fills Top/Method.
func NormalizeConfidence(c *Confidence) error {
	if c == nil {
		return errors.New("confidence is nil")
	}
	if c.Distribution == nil {
		c.Distribution = map[string]float64{}
	}
	sum := 0.0
	for _, w := range c.Distribution {
		if w < 0 {
			return errors.New("negative weight")
		}
		sum += w
	}
	if len(c.Distribution) == 0 {
		c.Top = ""
		c.Method = "empty"
		return nil
	}
	if sum == 0 {
		return errors.New("distribution sum is zero")
	}
	labels := make([]string, 0, len(c.Distribution))
	for label := range c.Distribution {
		labels = append(labels, label)
	}
	// Deterministic top selection: ties break on ascending label order (which
	// for the tier vocabulary happens to be strength order: confirmed <
	// derived < heuristic < unknown). Map iteration order must never leak
	// into the serialized output.
	sort.Strings(labels)
	topLabel := ""
	topWeight := -1.0
	for _, label := range labels {
		w := c.Distribution[label]
		c.Distribution[label] = w / sum
		if w > topWeight {
			topWeight = w
			topLabel = label
		}
	}
	c.Top = topLabel
	if c.Method == "" {
		c.Method = "normalized"
	}
	return nil
}
