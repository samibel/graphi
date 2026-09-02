package retrieval

import "regexp"

// exactIdentifierPattern is the constant regex the AC-6 exact-identifier
// rule applies (AC-6 calls out that the rule is a documented constant,
// never learned). It matches a bare identifier or a dotted identifier
// of one or more segments, each segment starting with [A-Za-z_] followed
// by [A-Za-z0-9_]*. The trailing quantifier is `*` (not `+`) so a bare
// identifier like "ExecuteC", "MarkFlagsMutuallyExclusive",
// "GenMarkdownTree" or "RegisterFlagCompletionFunc" matches — every
// `exact_identifier` query in the SW-258 dev set is a bare name, and the
// previous `+`-anchored rule was vacuous on the stratum it exists to
// protect (SW-263 Amendments, AC-6 widening). The package-private pattern
// remains directly visible to the package tests, which pin it byte-for-byte.
var exactIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// exactPathPattern is the constant regex the AC-6 exact-path rule applies.
// It matches a non-empty path that contains at least one "/" and otherwise
// only POSIX-safe characters.
var exactPathPattern = regexp.MustCompile(`^[A-Za-z0-9_./-]+/[A-Za-z0-9_./-]+$`)

// isExactIdentifier reports whether query matches the exact-identifier
// rule (AC-6). The function is the documented rule, exposed verbatim so
// a test can call it and assert no learned classifier sits between the
// regex and the verdict.
func isExactIdentifier(query string) bool {
	return exactIdentifierPattern.MatchString(query)
}

// isExactPath reports whether query matches the exact-path rule (AC-6).
func isExactPath(query string) bool {
	return exactPathPattern.MatchString(query)
}

// isExactQuery retains the old exact-query classifier for evaluator-only RRF
// modes. Shipped ModeAuto calls isExactPath directly and deliberately does not
// apply the identifier half.
func isExactQuery(query string) bool {
	return isExactIdentifier(query) || isExactPath(query)
}
