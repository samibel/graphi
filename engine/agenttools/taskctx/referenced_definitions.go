package taskctx

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	enginecontext "github.com/samibel/graphi/engine/context"
)

const (
	referenceSourceLimit     = 15
	referenceNameLimit       = 12
	referenceDefinitionLimit = 1
	referenceSearchLimit     = 8
	referenceMaxDeclLines    = 500
)

var qualifiedCallPattern = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\.([A-Z][A-Za-z0-9_]*)\s*\(`)

// referencedDefinitions resolves exact exported names visibly called by the
// current source candidates. It is a bounded source-to-definition bridge for
// documentation and usage examples whose graph nodes do not carry code edges.
// Approximate lexical matches are rejected after the selective symbol search.
func referencedDefinitions(ctx context.Context, queryText string, candidates []enginecontext.Candidate, reader enginecontext.Reader, symbols graphstore.SymbolLookupPort) ([]model.Node, error) {
	if reader == nil || symbols == nil || len(candidates) == 0 {
		return nil, nil
	}
	queryTerms := identifierTerms(queryText)
	definitionIntent := asksForDefinition(queryTerms)
	procedureIntent := asksForTestProcedure(queryText, queryTerms)
	if !definitionIntent && !procedureIntent {
		return nil, nil
	}
	limit := min(referenceSourceLimit, len(candidates))
	type namedScore struct {
		name  string
		score int
	}
	byName := map[string]int{}
	declarations := map[string]referenceFile{}
	for _, candidate := range candidates[:limit] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text, err := referenceText(ctx, candidate, reader, procedureIntent, declarations)
		if err != nil {
			// Match FilterReadable's posture: one unavailable working-tree
			// source must not make the whole context request unavailable.
			continue
		}
		for _, match := range qualifiedCallPattern.FindAllStringSubmatch(text, -1) {
			name := match[1]
			score := referencedNameScore(queryText, name)
			if score > byName[name] {
				byName[name] = score
			}
		}
	}
	names := make([]namedScore, 0, len(byName))
	for name, score := range byName {
		if score > 0 {
			names = append(names, namedScore{name: name, score: score})
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i].score != names[j].score {
			return names[i].score > names[j].score
		}
		return names[i].name < names[j].name
	})
	if len(names) > referenceNameLimit {
		names = names[:referenceNameLimit]
	}

	seen := map[model.NodeId]bool{}
	var out []model.Node
	for _, named := range names {
		matches, err := symbols.Search(ctx, named.name, referenceSearchLimit)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			n := match.Node
			if n.SourcePath() == "" || seen[n.ID()] {
				continue
			}
			if n.QualifiedName() != named.name && !strings.HasSuffix(n.QualifiedName(), "."+named.name) {
				continue
			}
			seen[n.ID()] = true
			out = append(out, n)
			if len(out) == referenceDefinitionLimit {
				return out, nil
			}
		}
	}
	return out, nil
}

type referenceFile struct {
	text  string
	spans map[string]parse.SourceSpan
}

func referenceText(ctx context.Context, candidate enginecontext.Candidate, reader enginecontext.Reader, declaration bool, cache map[string]referenceFile) (string, error) {
	if !declaration {
		text, _, err := reader.ReadSpan(candidate.Path, enginecontext.Span{
			Start: candidate.StartLine - snippetContext,
			End:   candidate.EndLine + snippetContext,
		})
		return text, err
	}
	file, ok := cache[candidate.Path]
	if !ok {
		text, got, err := reader.ReadSpan(candidate.Path, enginecontext.Span{Start: 1, End: 10001})
		if err != nil {
			return "", err
		}
		file = referenceFile{text: text, spans: map[string]parse.SourceSpan{}}
		if got.End <= 10000 && int64(len(text)) <= parse.DefaultResourceBounds().MaxFileSize {
			parsed, parseErr := parse.NewDefaultRegistry().Parse(ctx, candidate.Path, []byte(text))
			if parseErr == nil && parsed != nil {
				for _, n := range parsed.Nodes {
					if span, exists := parsed.Spans[n.ID()]; exists {
						file.spans[n.QualifiedName()] = span
					}
				}
			}
			if parsed != nil {
				parse.ReleaseRoot(parsed)
			}
		}
		cache[candidate.Path] = file
	}
	span, ok := file.spans[candidate.Symbol]
	if !ok || span.StartLine > candidate.StartLine || span.EndLine < candidate.StartLine {
		text, _, err := reader.ReadSpan(candidate.Path, enginecontext.Span{
			Start: candidate.StartLine - snippetContext,
			End:   candidate.EndLine + snippetContext,
		})
		return text, err
	}
	end := min(span.EndLine, span.StartLine+referenceMaxDeclLines-1)
	lines := strings.Split(file.text, "\n")
	if span.StartLine < 1 || span.StartLine > len(lines) {
		return "", nil
	}
	end = min(end, len(lines))
	return strings.Join(lines[span.StartLine-1:end], "\n"), nil
}

func referencedNameScore(queryText, name string) int {
	queryTerms := identifierTerms(queryText)
	nameTerms := identifierTerms(name)
	score := 0
	for qi, queryTerm := range queryTerms {
		for _, nameTerm := range nameTerms {
			if queryTerm == nameTerm || (len(queryTerm) >= 4 && len(nameTerm) >= 4 && (strings.HasPrefix(queryTerm, nameTerm) || strings.HasPrefix(nameTerm, queryTerm))) {
				score += 100 - min(qi, 99)
				break
			}
		}
	}
	if containsTerm(queryTerms, "function") && containsTerm(nameTerms, "func") {
		score += 200
	}
	return score
}

func asksForDefinition(terms []string) bool {
	for _, term := range []string{"function", "method", "definition", "defined", "implementation"} {
		if containsTerm(terms, term) {
			return true
		}
	}
	return false
}

func asksForTestProcedure(queryText string, terms []string) bool {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(queryText)), "how ") {
		return false
	}
	return containsTerm(terms, "test") || containsTerm(terms, "testing")
}

func containsTerm(terms []string, want string) bool {
	for _, term := range terms {
		if term == want {
			return true
		}
	}
	return false
}

func identifierTerms(text string) []string {
	var terms []string
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		terms = append(terms, strings.ToLower(string(current)))
		current = current[:0]
	}
	var previous rune
	for _, r := range text {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			previous = 0
			continue
		}
		if len(current) > 0 && unicode.IsUpper(r) && unicode.IsLower(previous) {
			flush()
		}
		current = append(current, r)
		previous = r
	}
	flush()
	return terms
}
