// Package eval is the token-parity eval harness with a CI-gated per-capability
// coverage matrix (story SW-012). It loads a frozen, labeled eval set, measures
// graphi-vs-baseline token ratios per case using a deterministic offline
// tokenizer, emits a version-stamped report, gates the public "~50× fewer
// tokens" claim on the measured aggregate (resolving open question OQ4 with
// evidence rather than assertion), and enforces a per-capability coverage matrix
// with a drift gate. The harness is hermetic: zero non-loopback network, no
// telemetry, CGo-disabled, deterministic byte-identical re-runs (SW-008 posture).
package eval

import "strings"

// Tokenize is the fixed, deterministic offline tokenizer. It splits on
// whitespace (strings.Fields), producing a stable token list for identical input
// with no model calls. Both the graphi (winnowed) and baseline (whole-file)
// contexts use the SAME tokenizer, so the per-case ratio is tokenizer-agnostic to
// first order — only the relative compression is load-bearing.
func Tokenize(text string) []string {
	return strings.Fields(text)
}

// CountTokens returns the deterministic token count of text.
func CountTokens(text string) int {
	return len(Tokenize(text))
}
