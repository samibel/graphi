package retrieval

import (
	"regexp"
	"strings"
)

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

// exactPathExtensions is the documented list of file extensions the
// bare-filename shape of the exact-path rule recognises (SW-270 AC-1). It
// mirrors the parser catalog's vocabulary in engine/classify, lowercase and
// without the dot; TestRules_ExactPathExtensionsTrackParserCatalog pins that
// every entry is a language classify knows. A bare filename is only a path
// query when graphi could have indexed a file of that kind.
var exactPathExtensions = []string{
	"go",
	"ts", "tsx", "js", "jsx", "mjs",
	"py", "java", "kt", "kts", "cs",
	"c", "h", "cpp", "cc", "cxx", "hpp",
	"rs", "rb", "php", "lua",
	"sh", "bash", "sql", "css",
	"md", "yaml", "yml", "toml", "json", "html",
	"tf", "hcl",
}

// exactPathPattern is the constant regex the exact-path rule applies. It
// recognises exactly two shapes, both over POSIX-safe characters only
// ([A-Za-z0-9_.-], plus "/" in the first shape). A "/" is NOT required:
//
//  1. a slash path — at least one "/" with non-empty text on both sides
//     (the original AC-6 shape), e.g. "doc/man_docs.go", "a/b";
//  2. a bare filename — no "/", a non-empty base followed by "." and one of
//     exactPathExtensions, e.g. "shell_completions.go", "README.md".
//
// Everything else is rejected, in particular: a dotted identifier such as
// "cmd.Execute" or "cobra.Command.AddCommand" (its last segment is not a
// known extension); a bare identifier such as "ExecuteC" (no extension —
// SW-263 lifted the identifier half of AC-6, and the revert that restored
// the path override deliberately kept that lift, so the path rule must
// never re-capture an identifier); an upper-case extension ("main.GO");
// an extension with no base name (".go"); and anything containing
// whitespace. Note that shape 2 overlaps exactIdentifierPattern (a bare
// filename is also a dotted-name shape); readyDispatch consults
// isExactPath first, and the identifier half is lifted in ModeAuto, so
// the path override wins — TestSemanticFirst_PathOverride_BareFilenameFires
// pins that. Shape 2 was added by SW-270. It is deliberately narrower
// than the broad path change SW-263 tried and reverted after it cost
// exact_path its perfect score; the per-stratum before/after measurement
// on the SW-258 dev split lives under
// docs/eval/retrieval/runs/2026-09-04-sw270-bare-filename-path-rule/.
var exactPathPattern = regexp.MustCompile(
	`^(?:[A-Za-z0-9_./-]+/[A-Za-z0-9_./-]+|[A-Za-z0-9_.-]+\.(?:` + strings.Join(exactPathExtensions, "|") + `))$`)

// isExactIdentifier reports whether query matches the exact-identifier
// rule (AC-6). The function is the documented rule, exposed verbatim so
// a test can call it and assert no learned classifier sits between the
// regex and the verdict.
func isExactIdentifier(query string) bool {
	return exactIdentifierPattern.MatchString(query)
}

// isExactPath reports whether query matches the exact-path rule (AC-6,
// widened to bare filenames by SW-270): a slash path or a bare filename
// with a known source extension, as documented on exactPathPattern.
func isExactPath(query string) bool {
	return exactPathPattern.MatchString(query)
}

// isExactQuery retains the old exact-query classifier for evaluator-only RRF
// modes. Shipped ModeAuto calls isExactPath directly and deliberately does not
// apply the identifier half.
func isExactQuery(query string) bool {
	return isExactIdentifier(query) || isExactPath(query)
}
