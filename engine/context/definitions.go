package context

import (
	"context"
	"math"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/samibel/graphi/core/parse"
)

// AssembleDefinitions retains complete declarations where they fit, and a
// query-focused contiguous window within a declaration otherwise. It preserves
// upstream rank as provenance, deduplicates identical source ranges and selects
// snippets by query-term evidence, rank prior and bounded source cost. Large
// declarations compete as query-focused windows, leaving room for complementary
// sources. All costs are the same whitespace snippet counts as Assemble;
// neither the budget nor wire accounting changes.
// Parser spans are advisory: unsupported, malformed, or oversized source uses
// the existing bounded point window. A source read error is returned.
func AssembleDefinitions(ctx context.Context, query string, candidates []Candidate, opts Options, reader Reader) (Bundle, error) {
	opts = opts.withDefaults()
	bundle := Bundle{Query: query, Budget: opts.Budget, Snippets: []Snippet{}, MethodVersion: "context-definitions/3"}
	if opts.Budget <= 0 || len(candidates) == 0 {
		return bundle, nil
	}
	ranked := rankCandidates(candidates)
	reg := parse.NewDefaultRegistry()
	spans := map[string]map[string]parse.SourceSpan{}
	seen := map[Citation]bool{}
	contextExpansions := map[Citation]Snippet{}
	var snippets []Snippet
	for _, c := range ranked {
		if err := ctx.Err(); err != nil {
			return Bundle{}, err
		}
		byName, loaded := spans[c.Path]
		if !loaded {
			byName = map[string]parse.SourceSpan{}
			// Bound parser input. The extra line detects truncation; an
			// incomplete file must never be treated as a complete AST.
			text, got, err := reader.ReadSpan(c.Path, Span{Start: 1, End: 10001})
			if err != nil {
				return Bundle{}, err
			}
			if got.End <= 10000 && int64(len(text)) <= parse.DefaultResourceBounds().MaxFileSize {
				parsed, err := reg.Parse(ctx, c.Path, []byte(text))
				if err == nil && parsed != nil {
					for _, n := range parsed.Nodes {
						if span, ok := parsed.Spans[n.ID()]; ok {
							byName[n.QualifiedName()] = span
						}
					}
				}
				if parsed != nil {
					parse.ReleaseRoot(parsed)
				}
			}
			spans[c.Path] = byName
		}
		padding := opts.ContextLines
		contextualScalar := false
		if span, ok := byName[c.Symbol]; ok && span.StartLine <= c.StartLine && span.EndLine >= c.StartLine {
			c.StartLine, c.EndLine = span.StartLine, span.EndLine
			padding = 0
			contextualScalar = keepsSiblingContext(c.Kind)
		}
		snip, err := winnow(reader, c, padding)
		if err != nil {
			return Bundle{}, err
		}
		if contextualScalar && opts.ContextLines > 0 && snip.Text != "" {
			expanded, err := winnow(reader, c, opts.ContextLines)
			if err != nil {
				return Bundle{}, err
			}
			if expanded.Text != "" {
				contextExpansions[snip.Citation] = expanded
			}
		}
		if snip.Text != "" && !seen[snip.Citation] {
			snippets = append(snippets, snip)
			seen[snip.Citation] = true
		}
	}
	if len(snippets) == 0 {
		return bundle, nil
	}
	// A lone name/path/topic supplies no multi-term discrimination signal.
	// Retain upstream depth and fair reservation for that shape instead of
	// promoting many cheap fragments that repeat the same one word.
	if len(strings.Fields(query)) < 2 {
		share := opts.Budget / len(snippets)
		reserved := 0
		for _, s := range snippets {
			reserved += min(share, s.Tokens)
		}
		for _, s := range snippets {
			reserved -= min(share, s.Tokens)
			allowance := opts.Budget - bundle.Tokens - reserved
			if allowance <= 0 {
				continue
			}
			if s.Tokens > allowance {
				s = definitionWindow(query, s, allowance)
			}
			if s.Text != "" {
				bundle.Snippets = append(bundle.Snippets, s)
				bundle.Tokens += s.Tokens
			}
		}
		expandWithUnusedBudget(&bundle, contextExpansions)
		return bundle, nil
	}
	words := definitionTerms(query)
	type choice struct {
		snippet Snippet
		value   int
		cost    int
	}
	var choices []choice
	// Compare bounded source windows, not the vocabulary of an arbitrarily
	// large function that cannot be emitted. Keep room for complementary
	// sources; a single-candidate request can spend the entire budget.
	windowBudget := opts.Budget
	if len(snippets) > 1 {
		windowBudget = max(1, opts.Budget/2)
	}
	for i, full := range snippets {
		if err := ctx.Err(); err != nil {
			return Bundle{}, err
		}
		s := full
		if s.Tokens > windowBudget {
			s = definitionWindow(query, s, windowBudget)
		}
		if s.Text == "" {
			continue
		}
		matches := 0
		lower := strings.ToLower(s.Text)
		for _, word := range words {
			if strings.Contains(lower, word) {
				matches++
			}
		}
		base := strings.ToLower(path.Base(s.Citation.Path))
		stem := strings.TrimSuffix(base, path.Ext(base))
		for _, word := range words {
			if stem == word {
				matches++
				break
			}
		}
		// Rank is a prior, not an admission cutoff. A square-root cost
		// discount avoids both giant-source domination and a hard preference
		// for tiny wrappers. The constant overhead discourages fragments.
		value := (1 + matches) * 1000 * (len(snippets) + 1) / (len(snippets) + 1 + i)
		cost := max(1, int(math.Sqrt(float64(s.Tokens+16))))
		choices = append(choices, choice{s, value, cost})
	}
	sort.SliceStable(choices, func(i, j int) bool {
		return choices[i].value*choices[j].cost > choices[j].value*choices[i].cost
	})
	for _, c := range choices {
		remaining := opts.Budget - bundle.Tokens
		if remaining <= 0 {
			break
		}
		s := c.snippet
		if s.Tokens > remaining {
			s = definitionWindow(query, s, remaining)
		}
		if s.Text != "" {
			bundle.Snippets = append(bundle.Snippets, s)
			bundle.Tokens += s.Tokens
		}
	}
	expandWithUnusedBudget(&bundle, contextExpansions)
	sort.SliceStable(bundle.Snippets, func(i, j int) bool {
		a, b := bundle.Snippets[i], bundle.Snippets[j]
		return candidateLess(Candidate{Rank: a.Rank, Path: a.Citation.Path, StartLine: a.Citation.StartLine, EndLine: a.Citation.EndLine},
			Candidate{Rank: b.Rank, Path: b.Citation.Path, StartLine: b.Citation.StartLine, EndLine: b.Citation.EndLine})
	})
	return bundle, nil
}

// expandWithUnusedBudget enriches selected scalar declarations with their
// bounded sibling context only after core source selection is complete. The
// expansion is all-or-nothing and spends only otherwise-unused budget, so a
// grouped const/var block can never displace or truncate an answer-bearing
// function that already won selection.
func expandWithUnusedBudget(bundle *Bundle, expansions map[Citation]Snippet) {
	if bundle == nil || len(expansions) == 0 {
		return
	}
	for i := range bundle.Snippets {
		expanded, ok := expansions[bundle.Snippets[i].Citation]
		if !ok {
			continue
		}
		delta := expanded.Tokens - bundle.Snippets[i].Tokens
		if delta > bundle.Budget-bundle.Tokens {
			continue
		}
		bundle.Snippets[i] = expanded
		bundle.Tokens += delta
	}
}

// Constants and variables are frequently one value specification inside a
// grouped declaration. The parser span correctly identifies that one symbol,
// but narrowing to it discards the group delimiters and adjacent values that
// define the usable protocol. Retain the caller's already-bounded context for
// these scalar declaration kinds; block declarations keep their exact parser
// boundary.
func keepsSiblingContext(kind string) bool {
	switch strings.ToLower(kind) {
	case "constant", "variable":
		return true
	default:
		return false
	}
}

// definitionWindow scores whole source lines. A window earns each meaningful
// query term once, so repeated boilerplate cannot win by repetition. Ties keep
// the earlier window; all emitted bytes remain a contiguous source slice.
func definitionWindow(query string, s Snippet, budget int) Snippet {
	words := definitionTerms(query)
	return definitionWindowTerms(words, s, budget)
}

func definitionTerms(query string) []string {
	terms := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' })
	stop := " a an and are as at be before by can do does for from how in into is it of on or that the their this to what when where which with "
	unique := map[string]bool{}
	var words []string
	for _, word := range terms {
		if len(word) < 3 || strings.Contains(stop, " "+word+" ") || unique[word] {
			continue
		}
		unique[word] = true
		words = append(words, word)
	}
	return words
}

func definitionWindowTerms(words []string, s Snippet, budget int) Snippet {
	lines := strings.Split(s.Text, "\n")
	costs := make([]int, len(lines))
	matches := make([][]int, len(lines))
	for i, line := range lines {
		costs[i] = countTokens(line)
		lower := strings.ToLower(line)
		for j, word := range words {
			if strings.Contains(lower, word) {
				matches[i] = append(matches[i], j)
			}
		}
	}
	bestStart, bestEnd, bestScore, bestTokens := 0, 0, -1, 0
	counts := make([]int, len(words))
	tokens, end, score := 0, 0, 0
	for start := range lines {
		for end < len(lines) {
			n := costs[end]
			if tokens+n > budget {
				break
			}
			tokens += n
			for _, i := range matches[end] {
				if counts[i] == 0 {
					score++
				}
				counts[i]++
			}
			end++
		}
		if end > start && score > bestScore {
			bestStart, bestEnd, bestScore, bestTokens = start, end, score, tokens
		}
		if end == start {
			end++ // a single line exceeds the budget; try later lines
		} else {
			tokens -= costs[start]
			for _, i := range matches[start] {
				counts[i]--
				if counts[i] == 0 {
					score--
				}
			}
		}
	}
	s.Text = strings.Join(lines[bestStart:bestEnd], "\n")
	s.Citation.StartLine += bestStart
	s.Citation.EndLine = s.Citation.StartLine + bestEnd - bestStart - 1
	s.Tokens = bestTokens
	return s
}
