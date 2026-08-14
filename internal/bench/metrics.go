package bench

import (
	"sort"
	"time"
)

const (
	ExternalBinaryBuildContract = "external-binary/unverified"
	CustomBuildContract         = "custom-build/unverified"
)

// Metrics are the measured benchmark values (the original four core metrics
// plus the P4/TODO-19 incremental-indexing suite) and provenance. Durations are
// expressed as float milliseconds; binary size as bytes.
type Metrics struct {
	ColdStartP95MS  float64 `json:"cold_start_p95_ms"` // daemon/engine cold-start to first served query, P95
	FullIndexMS     float64 `json:"full_index_ms"`     // engine/ingest IngestAll over the frozen fixture, median
	FreshnessLagMS  float64 `json:"freshness_lag_ms"`  // hot-index IngestChanged + query round-trip
	BinarySizeBytes int64   `json:"binary_size_bytes"` // size of the static default binary

	// P4 / TODO-19 incremental-indexing suite.
	IncrementalTenFileMS float64 `json:"incremental_ten_file_ms"` // 10-file change: IngestChanged + query round-trip
	BranchSwitchSimMS    float64 `json:"branch_switch_sim_ms"`    // simulated checkout delta (modify+add+delete) + round-trip
	MCPStartupMS         float64 `json:"mcp_startup_ms"`          // built binary `mcp -db`: spawn → initialize response, median
	SymbolLookupMS       float64 `json:"symbol_lookup_ms"`        // lexical symbol lookup on the hot store, median
	CallersQueryMS       float64 `json:"callers_query_ms"`        // callers structural query on a resolved id, median
	ContextQueryMS       float64 `json:"context_query_ms"`        // task_context one-call bundle on the hot store, median
	IndexHeapAllocBytes  int64   `json:"index_heap_alloc_bytes"`  // post-GC heap after a full fixture index
	BuildContract        string  `json:"build_contract"`
	BuildGoVersion       string  `json:"build_go_version"`
	BuildGOOS            string  `json:"build_goos"`
	BuildGOARCH          string  `json:"build_goarch"`
	BuildGOAMD64         string  `json:"build_goamd64,omitempty"`
	BuildCGOEnabled      string  `json:"build_cgo_enabled"`
	BuildVCSRevision     string  `json:"build_vcs_revision,omitempty"`
	BuildVCSModified     string  `json:"build_vcs_modified,omitempty"`
	FixtureDigest        string  `json:"fixture_digest"`
	BaselineVersion      string  `json:"baseline_version"`
	Samples              int     `json:"samples"`
	// ProfileMetrics holds per-profile index/db/edge/query metrics keyed by
	// profile name (fast/balanced/deep). Added for EP-022 profile-aware budgets.
	ProfileMetrics map[string]ProfileMetric `json:"profile_metrics,omitempty"`
}

// ProfileMetric is the measured data for a single index profile.
type ProfileMetric struct {
	IndexMS        float64 `json:"index_ms"`
	DBSizeBytes    int64   `json:"db_size_bytes"`
	EdgeCount      int64   `json:"edge_count"`
	QueryLatencyMS float64 `json:"query_latency_ms"`
}

// Map returns the metric-name → value mapping consumed by Gate.
func (m Metrics) Map() map[string]float64 {
	out := map[string]float64{
		"cold_start_p95_ms": m.ColdStartP95MS,
		"full_index_ms":     m.FullIndexMS,
		"freshness_lag_ms":  m.FreshnessLagMS,
		"binary_size_bytes": float64(m.BinarySizeBytes),

		"incremental_ten_file_ms": m.IncrementalTenFileMS,
		"branch_switch_sim_ms":    m.BranchSwitchSimMS,
		"symbol_lookup_ms":        m.SymbolLookupMS,
		"callers_query_ms":        m.CallersQueryMS,
		"context_query_ms":        m.ContextQueryMS,
		"index_heap_alloc_bytes":  float64(m.IndexHeapAllocBytes),
	}
	// MCP startup is only measured for internally built binaries; an external
	// binary omits the metric instead of reporting a fake zero (the budget
	// gate then flags it as unmeasured, which is the honest outcome).
	if m.MCPStartupMS > 0 {
		out["mcp_startup_ms"] = m.MCPStartupMS
	}
	for name, pm := range m.ProfileMetrics {
		out[name+"_index_ms"] = pm.IndexMS
		out[name+"_db_size_bytes"] = float64(pm.DBSizeBytes)
		out[name+"_edge_count"] = float64(pm.EdgeCount)
		out[name+"_query_latency_ms"] = pm.QueryLatencyMS
	}
	return out
}

// P95 returns the 95th-percentile duration (nearest-rank) of samples. Empty
// input returns zero.
func P95(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	// nearest-rank: index = ceil(p * n) - 1
	idx := int((95.0/100.0)*float64(len(s))+0.999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return s[idx]
}

// Median returns the median duration of samples. Empty input returns zero.
func Median(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), samples...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// ms converts a duration to float milliseconds.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }
