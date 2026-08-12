// Package overview implements the repo_overview agent tool (labs): the
// one-call "what is this repository?" summary — totals, directory tree,
// language mix, entry-point candidates, high-centrality symbols, test and
// generated/vendored areas, external boundaries, optional dependency
// communities, and suggested next calls.
//
// The default call reads ONLY the compact aggregates (BriefStats +
// TrustStats) plus at most two selective lookups — never a catalog scan
// (CORE-02). The Communities option is the documented opt-in full-graph pass
// (Louvain over the whole edge set); leaving it off keeps the call cheap on
// any repository size.
package overview

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/community"
	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	pathclass "github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/diagnostic"
)

const tool = "repo_overview"

// MethodVersion stamps the assembly logic version into the summary.
const MethodVersion = "repo_overview/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandIdentity    = 10
	bandTree        = 9
	bandLanguages   = 8
	bandEntrypoints = 7
	bandCentral     = 6
	bandTests       = 5
	bandGenerated   = 4
	bandBoundaries  = 3
	bandCommunities = 2
	bandNext        = 1
)

// Defaults and section caps.
const (
	DefaultMaxItems = 60
	topSymbols      = 10
	topBoundaries   = 5
	treeRows        = 15
	languageRows    = 10
	testRows        = 8
	generatedRows   = 5
	communityRows   = 8
)

// Params carries the repo_overview inputs.
type Params struct {
	// Deps are the shared engine services.
	Deps resolve.Deps
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
	// Communities opts into the documented full-graph community pass.
	Communities bool
}

func (p Params) maxItems() int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}

// clampScore keeps a section score inside its band.
func clampScore(s int) int {
	if s >= 1<<20 {
		return 1<<20 - 1
	}
	if s < 0 {
		return 0
	}
	return s
}

// Assemble builds the one-call repository summary from the compact aggregates.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	reader := p.Deps.Query.Reader()
	agg, aok := reader.(graphstore.BriefAggregatePort)
	trustAgg, tok := reader.(graphstore.TrustAggregatePort)
	if !aok || !tok {
		return shape.Unavailable(tool), nil
	}

	stats, err := agg.BriefStats(ctx, topSymbols)
	if err != nil {
		return nil, err
	}
	trust, err := trustAgg.TrustStats(ctx, topBoundaries)
	if err != nil {
		return nil, err
	}
	if stats.TotalNodes == 0 {
		return &contract.Result{
			Outcome: contract.OutcomeEmpty,
			Summary: tool + ": the graph is empty — run `graphi index` (or `graphi sync`) first",
			Confidence: contract.Confidence{
				Distribution: map[string]float64{"unknown": 1},
				Top:          "unknown",
				Method:       "empty_graph",
			},
		}, nil
	}

	ev := shape.NewEvidenceSet()
	var items []contract.Item

	// Band 10: identity/totals.
	items = append(items, contract.Item{
		RefID: "identity",
		Rank:  bandIdentity << 20,
		Reason: fmt.Sprintf("repository: %d nodes, %d edges across %d files [edges: confirmed %d, derived %d, heuristic %d; external nodes %d]",
			stats.TotalNodes, stats.TotalEdges, len(stats.Files),
			stats.TierCounts[model.TierConfirmed], stats.TierCounts[model.TierDerived], stats.TierCounts[model.TierHeuristic],
			trust.ExternalNodes),
	})

	// Band 9: directory tree (two-segment roll-up by symbol count).
	type dirRow struct {
		dir     string
		files   int
		symbols int
		sample  string
	}
	dirs := map[string]*dirRow{}
	for _, f := range stats.Files {
		key := dirKey(f.Path)
		row := dirs[key]
		if row == nil {
			row = &dirRow{dir: key, sample: f.Path}
			dirs[key] = row
		}
		row.files++
		row.symbols += f.SymbolCount
	}
	dirRows := make([]*dirRow, 0, len(dirs))
	for _, row := range dirs {
		dirRows = append(dirRows, row)
	}
	sort.Slice(dirRows, func(i, j int) bool {
		if dirRows[i].symbols != dirRows[j].symbols {
			return dirRows[i].symbols > dirRows[j].symbols
		}
		return dirRows[i].dir < dirRows[j].dir
	})
	for i, row := range dirRows {
		if i >= treeRows {
			break
		}
		evID := ev.Add(row.sample, 0, "tree")
		items = append(items, contract.Item{
			RefID:          "dir:" + row.dir,
			Rank:           bandTree<<20 + clampScore(row.symbols),
			Reason:         fmt.Sprintf("dir: %s — %d file(s), %d symbol(s)", row.dir, row.files, row.symbols),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 8: language mix by symbol count.
	type langRow struct {
		lang    string
		files   int
		symbols int
		sample  string
	}
	langs := map[string]*langRow{}
	for _, f := range stats.Files {
		lang := pathclass.Language(f.Path)
		if lang == "" {
			lang = "other"
		}
		row := langs[lang]
		if row == nil {
			row = &langRow{lang: lang, sample: f.Path}
			langs[lang] = row
		}
		row.files++
		row.symbols += f.SymbolCount
	}
	langRows := make([]*langRow, 0, len(langs))
	for _, row := range langs {
		langRows = append(langRows, row)
	}
	sort.Slice(langRows, func(i, j int) bool {
		if langRows[i].symbols != langRows[j].symbols {
			return langRows[i].symbols > langRows[j].symbols
		}
		return langRows[i].lang < langRows[j].lang
	})
	topLanguage := "unknown"
	if len(langRows) > 0 {
		topLanguage = langRows[0].lang
	}
	for i, row := range langRows {
		if i >= languageRows {
			break
		}
		evID := ev.Add(row.sample, 0, "language")
		items = append(items, contract.Item{
			RefID:          "lang:" + row.lang,
			Rank:           bandLanguages<<20 + clampScore(row.symbols),
			Reason:         fmt.Sprintf("language: %s — %d symbol(s) across %d file(s)", row.lang, row.symbols, row.files),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 7: entry-point candidates — the main.main probe, meta-flagged
	// high-centrality symbols, and the cmd/-tree path heuristic. Heuristic
	// tier by design; the reasons say which signal fired.
	entrypoints := 0
	if symbols, ok := reader.(graphstore.SymbolLookupPort); ok {
		mains, err := symbols.QualifiedName(ctx, "main.main")
		if err != nil {
			return nil, err
		}
		for _, n := range mains {
			entrypoints++
			evID := ev.Add(n.SourcePath(), n.Line(), "entrypoint")
			items = append(items, contract.Item{
				RefID:          string(n.ID()),
				Rank:           bandEntrypoints<<20 + 1000,
				Reason:         fmt.Sprintf("entrypoint: %s %s (%s:%d) [go-main]", n.Kind(), n.QualifiedName(), n.SourcePath(), n.Line()),
				EvidenceRefIDs: []string{evID},
			})
		}
	}
	for _, s := range stats.TopInbound {
		if !diagnostic.IsEntryPoint(s.Node) {
			continue
		}
		entrypoints++
		evID := ev.Add(s.Node.SourcePath(), s.Node.Line(), "entrypoint")
		items = append(items, contract.Item{
			RefID:          string(s.Node.ID()),
			Rank:           bandEntrypoints<<20 + clampScore(s.InboundEdges),
			Reason:         fmt.Sprintf("entrypoint: %s %s (%s:%d) [meta]", s.Node.Kind(), s.Node.QualifiedName(), s.Node.SourcePath(), s.Node.Line()),
			EvidenceRefIDs: []string{evID},
		})
	}
	for _, row := range dirRows {
		first := strings.SplitN(row.dir, "/", 2)[0]
		if first != "cmd" {
			continue
		}
		entrypoints++
		evID := ev.Add(row.sample, 0, "entrypoint")
		items = append(items, contract.Item{
			RefID:          "entry:" + row.dir,
			Rank:           bandEntrypoints<<20 + clampScore(row.symbols/10),
			Reason:         fmt.Sprintf("entrypoint: %s — command tree [path heuristic]", row.dir),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 6: high-centrality symbols (inbound degree from the aggregate).
	for _, s := range stats.TopInbound {
		evID := ev.Add(s.Node.SourcePath(), s.Node.Line(), "central")
		items = append(items, contract.Item{
			RefID:          string(s.Node.ID()),
			Rank:           bandCentral<<20 + clampScore(s.InboundEdges),
			Reason:         fmt.Sprintf("central: %s %s (%s:%d) — %d inbound edge(s)", s.Node.Kind(), s.Node.QualifiedName(), s.Node.SourcePath(), s.Node.Line(), s.InboundEdges),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Bands 5 and 4: test and generated/vendored areas (directory roll-ups).
	testFiles := emitAreaRows(&items, ev, stats.Files, pathclass.IsTestPath, "tests", bandTests, testRows)
	emitAreaRows(&items, ev, stats.Files, pathclass.IsGeneratedPath, "generated", bandGenerated, generatedRows)

	// Band 3: external boundaries from the trust aggregate.
	for _, b := range trust.TopBoundaries {
		items = append(items, contract.Item{
			RefID:  "boundary:" + b.QualifiedName,
			Rank:   bandBoundaries<<20 + clampScore(b.IncidentEdges),
			Reason: fmt.Sprintf("boundary: %s — %d incident edge(s) [external]", b.QualifiedName, b.IncidentEdges),
		})
	}

	// Band 2: dependency communities — the documented opt-in full-graph pass.
	if p.Communities {
		if err := emitCommunities(ctx, reader, ev, &items); err != nil {
			return nil, err
		}
	}

	// Band 1: suggested next calls, concrete where possible.
	next := []string{
		"task-context \"<your task>\" — ranked, token-budgeted task context",
		"search <term> — lexical/symbol discovery",
	}
	if len(stats.TopInbound) > 0 {
		next = append([]string{fmt.Sprintf("symbol-context %s — unified view of the most central symbol", stats.TopInbound[0].Node.QualifiedName())}, next...)
	}
	for i, n := range next {
		items = append(items, contract.Item{
			RefID:  fmt.Sprintf("next-%d", i+1),
			Rank:   bandNext<<20 + (len(next) - i),
			Reason: "next: graphi " + n,
		})
	}

	// Confidence from the aggregate tier counts.
	tally := shape.TierTally{}
	for tier, n := range stats.TierCounts {
		tally[string(tier)] = float64(n)
	}

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("repo_overview: %d nodes, %d edges, %d files — top language %s, %d entrypoint candidate(s), %d test file(s), %d external boundary(ies) (%s)",
			stats.TotalNodes, stats.TotalEdges, len(stats.Files), topLanguage, entrypoints, testFiles, len(trust.TopBoundaries), MethodVersion),
		Items:      items,
		Evidence:   ev.List(),
		Confidence: tally.Confidence("unknown", "aggregate"),
	}
	return shape.Finish(r, p.maxItems())
}

// dirKey rolls a file path up to its leading one or two segments.
func dirKey(path string) string {
	segs := strings.Split(path, "/")
	switch {
	case len(segs) <= 1:
		return "."
	case len(segs) == 2:
		return segs[0]
	default:
		return segs[0] + "/" + segs[1]
	}
}

// emitAreaRows rolls the matching files up by directory and emits up to
// maxRows items into the given band. It returns the total matching file count.
func emitAreaRows(items *[]contract.Item, ev *shape.EvidenceSet, files []graphstore.BriefFileStats, match func(string) bool, label string, band, maxRows int) int {
	type areaRow struct {
		dir    string
		files  int
		sample string
	}
	areas := map[string]*areaRow{}
	total := 0
	for _, f := range files {
		if !match(f.Path) {
			continue
		}
		total++
		key := dirKey(f.Path)
		row := areas[key]
		if row == nil {
			row = &areaRow{dir: key, sample: f.Path}
			areas[key] = row
		}
		row.files++
	}
	rows := make([]*areaRow, 0, len(areas))
	for _, row := range areas {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].files != rows[j].files {
			return rows[i].files > rows[j].files
		}
		return rows[i].dir < rows[j].dir
	})
	for i, row := range rows {
		if i >= maxRows {
			break
		}
		evID := ev.Add(row.sample, 0, label)
		*items = append(*items, contract.Item{
			RefID:          label + ":" + row.dir,
			Rank:           band<<20 + clampScore(row.files),
			Reason:         fmt.Sprintf("%s: %s — %d file(s)", label, row.dir, row.files),
			EvidenceRefIDs: []string{evID},
		})
	}
	return total
}

// emitCommunities runs the opt-in Louvain pass over the full graph and emits
// the largest communities with representative member names.
func emitCommunities(ctx context.Context, reader interface {
	Nodes(ctx context.Context, q graphstore.Query) ([]model.Node, error)
	Edges(ctx context.Context, q graphstore.Query) ([]model.Edge, error)
}, ev *shape.EvidenceSet, items *[]contract.Item) error {
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return err
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return err
	}
	byID := make(map[model.NodeId]model.Node, len(nodes))
	ids := make([]model.NodeId, 0, len(nodes))
	for _, n := range nodes {
		byID[n.ID()] = n
		ids = append(ids, n.ID())
	}
	res := community.Detect(ids, edges)
	comms := res.Communities
	sort.Slice(comms, func(i, j int) bool {
		if len(comms[i].Members) != len(comms[j].Members) {
			return len(comms[i].Members) > len(comms[j].Members)
		}
		return comms[i].ID < comms[j].ID
	})
	for i, c := range comms {
		if i >= communityRows {
			break
		}
		members := append([]model.NodeId{}, c.Members...)
		sort.Slice(members, func(a, b int) bool { return members[a] < members[b] })
		names := make([]string, 0, 3)
		sample := ""
		for _, m := range members {
			n, ok := byID[m]
			if !ok || n.QualifiedName() == "" {
				continue
			}
			if sample == "" && n.SourcePath() != "" {
				sample = n.SourcePath()
			}
			if len(names) < 3 {
				names = append(names, n.QualifiedName())
			}
		}
		var evIDs []string
		if sample != "" {
			evIDs = []string{ev.Add(sample, 0, "community")}
		}
		*items = append(*items, contract.Item{
			RefID:          fmt.Sprintf("community:%d", c.ID),
			Rank:           bandCommunities<<20 + clampScore(len(c.Members)),
			Reason:         fmt.Sprintf("community: %d — %d member(s); e.g. %s", c.ID, len(c.Members), strings.Join(names, ", ")),
			EvidenceRefIDs: evIDs,
		})
	}
	return nil
}
