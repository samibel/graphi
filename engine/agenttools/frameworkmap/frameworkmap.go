// Package frameworkmap implements the framework_map agent tool (labs, P3
// framework intelligence): the application-level view derived from the
// framework annotations and decorators the parsers already record — HTTP
// routes, event handlers, injection points, DI-managed components, and
// configuration units. It turns the pure code graph into the beginning of an
// APPLICATION graph without any new parsing, materialized edges, or LLM
// guessing: every fact cites the annotation and the definition site.
//
// Providers are deterministic annotation→category tables selected by source
// language: spring (Java/Kotlin annotations), nest (TypeScript/JavaScript
// decorators — NestJS and Angular), and dotnet (C# attributes). Languages
// whose parsers record no annotation metadata (Go, Python) honestly yield no
// facts — the empty outcome says so instead of pretending coverage.
//
// Cost model: one node catalog read (facts are a pure function of node meta),
// no edge reads, no hydration.
package frameworkmap

import (
	"context"
	"fmt"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
	pathclass "github.com/samibel/graphi/engine/classify"
)

const tool = "framework_map"

// MethodVersion stamps the derivation logic version into the summary.
const MethodVersion = "framework_map/1"

// Rank bands (rank = band<<20 + score, score < 1<<20).
const (
	bandIdentity   = 10
	bandRoutes     = 8
	bandEvents     = 6
	bandInjections = 5
	bandComponents = 4
	bandConfig     = 3
	bandNext       = 1
)

// Section caps and defaults.
const (
	DefaultMaxItems = 60
	routeRows       = 20
	eventRows       = 12
	injectionRows   = 12
	componentRows   = 15
	configRows      = 10
)

// Fact categories (the application-graph vocabulary).
const (
	catRoute     = "http_route"
	catEvent     = "handles_event"
	catInjection = "injects"
	catComponent = "component"
	catConfig    = "configures"
)

// annotationFact maps one recorded annotation to a category and a detail
// label (e.g. the HTTP verb).
type annotationFact struct {
	category string
	detail   string
}

// provider is a deterministic annotation→fact table for one framework family.
type provider struct {
	name  string
	table map[string]annotationFact
}

// providers, selected by pathclass.Language of the defining source file. The
// table entries mirror the annotation names the extractors record (the bare
// token after '@' / the decorator identifier / the attribute name).
var providersByLanguage = map[string]provider{
	"Java":   springProvider,
	"Kotlin": springProvider,

	"TypeScript": nestProvider,
	"JavaScript": nestProvider,

	"C#": dotnetProvider,
}

var springProvider = provider{name: "spring", table: map[string]annotationFact{
	"RequestMapping": {catRoute, "route"},
	"GetMapping":     {catRoute, "GET"},
	"PostMapping":    {catRoute, "POST"},
	"PutMapping":     {catRoute, "PUT"},
	"DeleteMapping":  {catRoute, "DELETE"},
	"PatchMapping":   {catRoute, "PATCH"},

	"EventListener":              {catEvent, "event"},
	"TransactionalEventListener": {catEvent, "event"},
	"KafkaListener":              {catEvent, "kafka"},
	"RabbitListener":             {catEvent, "rabbit"},
	"Scheduled":                  {catEvent, "scheduled"},

	"Autowired": {catInjection, "autowired"},
	"Inject":    {catInjection, "inject"},

	"RestController": {catComponent, "rest-controller"},
	"Controller":     {catComponent, "controller"},
	"Service":        {catComponent, "service"},
	"Repository":     {catComponent, "repository"},
	"Component":      {catComponent, "component"},

	"Configuration": {catConfig, "configuration"},
	"Bean":          {catConfig, "bean"},
}}

var nestProvider = provider{name: "nest", table: map[string]annotationFact{
	"Get":     {catRoute, "GET"},
	"Post":    {catRoute, "POST"},
	"Put":     {catRoute, "PUT"},
	"Delete":  {catRoute, "DELETE"},
	"Patch":   {catRoute, "PATCH"},
	"Head":    {catRoute, "HEAD"},
	"Options": {catRoute, "OPTIONS"},
	"All":     {catRoute, "ALL"},

	"EventPattern":   {catEvent, "event"},
	"MessagePattern": {catEvent, "message"},
	"OnEvent":        {catEvent, "event"},
	"Cron":           {catEvent, "scheduled"},

	"Inject": {catInjection, "inject"},

	"Controller": {catComponent, "controller"},
	"Injectable": {catComponent, "injectable"},
	"Component":  {catComponent, "component"}, // Angular
	"Directive":  {catComponent, "directive"}, // Angular
	"Pipe":       {catComponent, "pipe"},      // Angular

	"Module":   {catConfig, "module"},
	"NgModule": {catConfig, "module"},
}}

var dotnetProvider = provider{name: "dotnet", table: map[string]annotationFact{
	"HttpGet":    {catRoute, "GET"},
	"HttpPost":   {catRoute, "POST"},
	"HttpPut":    {catRoute, "PUT"},
	"HttpDelete": {catRoute, "DELETE"},
	"HttpPatch":  {catRoute, "PATCH"},
	"Route":      {catRoute, "route"},

	"FromServices": {catInjection, "from-services"},

	"ApiController": {catComponent, "api-controller"},
	"Controller":    {catComponent, "controller"},
}}

// Params carries the framework_map inputs.
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

// fact is one derived application-graph fact.
type fact struct {
	node       model.Node
	provider   string
	annotation string
	category   string
	detail     string
}

// Assemble derives the application-level framework view from recorded node
// annotations.
func Assemble(ctx context.Context, p Params) (*contract.Result, error) {
	if !p.Deps.Available() {
		return shape.Unavailable(tool), nil
	}
	reader := p.Deps.Query.Reader()
	nodes, err := reader.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
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

	var facts []fact
	activeProviders := map[string]struct{}{}
	annotated := 0
	for _, n := range nodes {
		annos := n.Meta().Annotations
		if len(annos) == 0 {
			continue
		}
		annotated++
		prov, ok := providersByLanguage[pathclass.Language(n.SourcePath())]
		if !ok {
			continue
		}
		for _, a := range annos {
			f, ok := prov.table[a]
			if !ok {
				continue
			}
			activeProviders[prov.name] = struct{}{}
			facts = append(facts, fact{node: n, provider: prov.name, annotation: a, category: f.category, detail: f.detail})
		}
	}

	if len(facts) == 0 {
		return &contract.Result{
			Outcome: contract.OutcomeEmpty,
			Summary: fmt.Sprintf("%s: no framework annotations mapped — %d annotated symbol(s) in the graph, providers spring/nest/dotnet cover Java, Kotlin, TypeScript, JavaScript, and C# (Go and Python sources carry no annotation metadata) (%s)",
				tool, annotated, MethodVersion),
			Confidence: contract.Confidence{
				Distribution: map[string]float64{"unknown": 1},
				Top:          "unknown",
				Method:       "no_framework_annotations",
			},
		}, nil
	}

	// Deterministic order: by source position, then annotation.
	sort.Slice(facts, func(i, j int) bool {
		a, b := facts[i], facts[j]
		if a.node.SourcePath() != b.node.SourcePath() {
			return a.node.SourcePath() < b.node.SourcePath()
		}
		if a.node.Line() != b.node.Line() {
			return a.node.Line() < b.node.Line()
		}
		return a.annotation < b.annotation
	})

	ev := shape.NewEvidenceSet()
	var items []contract.Item

	counts := map[string]int{}
	for _, f := range facts {
		counts[f.category]++
	}

	provNames := make([]string, 0, len(activeProviders))
	for name := range activeProviders {
		provNames = append(provNames, name)
	}
	sort.Strings(provNames)

	items = append(items, contract.Item{
		RefID: "identity",
		Rank:  bandIdentity << 20,
		Reason: fmt.Sprintf("framework_map: %d fact(s) from provider(s) %v — %d route(s), %d event handler(s), %d injection(s), %d component(s), %d configuration unit(s)",
			len(facts), provNames, counts[catRoute], counts[catEvent], counts[catInjection], counts[catComponent], counts[catConfig]),
	})

	emit := func(category, label string, band, cap int) {
		emitted := 0
		for _, f := range facts {
			if f.category != category {
				continue
			}
			if emitted >= cap {
				break
			}
			evID := ev.Add(f.node.SourcePath(), f.node.Line(), category)
			items = append(items, contract.Item{
				RefID:          fmt.Sprintf("%s:%s:%s", category, f.node.ID(), f.annotation),
				Rank:           band<<20 + clampScore(cap-emitted),
				Reason:         fmt.Sprintf("%s: %s %s (%s:%d) — @%s [%s, %s]", label, f.node.Kind(), f.node.QualifiedName(), f.node.SourcePath(), f.node.Line(), f.annotation, f.provider, f.detail),
				EvidenceRefIDs: []string{evID},
			})
			emitted++
		}
	}
	emit(catRoute, "route", bandRoutes, routeRows)
	emit(catEvent, "event", bandEvents, eventRows)
	emit(catInjection, "injection", bandInjections, injectionRows)
	emit(catComponent, "component", bandComponents, componentRows)
	emit(catConfig, "config", bandConfig, configRows)

	items = append(items, contract.Item{
		RefID:  "next-1",
		Rank:   bandNext<<20 + 1,
		Reason: "next: graphi symbol-context <route handler> — the unified view of one endpoint",
	})

	r := &contract.Result{
		Outcome: contract.OutcomeFound,
		Summary: fmt.Sprintf("framework_map: %d fact(s) — %d route(s), %d event handler(s), %d component(s) via %v (%s)",
			len(facts), counts[catRoute], counts[catEvent], counts[catComponent], provNames, MethodVersion),
		Items:    items,
		Evidence: ev.List(),
		Confidence: contract.Confidence{
			// Facts are read verbatim from parser-recorded annotations.
			Distribution: map[string]float64{"confirmed": 1},
			Top:          "confirmed",
			Method:       "recorded_annotations",
		},
	}
	return shape.Finish(r, p.maxItems())
}
