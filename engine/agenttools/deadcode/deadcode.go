// Package deadcode implements the dead_code agent tool (labs, P2 dead code):
// precise dead-code candidates in the C1 contract envelope, with an integer
// signal model and EXPLICIT exclusion reasons — "better than references == 0".
//
// The detection core is the existing EP-015 dead_symbol diagnostic
// (engine/diagnostic): live-inbound-reference analysis over calls, references,
// implements, inherits, and overrides edges, with the shared entry-point
// policy (annotations like @Bean/@Test, main signatures, test paths, override
// and decorated flags) downgrading framework-invoked lookalikes instead of
// flagging them. This tool adds what an agent needs on top: a pinned integer
// confidence score per candidate (exported API and dynamic-dispatch methods
// score lower), the exclusion rows made visible with their reasons, and the
// canonical contract shape shared by every surface.
//
// Cost model: the diagnostic performs the documented whole-graph pass (one
// node + one edge catalog read — dead-code analysis needs every edge by
// definition); this wrapper adds one compact aggregate probe and one selective
// NodesByID hydration.
package deadcode

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	pathclass "github.com/samibel/graphi/engine/classify"
	"github.com/samibel/graphi/engine/diagnostic"
)

const tool = "dead_code"

// MethodVersion stamps the scoring logic version into the summary.
const MethodVersion = "dead_code/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandIdentity   = 10
	bandCandidates = 9
	bandExclusions = 5
	bandNext       = 1
)

// Section caps and defaults.
const (
	DefaultMaxItems = 40
	candidateRows   = 30
	exclusionRows   = 12
)

// Integer signal model — pinned penalties, quoted in the reasons when they
// fire. A candidate starts at fullScore (the diagnostic's exact finding: zero
// live inbound references) and loses points for signals that make deletion
// riskier.
const (
	fullScore = 100
	// exportedPenalty: the symbol is part of the exported API surface and may
	// be consumed outside this repository's graph.
	exportedPenalty = 40
	// dynamicDispatchPenalty: methods may satisfy an interface the static
	// graph resolves to the declared type, not this concrete member.
	dynamicDispatchPenalty = 25
	// unknownVisibilityPenalty: the language has no decidable convention for
	// exported-ness, so the exported risk cannot be ruled out.
	unknownVisibilityPenalty = 10
	// Score bands for the confidence distribution.
	highScoreMin   = 80
	mediumScoreMin = 50
)

// Params carries the dead_code inputs.
type Params struct {
	// Deps are the shared engine services.
	Deps resolve.Deps
	// MaxItems caps the item list (0 selects DefaultMaxItems).
	MaxItems int
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

// Assemble runs the dead-symbol diagnostic and shapes it into the contract
// envelope: scored candidates first, then the visible exclusions.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	reader := p.Deps.Query.Reader()

	// Empty-graph probe via the compact aggregate (mirrors repo_overview).
	if agg, ok := reader.(graphstore.BriefAggregatePort); ok {
		stats, err := agg.BriefStats(ctx, 1)
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
	}

	// ExplainSuppressed keeps test/generated/vendored-path findings visible
	// with their suppression category, so the exclusion rows can cite WHY.
	res, err := diagnostic.DiagnoseWithOptions(ctx, reader, []string{diagnostic.KindDeadSymbol}, diagnostic.DiagnoseOptions{ExplainSuppressed: true})
	if err != nil {
		return nil, err
	}

	// Hydrate the flagged symbols once (selective port) for kind/name-aware
	// scoring; degrade to message-only rows when the port is absent.
	byID := map[model.NodeId]model.Node{}
	if lookup, ok := reader.(graphstore.GraphLookup); ok {
		ids := make([]model.NodeId, 0, len(res.Diagnostics))
		seen := map[model.NodeId]struct{}{}
		for _, d := range res.Diagnostics {
			if d.Symbol == "" {
				continue
			}
			if _, dup := seen[d.Symbol]; dup {
				continue
			}
			seen[d.Symbol] = struct{}{}
			ids = append(ids, d.Symbol)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		nodes, err := lookup.NodesByID(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			byID[n.ID()] = n
		}
	}

	ev := shape.NewEvidenceSet()
	var items []contract.Item

	type candidate struct {
		d       diagnostic.Diagnostic
		score   int
		signals []string
	}
	var candidates []candidate
	type exclusion struct {
		d      diagnostic.Diagnostic
		reason string
	}
	var exclusions []exclusion

	for _, d := range res.Diagnostics {
		switch {
		case d.Suppression != "":
			exclusions = append(exclusions, exclusion{d, exclusionReason(d.Suppression)})
		case d.Reason == diagnostic.ReasonEntrypointCandidate:
			exclusions = append(exclusions, exclusion{d, "framework/language entry point (annotation, main, test path, override, or decorated) — live despite zero in-graph references"})
		case d.Severity == diagnostic.SeverityWarning:
			score := fullScore
			signals := []string{"0 live inbound references"}
			n, hydrated := byID[d.Symbol]
			// Go init functions are invoked by the runtime, never via call
			// edges — an exclusion, not a candidate.
			if hydrated && isGoInit(n) {
				exclusions = append(exclusions, exclusion{d, "Go init function — invoked by the runtime at package initialization, not via call edges"})
				continue
			}
			if hydrated {
				switch visibility(n) {
				case visExported:
					score -= exportedPenalty
					signals = append(signals, fmt.Sprintf("exported — may be consumed outside this graph (-%d)", exportedPenalty))
				case visUnexported:
					signals = append(signals, "unexported")
				default:
					score -= unknownVisibilityPenalty
					signals = append(signals, fmt.Sprintf("visibility undecidable for this language (-%d)", unknownVisibilityPenalty))
				}
				if n.Kind() == "method" {
					score -= dynamicDispatchPenalty
					signals = append(signals, fmt.Sprintf("method — may satisfy an interface via dynamic dispatch (-%d)", dynamicDispatchPenalty))
				}
			} else {
				score -= unknownVisibilityPenalty
				signals = append(signals, fmt.Sprintf("symbol not hydrated — visibility unknown (-%d)", unknownVisibilityPenalty))
			}
			signals = append(signals, "not generated", "not a test fixture", "no entry-point marker")
			candidates = append(candidates, candidate{d, score, signals})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if candidates[i].d.File != candidates[j].d.File {
			return candidates[i].d.File < candidates[j].d.File
		}
		if candidates[i].d.Line != candidates[j].d.Line {
			return candidates[i].d.Line < candidates[j].d.Line
		}
		return candidates[i].d.Symbol < candidates[j].d.Symbol
	})

	// Band 10: identity/totals.
	items = append(items, contract.Item{
		RefID: "identity",
		Rank:  bandIdentity << 20,
		Reason: fmt.Sprintf("dead_code: %d candidate(s), %d exclusion(s) with reasons — %d finding(s) analyzed [signals: inbound references, visibility, dispatch, entry points, suppression]",
			len(candidates), len(exclusions), res.Summary.TotalAnalyzed),
	})

	// Band 9: scored candidates.
	for i, c := range candidates {
		if i >= candidateRows {
			break
		}
		name := c.d.Message
		if n, ok := byID[c.d.Symbol]; ok {
			name = fmt.Sprintf("%s %s", n.Kind(), n.QualifiedName())
		}
		evID := ev.Add(c.d.File, c.d.Line, "dead")
		items = append(items, contract.Item{
			RefID:          string(c.d.Symbol),
			Rank:           bandCandidates<<20 + clampScore(c.score*1000+(candidateRows-i)),
			Reason:         fmt.Sprintf("dead candidate: %s (%s:%d) — score %d/100 [%s]", name, c.d.File, c.d.Line, c.score, strings.Join(c.signals, ", ")),
			EvidenceRefIDs: []string{evID},
		})
	}

	// Band 5: the exclusions, visible with their reasons (never silently
	// dropped — the roadmap's exclusion_reasons made first-class).
	for i, x := range exclusions {
		if i >= exclusionRows {
			break
		}
		name := x.d.Message
		if n, ok := byID[x.d.Symbol]; ok {
			name = fmt.Sprintf("%s %s", n.Kind(), n.QualifiedName())
		}
		evID := ev.Add(x.d.File, x.d.Line, "excluded")
		items = append(items, contract.Item{
			RefID:          "excluded:" + string(x.d.Symbol),
			Rank:           bandExclusions<<20 + clampScore(exclusionRows-i),
			Reason:         fmt.Sprintf("excluded: %s (%s:%d) — %s", name, x.d.File, x.d.Line, x.reason),
			EvidenceRefIDs: []string{evID},
		})
	}

	if len(candidates) == 0 {
		items = append(items, contract.Item{
			RefID: "clean",
			Rank:  bandCandidates << 20,
			Reason: fmt.Sprintf("clean: no dead-code candidates — every reportable symbol has live inbound references (%d entry-point/suppression exclusion(s) listed below)",
				len(exclusions)),
		})
	}

	// Band 1: suggested next calls.
	next := []string{"symbol-context <candidate> — verify before removing"}
	if len(candidates) > 0 {
		next = append([]string{fmt.Sprintf("safe-delete %s — the guarded delete for the top candidate", candidates[0].d.Symbol)}, next...)
	}
	for i, n := range next {
		items = append(items, contract.Item{
			RefID:  fmt.Sprintf("next-%d", i+1),
			Rank:   bandNext<<20 + (len(next) - i),
			Reason: "next: graphi " + n,
		})
	}

	// Confidence distribution over the score bands.
	dist := map[string]float64{}
	for _, c := range candidates {
		switch {
		case c.score >= highScoreMin:
			dist["high"]++
		case c.score >= mediumScoreMin:
			dist["medium"]++
		default:
			dist["low"]++
		}
	}
	conf := contract.Confidence{Distribution: dist, Method: "integer_signal_model"}
	if len(candidates) == 0 {
		conf.Distribution = map[string]float64{"none": 1}
		conf.Top = "none"
	} else {
		top, best := "", -1.0
		for _, band := range []string{"high", "medium", "low"} {
			if dist[band] > best {
				top, best = band, dist[band]
			}
		}
		conf.Top = top
		// The distribution is a probability vector: normalize the band counts.
		total := float64(len(candidates))
		for band := range dist {
			dist[band] /= total
		}
	}

	summary := fmt.Sprintf("dead_code: %d candidate(s), %d excluded with reasons, %d finding(s) analyzed (%s)",
		len(candidates), len(exclusions), res.Summary.TotalAnalyzed, MethodVersion)
	if len(candidates) > 0 {
		name := candidates[0].d.Message
		if n, ok := byID[candidates[0].d.Symbol]; ok {
			name = n.QualifiedName()
		}
		summary = fmt.Sprintf("dead_code: %d candidate(s) — top %s (score %d/100), %d excluded with reasons, %d finding(s) analyzed (%s)",
			len(candidates), name, candidates[0].score, len(exclusions), res.Summary.TotalAnalyzed, MethodVersion)
	}

	r := &contract.Result{
		Outcome:    contract.OutcomeFound,
		Summary:    summary,
		Items:      items,
		Evidence:   ev.List(),
		Confidence: conf,
	}
	return shape.Finish(r, p.maxItems())
}

// exclusionReason renders a suppression category as the roadmap-style
// exclusion reason an agent can act on.
func exclusionReason(cat diagnostic.SuppressionCategory) string {
	switch cat {
	case diagnostic.SuppressionTestCode:
		return "test fixture — exercised by the test framework, not dead (suppression: test_code)"
	case diagnostic.SuppressionGenerated:
		return "generated/vendored path — regenerated, not hand-maintained (suppression: generated)"
	case diagnostic.SuppressionFrameworkEntrypoint:
		return "framework/language entry point — invoked by reflection or the runtime (suppression: framework_entrypoint)"
	case diagnostic.SuppressionPublicAPINoEvidence:
		return "exported API with no in-graph usage evidence — may be consumed outside this repository (suppression: public_api_no_evidence)"
	case diagnostic.SuppressionConfiguredPath:
		return "path excluded by suppression configuration (suppression: configured_path)"
	default:
		return fmt.Sprintf("suppressed (%s)", cat)
	}
}

// isGoInit reports whether n is a Go init function (runtime-invoked).
func isGoInit(n model.Node) bool {
	if n.Kind() != "function" || pathclass.Language(n.SourcePath()) != "Go" {
		return false
	}
	name := n.QualifiedName()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	return name == "init"
}

type visKind int

const (
	visUnknown visKind = iota
	visExported
	visUnexported
)

// visibility decides exported-ness where the language has a decidable
// convention: Go (upper-case initial identifier) and Python (leading
// underscore). Everything else is unknown — the score model penalizes the
// uncertainty instead of guessing.
func visibility(n model.Node) visKind {
	name := n.QualifiedName()
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return visUnknown
	}
	switch pathclass.Language(n.SourcePath()) {
	case "Go":
		r, _ := utf8.DecodeRuneInString(name)
		if unicode.IsUpper(r) {
			return visExported
		}
		return visUnexported
	case "Python":
		if strings.HasPrefix(name, "_") {
			return visUnexported
		}
		return visExported
	default:
		return visUnknown
	}
}
