package githistory

import "sort"

// CoChangeGroup is a set of file paths that frequently change together in the
// same commit within the bounded window. The group is identified by its member
// paths (sorted for determinism) and carries the number of commits in which
// ALL members co-occurred.
type CoChangeGroup struct {
	// Files is the sorted list of file paths in this co-change group.
	Files []string `json:"files"`
	// CoCommits is the number of commits in which all files in the group
	// were changed together.
	CoCommits int `json:"co_commits"`
}

// computeCoChangeGroups identifies file pairs that frequently change together.
// For each commit that touches >= 2 files, every unordered pair of files is
// counted. Pairs with count >= minCoCommits are returned as CoChangeGroups.
//
// We use pairs (not arbitrary N-tuples) because pair co-change is the standard
// metric in the literature and keeps complexity O(commits * files^2-per-commit)
// manageable. minCoCommits defaults to 2 if <= 0.
func computeCoChangeGroups(commits []Commit, minCoCommits int) []CoChangeGroup {
	if minCoCommits <= 0 {
		minCoCommits = 2
	}

	// Count co-occurrences for every unordered file pair.
	type pair struct{ a, b string }
	counts := make(map[pair]int)

	for _, c := range commits {
		files := uniqueSorted(c.FilesChanged)
		for i := 0; i < len(files); i++ {
			for j := i + 1; j < len(files); j++ {
				// files are already sorted, so a < b is guaranteed.
				counts[pair{a: files[i], b: files[j]}]++
			}
		}
	}

	// Filter to pairs that meet the minimum threshold and materialize.
	var out []CoChangeGroup
	for p, n := range counts {
		if n >= minCoCommits {
			out = append(out, CoChangeGroup{
				Files:     []string{p.a, p.b},
				CoCommits: n,
			})
		}
	}

	// Sort for determinism: by first file, then second file.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files[0] != out[j].Files[0] {
			return out[i].Files[0] < out[j].Files[0]
		}
		return out[i].Files[1] < out[j].Files[1]
	})
	return out
}

// uniqueSorted returns a sorted, deduplicated copy of ss.
func uniqueSorted(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
