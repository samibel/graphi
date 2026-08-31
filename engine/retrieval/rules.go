package retrieval

import "regexp"

// exactIdentifierPattern is the constant regex the AC-6 exact-identifier
// rule applies (AC-6 calls out that the rule is a documented constant,
// never learned). It matches a dotted identifier of two or more segments,
// each segment starting with [A-Za-z_] followed by [A-Za-z0-9_]*. The
// pattern is exported so a test can assert the rule is byte-identical
// across revisions and so a surface can render the rule verbatim.
var exactIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+$`)

// ExactPathPattern is the constant regex the AC-6 exact-path rule applies.
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

// isExactQuery is the AC-6 verdict: an exact query is one that matches
// either rule. Exact queries must be lexical-dominant: semantic
// contributes at most as a tie-break.
func isExactQuery(query string) bool {
	return isExactIdentifier(query) || isExactPath(query)
}
