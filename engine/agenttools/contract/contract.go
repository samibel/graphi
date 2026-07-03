// Package contract defines the canonical agent-response envelope and serializer
// shared by the EP-020 agent-first tools (explain_symbol, related_files,
// change_risk) and the C1 common-contract serializer.
package contract

import (
	"errors"
	"fmt"
	"math"
)

// Outcome is the result classification.
type Outcome string

const (
	OutcomeOK        Outcome = "ok"
	OutcomeAmbiguous Outcome = "ambiguous"
	OutcomeEmpty     Outcome = "empty"
	OutcomeError     Outcome = "error"
)

var validOutcomes = map[Outcome]bool{
	OutcomeOK:        true,
	OutcomeAmbiguous: true,
	OutcomeEmpty:     true,
	OutcomeError:     true,
}

// Valid reports whether o is a known outcome value.
func (o Outcome) Valid() bool { return validOutcomes[o] }

// Evidence is a file:line citation backing an item.
type Evidence struct {
	RefID string `json:"ref_id"`
	Path  string `json:"path"`
	Line  int    `json:"line"`
	Span  string `json:"span,omitempty"`
	Role  string `json:"role"`
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
	topLabel := ""
	topWeight := -1.0
	for label, w := range c.Distribution {
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
