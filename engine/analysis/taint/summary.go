package taint

// SummaryProvider is the pluggable interface for interprocedural summaries
// (SW-030 Sharir-Pnueli). It abstracts how a callee's taint transfer behavior
// is summarized so SW-028 can develop independently of SW-030.
//
// V1 ships with NoOpSummaryProvider which conservatively over-approximates:
// all inputs may reach all outputs. When SW-030 lands, swap in the real
// implementation.
type SummaryProvider interface {
	// HasSummary reports whether a procedure summary is available.
	HasSummary(procID string) bool
	// TransferLabels applies the callee's summary to the given input labels,
	// returning the output labels. If no summary exists, implementations should
	// return the input labels unchanged (conservative over-approximation).
	TransferLabels(procID string, inputLabels LabelSet) LabelSet
}

// NoOpSummaryProvider is the conservative stub: every input label reaches
// every output (no filtering, no interprocedural precision). This allows
// SW-028 to ship independently of SW-030.
type NoOpSummaryProvider struct{}

// HasSummary always returns false — no summaries are available.
func (NoOpSummaryProvider) HasSummary(string) bool { return false }

// TransferLabels returns the input labels unchanged (conservative
// over-approximation: all taint passes through).
func (NoOpSummaryProvider) TransferLabels(_ string, input LabelSet) LabelSet { return input }
