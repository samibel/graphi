// SW-264 AC-2 / AC-3 / AC-4 / AC-8 — task_context/2.
//
// The /2 path uses the post-ingest retrieval instance (resolve.Deps.Retrieval)
// as its seed source instead of resolve.Seeds, then runs the same bounded hop
// the v1 path runs. Every seed carries claim_type="source_match" (the span
// came from a retrieval row); every neighbour reached via an edge carries
// claim_type="graph_relation" with the edge's provenance tier on EdgeTier.
// Bundle ordering matches v1's bands (answer span → definition → callers/
// callees → tests/config) and the bundle bound holds under a 1200-token
// budget exactly as v1's does.
//
// AC-8 graceful fallback: with no embedder configured (Deps.Retrieval == nil)
// or a non-ready generation, /2 falls back to the v1 lexical seeding path
// and stamps `degradation: <state>` on the summary, no error. The shipped
// default remains lexical-only and the fallback's audit trail (the typed
// degradation state) makes the v2 path observably different from v1.
//
// Layering: taskctx imports engine/agenttools/resolve (its own dep) but does
// NOT import engine/agenttools/hybridsearch, so neither agent tool imports
// the other's package (AC-5). Both reach the retrieval instance through the
// single Composition.Client() wiring.
package taskctx

import (
	"context"
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
)

// MethodVersionV2 is the audit stamp task_context/2 prints in every summary,
// distinct from the /1 MethodVersion so a reader of the bytes can tell which
// path produced the result.
const MethodVersionV2 = "task_context/2"

// retrievalSeedLimit is the cap on retrieval seeds AssembleV2 promotes to
// the primary band. It mirrors seedLimit (5) for the v1 lexical path so
// the bundle widths are comparable across versions.
const retrievalSeedLimit = 5

// Retrieve is the narrow interface the v2 path reads from Deps.Retrieval.
// The retrieval module's own Retriever (resolve.Retriever) already exposes
// this method, so the v2 implementation uses it directly via Deps.
//
// It is re-declared here so the function signature documents what
// AssembleV2 consumes; the resolve package's Retriever satisfies it.
type Retrieve interface {
	Retrieve(ctx context.Context, req resolve.RetrieverRequest) (resolve.RetrieverResult, error)
}

// AssembleV2 is the AC-2 entry point for /2. Same Params as Assemble; the
// seed path is the post-ingest retrieval instance when Deps.Retrieval is
// wired, the v1 lexical seeding path otherwise. AC-8 covers both branches.
//
// The retrieval module is composed once at Composition.Client() and shared
// with engine/agenttools/hybridsearch's /2 path; AC-5's pointer-equality
// test (TestTaskContextV2_SharesRetrievalPointer) makes the sharing
// mechanical.
func AssembleV2(ctx context.Context, p Params) (*contract.Result, error) {
	if strings.TrimSpace(p.Task) == "" {
		return nil, fmt.Errorf("missing task text")
	}
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}

	bounded, bok := p.Deps.Query.Reader().(graphstore.BoundedGraphLookup)
	lookup, lok := p.Deps.Query.Reader().(graphstore.GraphLookup)
	if !bok || !lok {
		return shape.Unavailable(tool), nil
	}

	seeds, method, degradation, retrievalSummary, retrievalRows, err := resolveSeedsV2(ctx, p.Deps, p.Task)
	if err != nil {
		return nil, err
	}
	if len(seeds) == 0 {
		return shape.Empty(tool, p.Task), nil
	}

	// One bounded hop per seed: inbound + outbound, all kinds.
	scores := map[model.NodeId]*neighborScore{}
	tally := shape.TierTally{}
	truncated := false
	fanIn := 0
	var inboundNeighborIDs []model.NodeId
	seedIDs := make(map[model.NodeId]int, len(seeds))
	seedFiles := map[string]struct{}{}
	for i, s := range seeds {
		seedIDs[s.ID()] = i
		if s.SourcePath() != "" {
			seedFiles[s.SourcePath()] = struct{}{}
		}
	}
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
				continue
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

	// Band 9: primary symbols (the seeds) — claim_type=source_match (AC-3).
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
	itemByNode := map[model.NodeId]int{}
	for i, s := range seeds {
		// Span preservation: when the seed came from a retrieval row, the
		// row's "start-end" span travels through to the evidence so a reader
		// of the bytes can tell the cited range exactly. /1 stamped only
		// Line (a point match); /2 stamps Span when the seed had one.
		// retrievalRows is a per-call local returned by resolveSeedsV2; it
		// does NOT go through any package-level mutable state, so concurrent
		// AssembleV2 callers cannot read each other's rows (see
		// v2_race_test.go for the proof).
		var span string
		var line int
		if i < len(retrievalRows) {
			row := retrievalRows[i]
			span = row.Span
			line = parseSpanLine(span, s.Line())
		}
		if line == 0 {
			line = s.Line()
		}
		var evID string
		if span != "" {
			evID = ev.AddSourceMatchWithSpan(s.SourcePath(), line, "primary", span)
		} else {
			evID = ev.AddSourceMatch(s.SourcePath(), line, "primary")
		}
		itemByNode[s.ID()] = len(items)
		items = append(items, contract.Item{
			RefID:          string(s.ID()),
			Rank:           bandPrimary<<20 + seedScore(i),
			Reason:         fmt.Sprintf("primary: %s %s (%s:%d) score %d [seed %d, %s]", s.Kind(), s.QualifiedName(), s.SourcePath(), s.Line(), seedScore(i), i+1, method),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Bands 8..5: related, callers, callees, tests — claim_type=graph_relation (AC-3).
	related, callers, callees, tests := 0, 0, 0, 0
	for _, ns := range ranked {
		n, ok := nodeByID[ns.id]
		if !ok || n.SourcePath() == "" {
			continue
		}
		// graph_relation evidence: one entry per unique (path, line, role),
		// carrying the edge's provenance tier.
		var evIDs []string
		for _, ref := range dedupeStrings(ns.evidence) {
			path, line := shape.SplitEvidenceRef(ref)
			evIDs = append(evIDs, ev.AddGraphRelation(path, line, "relation", dominantTier(ns.tally)))
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

	// Band 4: configuration files near the seeds.
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
			evID := ev.AddSourceMatch(row.path, 0, "config")
			items = append(items, contract.Item{
				RefID:          "config:" + row.path,
				Rank:           bandConfigs<<20 + row.score,
				Reason:         fmt.Sprintf("config: %s score %d [shared-dir depth]", row.path, row.score),
				EvidenceRefIDs: []string{evID},
			})
		}
	}

	// Band 3: related-file roll-up.
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
		evID := ev.AddSourceMatch(f.path, 0, "file")
		items = append(items, contract.Item{
			RefID:          "file:" + f.path,
			Rank:           bandFiles<<20 + f.score,
			Reason:         fmt.Sprintf("file: %s score %d [neighbor roll-up]", f.path, f.score),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 2: risk.
	level := risk.LevelFor(fanIn, len(dependentFiles))
	if len(items) > 0 {
		items = append(items, contract.Item{
			RefID:          "risk",
			Rank:           bandRisk << 20,
			Reason:         fmt.Sprintf("risk: %s — fan-in %d, %d dependent file(s)", level, fanIn, len(dependentFiles)),
			EvidenceRefIDs: []string{items[0].EvidenceRefIDs[0]},
		})
	}

	// Band 1: read order.
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
		evID := ev.AddSourceMatch(path, 0, "read")
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("read-%d", i+1),
			Rank:           bandReadOrder<<20 + (len(readOrder) - i),
			Reason:         fmt.Sprintf("read %d: %s", i+1, path),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Token-budgeted snippets. /2 stamps text_hash on each snippet (AC-3).
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
			evID := ev.AddSnippetWithHash(snip.Citation.Path, snip.Citation.StartLine, "snippet", span, snip.Text)
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

	summary := buildV2Summary(len(seeds), p.Task, related, callers, callees, tests, configs, fileRows, string(level), snippetSummary, degradation, retrievalSummary)
	r := &contract.Result{
		Outcome:    contract.OutcomeFound,
		Summary:    summary,
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

// resolveSeedsV2 is the AC-2 / AC-8 seed resolver.
//
//   - Deps.Retrieval nil: fall back to the v1 lexical seeding path (resolve.Seeds
//   - the token fallback) and stamp degradation="lexical_only".
//   - Retrieval reports a non-ready state: same lexical fallback, with the typed
//     degradation stamp verbatim.
//   - Retrieval reports "ready": seed from the top retrieval rows. Each row's
//     NodeID is matched against the graph; rows whose NodeID is not resolvable
//     (e.g. an external document with no source path) are dropped. The seed
//     resolution method is reported as MethodSearch so the per-item reason
//     names the seed provenance honestly.
//
// The function always returns at most retrievalSeedLimit seeds; the v1 seed
// resolution caps at seedLimit (5), so a v2 reader can compare the bundle
// widths apples-to-apples across versions.
func resolveSeedsV2(ctx context.Context, deps resolve.Deps, task string) ([]model.Node, resolve.Method, string, resolve.RetrieverSummary, []resolve.RetrieverRow, error) {
	if deps.Retrieval == nil {
		nodes, method, err := resolveSeeds(ctx, deps, task)
		return nodes, method, "lexical_only", resolve.RetrieverSummary{}, nil, err
	}
	res, err := deps.Retrieval.Retrieve(ctx, resolve.RetrieverRequest{Query: task, Limit: retrievalSeedLimit})
	if err != nil {
		// An infrastructure error on the retrieval path is the same fail-soft
		// posture as AC-7 in SW-263: fall back to lexical seeding, no error
		// surfaces, the reader sees `degradation: lexical_only` in the
		// summary as the audit trail.
		nodes, method, lerr := resolveSeeds(ctx, deps, task)
		return nodes, method, "lexical_only", resolve.RetrieverSummary{}, nil, lerr
	}
	if res.Degradation != "ready" {
		nodes, method, lerr := resolveSeeds(ctx, deps, task)
		return nodes, method, res.Degradation, res.Summary, res.Rows, lerr
	}
	// Hydrate the top rows from the graph.
	lookup, ok := deps.Query.Reader().(graphstore.GraphLookup)
	if !ok {
		nodes, method, lerr := resolveSeeds(ctx, deps, task)
		return nodes, method, "lexical_only", res.Summary, res.Rows, lerr
	}
	ids := make([]model.NodeId, 0, len(res.Rows))
	for _, r := range res.Rows {
		ids = append(ids, model.NodeId(r.NodeID))
	}
	hydrated, err := lookup.NodesByID(ctx, ids)
	if err != nil {
		return nil, "", "ready", res.Summary, res.Rows, err
	}
	// Keep the retrieval order (Final desc) — but cap at retrievalSeedLimit
	// and dedupe on canonical node id, matching the v1 lexical path's
	// first-N-wins behaviour. The matching row is recorded in
	// rowBySeedID so the per-seed span survives to the evidence step.
	rowBySeedID := map[model.NodeId]resolve.RetrieverRow{}
	for _, r := range res.Rows {
		rowBySeedID[model.NodeId(r.NodeID)] = r
	}
	seen := map[model.NodeId]bool{}
	nodes := make([]model.Node, 0, len(hydrated))
	rowOrder := make([]resolve.RetrieverRow, 0, len(hydrated))
	for _, n := range hydrated {
		if seen[n.ID()] {
			continue
		}
		seen[n.ID()] = true
		nodes = append(nodes, n)
		if r, ok := rowBySeedID[n.ID()]; ok {
			rowOrder = append(rowOrder, r)
		} else {
			rowOrder = append(rowOrder, resolve.RetrieverRow{})
		}
		if len(nodes) >= retrievalSeedLimit {
			break
		}
	}
	return nodes, resolve.MethodSearch, "ready", res.Summary, rowOrder, nil
}

// parseSpanLine returns the 1-based start line of a "start-end" span, falling
// back to the supplied default when the span is empty or malformed. It is the
// inverse of retrieval.spanFromLine for the seed evidence step.
func parseSpanLine(span string, fallback int) int {
	if span == "" {
		return fallback
	}
	for i := 0; i < len(span); i++ {
		if span[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				c := span[j]
				if c < '0' || c > '9' {
					return fallback
				}
				n = n*10 + int(c-'0')
			}
			if n > 0 {
				return n
			}
			return fallback
		}
	}
	return fallback
}

// dominantTier returns the most-frequent tier in a tally, breaking ties on
// ascending label order. Used to stamp the EdgeTier field on graph_relation
// evidence: when a neighbour is reached via several edges of mixed tiers,
// the dominant tier names what most of the evidence said.
func dominantTier(t shape.TierTally) string {
	if len(t) == 0 {
		return ""
	}
	bestLabel := ""
	bestCount := -1.0
	for label, count := range t {
		if count > bestCount || (count == bestCount && label < bestLabel) {
			bestLabel = label
			bestCount = count
		}
	}
	return bestLabel
}

// buildV2Summary assembles the /2 summary. The /1 summary template is
// preserved (so existing summary parsers that key on "task_context:" still
// find the call), with the /2 audit stamp appended and the AC-8 degradation
// trailer when the fallback ran.
func buildV2Summary(seeds int, task string, related, callers, callees, tests, configs, files int, riskLevel, snippetSummary, degradation string, retrieval resolve.RetrieverSummary) string {
	versionStamp := retrieval.RetrievalVersion
	if versionStamp == "" {
		versionStamp = "retrieval/0"
	}
	base := fmt.Sprintf("task_context/2: %d seed(s) for %q — %d related, %d callers, %d callees, %d tests, %d configs, %d files, risk %s (%s; %s; weights %s; %s",
		seeds, task, related, callers, callees, tests, configs, files, riskLevel, MethodVersionV2, versionStamp, retrieval.WeightsHash, snippetSummary)
	if retrieval.Strategy != "" {
		base += "; strategy " + retrieval.Strategy
	}
	if degradation != "" && degradation != "ready" {
		base += "; degradation: " + degradation
	}
	if degradation == "ready" {
		base += "; degradation: ready"
	}
	return base + ")"
}

// Compile-time check: the seed resolver must reach both the lexical and the
// retrieval paths through the same Deps, so a single composition root can
// hand either surface a complete Deps value. The retrieve interface is
// satisfied by resolve.Retriever; the unused declaration is the type guard.
var _ Retrieve = (resolve.Retriever)(nil)
