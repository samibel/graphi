package evalreport

// SW-123 (EVAL-02): the full-run raw-evidence payload. One report = one pinned
// corpus repository measured end-to-end in ONE process (clone → index → warm
// query classes), so the recorded peak RSS is attributable to that repo alone.
// These reports are the raw evidence the U5 budgets are frozen from; they are
// published as CI artifacts and committed under docs/eval/runs/.

import (
	"encoding/json"
	"fmt"
	"os"
)

// FullRunReport is the top-level envelope for one repo's full run.
type FullRunReport struct {
	Header Header `json:"header"`
	// RunnerClass names the machine class the numbers were recorded on
	// (e.g. "ubuntu-latest"). Budgets are only ever frozen from runs on the
	// reference runner class; anything else is a smoke run.
	RunnerClass string `json:"runner_class"`
	// Notes documents the measurement model (in-process session, sample
	// sizes) so a reader can interpret the numbers without the source.
	Notes string      `json:"notes,omitempty"`
	Repo  FullRepoRun `json:"repo"`
}

// FullRepoRun is the per-repository measurement set.
type FullRepoRun struct {
	Name string `json:"name"`
	// Ref/SHA document the pin actually checked out (empty for local-path
	// fixture entries, which need no clone).
	Ref     string `json:"ref,omitempty"`
	SHA     string `json:"sha,omitempty"`
	Tier    int    `json:"tier"`
	CloneMS int64  `json:"clone_ms,omitempty"`

	Index IndexMetrics `json:"index"`

	// WarmP95US is the p95 latency in MICROSECONDS per operation class
	// (structural, search, agent_tools) over the warm, already-indexed store
	// in the same session. Microseconds because the selective-read stable ops
	// are routinely sub-millisecond and a 0ms value cannot ratchet.
	WarmP95US map[string]int64 `json:"warm_p95_us"`
	// WarmP95USPerOp resolves the class pools to the individual operations, so
	// a class regression is attributable (e.g. ADR 0003 U2: whether agent_brief
	// or explain_symbol dominates the agent_tools class).
	WarmP95USPerOp map[string]int64 `json:"warm_p95_us_per_op"`
	// WarmSamples is the number of timed invocations pooled per class.
	WarmSamples map[string]int `json:"warm_samples"`
	// WarmOps lists the concrete operations pooled into each class, so the
	// class p95 is interpretable and re-runnable.
	WarmOps map[string][]string `json:"warm_ops"`

	// Searches are the manifest's expect_nonempty smoke assertions re-checked
	// against this run's index.
	Searches []SearchCheck `json:"searches,omitempty"`

	Pass     bool     `json:"pass"`
	Failures []string `json:"failures,omitempty"`
}

// IndexMetrics captures the cold full-index measurement.
type IndexMetrics struct {
	WallclockMS int64 `json:"wallclock_ms"`
	// PeakRSSMB is the process's peak resident set (getrusage MAXRSS) sampled
	// immediately after the index pass — the in-process session model.
	PeakRSSMB   int64 `json:"peak_rss_mb"`
	DBSizeBytes int64 `json:"db_size_bytes"`
	Nodes       int   `json:"nodes"`
	Edges       int   `json:"edges"`
	Files       int   `json:"files"`
}

// SearchCheck is one manifest search assertion outcome.
type SearchCheck struct {
	Query   string `json:"query"`
	Matches int    `json:"matches"`
	Pass    bool   `json:"pass"`
}

// WriteFullRunJSON writes the report as stable, indented JSON.
func WriteFullRunJSON(r FullRunReport, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("evalreport: marshal full-run report: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
