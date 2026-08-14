// Package codehealth implements the code_health agent tool (labs, P5 /
// roadmap TODO-21): exactly TEN deterministic health detectors in one call —
// deliberately not "50 rules at once". Every finding carries a severity, a
// cited definition site, a confidence label, and a CONCRETE remediation (the
// next graphi call or the structural fix), so an agent can act on it without
// interpretation.
//
// The detectors:
//
//  1. dependency_cycles          — Tarjan SCCs over the symbol coupling graph
//  2. god_files                  — files holding too many symbols/edge endpoints
//  3. god_symbols                — high fan-in AND high fan-out on one symbol
//  4. high_fan_in                — most-depended-upon symbols above threshold
//  5. high_fan_out               — symbols depending on too much
//  6. dead_symbols               — zero live inbound references (EP-015 core)
//  7. unstable_dependencies      — heavily depended-upon packages that are
//     themselves unstable (I = E/(A+E))
//  8. duplicate_dependency_paths — a direct edge shadowed by indirect routes
//  9. layer_violations           — community cycles + against-dominant edges
//     (the architecture_violations model)
//  10. change_hotspots            — churn × centrality over bounded git history
//
// Pinned integer thresholds are quoted in every reason. Cost model: one node +
// one edge catalog read here, plus the archintel community pass and the
// dead-symbol diagnostic pass (each one more catalog read) — three documented
// whole-graph passes total; health needs the whole graph by definition.
package codehealth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/archintel"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	"github.com/samibel/graphi/engine/analysis/githistory"
	"github.com/samibel/graphi/engine/community"
	"github.com/samibel/graphi/engine/diagnostic"
)

const tool = "code_health"

// MethodVersion stamps the detector logic version into the summary.
const MethodVersion = "code_health/1"

// Rank bands (rank = band<<20 + score, score < 1<<20): findings by severity,
// so the item cap truncates informational rows first.
const (
	bandIdentity = 10
	bandHigh     = 9
	bandWarn     = 6
	bandInfo     = 3
	bandNext     = 1
)

// Pinned detector thresholds — quoted in the reasons when they fire.
const (
	DefaultMaxItems = 60

	cycleReportCap   = 5  // SCCs reported (largest first)
	cycleHighMembers = 5  // SCC size that escalates warn → high
	godFileSymbols   = 40 // symbols in one file
	godFileEndpoints = 300
	godSymbolDegree  = 15 // fan-in AND fan-out both at/above
	highFanIn        = 40
	highFanOut       = 40
	degreeReportCap  = 5
	unstableMinAff   = 20 // afferent coupling floor for the instability check
	unstablePctMin   = 70 // instability percentage E*100/(A+E)
	dupScanFanOutCap = 48 // nodes with larger fan-out are skipped (bounded scan)
	dupReportCap     = 5
	archReportCap    = 3
	hotspotReportCap = 3
	deadTopNames     = 3
)

// Params carries the code_health inputs.
type Params struct {
	// Deps are the shared engine services.
	Deps resolve.Deps
	// Provider is the surface-boundary git-history source for the
	// change_hotspots detector. Nil degrades that ONE detector to a typed
	// informational row; the other nine still run.
	Provider githistory.GitProvider
	// MaxCommits bounds the history window (0 = githistory default).
	MaxCommits int
	// Now overrides the reference time for the history window (zero = wall
	// clock; tests pass a fixed time for byte determinism).
	Now time.Time
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
}

func (p Params) maxItems() int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}

func clampScore(s int) int {
	if s >= 1<<20 {
		return 1<<20 - 1
	}
	if s < 0 {
		return 0
	}
	return s
}

// finding is one detector result before rendering.
type finding struct {
	detector    string
	severity    string // "high" | "warn" | "info"
	confidence  string // "confirmed" (graph fact) | "heuristic" (threshold call)
	message     string
	remediation string
	score       int    // within-band ordering
	path        string // evidence citation (optional)
	line        int
}

// Assemble runs the ten detectors and shapes their findings into the contract
// envelope.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	reader := p.Deps.Query.Reader()
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}

	// Symbol-only projection (the WP-14 hygiene rule).
	byID := make(map[model.NodeId]model.Node, len(nodes))
	ids := make([]model.NodeId, 0, len(nodes))
	for _, n := range nodes {
		if community.IsArtifactKind(n.Kind()) {
			continue
		}
		byID[n.ID()] = n
		ids = append(ids, n.ID())
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if len(ids) == 0 {
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

	// Coupling projection: degree maps, out-adjacency, per-file and
	// per-package aggregates over the coupling edge kinds.
	coupling := map[string]bool{"calls": true, "references": true, "imports": true, "implements": true, "inherits": true, "overrides": true}
	inDeg := map[model.NodeId]int{}
	outDeg := map[model.NodeId]int{}
	outAdj := map[model.NodeId][]model.NodeId{}
	fileSymbols := map[string]int{}
	fileEndpoints := map[string]int{}
	pkgStats := map[string]*pkgStat{}
	pkgOf := func(id model.NodeId) string {
		qn := byID[id].QualifiedName()
		if i := strings.LastIndexByte(qn, '.'); i >= 0 {
			return qn[:i]
		}
		return qn
	}
	for _, n := range byID {
		if n.SourcePath() != "" {
			fileSymbols[n.SourcePath()]++
		}
	}
	for _, e := range edges {
		from, fok := byID[e.From()]
		to, tok := byID[e.To()]
		if !fok || !tok || !coupling[e.Kind()] {
			continue
		}
		outDeg[e.From()]++
		inDeg[e.To()]++
		outAdj[e.From()] = append(outAdj[e.From()], e.To())
		if from.SourcePath() != "" {
			fileEndpoints[from.SourcePath()]++
		}
		if to.SourcePath() != "" {
			fileEndpoints[to.SourcePath()]++
		}
		if pf, pt := pkgOf(e.From()), pkgOf(e.To()); pf != pt {
			if pkgStats[pf] == nil {
				pkgStats[pf] = &pkgStat{}
			}
			if pkgStats[pt] == nil {
				pkgStats[pt] = &pkgStat{}
			}
			pkgStats[pf].efferent++
			pkgStats[pt].afferent++
		}
	}
	for id := range outAdj {
		adj := outAdj[id]
		sort.Slice(adj, func(i, j int) bool { return adj[i] < adj[j] })
	}

	var findings []finding
	counts := map[string]int{}
	add := func(f finding) {
		findings = append(findings, f)
		counts[f.detector]++
	}

	detectCycles(ids, outAdj, byID, add)
	detectGodFiles(fileSymbols, fileEndpoints, add)
	detectDegrees(ids, inDeg, outDeg, byID, add)
	detectUnstablePackages(pkgStats, add)
	detectDuplicatePaths(ids, outAdj, outDeg, byID, add)
	if err := detectDeadSymbols(ctx, p.Deps, add); err != nil {
		return nil, err
	}
	if err := detectArchitecture(ctx, p.Deps, add); err != nil {
		return nil, err
	}
	if err := detectChangeHotspots(ctx, p, fileEndpoints, add); err != nil {
		return nil, err
	}

	// Render: identity, findings by severity band, clean row, next calls.
	ev := shape.NewEvidenceSet()
	var items []contract.Item
	sevCount := map[string]int{}
	for _, f := range findings {
		sevCount[f.severity]++
	}
	items = append(items, contract.Item{
		RefID: "identity",
		Rank:  bandIdentity << 20,
		Reason: fmt.Sprintf("code_health: %d finding(s) across 10 detectors over %d symbols — %d high, %d warn, %d info",
			len(findings), len(ids), sevCount["high"], sevCount["warn"], sevCount["info"]),
	})
	for i, f := range findings {
		band := bandWarn
		switch f.severity {
		case "high":
			band = bandHigh
		case "info":
			band = bandInfo
		}
		var evIDs []string
		if f.path != "" {
			evIDs = []string{ev.Add(f.path, f.line, f.detector)}
		}
		items = append(items, contract.Item{
			RefID:          fmt.Sprintf("%s-%d", f.detector, i),
			Rank:           band<<20 + clampScore(f.score),
			Reason:         fmt.Sprintf("%s: %s [severity %s, confidence %s] — remediation: %s", f.detector, f.message, f.severity, f.confidence, f.remediation),
			EvidenceRefIDs: evIDs,
		})
	}
	if len(findings) == 0 {
		items = append(items, contract.Item{
			RefID:  "clean",
			Rank:   bandHigh << 20,
			Reason: fmt.Sprintf("clean: all 10 detectors ran and none fired over %d symbols — thresholds are pinned in %s", len(ids), MethodVersion),
		})
	}
	items = append(items, contract.Item{
		RefID:  "next-1",
		Rank:   bandNext<<20 + 1,
		Reason: "next: graphi architecture-violations / dead-code / hotspots — the per-family deep dives",
	})

	dist := map[string]float64{}
	conf := contract.Confidence{Method: "deterministic_detectors"}
	if len(findings) == 0 {
		dist["none"] = 1
		conf.Top = "none"
	} else {
		for _, f := range findings {
			dist[f.severity]++
		}
		top, best := "", -1.0
		for _, s := range []string{"high", "warn", "info"} {
			if dist[s] > best {
				top, best = s, dist[s]
			}
		}
		conf.Top = top
		for s := range dist {
			dist[s] /= float64(len(findings))
		}
	}
	conf.Distribution = dist

	det := make([]string, 0, len(counts))
	for d := range counts {
		det = append(det, d)
	}
	sort.Strings(det)
	parts := make([]string, 0, len(det))
	for _, d := range det {
		parts = append(parts, fmt.Sprintf("%s %d", d, counts[d]))
	}
	summary := fmt.Sprintf("code_health: %d finding(s) across 10 detectors — %s (%s)", len(findings), strings.Join(parts, ", "), MethodVersion)
	if len(findings) == 0 {
		summary = fmt.Sprintf("code_health: clean — all 10 detectors ran, none fired over %d symbols (%s)", len(ids), MethodVersion)
	}

	r := &contract.Result{
		Outcome:    contract.OutcomeFound,
		Summary:    summary,
		Items:      items,
		Evidence:   ev.List(),
		Confidence: conf,
	}
	return shape.FinishLabs(r, p.maxItems())
}

// detectCycles finds strongly connected components of size >= 2 over the
// coupling graph (iterative Tarjan; deterministic by sorted node ids).
func detectCycles(ids []model.NodeId, outAdj map[model.NodeId][]model.NodeId, byID map[model.NodeId]model.Node, add func(finding)) {
	index := map[model.NodeId]int{}
	low := map[model.NodeId]int{}
	onStack := map[model.NodeId]bool{}
	var stack []model.NodeId
	var sccs [][]model.NodeId
	next := 0

	type frame struct {
		v  model.NodeId
		ai int
	}
	for _, root := range ids {
		if _, seen := index[root]; seen {
			continue
		}
		work := []frame{{v: root}}
		index[root], low[root] = next, next
		next++
		stack = append(stack, root)
		onStack[root] = true
		for len(work) > 0 {
			f := &work[len(work)-1]
			adj := outAdj[f.v]
			if f.ai < len(adj) {
				w := adj[f.ai]
				f.ai++
				if _, seen := index[w]; !seen {
					index[w], low[w] = next, next
					next++
					stack = append(stack, w)
					onStack[w] = true
					work = append(work, frame{v: w})
				} else if onStack[w] && low[f.v] > index[w] {
					low[f.v] = index[w]
				}
				continue
			}
			work = work[:len(work)-1]
			if len(work) > 0 {
				parent := work[len(work)-1].v
				if low[parent] > low[f.v] {
					low[parent] = low[f.v]
				}
			}
			if low[f.v] == index[f.v] {
				var comp []model.NodeId
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					comp = append(comp, w)
					if w == f.v {
						break
					}
				}
				if len(comp) >= 2 {
					sort.Slice(comp, func(i, j int) bool { return comp[i] < comp[j] })
					sccs = append(sccs, comp)
				}
			}
		}
	}
	sort.Slice(sccs, func(i, j int) bool {
		if len(sccs[i]) != len(sccs[j]) {
			return len(sccs[i]) > len(sccs[j])
		}
		return sccs[i][0] < sccs[j][0]
	})
	for i, comp := range sccs {
		if i >= cycleReportCap {
			break
		}
		names := make([]string, 0, 3)
		for _, id := range comp[:min(3, len(comp))] {
			names = append(names, byID[id].QualifiedName())
		}
		sev := "warn"
		if len(comp) >= cycleHighMembers {
			sev = "high"
		}
		first := byID[comp[0]]
		add(finding{
			detector:    "dependency_cycles",
			severity:    sev,
			confidence:  "confirmed",
			message:     fmt.Sprintf("%d symbols form a dependency cycle (e.g. %s)", len(comp), strings.Join(names, " ⇄ ")),
			remediation: fmt.Sprintf("break the weakest edge; inspect with graphi neighborhood %s", first.QualifiedName()),
			score:       len(comp),
			path:        first.SourcePath(),
			line:        first.Line(),
		})
	}
}

// detectGodFiles flags files above the pinned symbol/endpoint thresholds.
func detectGodFiles(fileSymbols, fileEndpoints map[string]int, add func(finding)) {
	paths := make([]string, 0, len(fileSymbols))
	for p := range fileSymbols {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		syms, eps := fileSymbols[p], fileEndpoints[p]
		if syms < godFileSymbols && eps < godFileEndpoints {
			continue
		}
		sev := "warn"
		if syms >= 2*godFileSymbols || eps >= 2*godFileEndpoints {
			sev = "high"
		}
		add(finding{
			detector:    "god_files",
			severity:    sev,
			confidence:  "heuristic",
			message:     fmt.Sprintf("%s holds %d symbols and %d edge endpoints (thresholds %d/%d)", p, syms, eps, godFileSymbols, godFileEndpoints),
			remediation: "split along community lines; see graphi architecture",
			score:       syms + eps/10,
			path:        p,
		})
	}
}

// detectDegrees flags god symbols (high fan-in AND fan-out) plus the
// high-fan-in and high-fan-out tops.
func detectDegrees(ids []model.NodeId, inDeg, outDeg map[model.NodeId]int, byID map[model.NodeId]model.Node, add func(finding)) {
	emitTop := func(detector string, deg map[model.NodeId]int, threshold int, msg func(n model.Node, d int) string, remediation string) {
		var hits []model.NodeId
		for _, id := range ids {
			if deg[id] >= threshold {
				hits = append(hits, id)
			}
		}
		sort.Slice(hits, func(i, j int) bool {
			if deg[hits[i]] != deg[hits[j]] {
				return deg[hits[i]] > deg[hits[j]]
			}
			return hits[i] < hits[j]
		})
		for i, id := range hits {
			if i >= degreeReportCap {
				break
			}
			n := byID[id]
			add(finding{
				detector:    detector,
				severity:    "warn",
				confidence:  "heuristic",
				message:     msg(n, deg[id]),
				remediation: remediation,
				score:       deg[id],
				path:        n.SourcePath(),
				line:        n.Line(),
			})
		}
	}
	// God symbols first (both directions heavy).
	var gods []model.NodeId
	for _, id := range ids {
		if inDeg[id] >= godSymbolDegree && outDeg[id] >= godSymbolDegree {
			gods = append(gods, id)
		}
	}
	sort.Slice(gods, func(i, j int) bool {
		di, dj := inDeg[gods[i]]+outDeg[gods[i]], inDeg[gods[j]]+outDeg[gods[j]]
		if di != dj {
			return di > dj
		}
		return gods[i] < gods[j]
	})
	godSet := map[model.NodeId]bool{}
	for i, id := range gods {
		if i >= degreeReportCap {
			break
		}
		godSet[id] = true
		n := byID[id]
		add(finding{
			detector:    "god_symbols",
			severity:    "high",
			confidence:  "heuristic",
			message:     fmt.Sprintf("%s %s has fan-in %d AND fan-out %d (both ≥ %d)", n.Kind(), n.QualifiedName(), inDeg[id], outDeg[id], godSymbolDegree),
			remediation: fmt.Sprintf("split responsibilities; graphi symbol-context %s", n.QualifiedName()),
			score:       inDeg[id] + outDeg[id],
			path:        n.SourcePath(),
			line:        n.Line(),
		})
	}
	emitTop("high_fan_in", inDeg, highFanIn, func(n model.Node, d int) string {
		return fmt.Sprintf("%s %s has %d inbound coupling edges (≥ %d)", n.Kind(), n.QualifiedName(), d, highFanIn)
	}, "treat as a frozen contract; changes here fan out — graphi change-impact <symbol>")
	emitTop("high_fan_out", outDeg, highFanOut, func(n model.Node, d int) string {
		return fmt.Sprintf("%s %s depends on %d symbols (≥ %d)", n.Kind(), n.QualifiedName(), d, highFanOut)
	}, "extract collaborators; graphi callees <symbol> shows the spread")
	_ = godSet
}

// pkgStat aggregates a package's cross-package coupling.
type pkgStat struct{ afferent, efferent int }

// detectUnstablePackages flags packages that are heavily depended upon yet
// themselves unstable: instability I = E*100/(A+E) at/above the pinned floor.
func detectUnstablePackages(pkgStats map[string]*pkgStat, add func(finding)) {
	names := make([]string, 0, len(pkgStats))
	for name := range pkgStats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := pkgStats[name]
		total := st.afferent + st.efferent
		if st.afferent < unstableMinAff || total == 0 {
			continue
		}
		instability := st.efferent * 100 / total
		if instability < unstablePctMin {
			continue
		}
		add(finding{
			detector:    "unstable_dependencies",
			severity:    "warn",
			confidence:  "heuristic",
			message:     fmt.Sprintf("package %s is depended on %d× yet has instability %d%% (efferent %d; thresholds ≥%d afferent, ≥%d%%)", name, st.afferent, instability, st.efferent, unstableMinAff, unstablePctMin),
			remediation: "stable packages should not depend outward: invert the dependency or split the package",
			score:       st.afferent,
		})
	}
}

// detectDuplicatePaths flags direct edges that are also reachable through one
// intermediate hop (A→B and A→X→B) — a dependency expressed twice. The scan is
// bounded to nodes with fan-out ≤ dupScanFanOutCap.
func detectDuplicatePaths(ids []model.NodeId, outAdj map[model.NodeId][]model.NodeId, outDeg map[model.NodeId]int, byID map[model.NodeId]model.Node, add func(finding)) {
	type dup struct {
		a, b   model.NodeId
		routes int
	}
	var dups []dup
	for _, a := range ids {
		if outDeg[a] > dupScanFanOutCap {
			continue
		}
		direct := map[model.NodeId]bool{}
		for _, b := range outAdj[a] {
			direct[b] = true
		}
		routes := map[model.NodeId]int{}
		for _, x := range outAdj[a] {
			if outDeg[x] > dupScanFanOutCap {
				continue
			}
			for _, b := range outAdj[x] {
				if b != a && b != x && direct[b] {
					routes[b]++
				}
			}
		}
		for b, r := range routes {
			dups = append(dups, dup{a, b, r})
		}
	}
	sort.Slice(dups, func(i, j int) bool {
		if dups[i].routes != dups[j].routes {
			return dups[i].routes > dups[j].routes
		}
		if dups[i].a != dups[j].a {
			return dups[i].a < dups[j].a
		}
		return dups[i].b < dups[j].b
	})
	for i, d := range dups {
		if i >= dupReportCap {
			break
		}
		na, nb := byID[d.a], byID[d.b]
		add(finding{
			detector:    "duplicate_dependency_paths",
			severity:    "warn",
			confidence:  "confirmed",
			message:     fmt.Sprintf("%s depends on %s directly AND through %d indirect route(s)", na.QualifiedName(), nb.QualifiedName(), d.routes),
			remediation: "depend on one level only: drop the direct edge or stop re-exporting through the intermediary",
			score:       d.routes,
			path:        na.SourcePath(),
			line:        na.Line(),
		})
	}
}

// detectDeadSymbols reuses the EP-015 dead_symbol diagnostic (default gates)
// and summarizes the default-visible warnings.
func detectDeadSymbols(ctx context.Context, deps resolve.Deps, add func(finding)) error {
	res, err := diagnostic.DiagnoseWithOptions(ctx, deps.Query.Reader(), []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{})
	if err != nil {
		return err
	}
	var names []string
	count := 0
	first := diagnostic.Diagnostic{}
	for _, d := range res.Diagnostics {
		if d.Severity != diagnostic.SeverityWarning || d.Suppression != "" {
			continue
		}
		if count == 0 {
			first = d
		}
		count++
		if len(names) < deadTopNames {
			names = append(names, fmt.Sprintf("%s:%d", d.File, d.Line))
		}
	}
	if count == 0 {
		return nil
	}
	add(finding{
		detector:    "dead_symbols",
		severity:    "warn",
		confidence:  "confirmed",
		message:     fmt.Sprintf("%d symbol(s) with zero live inbound references (e.g. %s)", count, strings.Join(names, ", ")),
		remediation: "graphi dead-code — scored candidates with exclusion reasons, then safe-delete",
		score:       count,
		path:        first.File,
		line:        first.Line,
	})
	return nil
}

// detectArchitecture reuses the architecture_violations community model:
// dependency cycles between communities (high) and against-dominant-direction
// edges (warn).
func detectArchitecture(ctx context.Context, deps resolve.Deps, add func(finding)) error {
	view, err := archintel.Health(ctx, deps)
	if err != nil {
		return err
	}
	for i, cyc := range view.Cycles {
		if i >= archReportCap {
			break
		}
		add(finding{
			detector:    "layer_violations",
			severity:    "high",
			confidence:  "confirmed",
			message:     "community dependency cycle: " + cyc,
			remediation: "graphi architecture-violations — full cycle list with edge counts",
			score:       archReportCap - i,
		})
	}
	for i, be := range view.BackEdges {
		if i >= archReportCap {
			break
		}
		add(finding{
			detector:    "layer_violations",
			severity:    "warn",
			confidence:  "confirmed",
			message:     "unexpected dependency: " + be,
			remediation: "graphi architecture-violations — full back-edge list",
			score:       archReportCap - i,
		})
	}
	return nil
}

// detectChangeHotspots ranks churn × centrality over bounded git history; with
// no provider it emits one typed informational row instead of guessing.
func detectChangeHotspots(ctx context.Context, p Params, fileEndpoints map[string]int, add func(finding)) error {
	if p.Provider == nil {
		add(finding{
			detector:    "change_hotspots",
			severity:    "info",
			confidence:  "confirmed",
			message:     "no git history on this surface (attach mode or no repository root) — churn × complexity not assessed",
			remediation: "open the session from a git repository, or run graphi hotspots there",
			score:       1,
		})
		return nil
	}
	hist, err := githistory.New(p.Provider, githistory.Config{MaxCommits: p.MaxCommits, Now: p.Now}).Run(ctx)
	if err != nil {
		return err
	}
	type row struct {
		path           string
		commits, score int
	}
	var rows []row
	for _, churn := range hist.ChurnScores {
		score := churn.Commits * (1 + fileEndpoints[churn.Path])
		if churn.Commits >= 2 && fileEndpoints[churn.Path] > 0 {
			rows = append(rows, row{churn.Path, churn.Commits, score})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].score != rows[j].score {
			return rows[i].score > rows[j].score
		}
		return rows[i].path < rows[j].path
	})
	for i, r := range rows {
		if i >= hotspotReportCap {
			break
		}
		add(finding{
			detector:    "change_hotspots",
			severity:    "warn",
			confidence:  "confirmed",
			message:     fmt.Sprintf("%s — %d commit(s) in the window × %d edge endpoints (score %d)", r.path, r.commits, fileEndpoints[r.path], r.score),
			remediation: "graphi hotspots — the full churn × centrality ranking with bus-factor warnings",
			score:       r.score,
			path:        r.path,
		})
	}
	return nil
}
