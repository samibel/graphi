package retrieval

import (
	"sort"
	"strings"
	"unicode"
)

const lifecycleFocusWords = 4

// lifecycleFocusedQuery adds one deterministic semantic view only for
// initialization-order questions. Their concrete lifecycle phrase is commonly
// at the tail of a longer question, while broad flag/configuration nouns at the
// front otherwise dominate small embedding models.
func lifecycleFocusedQuery(queryText string) string {
	if isExactIdentifier(queryText) || isExactPath(queryText) {
		return ""
	}
	fields := strings.Fields(queryText)
	if len(fields) <= lifecycleFocusWords {
		return ""
	}
	var lifecycle, callable bool
	for _, field := range fields {
		word := strings.ToLower(strings.TrimFunc(field, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		}))
		lifecycle = lifecycle || word == "init" || strings.HasPrefix(word, "initializ")
		callable = callable || word == "function" || word == "hook" || word == "callback"
	}
	if !lifecycle || !callable {
		return ""
	}
	tail := append([]string(nil), fields[len(fields)-lifecycleFocusWords:]...)
	for i, field := range tail {
		tail[i] = strings.TrimFunc(field, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		})
		if tail[i] == "" {
			return ""
		}
	}
	return strings.Join(tail, " ")
}

func mergeFocusedSemanticHits(primary, focused []semanticHit) []semanticHit {
	type key struct{ document, node string }
	merged := append([]semanticHit(nil), primary...)
	positions := make(map[key]int, len(primary)+len(focused))
	for i, hit := range merged {
		positions[key{hit.DocumentID, hit.NodeID}] = i
	}
	for _, hit := range focused {
		k := key{hit.DocumentID, hit.NodeID}
		if i, ok := positions[k]; ok {
			if quantiseScore(hit.CosineScore) > quantiseScore(merged[i].CosineScore) {
				merged[i] = hit
			}
			continue
		}
		positions[k] = len(merged)
		merged = append(merged, hit)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		is, js := quantiseScore(merged[i].CosineScore), quantiseScore(merged[j].CosineScore)
		if is != js {
			return is > js
		}
		if merged[i].NodeID != merged[j].NodeID {
			return merged[i].NodeID < merged[j].NodeID
		}
		return merged[i].DocumentID < merged[j].DocumentID
	})
	return merged
}
