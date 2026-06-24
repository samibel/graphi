package parse

import (
	"fmt"
	"sort"
)

// ImpureParser names a single registered parser whose declared Runtime is NOT in
// the pure-Go allowlist — i.e. a CGO (or otherwise non-pure) backend that must
// never reach the default tier. It is the structured offender record the no-CGO
// guard returns so a failure can name exactly which language/runtime regressed.
type ImpureParser struct {
	Language string  // canonical language identifier of the offending parser
	Runtime  Runtime // the declared (impure) runtime
}

func (p ImpureParser) String() string {
	return fmt.Sprintf("%s (runtime=%q)", p.Language, p.Runtime)
}

// AssertPureGoDefaults is the single, exported registration-level no-CGO guard for
// the default tier (SW-055 AC#2/AC#4). It enumerates every parser registered in r
// and returns the ones whose declared Runtime is not CGo-free. A nil/empty result
// means the registry is provably pure-Go; a non-empty result is a CGo-free
// regression that names the offending language(s) and runtime(s).
//
// It is deliberately ONE function exercised by BOTH the positive guard test (over
// RegisterDefaults output → expect no offenders) and the negative/anti-vacuity
// test (over a throwaway registry seeded with a synthetic CGO-marked parser →
// expect it rejected), so the guard cannot pass vacuously: the same code path that
// must accept the real defaults must also reject a planted offender.
//
// It is tag-independent: it inspects in-process runtime registration state, so it
// holds identically under tag-free `go test` and under subset-tagged builds. It
// complements (does not replace) the build/import-graph CGo scan in
// internal/cgoconformance — defense-in-depth, two layers.
func AssertPureGoDefaults(r *Registry) []ImpureParser {
	if r == nil {
		return nil
	}
	// Deduplicate by language: a parser may be indexed under several extensions,
	// but we report one offender per language.
	seen := make(map[string]struct{})
	var offenders []ImpureParser

	r.mu.RLock()
	parsers := make([]Parser, 0, len(r.byLang))
	for _, p := range r.byLang {
		parsers = append(parsers, p)
	}
	r.mu.RUnlock()

	for _, p := range parsers {
		if p == nil {
			continue
		}
		lang := p.Language()
		if _, dup := seen[lang]; dup {
			continue
		}
		seen[lang] = struct{}{}
		if !p.Runtime().IsPureGo() {
			offenders = append(offenders, ImpureParser{Language: lang, Runtime: p.Runtime()})
		}
	}
	sort.Slice(offenders, func(i, j int) bool { return offenders[i].Language < offenders[j].Language })
	return offenders
}

// RegisteredRuntimes returns the sorted set of distinct Runtimes declared by the
// parsers registered in r. Useful for the positive guard's assertion that the
// default tier draws ONLY from the pure-Go runtime set (and, specifically, that
// the CGO go-sitter-forest runtime is absent at the registration layer).
func RegisteredRuntimes(r *Registry) []Runtime {
	if r == nil {
		return nil
	}
	set := make(map[Runtime]struct{})
	r.mu.RLock()
	for _, p := range r.byLang {
		if p != nil {
			set[p.Runtime()] = struct{}{}
		}
	}
	r.mu.RUnlock()
	out := make([]Runtime, 0, len(set))
	for rt := range set {
		out = append(out, rt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FormatImpureFailure renders a clear, release-blocking failure message naming the
// offending parsers for a non-empty AssertPureGoDefaults result.
func FormatImpureFailure(offenders []ImpureParser) string {
	if len(offenders) == 0 {
		return ""
	}
	names := make([]string, 0, len(offenders))
	for _, o := range offenders {
		names = append(names, o.String())
	}
	return fmt.Sprintf(
		"no-CGO default-tier guard: %d non-pure-Go parser(s) reachable from RegisterDefaults: %v — the default tier MUST register only pure-Go runtimes (go/ast, stdlib, gotreesitter); a CGO grammar (e.g. go-sitter-forest) must never reach the default build",
		len(offenders), names,
	)
}
