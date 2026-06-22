package interproc

import "fmt"

// Caps defines the 5-dimensional resource bounds for interprocedural analysis.
// When any cap is exceeded, the analyzer emits a diagnostic, marks affected
// summaries as approximate, and records the cap-hit. No silent truncation.
type Caps struct {
	// MaxProcedures bounds the number of distinct procedures analyzed.
	// Zero means unlimited.
	MaxProcedures int `json:"max_procedures,omitempty"`
	// MaxIterations bounds the fixpoint iterations per SCC.
	// Zero means unlimited.
	MaxIterations int `json:"max_iterations,omitempty"`
	// MaxSCCSize bounds the maximum SCC size before conservative
	// over-approximation is used instead of precise fixpoint.
	// Zero means unlimited.
	MaxSCCSize int `json:"max_scc_size,omitempty"`
	// MaxSummaryEntries bounds the summary cache size. Zero means unlimited.
	MaxSummaryEntries int `json:"max_summary_entries,omitempty"`
	// MaxTotalWork bounds the total work units (procedure visits across all
	// SCCs and iterations). Zero means unlimited.
	MaxTotalWork int `json:"max_total_work,omitempty"`
}

// DefaultCaps returns sensible defaults for interprocedural analysis.
func DefaultCaps() Caps {
	return Caps{
		MaxProcedures:     5000,
		MaxIterations:     50,
		MaxSCCSize:        100,
		MaxSummaryEntries: 10000,
		MaxTotalWork:      500000,
	}
}

// CapHit records which cap was exceeded and the value at which it triggered.
type CapHit struct {
	Cap   string `json:"cap"`
	Value int    `json:"value"`
	Limit int    `json:"limit"`
}

func (c CapHit) String() string {
	return fmt.Sprintf("%s=%d (limit %d)", c.Cap, c.Value, c.Limit)
}

// checkProcedures reports whether the procedure count exceeds the cap.
func (c Caps) checkProcedures(n int) (CapHit, bool) {
	if c.MaxProcedures > 0 && n > c.MaxProcedures {
		return CapHit{Cap: "max_procedures", Value: n, Limit: c.MaxProcedures}, true
	}
	return CapHit{}, false
}

// checkIterations reports whether the iteration count exceeds the cap.
func (c Caps) checkIterations(n int) (CapHit, bool) {
	if c.MaxIterations > 0 && n > c.MaxIterations {
		return CapHit{Cap: "max_iterations", Value: n, Limit: c.MaxIterations}, true
	}
	return CapHit{}, false
}

// checkSCCSize reports whether the SCC size exceeds the cap.
func (c Caps) checkSCCSize(n int) (CapHit, bool) {
	if c.MaxSCCSize > 0 && n > c.MaxSCCSize {
		return CapHit{Cap: "max_scc_size", Value: n, Limit: c.MaxSCCSize}, true
	}
	return CapHit{}, false
}

// checkSummaryEntries reports whether the summary entry count exceeds the cap.
func (c Caps) checkSummaryEntries(n int) (CapHit, bool) {
	if c.MaxSummaryEntries > 0 && n > c.MaxSummaryEntries {
		return CapHit{Cap: "max_summary_entries", Value: n, Limit: c.MaxSummaryEntries}, true
	}
	return CapHit{}, false
}

// checkTotalWork reports whether the total work exceeds the cap.
func (c Caps) checkTotalWork(n int) (CapHit, bool) {
	if c.MaxTotalWork > 0 && n > c.MaxTotalWork {
		return CapHit{Cap: "max_total_work", Value: n, Limit: c.MaxTotalWork}, true
	}
	return CapHit{}, false
}
