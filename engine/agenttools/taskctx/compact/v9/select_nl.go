package v9

// Retrieval-ordered projection for natural-language questions (compact/10).
//
// The retrieval engine already ranks candidates with the fused semantic and
// lexical evidence the ranking gates measure. The earlier natural-language
// selector re-scored every candidate from scratch — word matches against a
// symbol's name, fixed bonuses across five orders of magnitude, a dozen
// query-shape predicates — and routinely placed a helper whose *name*
// contains the question's words above the implementation retrieval ranked
// first, leaving that implementation as a one-line anchor. This selector
// keeps the retrieval order for the primary candidates, allocates depth by
// that order, completes a region by its structural unit when the unit fits,
// grows a long unit around the lines that carry the question's words, and
// keeps one query-only lexical discovery region for recall.
//
// Nothing here reads a query identifier, a judgement, a target span, or a
// repository-specific path.

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

const (
	nlMaxSeeds    = 6
	nlDeepSeeds   = 6
	nlMaxOthers   = 2
	nlMaxFallback = 1
	// nlCompleteLimits bounds atomic unit completion by depth rank: the
	// first-ranked implementation may be finished whole up to this many
	// whitespace fields, later regions progressively less.
	nlLeadCompleteLimit   = 230
	nlSecondCompleteLimit = 120
	nlOtherCompleteLimit  = 90
)

type nlCandidate struct {
	compactTaskContextCandidate
	rank      int   // retrieval rank for seeds (1 first); 0 otherwise
	hits      int   // question-word hits inside the snippet
	lineScore []int // per-line question-word score, for directed growth
	unitFrom  int   // structural unit bounds within lines, or -1
	unitTo    int
}

func compactTaskContextSelectNaturalLanguage(query string, patterns []string, evidence []contract.Evidence, items []contract.Item, budget int) ([]CompactTaskContextSource, int, error) {
	type hint struct {
		symbol, kind string
		priority     int
		rank         int
	}
	hintByEvidence := map[string]hint{}
	for _, item := range items {
		symbol, kind := compactTaskContextItemSymbol(item.Reason)
		priority := compactTaskContextItemPriority(item.Reason)
		for _, ref := range item.EvidenceRefIDs {
			prior := hintByEvidence[ref]
			if priority > prior.priority || (priority == prior.priority && prior.symbol == "" && symbol != "") {
				hintByEvidence[ref] = hint{symbol: symbol, kind: kind, priority: priority, rank: item.Rank}
			}
		}
	}
	lowerPatterns := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		if compactTaskContextInformativePattern(pattern) {
			lowerPatterns = append(lowerPatterns, strings.ToLower(pattern))
		}
	}
	if len(lowerPatterns) == 0 {
		for _, pattern := range patterns {
			lowerPatterns = append(lowerPatterns, strings.ToLower(pattern))
		}
	}
	testIntent := compactTaskContextTestIntent(patterns)
	// A bare identifier ("Aliases", "post-run") asks for the thing so
	// named: the line that declares it outweighs every mention. A mention
	// alone earns nothing extra — a prose comment that happens to use the
	// word would otherwise outrank the declaration that only contains it.
	identifierQuery := ""
	if mode, plan := grepReadV2QueryPlan(query); mode == GrepReadV2ExactIdentifier && len(plan) > 0 {
		identifierQuery = plan[0]
	}
	var seeds, others, fallbacks []nlCandidate
	for order, item := range evidence {
		if item.ClaimType != "" || item.TextHash != shape.TextHash(item.Snippet) {
			return nil, 0, fmt.Errorf("compact task_context: invalid snippet evidence %q", item.RefID)
		}
		start, end, err := exactEvidenceSpan(item.Span)
		if err != nil || item.Line != start || end-start+1 != len(strings.Split(item.Snippet, "\n")) {
			return nil, 0, fmt.Errorf("compact task_context: invalid source span %q", item.Span)
		}
		lines := strings.Split(item.Snippet, "\n")
		h := hintByEvidence[item.RefID]
		c := nlCandidate{compactTaskContextCandidate: compactTaskContextCandidate{
			item: item, symbol: h.symbol, kind: h.kind, lines: lines, start: start, order: order,
			fallback: strings.HasPrefix(item.RefID, "grepread-"), itemPriority: h.priority,
		}}
		c.completeFallback = c.fallback && strings.HasPrefix(item.RefID, "grepread-hydrated-")
		c.lineScore = make([]int, len(lines))
		best, bestScore := 0, -1
		for i, line := range lines {
			lower := strings.ToLower(line)
			score := 0
			for _, pattern := range lowerPatterns {
				if strings.Contains(lower, pattern) {
					score += 10
					c.hits++
				}
			}
			trimmed := strings.TrimSpace(lower)
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "const ") {
				score += 4
			}
			if actual := nlWholeToken(line, identifierQuery); actual != "" && grepReadV2DeclarationLine([]byte(line), actual) {
				score += 100
			}
			c.lineScore[i] = score
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		c.anchor, c.from, c.to = best, best, best
		// Supporting regions compete on how strongly their best line answers,
		// not on how long they are: a 250-line generator mentions every
		// question word somewhere without answering anything.
		c.hits = bestScore*100 + min(c.hits, 50)
		c.unitFrom, c.unitTo = -1, -1
		if strings.HasSuffix(strings.ToLower(item.Path), ".md") {
			if from, to, ok := compactTaskContextMarkdownSection(lines, best+1); ok {
				c.unitFrom, c.unitTo = from-1, to-1
			}
		} else if from, to, ok := compactTaskContextUnitRange(c.compactTaskContextCandidate); ok && from <= best && best <= to {
			c.unitFrom, c.unitTo = from, to
		}
		isTest := strings.HasSuffix(strings.ToLower(item.Path), "_test.go")
		switch {
		case c.fallback:
			fallbacks = append(fallbacks, c)
		case (h.priority == 12_000 || h.priority == 4_000) && (!isTest || testIntent):
			c.rank = h.rank
			seeds = append(seeds, c)
		case isTest && !testIntent:
			// A test that retrieval ranked well still explains behaviour
			// second-hand; it competes with the other supporting regions,
			// below any reference into the implementation.
			c.hits /= 2
			c.itemPriority = 0
			others = append(others, c)
		default:
			others = append(others, c)
		}
	}
	// Retrieval order for seeds: the assembler's Rank is higher for earlier
	// rows; ties keep evidence order.
	sort.SliceStable(seeds, func(i, j int) bool {
		if seeds[i].rank != seeds[j].rank {
			return seeds[i].rank > seeds[j].rank
		}
		return seeds[i].order < seeds[j].order
	})
	// A question that spells a declaration's name as declared ("how does
	// Execute dispatch…") is asking about that declaration; it leads, whatever
	// retrieval put first. Only the exact spelling counts — "flag" is a word,
	// "Flag" is the method.
	for i := range seeds {
		if compactTaskContextQueryNamesSymbol(query, seeds[i].symbol) {
			named := seeds[i]
			copy(seeds[1:i+1], seeds[:i])
			seeds[0] = named
			break
		}
	}
	// Semantic and query-only discovery often land on the same declaration
	// with different windows. Keep the wider one; a duplicate would spend a
	// slot and budget repeating lines the reader already has.
	seeds = nlDedupe(seeds)
	others = nlDedupe(nlWithout(others, seeds))
	sort.SliceStable(others, func(i, j int) bool {
		if others[i].itemPriority != others[j].itemPriority {
			return others[i].itemPriority > others[j].itemPriority
		}
		if others[i].hits != others[j].hits {
			return others[i].hits > others[j].hits
		}
		return others[i].order < others[j].order
	})
	// A discovery hit hydrated to its whole declaration is worth more than
	// the raw window around the same line, and the same line found twice is
	// one region.
	sort.SliceStable(fallbacks, func(i, j int) bool {
		if fallbacks[i].completeFallback != fallbacks[j].completeFallback {
			return fallbacks[i].completeFallback
		}
		if fallbacks[i].hits != fallbacks[j].hits {
			return fallbacks[i].hits > fallbacks[j].hits
		}
		return fallbacks[i].order < fallbacks[j].order
	})
	fallbacks = nlDedupe(fallbacks)
	if len(seeds) > nlMaxSeeds {
		seeds = seeds[:nlMaxSeeds]
	}
	if len(others) > nlMaxOthers {
		others = others[:nlMaxOthers]
	}
	if len(fallbacks) > nlMaxFallback {
		fallbacks = fallbacks[:nlMaxFallback]
	}
	// A discovery region that only repeats a seed's declaration adds nothing.
	fallbacks = nlDropCoveredFallbacks(fallbacks, seeds)

	selected := append(append(append([]nlCandidate{}, seeds...), others...), fallbacks...)
	remaining := budget
	admitted := make([]bool, len(selected))
	for i := range selected {
		cost := len(strings.Fields(selected[i].lines[selected[i].anchor]))
		if cost == 0 || cost > remaining {
			continue
		}
		admitted[i] = true
		selected[i].cost = cost
		remaining -= cost
	}
	// Depth by retrieval order: finish the lead whole when it fits; otherwise
	// grow it around the question's words inside its own declaration. Then
	// the second seed, then a bounded window for everything else.
	quota := func(share int) int { return budget * share / 100 }
	grow := func(c *nlCandidate, limit, target int) {
		if nlCompleteUnit(c, &remaining, limit) {
			return
		}
		compactTaskContextGrowDocComment(&c.compactTaskContextCandidate, &remaining, 3)
		for c.cost < target && remaining > 0 && nlGrow(c, &remaining) {
		}
	}
	// The lead's own completion is reserved before anything below it is
	// finished: a lead that fits whole must not be left cut by the small
	// declarations ranked after it, and they must not be starved by a long
	// lead's growth. Both hold when the reserve is exactly the lead's need.
	leadReserve := 0
	if len(seeds) > 0 && admitted[0] && selected[0].unitFrom >= 0 {
		whole := len(strings.Fields(strings.Join(selected[0].lines[selected[0].unitFrom:selected[0].unitTo+1], "\n")))
		if whole <= nlLeadCompleteLimit && whole-selected[0].cost <= remaining {
			leadReserve = whole - selected[0].cost
		}
	}
	remaining -= leadReserve
	// Phase 1: what the lead calls. A small declaration the lead's own text
	// names is the next thing a reader looks for; finish it whole while it
	// is cheap, ahead of any lower-ranked seed.
	if len(seeds) > 0 && admitted[0] {
		leadText := strings.Join(selected[0].lines, "\n")
		for i := 1; i < len(selected); i++ {
			if !admitted[i] || selected[i].fallback || !nlLeadNames(leadText, selected[i].symbol) {
				continue
			}
			nlCompleteUnit(&selected[i], &remaining, nlOtherCompleteLimit)
		}
	}
	// Phase 2: every deep seed that is a small declaration is finished whole.
	// A complete small function is the best shape an answer can take and the
	// cheapest; it must not lose its lines to a long lead's growth.
	for i := 1; i < len(seeds) && i < nlDeepSeeds; i++ {
		if admitted[i] {
			nlCompleteUnit(&selected[i], &remaining, nlOtherCompleteLimit)
		}
	}
	// Phase 3: the lead, finished whole when it fits, otherwise grown around
	// the question's words inside its own declaration. A lead that would fit
	// whole but for trailing one-line citations takes their budget back: the
	// end of the declaration retrieval put first is worth more than a
	// citation of something it put tenth.
	remaining += leadReserve
	if len(seeds) > 0 && admitted[0] {
		lead := &selected[0]
		if lead.unitFrom >= 0 {
			whole := len(strings.Fields(strings.Join(lead.lines[lead.unitFrom:lead.unitTo+1], "\n")))
			for i := len(selected) - 1; i > 0 && whole <= nlLeadCompleteLimit && whole-lead.cost > remaining; i-- {
				if !admitted[i] || selected[i].complete {
					continue
				}
				admitted[i] = false
				remaining += selected[i].cost
				selected[i].cost = 0
			}
		}
		grow(lead, nlLeadCompleteLimit, quota(48))
	}
	// Phase 4: bounded depth for everything else, by retrieval order.
	for i := 1; i < len(selected); i++ {
		if !admitted[i] || selected[i].complete {
			continue
		}
		c := &selected[i]
		limit, target := nlOtherCompleteLimit, quota(6)
		switch {
		case i == 1 && len(seeds) > 1:
			limit, target = nlSecondCompleteLimit, quota(18)
		case i < len(seeds) && i >= nlDeepSeeds:
			// A seed past the depth window stays a citation: its anchor
			// line and doc comment, nothing more, so the reader can see it
			// exists without it costing a body's worth of budget.
			compactTaskContextGrowDocComment(&c.compactTaskContextCandidate, &remaining, 2)
			continue
		case c.fallback:
			limit, target = nlOtherCompleteLimit, quota(10)
		}
		grow(c, limit, target)
	}
	// Budget the quotas did not need is not spent: every extra line costs
	// the reader tokens, and the regions already hold what the question
	// asked for.
	_ = remaining
	out := make([]CompactTaskContextSource, 0, len(selected))
	for i, c := range selected {
		if !admitted[i] {
			continue
		}
		out = append(out, CompactTaskContextSource{
			Path: c.item.Path, Start: c.start + c.from, End: c.start + c.to,
			Text: strings.Join(c.lines[c.from:c.to+1], "\n"),
		})
	}
	out = compactTaskContextRemoveContainedSources(out)
	out, used := compactTaskContextTrimSources(out, budget)
	return out, used, nil
}

// nlCompleteUnit finishes a region's structural unit — Go declaration or
// Markdown section — when the whole unit costs at most maxCost and fits.
func nlCompleteUnit(c *nlCandidate, remaining *int, maxCost int) bool {
	if c.unitFrom < 0 || (c.fallback && !c.completeFallback) {
		return false
	}
	full := len(strings.Fields(strings.Join(c.lines[c.unitFrom:c.unitTo+1], "\n")))
	additional := full - c.cost
	if full <= 0 || full > maxCost || additional < 0 || additional > *remaining {
		return false
	}
	c.from, c.to, c.cost, c.complete = c.unitFrom, c.unitTo, full, true
	*remaining -= additional
	return true
}

// nlGrow extends a region by one line, towards whichever neighbouring line
// carries more of the question's words (downward on a tie), and never past
// the region's own structural unit when one is known.
func nlGrow(c *nlCandidate, remaining *int) bool {
	if c.complete {
		return false
	}
	lo, hi := 0, len(c.lines)-1
	if c.unitFrom >= 0 {
		lo, hi = c.unitFrom, c.unitTo
	}
	down, up := c.to+1 <= hi, c.from-1 >= lo
	if !down && !up {
		return false
	}
	pickDown := down
	if down && up {
		switch {
		case c.lineScore[c.from-1] > c.lineScore[c.to+1]:
			pickDown = false
		case c.lineScore[c.from-1] < c.lineScore[c.to+1]:
			pickDown = true
		default:
			// No signal either way: alternate, so a region grows around its
			// anchor rather than trailing away from it.
			pickDown = (c.to-c.from)%2 == 0
		}
	}
	index := c.to + 1
	if !pickDown {
		index = c.from - 1
	}
	cost := len(strings.Fields(c.lines[index]))
	if cost > *remaining {
		return false
	}
	if pickDown {
		c.to = index
	} else {
		c.from = index
	}
	c.cost += cost
	*remaining -= cost
	return true
}

// nlWholeToken reports the identifier on the line that the query token
// names as a whole identifier: the token itself when it is one, or the
// identifier its separated parts spell ("post-run" names PostRun).
func nlWholeToken(line, token string) string {
	switch {
	case token == "":
		return ""
	case grepReadV2Identifier.MatchString(token):
		if grepReadV2WholeIdentifierColumn([]byte(line), token) > 0 {
			return token
		}
	case grepReadV2SeparatedIdentifier.MatchString(token):
		folded := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(token))
		_, actual := grepReadV2FoldedIdentifierColumn([]byte(line), folded)
		return actual
	}
	return ""
}

// compactTaskContextQueryNamesSymbol reports whether the query spells the
// symbol's own name, case-sensitively and as a whole word.
func compactTaskContextQueryNamesSymbol(query, symbol string) bool {
	return nlLeadNames(query, symbol)
}

// nlLeadNames reports whether the lead's emitted text mentions the symbol's
// own name as a whole identifier.
func nlLeadNames(leadText, symbol string) bool {
	if dot := strings.LastIndex(symbol, "."); dot >= 0 {
		symbol = symbol[dot+1:]
	}
	if symbol == "" {
		return false
	}
	for _, identifier := range strings.FieldsFunc(leadText, func(r rune) bool {
		return r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if identifier == symbol {
			return true
		}
	}
	return false
}

func nlOverlaps(a, b nlCandidate) bool {
	if a.item.Path != b.item.Path {
		return false
	}
	aFrom, aTo := a.start, a.start+len(a.lines)-1
	bFrom, bTo := b.start, b.start+len(b.lines)-1
	return aFrom <= bTo && bFrom <= aTo
}

// nlDedupe keeps, for each set of overlapping windows on one path, the widest
// window, at the position of the earliest member so ordering is preserved.
func nlDedupe(list []nlCandidate) []nlCandidate {
	out := make([]nlCandidate, 0, len(list))
	for _, c := range list {
		merged := false
		for i := range out {
			if nlOverlaps(out[i], c) {
				if len(c.lines) > len(out[i].lines) {
					rank, order := out[i].rank, out[i].order
					out[i] = c
					out[i].rank, out[i].order = rank, order
				}
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, c)
		}
	}
	return out
}

// nlWithout drops candidates whose window overlaps a seed's window.
func nlWithout(list, seeds []nlCandidate) []nlCandidate {
	out := make([]nlCandidate, 0, len(list))
	for _, c := range list {
		covered := false
		for _, s := range seeds {
			if nlOverlaps(s, c) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, c)
		}
	}
	return out
}

func nlDropCoveredFallbacks(fallbacks, seeds []nlCandidate) []nlCandidate {
	out := fallbacks[:0]
	for _, f := range fallbacks {
		covered := false
		for _, s := range seeds {
			if s.item.Path == f.item.Path && s.start <= f.start+f.anchor && f.start+f.anchor < s.start+len(s.lines) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, f)
		}
	}
	return out
}
