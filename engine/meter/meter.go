package meter

// Meter is the per-call token metering engine. It is stateless across calls; use
// it as a value. Construct once with New or as a zero value.
type Meter struct {
	reader FileReader
}

// New constructs a Meter over the given local-only FileReader. If reader is nil
// the Meter's Record returns an error (it cannot read artifacts).
func New(reader FileReader) *Meter {
	return &Meter{reader: reader}
}

// Record emits the honest per-call token-savings record for one engine call.
//
//   - actualTokens is the tokens the call actually consumed, as reported by the
//     caller (typically engine/context.Bundle.Tokens). The meter never invents
//     it. It is attributed to exactly this call (CallID), to no other.
//   - artifacts is the set of target file paths the call would otherwise have
//     required a whole-file read of; the baseline is their token sum.
//
// The baseline is computed once per call via computeBaseline (frozen, pure). The
// record's BaselineVersion is captured at emit time (the current
// BaselineMethodVersion), so the record stays attributable to the method under
// which it was produced even after the method changes. SavingsTokens is
// BaselineTokens − ActualTokens, reported raw (may be negative; NOT clamped).
//
// Empty artifacts → BaselineAvailable=false, savings 0 (no favorable
// fabrication). A read error → returned error (fail-closed). The Meter is
// stateless: calling Record twice (even with the same CallID) emits two records
// — dedupe/rollup is the ledger's job (SW-019).
func (m *Meter) Record(callID, model string, actualTokens int, artifacts []string) (MeterRecord, error) {
	if m == nil || m.reader == nil {
		return MeterRecord{}, ErrNoReader
	}
	base, err := computeBaseline(m.reader, artifacts)
	if err != nil {
		return MeterRecord{}, err
	}

	rec := MeterRecord{
		CallID:            callID,
		Model:             model,
		ActualTokens:      actualTokens,
		BaselineTokens:    base.tokens,
		BaselineVersion:   BaselineMethodVersion, // captured at emit time
		BaselineAvailable: base.available,
		Artifacts:         dedupArtifacts(artifacts),
	}
	if base.available {
		rec.SavingsTokens = base.tokens - actualTokens // raw; may be negative; NOT clamped
	}
	return rec, nil
}

// dedupArtifacts returns the artifacts as carried on the record, preserving
// first-seen order with duplicates removed (for auditability). The empty-string
// entries are dropped.
func dedupArtifacts(artifacts []string) []string {
	out := make([]string, 0, len(artifacts))
	seen := make(map[string]struct{}, len(artifacts))
	for _, a := range artifacts {
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}
