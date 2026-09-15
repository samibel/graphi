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
	nlLeadCompleteLimit   = 250
	nlSecondCompleteLimit = 120
	nlOtherCompleteLimit  = 90
)

type nlCandidate struct {
	compactTaskContextCandidate
	rank               int   // retrieval rank for seeds (1 first); 0 otherwise
	hits               int   // question-word hits inside the snippet
	lineScore          []int // per-line question-word score, for directed growth
	unitFrom           int   // structural unit bounds within lines, or -1
	unitTo             int
	clustered          bool // long named declaration anchored on an internal query cluster
	declaresIdentifier bool // anchor declares the bare identifier in the query
	density            int  // distinct query terms per field in a small declaration
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
		declarationPatterns := make(map[string]bool)
		for i, line := range lines {
			lower := strings.ToLower(line)
			score := 0
			for _, pattern := range lowerPatterns {
				if nlContainsPattern(lower, pattern) {
					score += 10
					c.hits++
					declarationPatterns[pattern] = true
				}
			}
			trimmed := strings.TrimSpace(lower)
			if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "const ") {
				score += 4
			}
			if actual := nlWholeToken(line, identifierQuery); actual != "" && grepReadV2DeclarationLine([]byte(line), actual) {
				score += 100
				c.declaresIdentifier = true
			}
			c.lineScore[i] = score
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		if identifierQuery == "" && c.fallback && len(lines) > 40 && (h.kind == "function" || h.kind == "method") && compactTaskContextQueryNamesSymbol(query, h.symbol) {
			namedPattern := h.symbol
			if dot := strings.LastIndex(namedPattern, "."); dot >= 0 {
				namedPattern = namedPattern[dot+1:]
			}
			if anchor, distinct := nlBestQuestionCluster(lines, lowerPatterns, strings.ToLower(namedPattern)); distinct >= 3 {
				best = anchor
				c.clustered = true
			}
		}
		c.anchor, c.from, c.to = best, best, best
		// Supporting regions compete on how strongly their best line answers,
		// not on how long they are: a 250-line generator mentions every
		// question word somewhere without answering anything.
		c.hits = bestScore*100 + min(c.hits, 50)
		fieldCount := len(strings.Fields(item.Snippet))
		if len(declarationPatterns) >= 2 && fieldCount <= nlOtherCompleteLimit && (h.kind == "function" || h.kind == "method") {
			c.density = len(declarationPatterns) * 100_000 / max(1, fieldCount)
		}
		if c.completeFallback && fieldCount <= nlOtherCompleteLimit {
			c.hits += len(declarationPatterns) * 1000
		}
		c.unitFrom, c.unitTo = -1, -1
		if strings.HasSuffix(strings.ToLower(item.Path), ".md") {
			if from, to, ok := compactTaskContextMarkdownSection(lines, best+1); ok {
				c.unitFrom, c.unitTo = from-1, to-1
				whole := len(strings.Fields(strings.Join(lines[c.unitFrom:c.unitTo+1], "\n")))
				if whole > nlLeadCompleteLimit {
					if blockFrom, blockTo, ok := nlMarkdownParagraphWithFence(lines, best); ok {
						c.unitFrom, c.unitTo = blockFrom, blockTo
					}
				}
			}
		} else if from, to, ok := compactTaskContextUnitRange(c.compactTaskContextCandidate); ok && from <= best && best <= to {
			c.unitFrom, c.unitTo = from, to
		}
		if c.clustered {
			lo, hi := 0, len(lines)-1
			if c.unitFrom >= 0 {
				lo, hi = c.unitFrom, c.unitTo
			}
			c.from = max(lo, c.anchor-12)
			c.to = min(hi, c.anchor+12)
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
	namedLead := false
	for i := range seeds {
		if compactTaskContextQueryNamesSymbol(query, seeds[i].symbol) {
			named := seeds[i]
			copy(seeds[1:i+1], seeds[:i])
			seeds[0] = named
			namedLead = true
			break
		}
	}
	// Semantic and query-only discovery often land on the same declaration
	// with different windows. Keep the wider one; a duplicate would spend a
	// slot and budget repeating lines the reader already has.
	seeds = nlDedupe(seeds)
	if !namedLead && len(seeds) > 1 {
		best := 0
		for i := 1; i < len(seeds); i++ {
			if seeds[i].hits > seeds[best].hits {
				best = i
			}
		}
		if best > 0 && nlQueryNamesPathStem(patterns, seeds[best].item.Path) &&
			seeds[best].hits >= 3000 && seeds[best].hits >= seeds[0].hits+1000 {
			specific := seeds[best]
			copy(seeds[1:best+1], seeds[:best])
			seeds[0] = specific
		}
	}
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
		iNamed := compactTaskContextQueryNamesSymbol(query, fallbacks[i].symbol)
		jNamed := compactTaskContextQueryNamesSymbol(query, fallbacks[j].symbol)
		if iNamed != jNamed {
			return iNamed
		}
		iCoherent := fallbacks[i].completeFallback && (fallbacks[i].density > 0 || fallbacks[i].clustered)
		jCoherent := fallbacks[j].completeFallback && (fallbacks[j].density > 0 || fallbacks[j].clustered)
		if iCoherent != jCoherent {
			return iCoherent
		}
		if fallbacks[i].density != fallbacks[j].density {
			return fallbacks[i].density > fallbacks[j].density
		}
		if fallbacks[i].hits != fallbacks[j].hits {
			return fallbacks[i].hits > fallbacks[j].hits
		}
		return fallbacks[i].order < fallbacks[j].order
	})
	fallbacks = nlDedupe(fallbacks)
	// A bare identifier which names a field has no independently indexed
	// symbol item, so compactTaskContextSelect deliberately routes it through
	// this selector. The declaration line found by exact lexical discovery is
	// still the primary answer and must not remain below the retrieval-depth
	// cap merely because the enclosing type has a different symbol name.
	if identifierQuery != "" {
		promote := func(candidates *[]nlCandidate) bool {
			for i, candidate := range *candidates {
				if !candidate.declaresIdentifier {
					continue
				}
				*candidates = append((*candidates)[:i], (*candidates)[i+1:]...)
				seeds = append([]nlCandidate{candidate}, seeds...)
				return true
			}
			return false
		}
		if !promote(&seeds) && !promote(&others) {
			promote(&fallbacks)
		}
		namedLead = len(seeds) > 0 && seeds[0].declaresIdentifier
	}
	// Discovery and indexed retrieval are two ways to find the same source,
	// not two different relevance classes. If the query explicitly names a
	// declaration that only query-only discovery found, make it the lead just
	// as we do for a named indexed seed above. Keeping it in the fallback band
	// would give the strongest lexical signal only a supporting-region quota.
	if !namedLead {
		for i, candidate := range fallbacks {
			if !compactTaskContextQueryNamesSymbol(query, candidate.symbol) {
				continue
			}
			fallbacks = append(fallbacks[:i], fallbacks[i+1:]...)
			seeds = append([]nlCandidate{candidate}, seeds...)
			namedLead = true
			break
		}
	}
	// Keep a direct call edge intact at the breadth cap. Retrieval can place a
	// caller at the edge of the retained window and its small callee one row
	// below it; dropping the callee then turns a flow answer into an unrelated
	// breadth list. Only an exact symbol mention in an already retained seed
	// can promote one below-cap seed, and it replaces rather than widens the
	// fixed seed count.
	bestCaller, bestCallee, bestPair := -1, -1, -1
	for callerIndex := 0; callerIndex < min(nlMaxSeeds, len(seeds)); callerIndex++ {
		callerText := strings.Join(seeds[callerIndex].lines, "\n")
		for calleeIndex := nlMaxSeeds; calleeIndex < len(seeds); calleeIndex++ {
			callee := seeds[calleeIndex]
			if callee.hits < seeds[callerIndex].hits || !nlLeadNames(callerText, callee.symbol) {
				continue
			}
			if pair := seeds[callerIndex].hits + callee.hits; pair > bestPair {
				bestCaller, bestCallee, bestPair = callerIndex, calleeIndex, pair
			}
		}
	}
	if bestCallee >= 0 {
		callee := seeds[bestCallee]
		seeds = append(seeds[:bestCallee], seeds[bestCallee+1:]...)
		insertAt := bestCaller + 1
		seeds = append(seeds, nlCandidate{})
		copy(seeds[insertAt+1:], seeds[insertAt:])
		seeds[insertAt] = callee
	}
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
		cost := len(strings.Fields(strings.Join(selected[i].lines[selected[i].from:selected[i].to+1], "\n")))
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
		coherentLead := namedLead || strings.HasSuffix(strings.ToLower(lead.item.Path), ".md")
		leadTarget := quota(48)
		if lead.unitFrom >= 0 {
			whole := len(strings.Fields(strings.Join(lead.lines[lead.unitFrom:lead.unitTo+1], "\n")))
			required := whole - lead.cost
			eligible := whole <= nlLeadCompleteLimit
			if namedLead && (lead.kind == "function" || lead.kind == "method") && !eligible {
				share := 60
				if lead.clustered {
					share = 64
				}
				required = max(0, quota(share)-lead.cost)
				leadTarget = quota(share)
				eligible = true
			}
			for i := len(selected) - 1; i > 0 && eligible && required > remaining; i-- {
				if !admitted[i] || (selected[i].complete && !coherentLead) {
					continue
				}
				admitted[i] = false
				remaining += selected[i].cost
				selected[i].cost = 0
			}
		}
		if !namedLead && nlHasSeparatedStrongSignals(lead.lineScore) {
			leadTarget = quota(53)
			for i := len(selected) - 1; i > 0 && leadTarget-lead.cost > remaining; i-- {
				if !admitted[i] || selected[i].complete {
					continue
				}
				admitted[i] = false
				remaining += selected[i].cost
				selected[i].cost = 0
			}
		}
		grow(lead, nlLeadCompleteLimit, leadTarget)
		for count := 0; count < 3 && nlHasTrailingCloser(lead); count++ {
			cost := len(strings.Fields(lead.lines[lead.to+1]))
			for i := len(selected) - 1; i > 0 && cost > remaining; i-- {
				if !admitted[i] || selected[i].complete {
					continue
				}
				admitted[i] = false
				remaining += selected[i].cost
				selected[i].cost = 0
			}
			if cost > remaining {
				break
			}
			lead.to++
			lead.cost += cost
			remaining -= cost
		}
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
	out = compactTaskContextMergeAdjacentSources(out)
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
		case c.clustered:
			// Once a dense internal cluster has located the behaviour, grow
			// evenly around it. Following the next keyword can otherwise spend
			// the whole quota on earlier comments and cut the other half of the
			// decision branch.
			pickDown = c.to-c.anchor <= c.anchor-c.from
		case c.lineScore[c.from-1] > c.lineScore[c.to+1]:
			pickDown = false
		case c.lineScore[c.from-1] < c.lineScore[c.to+1]:
			pickDown = true
		default:
			upScore, upDistance := nlNextSignal(c.lineScore, c.from-1, -1, lo)
			downScore, downDistance := nlNextSignal(c.lineScore, c.to+1, 1, hi)
			switch {
			case upScore > downScore:
				pickDown = false
			case upScore < downScore:
				pickDown = true
			case upScore > 0 && upDistance < downDistance:
				pickDown = false
			case downScore > 0 && downDistance < upDistance:
				pickDown = true
			default:
				pickDown = (c.to-c.from)%2 == 0
			}
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

func nlNextSignal(scores []int, start, step, bound int) (best, distance int) {
	distance = len(scores) + 1
	for i, d := start, 1; ; i, d = i+step, d+1 {
		if (step < 0 && i < bound) || (step > 0 && i > bound) {
			break
		}
		if scores[i] > best {
			best, distance = scores[i], d
		}
	}
	return best, distance
}

func nlHasSeparatedStrongSignals(scores []int) bool {
	first := -1
	for line, score := range scores {
		if score < 20 {
			continue
		}
		if first >= 0 && line-first >= 8 {
			return true
		}
		if first < 0 {
			first = line
		}
	}
	return false
}

// nlBestQuestionCluster treats an explicitly named declaration as a search
// boundary and uses the other query terms to locate the requested behaviour
// inside it. A bounded window rewards several different terms appearing
// together, so a generic early mention cannot beat a later decision branch
// that contains the query's discriminating identifier.
func nlBestQuestionCluster(lines, patterns []string, namedPattern string) (anchor, distinct int) {
	const windowLines = 24
	bestStart, bestEnd := 0, 0
	bestQuality := -1
	for start := range lines {
		end := min(len(lines), start+windowLines)
		seen := make(map[string]bool)
		codeSeen := make(map[string]bool)
		for _, line := range lines[start:end] {
			lower := strings.ToLower(line)
			trimmed := strings.TrimSpace(lower)
			isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")
			for _, pattern := range patterns {
				if pattern != namedPattern && nlContainsPattern(lower, pattern) {
					seen[pattern] = true
					if !isComment {
						codeSeen[pattern] = true
					}
				}
			}
		}
		quality := len(seen)*100 + len(codeSeen)*10
		if quality > bestQuality {
			bestQuality, distinct, bestStart, bestEnd = quality, len(seen), start, end
		}
	}
	if distinct == 0 {
		return 0, 0
	}
	// Start in the middle of the best window rather than on its earliest or
	// latest matching line. The answer is commonly the branch joining the
	// signals, and a centred initial span preserves context on both sides.
	return (bestStart + bestEnd - 1) / 2, distinct
}

// nlMarkdownParagraphWithFence returns a prose paragraph and the fenced code
// example immediately attached to it. Long documentation sections often
// contain several topics under one heading; treating the paragraph and its
// example atomically keeps the answer coherent without paying for the entire
// section.
func nlMarkdownParagraphWithFence(lines []string, anchor int) (from, to int, ok bool) {
	if anchor < 0 || anchor >= len(lines) || strings.TrimSpace(lines[anchor]) == "" {
		return 0, 0, false
	}
	from = anchor
	for from > 0 {
		previous := strings.TrimSpace(lines[from-1])
		if previous == "" || strings.HasPrefix(previous, "#") || strings.HasPrefix(previous, "```") || strings.HasPrefix(previous, "~~~") {
			break
		}
		from--
	}
	paragraphEnd := anchor
	for paragraphEnd+1 < len(lines) && strings.TrimSpace(lines[paragraphEnd+1]) != "" {
		next := strings.TrimSpace(lines[paragraphEnd+1])
		if strings.HasPrefix(next, "#") || strings.HasPrefix(next, "```") || strings.HasPrefix(next, "~~~") {
			break
		}
		paragraphEnd++
	}
	fenceStart := paragraphEnd + 1
	for fenceStart < len(lines) && strings.TrimSpace(lines[fenceStart]) == "" {
		fenceStart++
	}
	if fenceStart >= len(lines) {
		return 0, 0, false
	}
	fence := strings.TrimSpace(lines[fenceStart])
	marker := ""
	if strings.HasPrefix(fence, "```") {
		marker = "```"
	} else if strings.HasPrefix(fence, "~~~") {
		marker = "~~~"
	} else {
		return 0, 0, false
	}
	for fenceEnd := fenceStart + 1; fenceEnd < len(lines); fenceEnd++ {
		if strings.HasPrefix(strings.TrimSpace(lines[fenceEnd]), marker) {
			return from, fenceEnd, true
		}
	}
	return 0, 0, false
}

// nlContainsPattern closes the small gap between an English base verb ending
// in silent e and its inflected spelling ("combine" / "combining"). The
// query stemmer already removes "ing" from inflected queries; applying the
// inverse-compatible prefix here makes matching symmetric without changing
// exact-identifier mode.
func nlContainsPattern(lowerLine, pattern string) bool {
	if strings.Contains(lowerLine, pattern) {
		return true
	}
	return len(pattern) > 5 && strings.HasSuffix(pattern, "e") && strings.Contains(lowerLine, strings.TrimSuffix(pattern, "e"))
}

func nlHasTrailingCloser(c *nlCandidate) bool {
	hi := len(c.lines) - 1
	if c.unitFrom >= 0 {
		hi = c.unitTo
	}
	if c.to >= hi {
		return false
	}
	trimmed := strings.TrimSpace(c.lines[c.to+1])
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if !strings.ContainsRune("}])(),;", r) {
			return false
		}
	}
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

func nlQueryNamesPathStem(patterns []string, sourcePath string) bool {
	if slash := strings.LastIndex(sourcePath, "/"); slash >= 0 {
		sourcePath = sourcePath[slash+1:]
	}
	if dot := strings.LastIndex(sourcePath, "."); dot > 0 {
		sourcePath = sourcePath[:dot]
	}
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, sourcePath) {
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

// nlDedupe keeps one candidate for overlapping windows on one path. A bounded
// hydrated unit with multiple query signals wins over an arbitrary read;
// otherwise the wider window wins. Ordering stays at the earliest member.
func nlDedupe(list []nlCandidate) []nlCandidate {
	out := make([]nlCandidate, 0, len(list))
	for _, c := range list {
		merged := false
		for i := range out {
			if nlOverlaps(out[i], c) {
				cHydrated := (strings.HasPrefix(c.item.RefID, "grepread-hydrated-") && (c.density > 0 || c.clustered)) ||
					(strings.HasSuffix(strings.ToLower(c.item.Path), ".md") && strings.HasPrefix(c.item.RefID, "hydrated-"))
				outHydrated := (strings.HasPrefix(out[i].item.RefID, "grepread-hydrated-") && (out[i].density > 0 || out[i].clustered)) ||
					(strings.HasSuffix(strings.ToLower(out[i].item.Path), ".md") && strings.HasPrefix(out[i].item.RefID, "hydrated-"))
				if (cHydrated && !outHydrated) ||
					(cHydrated == outHydrated && len(c.lines) > len(out[i].lines)) {
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
