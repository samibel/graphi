package eval

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CoverageBaseline is the committed coverage-matrix baseline.
type CoverageBaseline struct {
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

// LoadCoverageBaseline loads the embedded committed coverage baseline.
func LoadCoverageBaseline() (*CoverageBaseline, error) {
	var cb CoverageBaseline
	if err := json.Unmarshal(embeddedCoverageBaseline, &cb); err != nil {
		return nil, fmt.Errorf("eval: parse coverage baseline: %w", err)
	}
	if cb.Version == "" {
		return nil, fmt.Errorf("eval: coverage baseline missing version stamp")
	}
	return &cb, nil
}

// coveredCapabilities returns the sorted set of capabilities covered by the
// dataset (i.e. capabilities with at least one eval case).
func coveredCapabilities(ds *Dataset) []string {
	set := map[string]bool{}
	for _, c := range ds.Cases {
		set[c.Capability] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// DriftResult is the outcome of comparing current coverage against the baseline.
type DriftResult struct {
	Regressed bool     `json:"regressed"` // a previously-covered capability lost coverage, or count dropped
	Lost      []string `json:"lost,omitempty"`
	Gained    []string `json:"gained,omitempty"`
}

// CheckDrift compares current covered capabilities against the baseline. A
// regression (any baseline capability no longer covered, or a count drop) is
// reported with the lost capabilities named (AC: capability-level diff).
func CheckDrift(current, baseline []string) DriftResult {
	return checkDriftPointer(current, baseline)
}

// checkDriftPointer is the shared implementation (pointer receiver not needed;
// named distinctly to avoid an unused-method warning in the API surface).
func checkDriftPointer(current, baseline []string) DriftResult {
	cur := toSet(current)
	base := toSet(baseline)
	var lost, gained []string
	for b := range base {
		if !cur[b] {
			lost = append(lost, b)
		}
	}
	for c := range cur {
		if !base[c] {
			gained = append(gained, c)
		}
	}
	sort.Strings(lost)
	sort.Strings(gained)
	regressed := len(lost) > 0 || len(current) < len(baseline)
	return DriftResult{Regressed: regressed, Lost: lost, Gained: gained}
}

func toSet(ss []string) map[string]bool {
	m := map[string]bool{}
	for _, s := range ss {
		m[s] = true
	}
	return m
}
