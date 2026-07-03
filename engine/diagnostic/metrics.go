package diagnostic

// Metrics is the EP-019-facing read-only snapshot of a diagnose run. It is
// derived from Result.Summary and the shown diagnostics so it cannot drift
// from the actual output.
type Metrics struct {
	TotalDiagnostics     int                `json:"total_diagnostics"`
	DefaultCount         int                `json:"default_count"`
	AllCount             int                `json:"all_count"`
	SuppressedByCategory map[string]int     `json:"suppressed_by_category"`
	ShownDiagnostics     []Diagnostic       `json:"shown_diagnostics"`
}

// Metrics derives the EP-019 quality signals from the Result. It performs no
// I/O and introduces no outbound calls.
func (r Result) Metrics() Metrics {
	return Metrics{
		TotalDiagnostics:     len(r.Diagnostics),
		DefaultCount:         r.Summary.Shown,
		AllCount:             r.Summary.TotalAnalyzed,
		SuppressedByCategory: r.Summary.SuppressedByCategory,
		ShownDiagnostics:     r.Diagnostics,
	}
}
