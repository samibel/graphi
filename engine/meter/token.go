package meter

import "strings"

// countTokens is the deterministic offline tokenizer used by the meter. It
// mirrors internal/eval.CountTokens (whitespace split) so the baseline token
// accounting is consistent with the eval harness and with engine/context.
//
// It is deliberately local rather than imported from internal/eval: engine must
// not depend on internal/* tooling (layer integrity). The tokenizer is a 1-line
// stdlib call; the duplication is intentional and documented.
func countTokens(text string) int {
	return len(strings.Fields(text))
}
