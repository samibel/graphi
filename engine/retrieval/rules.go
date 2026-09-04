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

// exactPathPattern is the constant regex the exact-path rule applies. It
// recognises exactly two shapes, both over POSIX-safe characters only
// ([A-Za-z0-9_.-], plus "/" in the first shape), and nothing else. A "/"
// is NOT required:
//
//  1. a slash path — at least one "/" with non-empty text on both sides
//     (the original AC-6 shape, unchanged by SW-270), e.g. "doc/man_docs.go",
//     "a/b". No extension is required in this shape.
//  2. a bare Go filename — no "/", a non-empty base of [A-Za-z0-9_.-]
//     followed by the literal, lowercase suffix ".go", e.g.
//     "shell_completions.go", "flag_groups.go", "a.b.go" (dots inside the
//     base are fine; only the final ".go" is the extension).
//
// Shape 2 is deliberately ".go" only. It is the one extension family the
// SW-258 dev set exercises (cb-07, cb-09) and therefore the only one the
// before/after measurement under
// docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/
// covers. The first SW-270 build recognised every extension in the
// engine/classify catalog; review found that this silently gave path
// precedence to unmeasured, identifier-shaped queries such as "theme.css"
// or "config.json", so it was narrowed. Recognising other languages'
// filenames is a separately contracted, separately measured change.
//
// Everything else is rejected, in particular: a bare filename with any
// other suffix ("theme.css", "config.json", "README.md", "config.yaml",
// "index.html", "module.jsx", "script.kts", "notes.txt"); a dotted
// identifier such as "cmd.Execute" or "cobra.Command.AddCommand" (its
// last segment is not ".go"); a bare identifier such as "ExecuteC" (no
// extension — SW-263 lifted the identifier half of AC-6, and the revert
// that restored the path override deliberately kept that lift, so the
// path rule must never re-capture an identifier); an upper-case suffix
// ("main.GO"); ".go" with no base name; and anything containing
// whitespace.
//
// Note that shape 2 overlaps exactIdentifierPattern (a bare filename is
// also a dotted-name shape); readyDispatch consults isExactPath first,
// and the identifier half is lifted in ModeAuto, so the path override
// wins — TestSemanticFirst_PathOverride_BareFilenameFires pins that, and
// TestSemanticFirst_PathOverride_RejectedSuffixesDoNotFire pins that the
// rejected suffixes stay on the semantic-first path. Shape 2 was added by
// SW-270; it is narrower than the broad path change SW-263 tried and
// reverted after it cost exact_path its perfect score.
var exactPathPattern = regexp.MustCompile(`^(?:[A-Za-z0-9_./-]+/[A-Za-z0-9_./-]+|[A-Za-z0-9_.-]+\.go)$`)

// isExactIdentifier reports whether query matches the exact-identifier
// rule (AC-6). The function is the documented rule, exposed verbatim so
// a test can call it and assert no learned classifier sits between the
// regex and the verdict.
func isExactIdentifier(query string) bool {
	return exactIdentifierPattern.MatchString(query)
}

// isExactPath reports whether query matches the exact-path rule (AC-6,
// widened to bare ".go" filenames by SW-270): a slash path or a bare Go
// filename, exactly as documented on exactPathPattern.
func isExactPath(query string) bool {
	return exactPathPattern.MatchString(query)
}

// isExactQuery retains the old exact-query classifier for evaluator-only RRF
// modes. Shipped ModeAuto calls isExactPath directly and deliberately does not
// apply the identifier half.
func isExactQuery(query string) bool {
	return isExactIdentifier(query) || isExactPath(query)
}
