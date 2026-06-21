package context

import "strings"

// countTokens is the deterministic offline tokenizer used by the context engine.
// It mirrors internal/eval.CountTokens (whitespace split) so the bundle's token
// accounting is consistent with the eval harness's token-parity measurements.
//
// It is deliberately local rather than imported from internal/eval: engine must
// not depend on internal/* tooling (that would couple the runtime engine to the
// eval toolchain and place an engine→internal edge outside the ranked
// cmd→surfaces→engine→core chain). The tokenizer is a 1-line stdlib call; the
// duplication is intentional and documented.
func countTokens(text string) int {
	return len(strings.Fields(text))
}
