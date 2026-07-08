package taint

import (
	"sort"
	"strings"

	"github.com/samibel/graphi/core/model"
)

// PathStep is one step in a taint propagation path, carrying full provenance.
type PathStep struct {
	NodeID         model.NodeId         `json:"node_id"`
	Kind           string               `json:"kind"`
	QualifiedName  string               `json:"qualified_name"`
	SourcePath     string               `json:"source_path"`
	Line           int                  `json:"line"`
	Column         int                  `json:"column"`
	Labels         LabelSet             `json:"labels"`
	EdgeKind       string               `json:"edge_kind,omitempty"`
	Tier           model.ConfidenceTier `json:"confidence_tier,omitempty"`
	Confidence     float64              `json:"confidence,omitempty"`
	Reason         string               `json:"reason,omitempty"`
	DerivationRule string               `json:"derivation_rule,omitempty"`
}

// Finding is a single source→sink taint flow with full provenance on every
// propagation step. The path is ordered from source to sink.
type Finding struct {
	SourceID     model.NodeId `json:"source_id"`
	SourceName   string       `json:"source_name"`
	SourceDefID  string       `json:"source_def_id"`
	SinkID       model.NodeId `json:"sink_id"`
	SinkName     string       `json:"sink_name"`
	SinkDefID    string       `json:"sink_def_id"`
	SinkCategory string       `json:"sink_category"`
	Labels       LabelSet     `json:"labels"`
	Path         []PathStep   `json:"path"`
	PathLength   int          `json:"path_length"`
	Incomplete   bool         `json:"incomplete,omitempty"`
	CapHit       *capHit      `json:"cap_hit,omitempty"`
	ConfigHash   string       `json:"config_hash"`
}

// TaintResult is the complete output of a taint analysis run.
type TaintResult struct {
	Findings    []Finding `json:"findings"`
	Truncated   bool      `json:"truncated,omitempty"`
	Diagnostics []string  `json:"diagnostics,omitempty"`
	ConfigHash  string    `json:"config_hash"`
	// SinkCandidates / SourceCandidates count the graph nodes the config
	// classified as a sink / source before propagation ran (WP-04). They let the
	// dispatch layer tell "checked, no flow found" (candidates exist) apart from
	// "the graph CANNOT match a sink" (zero candidates) — the latter must never be
	// reported as an empty/clean result. A graph with zero sink candidates is an
	// honest "no_sink_candidates", not a false all-clear.
	SinkCandidates   int `json:"sink_candidates"`
	SourceCandidates int `json:"source_candidates"`
}

// sortFindings sorts findings in canonical order for deterministic output:
// by source node id, then sink node id, then path length, then label set
// string representation.
func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.SinkID != b.SinkID {
			return a.SinkID < b.SinkID
		}
		if a.PathLength != b.PathLength {
			return a.PathLength < b.PathLength
		}
		return strings.Join(a.Labels, ",") < strings.Join(b.Labels, ",")
	})
}
