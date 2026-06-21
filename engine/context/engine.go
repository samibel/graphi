package context

// Assemble is the single context-assembly entry point. It takes a query, the
// candidates from the EP-001 query/search layer, assembly options, and a source
// reader, and returns a deterministic, citation-backed, budget-bounded bundle.
//
// Algorithm (pure function of its inputs):
//  1. Rank candidates by the deterministic total order (Rank asc → Path asc →
//     StartLine asc → EndLine asc).
//  2. For each candidate in rank order, winnow it to its span (+ bounded context
//     padding) and compute its token count.
//  3. Include the snippet iff used + snippet.Tokens <= Budget (greedy rank-order
//     inclusion). The first snippet that does not fit stops inclusion; the
//     remainder is dropped. Budget <= 0 yields an empty bundle.
//  4. Bundle.Tokens == sum of included snippet tokens and is always <= Budget.
//
// The function performs no network I/O; the only I/O is via the caller-supplied
// reader (production: LocalReader, disk-only and remote-rejecting). It uses no
// wall-clock and no randomness, so identical inputs yield byte-identical bundles
// across processes.
//
// A read error on a candidate aborts the whole assembly with that error
// (fail-closed: never emit a partial/guessed bundle). Callers that want to skip
// unreadable candidates should pre-filter their candidate list.
func Assemble(query string, candidates []Candidate, opts Options, reader engineFor) (Bundle, error) {
	opts = opts.withDefaults()
	ranked := rankCandidates(candidates)

	bundle := Bundle{
		Query:         query,
		Budget:        opts.Budget,
		Tokens:        0,
		Snippets:      []Snippet{},
		MethodVersion: MethodVersion,
	}

	if opts.Budget <= 0 {
		return bundle, nil
	}

	used := 0
	for _, c := range ranked {
		snip, err := winnow(reader, c, opts.ContextLines)
		if err != nil {
			return Bundle{}, err
		}
		if used+snip.Tokens > opts.Budget {
			// First non-fitting snippet stops inclusion; drop the remainder.
			break
		}
		bundle.Snippets = append(bundle.Snippets, snip)
		used += snip.Tokens
	}
	bundle.Tokens = used
	return bundle, nil
}
