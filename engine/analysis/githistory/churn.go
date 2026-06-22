package githistory

// ChurnScore holds the churn signal for a single file path: the number of
// commits that touched the file within the bounded window, plus the SHA and
// timestamp of the most recent modifying commit (blame attribution).
type ChurnScore struct {
	// Path is the file path (repository-relative).
	Path string `json:"path"`
	// Commits is the number of commits that touched this file in the window.
	Commits int `json:"commits"`
	// LastCommitSHA is the SHA of the most recent commit that touched this file.
	LastCommitSHA string `json:"last_commit_sha"`
	// LastAuthor is the author of the most recent commit that touched this file.
	LastAuthor string `json:"last_author"`
}

// computeChurn derives per-file churn scores from the given commits.
// Commits are expected in reverse-chronological order (newest first), so the
// first commit that mentions a file is its most-recent modifier.
func computeChurn(commits []Commit) []ChurnScore {
	type accum struct {
		count         int
		lastCommitSHA string
		lastAuthor    string
	}
	m := make(map[string]*accum)

	for _, c := range commits {
		for _, f := range c.FilesChanged {
			a, ok := m[f]
			if !ok {
				// First (newest) commit touching this file — captures blame.
				a = &accum{
					lastCommitSHA: c.SHA,
					lastAuthor:    c.Author,
				}
				m[f] = a
			}
			a.count++
		}
	}

	// Materialize into a sorted slice for determinism.
	out := make([]ChurnScore, 0, len(m))
	for _, path := range sortedKeys(m) {
		a := m[path]
		out = append(out, ChurnScore{
			Path:          path,
			Commits:       a.count,
			LastCommitSHA: a.lastCommitSHA,
			LastAuthor:    a.lastAuthor,
		})
	}
	return out
}
