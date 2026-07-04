// Package brief assembles a bounded, cited task-start context packet for
// coding agents. Content is derived live from the indexed graph (start-here
// files, key symbols, hotspot risk) and the local memory store (known facts),
// falling back to honest "unavailable" notes when either source is missing.
// It follows the EP-020 C1 contract shape and emits both canonical JSON and
// deterministic Markdown. Local-only: no network calls are made.
package brief

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/related"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/memory"
)

// Tier is the product-level confidence classification for every brief item.
type Tier string

const (
	TierConfirmed Tier = "confirmed"
	TierDerived   Tier = "derived"
	TierHeuristic Tier = "heuristic"
)

// Section captures one headed brief section. Titles follow the PRD Markdown
// shape: Start Here, Relevant Symbols, Known Facts, Risks and Unknowns,
// Suggested Next Calls.
type Section struct {
	Title string
	Items []SectionItem
}

// SectionItem is one row in a brief section.
type SectionItem struct {
	Label       string
	Value       string
	EvidenceRef string // "path" or "path:line" citation
	Tier        Tier
}

// Params controls the assembly of a brief.
type Params struct {
	Topic       string
	ProjectName string
	RepoRoot    string
	MemoryPath  string // retained for wiring compatibility; Memory supersedes it

	// MaxItems bounds the total item count (0 = DefaultMaxItems). MaxSections
	// bounds the section count (0 = all). Exceeding either marks the result
	// truncated and downgrades the outcome to "partial".
	MaxItems    int
	MaxSections int

	// Deps carries the optional live graph/search services. Without them the
	// brief degrades to an honest graph-unavailable note.
	Deps resolve.Deps
	// Memory is the optional local memory store consulted for known facts.
	Memory *memory.Store
}

// DefaultMaxItems bounds a brief when the caller passes no cap.
const DefaultMaxItems = 40

// per-section bounds keep the packet compact regardless of graph size.
const (
	maxStartHere = 5
	maxSymbols   = 5
	maxFacts     = 5
	maxTopic     = 5
)

// graphView is the single-pass digest of the store the sections draw from.
type graphView struct {
	fileDegree    map[string]int // path → incident edge endpoints
	fileSymbols   map[string]int // path → node count
	nodeInDegree  map[model.NodeId]int
	nodes         map[model.NodeId]model.Node
	tierCount     map[model.ConfidenceTier]int
	totalEdges    int
	totalNodes    int
	available     bool
	unavailErr    error
	topicFiles    []contract.Item // resolved related-files rows for the topic
	topicResolved bool
}

// Assemble returns a canonical Result containing the brief sections.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	view := buildView(ctx, p)

	sections := []Section{
		{Title: "Project identity", Items: projectIdentity(p, view)},
		{Title: "Start Here", Items: startHere(p, view)},
		{Title: "Relevant Symbols", Items: relevantSymbols(view)},
		{Title: "Known Facts", Items: knownFacts(ctx, p)},
		{Title: "Risks and Unknowns", Items: risksAndUnknowns(p, view)},
		{Title: "Suggested Next Calls", Items: suggestedCalls(p, view)},
	}

	sectionsTruncated := false
	if p.MaxSections > 0 && len(sections) > p.MaxSections {
		sections = sections[:p.MaxSections]
		sectionsTruncated = true
	}

	var items []contract.Item
	var evidence []contract.Evidence
	evSet := map[string]bool{}
	refSeq := 0

	for si, sec := range sections {
		for _, it := range sec.Items {
			refSeq++
			itemRef := fmt.Sprintf("brief-%d-%d", si, refSeq)
			item := contract.Item{
				RefID:  itemRef,
				Rank:   1_000_000 - refSeq, // preserve section order under the rank sort
				Reason: fmt.Sprintf("%s: %s — %s (%s)", sec.Title, it.Label, it.Value, it.Tier),
			}
			if it.EvidenceRef != "" {
				if !evSet[it.EvidenceRef] {
					evSet[it.EvidenceRef] = true
					path, line := splitRef(it.EvidenceRef)
					evidence = append(evidence, contract.Evidence{
						RefID: it.EvidenceRef,
						Path:  path,
						Line:  line,
						Role:  "source",
					})
				}
				item.EvidenceRefIDs = []string{it.EvidenceRef}
			}
			items = append(items, item)
		}
	}

	maxItems := p.MaxItems
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	total := len(items)
	if total > maxItems {
		items = items[:maxItems]
	}
	truncated := sectionsTruncated || total > maxItems

	summary := briefSummary(p, view)
	outcome := contract.OutcomeFound
	if truncated {
		outcome = contract.OutcomePartial
	}

	conf := contract.Confidence{Distribution: tierDistribution(sections), Method: "brief_sections"}
	if err := contract.NormalizeConfidence(&conf); err != nil {
		return nil, err
	}
	conf.Method = "brief_sections"

	return &contract.Result{
		Outcome:    outcome,
		Summary:    summary,
		Items:      items,
		Evidence:   evidence,
		Confidence: conf,
		Limits: contract.Limits{
			CapApplied:     maxItems,
			TotalAvailable: total,
			Dropped:        total - len(items),
			Truncated:      truncated,
			Next:           "",
		},
	}, nil
}

// buildView digests the graph once and resolves the topic when given.
func buildView(ctx context.Context, p Params) *graphView {
	v := &graphView{
		fileDegree:   map[string]int{},
		fileSymbols:  map[string]int{},
		nodeInDegree: map[model.NodeId]int{},
		nodes:        map[model.NodeId]model.Node{},
		tierCount:    map[model.ConfidenceTier]int{},
	}
	if !p.Deps.Available() {
		return v
	}
	reader := p.Deps.Query.Reader()
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		v.unavailErr = err
		return v
	}
	edges, err := reader.Edges(ctx, graphstore.Query{})
	if err != nil {
		v.unavailErr = err
		return v
	}
	v.available = true
	v.totalNodes = len(nodes)
	v.totalEdges = len(edges)
	for _, n := range nodes {
		v.nodes[n.ID()] = n
		if n.SourcePath() != "" {
			v.fileSymbols[n.SourcePath()]++
		}
	}
	for _, e := range edges {
		v.tierCount[e.Tier()]++
		if from, ok := v.nodes[e.From()]; ok && from.SourcePath() != "" {
			v.fileDegree[from.SourcePath()]++
		}
		if to, ok := v.nodes[e.To()]; ok && to.SourcePath() != "" {
			v.fileDegree[to.SourcePath()]++
		}
		v.nodeInDegree[e.To()]++
	}
	if p.Topic != "" {
		if res, err := related.Files(ctx, p.Deps, p.Topic, related.DirectionBoth, maxTopic); err == nil {
			if res.Outcome == contract.OutcomeFound || res.Outcome == contract.OutcomePartial {
				v.topicResolved = true
				v.topicFiles = res.Items
			}
		}
	}
	return v
}

func briefSummary(p Params, v *graphView) string {
	var b strings.Builder
	if p.Topic != "" {
		fmt.Fprintf(&b, "Agent brief scoped to topic %q", p.Topic)
		if !v.topicResolved {
			b.WriteString(" (topic did not resolve in the graph)")
		}
		b.WriteString(". ")
	}
	if v.available {
		fmt.Fprintf(&b, "Derived from an indexed graph of %d symbols and %d edges.", v.totalNodes, v.totalEdges)
	} else {
		b.WriteString("Graph unavailable — content is limited; run `graphi index` and pass -db for a cited brief.")
	}
	return b.String()
}

func projectIdentity(p Params, v *graphView) []SectionItem {
	name := p.ProjectName
	tier := TierConfirmed
	if name == "" {
		name = "unknown"
		tier = TierHeuristic
		if v.available {
			// Heuristic: the most common top-level directory names the project.
			topCount := 0
			for path, n := range v.fileSymbols {
				root := path
				if i := strings.Index(path, "/"); i > 0 {
					root = path[:i]
				}
				if n > topCount {
					topCount = n
					name = root
				}
			}
		}
	}
	return []SectionItem{{Label: "name", Value: name, Tier: tier}}
}

// startHere ranks read-first files: topic-related files first (when resolved),
// then the most connected files in the graph.
func startHere(p Params, v *graphView) []SectionItem {
	var out []SectionItem
	for _, it := range v.topicFiles {
		out = append(out, SectionItem{
			Label:       it.RefID,
			Value:       "related to topic: " + it.Reason,
			EvidenceRef: it.RefID,
			Tier:        TierDerived,
		})
	}
	if !v.available {
		return out
	}
	type fileRank struct {
		path   string
		degree int
	}
	ranked := make([]fileRank, 0, len(v.fileDegree))
	for path, deg := range v.fileDegree {
		ranked = append(ranked, fileRank{path, deg})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].degree != ranked[j].degree {
			return ranked[i].degree > ranked[j].degree
		}
		return ranked[i].path < ranked[j].path
	})
	seen := map[string]bool{}
	for _, it := range out {
		seen[it.Label] = true
	}
	for _, fr := range ranked {
		if len(out) >= maxStartHere {
			break
		}
		if seen[fr.path] {
			continue
		}
		out = append(out, SectionItem{
			Label:       fr.path,
			Value:       fmt.Sprintf("%d symbols, %d edge endpoints", v.fileSymbols[fr.path], fr.degree),
			EvidenceRef: fr.path,
			Tier:        TierDerived,
		})
	}
	return out
}

// relevantSymbols ranks the most-referenced symbols (highest inbound degree).
func relevantSymbols(v *graphView) []SectionItem {
	if !v.available {
		return nil
	}
	type symRank struct {
		id  model.NodeId
		deg int
	}
	ranked := make([]symRank, 0, len(v.nodeInDegree))
	for id, deg := range v.nodeInDegree {
		if _, ok := v.nodes[id]; ok {
			ranked = append(ranked, symRank{id, deg})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].deg != ranked[j].deg {
			return ranked[i].deg > ranked[j].deg
		}
		return ranked[i].id < ranked[j].id
	})
	if len(ranked) > maxSymbols {
		ranked = ranked[:maxSymbols]
	}
	out := make([]SectionItem, 0, len(ranked))
	for _, sr := range ranked {
		n := v.nodes[sr.id]
		out = append(out, SectionItem{
			Label:       n.QualifiedName(),
			Value:       fmt.Sprintf("%s with %d inbound edges", n.Kind(), sr.deg),
			EvidenceRef: fmt.Sprintf("%s:%d", n.SourcePath(), n.Line()),
			Tier:        TierDerived,
		})
	}
	return out
}

// knownFacts surfaces stored memory entries with provenance. Secret-suspect
// entries are withheld (counted in Risks and Unknowns).
func knownFacts(ctx context.Context, p Params) []SectionItem {
	if p.Memory == nil {
		return nil
	}
	entries, err := p.Memory.ListMemory(ctx, memory.Query{}, 0)
	if err != nil {
		return nil
	}
	out := make([]SectionItem, 0, maxFacts)
	for _, e := range entries {
		if e.SecretSuspect {
			continue
		}
		if len(out) >= maxFacts {
			break
		}
		label := e.Kind
		if label == "" {
			label = "fact"
		}
		value := e.Payload
		if len(value) > 160 {
			value = value[:157] + "..."
		}
		if e.Source != "" {
			value += " [source: " + e.Source + "]"
		}
		out = append(out, SectionItem{
			Label:       label,
			Value:       value,
			EvidenceRef: e.Evidence,
			Tier:        factTier(e.Confidence),
		})
	}
	return out
}

func factTier(confidence string) Tier {
	switch confidence {
	case "confirmed":
		return TierConfirmed
	case "heuristic", "":
		return TierHeuristic
	default:
		return TierDerived
	}
}

// risksAndUnknowns states what the brief could NOT establish, plus structural
// risk signals (heuristic edge share).
func risksAndUnknowns(p Params, v *graphView) []SectionItem {
	var out []SectionItem
	if !v.available {
		reason := "no graph services wired; run `graphi index` and pass -db"
		if v.unavailErr != nil {
			reason = "graph read failed: " + v.unavailErr.Error()
		}
		out = append(out, SectionItem{Label: "graph unavailable", Value: reason, Tier: TierHeuristic})
	} else if v.totalEdges > 0 {
		h := v.tierCount[model.TierHeuristic]
		share := float64(h) / float64(v.totalEdges) * 100
		if h > 0 {
			out = append(out, SectionItem{
				Label: "heuristic edges",
				Value: fmt.Sprintf("%.0f%% of %d edges are heuristic — verify before acting on them", share, v.totalEdges),
				Tier:  TierDerived,
			})
		}
	}
	if p.Topic != "" && !v.topicResolved {
		out = append(out, SectionItem{
			Label: "topic unresolved",
			Value: fmt.Sprintf("%q matched nothing; try related_files with a path or symbol", p.Topic),
			Tier:  TierHeuristic,
		})
	}
	if p.Memory == nil {
		out = append(out, SectionItem{Label: "no memory", Value: "no memory store wired; stored project facts unavailable", Tier: TierHeuristic})
	} else {
		if withheld := countSecretFacts(p); withheld > 0 {
			out = append(out, SectionItem{
				Label: "sensitive facts withheld",
				Value: fmt.Sprintf("%d secret-suspect memory entrie(s) omitted from this brief", withheld),
				Tier:  TierConfirmed,
			})
		}
	}
	return out
}

func countSecretFacts(p Params) int {
	entries, err := p.Memory.ListMemory(context.Background(), memory.Query{}, 0)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.SecretSuspect {
			n++
		}
	}
	return n
}

// suggestedCalls proposes the documented agent workflow with concrete
// arguments drawn from the assembled brief.
func suggestedCalls(p Params, v *graphView) []SectionItem {
	topSymbol := ""
	topDeg := -1
	for id, deg := range v.nodeInDegree {
		n, ok := v.nodes[id]
		if !ok {
			continue
		}
		if deg > topDeg || (deg == topDeg && n.QualifiedName() < topSymbol) {
			topDeg = deg
			topSymbol = n.QualifiedName()
		}
	}
	target := p.Topic
	if target == "" {
		target = topSymbol
	}
	if target == "" {
		target = "<symbol or path>"
	}
	sym := topSymbol
	if sym == "" {
		sym = "<symbol>"
	}
	return []SectionItem{
		{Label: "related_files", Value: fmt.Sprintf("{\"target\": %q}", target), Tier: TierHeuristic},
		{Label: "explain_symbol", Value: fmt.Sprintf("{\"symbol\": %q}", sym), Tier: TierHeuristic},
		{Label: "change_risk", Value: fmt.Sprintf("{\"target\": %q} before editing", sym), Tier: TierHeuristic},
	}
}

// tierDistribution counts section-item tiers; contract.NormalizeConfidence
// rescales the counts and picks the deterministic top label.
func tierDistribution(sections []Section) map[string]float64 {
	dist := map[string]float64{}
	for _, sec := range sections {
		for _, it := range sec.Items {
			dist[string(it.Tier)]++
		}
	}
	if len(dist) == 0 {
		return map[string]float64{"unknown": 1}
	}
	return dist
}

// Markdown renders a deterministic Markdown view of the canonical Result,
// following the PRD section shape.
func Markdown(r *contract.Result) (string, error) {
	if r == nil {
		return "", fmt.Errorf("brief: result is nil")
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Agent Brief\n\n## Summary\n\n%s\n\n", r.Summary)
	if len(r.Items) == 0 {
		b.WriteString("_No items in this brief._\n")
		return strings.TrimSpace(b.String()), nil
	}

	// Group items by section title embedded in the reason.
	sections := map[string][]string{}
	var order []string
	for _, it := range r.Items {
		sec, rest := splitSection(it.Reason)
		if _, ok := sections[sec]; !ok {
			order = append(order, sec)
		}
		sections[sec] = append(sections[sec], rest)
	}
	for _, sec := range order {
		fmt.Fprintf(&b, "## %s\n\n", sec)
		for _, line := range sections[sec] {
			fmt.Fprintf(&b, "- %s\n", line)
		}
		b.WriteString("\n")
	}
	if r.Limits.Truncated {
		fmt.Fprintf(&b, "_Truncated: %d of %d items shown._\n", len(r.Items), r.Limits.TotalAvailable)
	}
	return strings.TrimSpace(b.String()), nil
}

func splitSection(s string) (string, string) {
	if i := strings.Index(s, ": "); i > 0 {
		return s[:i], s[i+2:]
	}
	return "General", s
}

// splitRef splits a "path:line" citation; a bare path yields line 1.
func splitRef(ref string) (string, int) {
	if i := strings.LastIndex(ref, ":"); i > 0 {
		var line int
		if _, err := fmt.Sscanf(ref[i+1:], "%d", &line); err == nil && line > 0 {
			return ref[:i], line
		}
	}
	return ref, 1
}
