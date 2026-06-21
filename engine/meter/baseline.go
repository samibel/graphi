package meter

// baselineResult is the output of the (pure) baseline computation.
type baselineResult struct {
	tokens    int
	available bool
}

// computeBaseline is the frozen, deterministic whole-file-read baseline. For the
// set of target artifact file paths a call would otherwise have required, it
// sums countTokens(whole-file content) over the UNIQUE paths (a repeated path is
// counted once — no double-count of the same file within one baseline).
//
// It is a pure function of (artifacts, file bytes): identical inputs yield an
// identical tokens value. Empty artifacts → available=false (honest
// unavailable). A genuine read error is propagated (fail-closed) rather than
// silently marked unavailable — honesty cuts both ways.
func computeBaseline(reader FileReader, artifacts []string) (baselineResult, error) {
	if len(artifacts) == 0 {
		// No honest whole-file-read equivalent can be defined for "no artifacts".
		// Mark unavailable rather than fabricating a favorable number.
		return baselineResult{tokens: 0, available: false}, nil
	}
	seen := make(map[string]struct{}, len(artifacts))
	total := 0
	for _, p := range artifacts {
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue // count each unique file once within this baseline
		}
		seen[p] = struct{}{}
		data, err := reader.ReadFile(p)
		if err != nil {
			return baselineResult{}, err // fail-closed: do not hide a read failure
		}
		total += countTokens(string(data))
	}
	return baselineResult{tokens: total, available: true}, nil
}
