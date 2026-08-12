// Package taskctx implements the task_context agent tool (labs): free-text
// task in, a deterministically ranked, token-budgeted, citation-backed context
// bundle out — primary symbols, related symbols, callers/callees, the tests
// and configuration files near the task, a related-file roll-up, a
// change_risk-consistent risk level, and a recommended read order.
//
// Ranking is the repo's audited composite-score style (suggest_reviewers):
// integer-only weights, version-pinned, hash-stamped in the summary, with the
// per-signal breakdown printed in every item reason. No floats are invented;
// no LLM estimates anything.
//
// Reads are selective (CORE-02): seed resolution via resolve.Seeds, one
// bounded incoming+outgoing read per seed, one batched hydration, one compact
// BriefStats aggregate for the config section. Test discovery is hop-1 by
// design here; the deeper bounded test walk lives in symbol_context.
package taskctx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/risk"
	"github.com/samibel/graphi/engine/agenttools/shape"
	pathclass "github.com/samibel/graphi/engine/classify"
	enginecontext "github.com/samibel/graphi/engine/context"
	"github.com/samibel/graphi/engine/query"
)

const tool = "task_context"

// MethodVersion stamps the assembly logic version into the summary.
const MethodVersion = "task_context/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandPrimary   = 9
	bandRelated   = 8
	bandCallers   = 7
	bandCallees   = 6
	bandTests     = 5
	bandConfigs   = 4
	bandFiles     = 3
	bandRisk      = 2
	bandReadOrder = 1
)

// Defaults and bounds.
const (
	DefaultTokenBudget = 1200
	DefaultMaxItems    = 40
	seedLimit          = 5
	edgesPerSeed       = 64
	snippetContext     = 6
	snippetNeighbors   = 3 // top-scored neighbors that get snippet candidates
	maxSectionRows     = 8 // per-section row cap before the global item cap
)

// taskWeights is the DOCUMENTED, fixed integer weight model. Integer only (no
// floats → no non-associativity). It is hashed into the summary's weights
// stamp so any change is auditable.
type taskWeights struct {
	SeedExact      int `json:"seed_exact"`
	SeedSearchBase int `json:"seed_search_base"`
	SeedSearchStep int `json:"seed_search_step"`
	KindCalls      int `json:"kind_calls"`
	KindHierarchy  int `json:"kind_hierarchy"`
	KindReferences int `json:"kind_references"`
	KindOther      int `json:"kind_other"`
	TierConfirmed  int `json:"tier_confirmed"`
	TierDerived    int `json:"tier_derived"`
	TierHeuristic  int `json:"tier_heuristic"`
	SameFileBonus  int `json:"same_file_bonus"`
}

var defaultWeights = taskWeights{
	SeedExact:      400,
	SeedSearchBase: 300,
	SeedSearchStep: 40,
	KindCalls:      50,
	KindHierarchy:  40,
	KindReferences: 25,
	KindOther:      10,
	TierConfirmed:  3,
	TierDerived:    2,
	TierHeuristic:  1,
	SameFileBonus:  30,
}

// WeightsHash is the auditable stamp of the active weight model (sha256 of
// its canonical JSON, first 8 hex chars), printed in every summary.
func WeightsHash() string {
	b, _ := json.Marshal(defaultWeights)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:8]
}

// Params carries the task_context inputs.
type Params struct {
	// Task is the free-text task description (or an exact symbol/path).
	Task string
	// TokenBudget bounds the snippet bundle; 0 selects DefaultTokenBudget,
	// negative disables snippets.
	TokenBudget int
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
	// Deps are the shared engine services.
	Deps resolve.Deps
	// Reader supplies source bytes for snippets. Nil disables snippets.
	Reader enginecontext.Reader
}

func (p Params) tokenBudget() int {
	if p.TokenBudget == 0 {
		return DefaultTokenBudget
	}
	if p.TokenBudget < 0 {
		return 0
	}
	return p.TokenBudget
}

func (p Params) maxItems() int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}

// neighborScore accumulates one neighbor's integer signal breakdown.
type neighborScore struct {
	id       model.NodeId
	calls    int
	hier     int
	refs     int
	other    int
	sameFile int
	inCalls  bool // has an inbound "calls" edge toward a seed (caller)
	outCalls bool // is called by a seed (callee)
	evidence []string
	tally    shape.TierTally
}

func (n *neighborScore) total() int {
	s := n.calls + n.hier + n.refs + n.other + n.sameFile
	if s >= 1<<20 {
		s = 1<<20 - 1
	}
	return s
}

func (n *neighborScore) breakdown() string {
	return fmt.Sprintf("calls %d, hierarchy %d, refs %d, other %d, same-file %d",
		n.calls, n.hier, n.refs, n.other, n.sameFile)
}

// Assemble resolves the task to seeds, expands one bounded hop, ranks with the
// fixed integer weight model, and emits the sectioned C1 contract result.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if strings.TrimSpace(p.Task) == "" {
		return nil, fmt.Errorf("missing task text")
	}
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}

	seeds, method, err := resolveSeeds(ctx, p.Deps, p.Task)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return shape.Empty(tool, p.Task), nil
	}

	bounded, bok := p.Deps.Query.Reader().(graphstore.BoundedGraphLookup)
	lookup, lok := p.Deps.Query.Reader().(graphstore.GraphLookup)
	if !bok || !lok {
		return shape.Unavailable(tool), nil
	}

	seedIDs := make(map[model.NodeId]int, len(seeds)) // id → seed index
	seedFiles := map[string]struct{}{}
	for i, s := range seeds {
		seedIDs[s.ID()] = i
		if s.SourcePath() != "" {
			seedFiles[s.SourcePath()] = struct{}{}
		}
	}

	// One bounded hop per seed: inbound + outbound, all kinds.
	scores := map[model.NodeId]*neighborScore{}
	tally := shape.TierTally{}
	truncated := false
	fanIn := 0
	var inboundNeighborIDs []model.NodeId
	for _, s := range seeds {
		in, trunc, err := bounded.IncomingBounded(ctx, s.ID(), edgesPerSeed)
		if err != nil {
			return nil, err
		}
		truncated = truncated || trunc
		out, trunc2, err := bounded.OutgoingBounded(ctx, s.ID(), edgesPerSeed)
		if err != nil {
			return nil, err
		}
		truncated = truncated || trunc2
		edges := append(append([]model.Edge{}, in...), out...)
		sort.Slice(edges, func(i, j int) bool { return edges[i].ID() < edges[j].ID() })
		for _, e := range edges {
			inbound := e.To() == s.ID()
			other := e.From()
			if !inbound {
				other = e.To()
			}
			if _, isSeed := seedIDs[other]; isSeed || other == s.ID() {
				continue // seed-to-seed and self edges shape no neighbor
			}
			if inbound {
				fanIn++
				inboundNeighborIDs = append(inboundNeighborIDs, other)
			}
			ns := scores[other]
			if ns == nil {
				ns = &neighborScore{id: other, tally: shape.TierTally{}}
				scores[other] = ns
			}
			pts := kindPoints(e.Kind()) * tierMultiplier(e.Tier())
			switch classifyKind(e.Kind()) {
			case "calls":
				ns.calls += pts
				if inbound {
					ns.inCalls = true
				} else {
					ns.outCalls = true
				}
			case "hierarchy":
				ns.hier += pts
			case "references":
				ns.refs += pts
			default:
				ns.other += pts
			}
			ns.evidence = append(ns.evidence, e.Evidence()...)
			ns.tally.Count(e.Tier())
			tally.Count(e.Tier())
		}
	}

	// One batched hydration for every scored neighbor, in canonical id order.
	neighborIDs := make([]model.NodeId, 0, len(scores))
	for id := range scores {
		neighborIDs = append(neighborIDs, id)
	}
	sort.Slice(neighborIDs, func(i, j int) bool { return neighborIDs[i] < neighborIDs[j] })
	hydrated, err := lookup.NodesByID(ctx, neighborIDs)
	if err != nil {
		return nil, err
	}
	nodeByID := make(map[model.NodeId]model.Node, len(hydrated))
	for _, n := range hydrated {
		nodeByID[n.ID()] = n
		if ns := scores[n.ID()]; ns != nil && n.SourcePath() != "" {
			if _, same := seedFiles[n.SourcePath()]; same {
				ns.sameFile = defaultWeights.SameFileBonus
			}
		}
	}

	dependentFiles := map[string]struct{}{}
	for _, id := range inboundNeighborIDs {
		n, ok := nodeByID[id]
		if !ok || n.SourcePath() == "" {
			continue
		}
		if _, own := seedFiles[n.SourcePath()]; !own {
			dependentFiles[n.SourcePath()] = struct{}{}
		}
	}

	// Rank neighbors: total desc → id asc.
	ranked := make([]*neighborScore, 0, len(scores))
	for _, id := range neighborIDs {
		ranked = append(ranked, scores[id])
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].total() != ranked[j].total() {
			return ranked[i].total() > ranked[j].total()
		}
		return ranked[i].id < ranked[j].id
	})

	ev := shape.NewEvidenceSet()
	var items []contract.Item

	// Band 9: primary symbols (the seeds).
	seedScore := func(i int) int {
		if method.Exact() {
			return defaultWeights.SeedExact
		}
		s := defaultWeights.SeedSearchBase - i*defaultWeights.SeedSearchStep
		if s < 1 {
			s = 1
		}
		return s
	}
	itemByNode := map[model.NodeId]int{} // node id → index into items (for snippet refs)
	for i, s := range seeds {
		evID := ev.Add(s.SourcePath(), s.Line(), "primary")
		itemByNode[s.ID()] = len(items)
		items = append(items, contract.Item{
			RefID:          string(s.ID()),
			Rank:           bandPrimary<<20 + seedScore(i),
			Reason:         fmt.Sprintf("primary: %s %s (%s:%d) score %d [seed %d, %s]", s.Kind(), s.QualifiedName(), s.SourcePath(), s.Line(), seedScore(i), i+1, method),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Bands 8..5: related symbols, callers, callees, tests.
	related, callers, callees, tests := 0, 0, 0, 0
	for _, ns := range ranked {
		n, ok := nodeByID[ns.id]
		if !ok || n.SourcePath() == "" {
			continue
		}
		var evIDs []string
		for _, ref := range dedupeStrings(ns.evidence) {
			evIDs = append(evIDs, ev.AddRef(ref, "relation"))
		}
		describe := func(section string) string {
			return fmt.Sprintf("%s: %s %s (%s:%d) score %d [%s]", section, n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line(), ns.total(), ns.breakdown())
		}
		if pathclass.IsTestPath(n.SourcePath()) {
			if tests < maxSectionRows {
				tests++
				items = append(items, contract.Item{RefID: string(ns.id), Rank: bandTests<<20 + ns.total(), Reason: describe("test"), EvidenceRefIDs: evIDs})
			}
			continue
		}
		if related < maxSectionRows {
			related++
			idx := len(items)
			items = append(items, contract.Item{RefID: string(ns.id), Rank: bandRelated<<20 + ns.total(), Reason: describe("related"), EvidenceRefIDs: evIDs})
			if _, seen := itemByNode[ns.id]; !seen {
				itemByNode[ns.id] = idx
			}
		}
		if ns.inCalls && callers < maxSectionRows {
			callers++
			items = append(items, contract.Item{RefID: string(ns.id), Rank: bandCallers<<20 + ns.total(), Reason: describe("caller"), EvidenceRefIDs: evIDs})
		}
		if ns.outCalls && callees < maxSectionRows {
			callees++
			items = append(items, contract.Item{RefID: string(ns.id), Rank: bandCallees<<20 + ns.total(), Reason: describe("callee"), EvidenceRefIDs: evIDs})
		}
	}

	// Band 4: configuration files near the seeds (compact aggregate; the
	// config score is the shared directory-prefix depth with any seed file).
	configs := 0
	if agg, ok := p.Deps.Query.Reader().(graphstore.BriefAggregatePort); ok {
		stats, err := agg.BriefStats(ctx, 1)
		if err != nil {
			return nil, err
		}
		type configRow struct {
			path  string
			score int
		}
		var rows []configRow
		for _, f := range stats.Files {
			if !pathclass.IsConfigPath(f.Path) {
				continue
			}
			depth := 0
			for seedFile := range seedFiles {
				if d := sharedPrefixSegments(f.Path, seedFile); d > depth {
					depth = d
				}
			}
			rows = append(rows, configRow{path: f.Path, score: depth*10 + 1})
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].score != rows[j].score {
				return rows[i].score > rows[j].score
			}
			return rows[i].path < rows[j].path
		})
		for _, row := range rows {
			if configs >= maxSectionRows {
				break
			}
			configs++
			evID := ev.Add(row.path, 0, "config")
			items = append(items, contract.Item{
				RefID:          "config:" + row.path,
				Rank:           bandConfigs<<20 + row.score,
				Reason:         fmt.Sprintf("config: %s score %d [shared-dir depth]", row.path, row.score),
				EvidenceRefIDs: []string{evID},
			})
		}
	}

	// Band 3: related-file roll-up (integer sum of neighbor scores per file).
	fileScores := map[string]int{}
	for _, ns := range ranked {
		n, ok := nodeByID[ns.id]
		if !ok || n.SourcePath() == "" {
			continue
		}
		fileScores[n.SourcePath()] += ns.total()
	}
	type fileRow struct {
		path  string
		score int
	}
	var files []fileRow
	for path, score := range fileScores {
		if score >= 1<<20 {
			score = 1<<20 - 1
		}
		files = append(files, fileRow{path, score})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].score != files[j].score {
			return files[i].score > files[j].score
		}
		return files[i].path < files[j].path
	})
	fileRows := 0
	for _, f := range files {
		if fileRows >= maxSectionRows {
			break
		}
		fileRows++
		evID := ev.Add(f.path, 0, "file")
		items = append(items, contract.Item{
			RefID:          "file:" + f.path,
			Rank:           bandFiles<<20 + f.score,
			Reason:         fmt.Sprintf("file: %s score %d [neighbor roll-up]", f.path, f.score),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 2: risk over the seeds' hop-1 evidence (single-source thresholds).
	level := risk.LevelFor(fanIn, len(dependentFiles))
	if len(items) > 0 {
		items = append(items, contract.Item{
			RefID:          "risk",
			Rank:           bandRisk << 20,
			Reason:         fmt.Sprintf("risk: %s — fan-in %d, %d dependent file(s)", level, fanIn, len(dependentFiles)),
			EvidenceRefIDs: []string{items[0].EvidenceRefIDs[0]},
		})
	}

	// Band 1: recommended read order — seed files first (by seed order), then
	// the top related files not already listed.
	readOrder := make([]string, 0, len(seeds)+3)
	seenRead := map[string]struct{}{}
	for _, s := range seeds {
		if s.SourcePath() == "" {
			continue
		}
		if _, dup := seenRead[s.SourcePath()]; dup {
			continue
		}
		seenRead[s.SourcePath()] = struct{}{}
		readOrder = append(readOrder, s.SourcePath())
	}
	for _, f := range files {
		if len(readOrder) >= len(seeds)+3 {
			break
		}
		if _, dup := seenRead[f.path]; dup {
			continue
		}
		seenRead[f.path] = struct{}{}
		readOrder = append(readOrder, f.path)
	}
	for i, path := range readOrder {
		evID := ev.Add(path, 0, "read")
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("read-%d", i+1),
			Rank:           bandReadOrder<<20 + (len(readOrder) - i),
			Reason:         fmt.Sprintf("read %d: %s", i+1, path),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Token-budgeted snippets: seed definitions plus the top-scored neighbors.
	budget := p.tokenBudget()
	snippetSummary := "snippets disabled"
	snippetHint := ""
	if budget > 0 && p.Reader != nil {
		var cands []enginecontext.Candidate
		candNode := []model.NodeId{}
		add := func(n model.Node) {
			cands = append(cands, enginecontext.Candidate{
				Path:      n.SourcePath(),
				StartLine: n.Line(),
				EndLine:   n.Line(),
				Rank:      float64(len(cands)),
				Symbol:    n.QualifiedName(),
				Kind:      n.Kind(),
			})
			candNode = append(candNode, n.ID())
		}
		for _, s := range seeds {
			add(s)
		}
		taken := 0
		for _, ns := range ranked {
			if taken >= snippetNeighbors {
				break
			}
			if n, ok := nodeByID[ns.id]; ok && n.SourcePath() != "" {
				add(n)
				taken++
			}
		}
		total := len(cands)
		readable := enginecontext.FilterReadable(p.Reader, cands)
		if len(readable) < total {
			snippetHint = "some sources were unreadable from this working directory; run from the repository root to include every snippet"
		}
		bundle, err := enginecontext.Assemble(p.Task, readable, enginecontext.Options{Budget: budget, ContextLines: snippetContext}, p.Reader)
		if err != nil {
			return nil, err
		}
		for _, snip := range bundle.Snippets {
			span := fmt.Sprintf("%d-%d", snip.Citation.StartLine, snip.Citation.EndLine)
			evID := ev.AddSnippet(snip.Citation.Path, snip.Citation.StartLine, "snippet", span, snip.Text)
			idx := int(snip.Rank)
			if idx >= 0 && idx < len(candNode) {
				if itemIdx, ok := itemByNode[candNode[idx]]; ok {
					items[itemIdx].EvidenceRefIDs = append(items[itemIdx].EvidenceRefIDs, evID)
				}
			}
		}
		snippetSummary = fmt.Sprintf("%d/%d snippet tokens", bundle.Tokens, budget)
		if len(bundle.Snippets) < len(readable) && snippetHint == "" {
			snippetHint = fmt.Sprintf("raise token_budget (>%d) to include %d more snippet(s)", budget, len(readable)-len(bundle.Snippets))
		}
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("task_context: %d seed(s) for %q — %d related, %d callers, %d callees, %d tests, %d configs, %d files, risk %s (%s; weights %s; %s)",
			len(seeds), p.Task, related, callers, callees, tests, configs, fileRows, level, MethodVersion, WeightsHash(), snippetSummary),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("heuristic", "seeds_only"),
	}
	out, err := shape.Finish(r, p.maxItems())
	if err != nil {
		return nil, err
	}
	if truncated {
		out.Limits.Truncated = true
		if out.Outcome == contract.OutcomeFound {
			out.Outcome = contract.OutcomePartial
		}
	}
	if out.Limits.Next == "" && snippetHint != "" {
		out.Limits.Next = snippetHint
	}
	return out, nil
}

// resolveSeeds resolves the task text into seed nodes. Exact forms win; then
// the quoted-FTS search; then the deterministic token fallback (a multi-word
// task must not stall on a zero-hit quoted expression). Ambiguous exact-name
// matches all become seeds — a free-text tool must not stall on ambiguity.
func resolveSeeds(ctx context.Context, deps resolve.Deps, task string) ([]model.Node, resolve.Method, error) {
	res, err := resolve.Seeds(ctx, deps, task, seedLimit)
	if err != nil {
		return nil, "", err
	}
	if res.Ambiguous() {
		nodes := make([]model.Node, 0, len(res.Candidates))
		for _, c := range res.Candidates {
			if len(nodes) >= seedLimit {
				break
			}
			nodes = append(nodes, c.Node)
		}
		return nodes, res.Method, nil
	}
	if res.Resolved() {
		nodes := res.Nodes
		if len(nodes) > seedLimit {
			nodes = nodes[:seedLimit]
		}
		return nodes, res.Method, nil
	}
	nodes, err := tokenFallbackSeeds(ctx, deps, task)
	if err != nil {
		return nil, "", err
	}
	return nodes, resolve.MethodSearch, nil
}

// fallbackStopwords are common function words dropped by the token fallback.
var fallbackStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "this": true,
	"that": true, "are": true, "was": true, "not": true, "you": true,
	"add": true, "fix": true, "make": true, "use": true, "new": true,
	"into": true, "from": true, "when": true, "then": true, "should": true,
}

// tokenFallbackSeeds splits the task on whitespace, drops stopwords and short
// tokens, searches each token in order, and merges the first distinct hits.
// Purely deterministic: token order, then match order, first seedLimit wins.
func tokenFallbackSeeds(ctx context.Context, deps resolve.Deps, task string) ([]model.Node, error) {
	if deps.Search == nil {
		return nil, nil
	}
	lookup, ok := deps.Query.Reader().(graphstore.GraphLookup)
	if !ok {
		return nil, nil
	}
	seen := map[model.NodeId]struct{}{}
	var ids []model.NodeId
	tokens := strings.Fields(strings.ToLower(task))
	searched := 0
	for _, tok := range tokens {
		tok = strings.Trim(tok, ".,;:!?()[]{}\"'`")
		if len(tok) < 3 || fallbackStopwords[tok] {
			continue
		}
		if searched >= 6 {
			break
		}
		searched++
		resp, err := deps.Search.Search(ctx, tok, seedLimit)
		if err != nil {
			return nil, err
		}
		for _, m := range resp.Matches {
			id := model.NodeId(m.NodeID)
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if len(ids) > seedLimit {
		ids = ids[:seedLimit]
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return lookup.NodesByID(ctx, ids)
}

// classifyKind buckets an edge kind into the weight model's vocabulary.
func classifyKind(kind string) string {
	switch kind {
	case query.EdgeKindCalls:
		return "calls"
	case query.EdgeKindImplements, query.EdgeKindInherits, query.EdgeKindOverrides:
		return "hierarchy"
	case query.EdgeKindReferences:
		return "references"
	default:
		return "other"
	}
}

func kindPoints(kind string) int {
	switch classifyKind(kind) {
	case "calls":
		return defaultWeights.KindCalls
	case "hierarchy":
		return defaultWeights.KindHierarchy
	case "references":
		return defaultWeights.KindReferences
	default:
		return defaultWeights.KindOther
	}
}

func tierMultiplier(tier model.ConfidenceTier) int {
	switch tier {
	case model.TierConfirmed:
		return defaultWeights.TierConfirmed
	case model.TierDerived:
		return defaultWeights.TierDerived
	default:
		return defaultWeights.TierHeuristic
	}
}

// sharedPrefixSegments counts the leading equal path segments of a and b.
func sharedPrefixSegments(a, b string) int {
	as := strings.Split(a, "/")
	bs := strings.Split(b, "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// dedupeStrings returns the distinct entries of in, preserving first-seen order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
