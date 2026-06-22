package githistory

// BusFactor holds the bus-factor signal for a single file path: the number of
// unique authors who touched the file within the bounded window. A bus factor
// of 1 means a single author owns the file — a risk signal (if that person
// leaves, no one else knows the code).
type BusFactor struct {
	// Path is the file path (repository-relative).
	Path string `json:"path"`
	// UniqueAuthors is the number of distinct authors who touched this file.
	UniqueAuthors int `json:"unique_authors"`
	// Authors is the sorted list of distinct author identities.
	Authors []string `json:"authors"`
	// Risky is true when UniqueAuthors <= the configured threshold (default 1).
	Risky bool `json:"risky"`
}

// computeBusFactors derives per-file bus-factor signals from the given commits.
// threshold is the maximum UniqueAuthors count that is considered risky
// (inclusive). The default is 1: a single author = risky.
func computeBusFactors(commits []Commit, threshold int) []BusFactor {
	if threshold <= 0 {
		threshold = 1
	}

	// Accumulate unique authors per file.
	authorSets := make(map[string]map[string]struct{})
	for _, c := range commits {
		for _, f := range c.FilesChanged {
			s, ok := authorSets[f]
			if !ok {
				s = make(map[string]struct{})
				authorSets[f] = s
			}
			s[c.Author] = struct{}{}
		}
	}

	// Materialize into sorted slice.
	out := make([]BusFactor, 0, len(authorSets))
	for _, path := range sortedKeys(authorSets) {
		authMap := authorSets[path]
		authors := sortedKeys(authMap)
		out = append(out, BusFactor{
			Path:          path,
			UniqueAuthors: len(authors),
			Authors:       authors,
			Risky:         len(authors) <= threshold,
		})
	}
	return out
}

// sortedKeys is generic and already in provider.go; this file uses it via the
// package-level function.
