// Package shape holds the shared result-shaping helpers for the EP-020
// agent-first tools: evidence collection with stable ref ids, confidence
// distributions derived from edge tiers, and the common ambiguous / empty /
// unavailable envelopes. Keeping these in one place guarantees the three tools
// stay byte-consistent in how they cite and hedge.
package shape

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/query"
)

// DefaultMaxItems is the item cap applied when the caller passes none.
const DefaultMaxItems = 20

// EvidenceSet accumulates deduplicated evidence entries with deterministic
// "e1","e2",… ref ids in insertion order.
type EvidenceSet struct {
	entries []contract.Evidence
	index   map[string]string // path:line:role → ref id
}

// NewEvidenceSet returns an empty evidence accumulator.
func NewEvidenceSet() *EvidenceSet {
	return &EvidenceSet{index: map[string]string{}}
}

// Add records one evidence citation and returns its ref id, deduplicating
// repeats of the same (path, line, role).
func (s *EvidenceSet) Add(path string, line int, role string) string {
	key := path + ":" + strconv.Itoa(line) + ":" + role
	if id, ok := s.index[key]; ok {
		return id
	}
	id := "e" + strconv.Itoa(len(s.entries)+1)
	s.index[key] = id
	s.entries = append(s.entries, contract.Evidence{RefID: id, Path: path, Line: line, Role: role})
	return id
}

// AddRef parses a model-edge evidence string ("path:line") and records it.
// Unparseable refs are cited verbatim with line 0 so no citation is dropped.
func (s *EvidenceSet) AddRef(ref, role string) string {
	path, line := SplitEvidenceRef(ref)
	return s.Add(path, line, role)
}

// List returns the accumulated evidence in insertion order.
func (s *EvidenceSet) List() []contract.Evidence { return s.entries }

// SplitEvidenceRef splits a canonical "path:line" evidence string. A ref
// without a trailing line number yields (ref, 0).
func SplitEvidenceRef(ref string) (string, int) {
	i := strings.LastIndex(ref, ":")
	if i <= 0 {
		return ref, 0
	}
	line, err := strconv.Atoi(ref[i+1:])
	if err != nil || line < 0 {
		return ref, 0
	}
	return ref[:i], line
}

// TierTally counts edge confidence tiers for a distribution.
type TierTally map[string]float64

// Count adds one edge's tier to the tally.
func (t TierTally) Count(tier model.ConfidenceTier) { t[string(tier)]++ }

// CountResultEdges adds every edge of a query result to the tally.
func (t TierTally) CountResultEdges(edges []query.ResultEdge) {
	for _, e := range edges {
		t.Count(e.Tier)
	}
}

// Confidence converts the tally into a normalized contract confidence with
// method "edge_tiers". An empty tally yields the given fallback label with
// method set to the fallback method.
func (t TierTally) Confidence(fallbackLabel, fallbackMethod string) contract.Confidence {
	c := contract.Confidence{Distribution: map[string]float64{}, Method: "edge_tiers"}
	for label, n := range t {
		c.Distribution[label] = n
	}
	if len(c.Distribution) == 0 {
		c.Distribution[fallbackLabel] = 1
		c.Method = fallbackMethod
	}
	// Distribution is well-formed by construction; normalization cannot fail.
	_ = contract.NormalizeConfidence(&c)
	return c
}

// Unavailable is the shared envelope for a tool that cannot run because the
// graph services are not wired on this surface.
func Unavailable(tool string) *contract.Result {
	return &contract.Result{
		Outcome: contract.OutcomeUnavailable,
		Summary: tool + ": graph services unavailable on this surface; open an indexed database (graphi index, then pass -db)",
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"unknown": 1},
			Top:          "unknown",
			Method:       "unavailable",
		},
	}
}

// Empty is the shared envelope for an unresolved reference, with next-step
// hints per the PRD (never a guess).
func Empty(tool, ref string) *contract.Result {
	return &contract.Result{
		Outcome: contract.OutcomeEmpty,
		Summary: fmt.Sprintf("%s: no symbol or file matched %q; try `search` for discovery, a repo-relative path, a qualified name, or a 16-hex node id", tool, ref),
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"unknown": 1},
			Top:          "unknown",
			Method:       "unresolved",
		},
	}
}

// Ambiguous shapes candidate nodes into the shared ambiguous envelope so the
// agent can pick one and retry — items carry node ids as ref ids.
func Ambiguous(tool, ref string, cands []resolve.Candidate) *contract.Result {
	ev := NewEvidenceSet()
	items := make([]contract.Item, 0, len(cands))
	for i, c := range cands {
		n := c.Node
		refID := string(n.ID())
		evID := ev.Add(n.SourcePath(), n.Line(), "candidate")
		items = append(items, contract.Item{
			RefID:          refID,
			Rank:           len(cands) - i,
			Reason:         fmt.Sprintf("candidate: %s %s (%s:%d)", n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line()),
			EvidenceRefIDs: []string{evID},
		})
	}
	return &contract.Result{
		Outcome:  contract.OutcomeAmbiguous,
		Summary:  fmt.Sprintf("%s: %q is ambiguous — %d candidates; retry with a node id or full path", tool, ref, len(cands)),
		Items:    items,
		Evidence: ev.List(),
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"unknown": 1},
			Top:          "unknown",
			Method:       "ambiguous",
		},
	}
}

// Finish applies the item cap, stamps limits, and downgrades the outcome to
// "partial" when the cap truncated the item list. maxItems <= 0 selects
// DefaultMaxItems.
func Finish(r *contract.Result, maxItems int) (*contract.Result, error) {
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	out, err := contract.ApplyItemCap(r, maxItems)
	if err != nil {
		return nil, err
	}
	if out.Limits.Truncated && out.Outcome == contract.OutcomeFound {
		out.Outcome = contract.OutcomePartial
	}
	return out, nil
}

// SortStrings returns a sorted copy of the given set's keys.
func SortStrings(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
