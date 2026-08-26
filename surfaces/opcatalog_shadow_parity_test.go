package surfaces_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/opcatalog"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/coverage"
	"github.com/samibel/graphi/surfaces/client"
	httpsurface "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// SW-223 (AX-03) AC-3, second half — the shadow catalog vs the HTTP operation
// list and the coverage matrix.
//
// The MCP half of AC-3 lives in surfaces/mcp/opcatalog_parity_test.go, inside
// package mcp, because the descriptor builders are unexported. It cannot also
// hold these two checks: surfaces/http and internal/coverage BOTH import
// surfaces/mcp, so an in-package test importing them would be an import cycle.
// This external test package is the place where all three can be seen at once.
//
// Neither check is an equality. The three lists genuinely describe different
// things and pretending otherwise would produce a gate that has to be
// suppressed the first time it is right:
//
//   - HTTP exposes analyzers the MCP surface has no tool for (analyze/metrics,
//     analyze/communities, …) and omits operations that have no HTTP route
//     (savings, memory, graph_health, …). So the catalog declares the resource
//     each operation IS reachable at, and the unclaimed remainder is an
//     explicit, named allow-list — a new HTTP resource forces a decision rather
//     than sliding in.
//   - The coverage matrix's `mcp-tool` rows ARE one-to-one with ToolNames(),
//     which the coverage gate already enforces, so this checks the catalog
//     against them on both membership and tier.

func loadShadow(t *testing.T) *opcatalog.Catalog {
	t.Helper()
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("opcatalog.Shadow(): %v", err)
	}
	return catalog
}

// httpContractResources serves GET /contract through the production handler
// chain — the same path a real client negotiates over — and returns the
// resource list. Analyzer names come from the real analysis service, not a
// hand-copied list, so a new analyzer reaches this test automatically.
func httpContractResources(t *testing.T, labs bool) []string {
	t.Helper()
	value := ""
	if labs {
		value = "1"
	}
	t.Setenv(httpsurface.LabsEnvVar, value)

	store := graphstore.NewMemStore()
	t.Cleanup(func() { _ = store.Close() })
	direct := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
	srv := httpsurface.New(direct, nil).WithDescriptors(analysis.NewDefaultService(store).Names())

	req := httptest.NewRequest(http.MethodGet, "/contract", nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /contract: status %d, body %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Payload struct {
			Resources []string `json:"resources"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode /contract %q: %v", rec.Body.String(), err)
	}
	if len(envelope.Payload.Resources) == 0 {
		t.Fatalf("/contract advertised no resources: %s", rec.Body.String())
	}
	return envelope.Payload.Resources
}

// httpResourcesNotClaimedByAnyOperation is the explicit remainder: HTTP
// resources that exist for engine analyzers with no MCP tool of their own. They
// are listed here rather than skipped, so a NEW unclaimed resource fails this
// test and someone has to decide whether it belongs in the catalog.
var httpResourcesNotClaimedByAnyOperation = map[string]string{
	"analyze/batched":         "engine analyzer with no MCP tool: batched analyzer dispatch",
	"analyze/call-chain":      "engine analyzer with no MCP tool: call-chain resolution",
	"analyze/communities":     "engine analyzer with no MCP tool: Louvain community detection",
	"analyze/concept":         "engine analyzer with no MCP tool: concept resolution",
	"analyze/metrics":         "engine analyzer with no MCP tool: graph metrics",
	"analyze/notebook-ingest": "engine analyzer with no MCP tool: notebook ingest",
	"analyze/taint-query":     "engine analyzer with no MCP tool: taint query",
	"analyze/watcher-status":  "engine analyzer with no MCP tool: filesystem-watcher health",
}

// diffHTTPResources compares the catalog's declared HTTP resources against a
// live /contract resource list. wantEvery reports whether EVERY declared
// resource must be present (true for the Labs contract, which is the complete
// catalog) or only the Stable ones (the shipped default).
func diffHTTPResources(specs []opcatalog.OperationSpec, live []string, labs bool) []string {
	liveSet := make(map[string]bool, len(live))
	for _, resource := range live {
		liveSet[resource] = true
	}
	claimed := make(map[string]string, len(specs))
	var problems []string
	for _, spec := range specs {
		if spec.HTTPResource == "" {
			continue
		}
		if owner, dup := claimed[spec.HTTPResource]; dup {
			problems = append(problems, fmt.Sprintf(
				"HTTP resource %q is claimed by both %q and %q", spec.HTTPResource, owner, spec.ID))
		}
		claimed[spec.HTTPResource] = spec.ID
		mustBeAdvertised := labs || spec.Tier == opcatalog.TierStable
		switch {
		case mustBeAdvertised && !liveSet[spec.HTTPResource]:
			problems = append(problems, fmt.Sprintf(
				"%s: catalog declares HTTP resource %q, which the live /contract does not advertise",
				spec.ID, spec.HTTPResource))
		case !labs && spec.Tier == opcatalog.TierLabs && liveSet[spec.HTTPResource]:
			problems = append(problems, fmt.Sprintf(
				"%s: a Labs operation's resource %q is advertised by the SHIPPED DEFAULT contract",
				spec.ID, spec.HTTPResource))
		}
	}
	for _, resource := range live {
		if _, ok := claimed[resource]; ok {
			continue
		}
		if _, allowed := httpResourcesNotClaimedByAnyOperation[resource]; allowed {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"the HTTP contract advertises %q, which no catalog operation claims and which is not "+
				"in httpResourcesNotClaimedByAnyOperation — decide whether it is an operation",
			resource))
	}
	return problems
}

func TestAX03_ShadowCatalog_MatchesTheHTTPOperationList(t *testing.T) {
	specs := loadShadow(t).All()

	t.Run("labs", func(t *testing.T) {
		for _, problem := range diffHTTPResources(specs, httpContractResources(t, true), true) {
			t.Error(problem)
		}
	})
	t.Run("shipped-default", func(t *testing.T) {
		for _, problem := range diffHTTPResources(specs, httpContractResources(t, false), false) {
			t.Error(problem)
		}
	})
}

// The shipped default /contract must be exactly the catalog's Stable
// operations' HTTP resources — eleven of them. This is the strict half: it is
// an equality, because the default HTTP profile and the frozen Stable set are
// the same promise seen from two sides.
func TestAX03_ShadowCatalog_StableHTTPContractIsExact(t *testing.T) {
	catalog := loadShadow(t)
	var want []string
	for _, spec := range catalog.All() {
		if spec.Tier == opcatalog.TierStable && spec.HTTPResource != "" {
			want = append(want, spec.HTTPResource)
		}
	}
	sort.Strings(want)
	got := append([]string(nil), httpContractResources(t, false)...)
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("shipped-default /contract advertises %d resources %v; the catalog's Stable "+
			"operations declare %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("shipped-default /contract %v differs from the catalog's Stable resources %v", got, want)
		}
	}
}

// diffCoverageMatrix compares the catalog against the coverage matrix's
// mcp-tool rows on membership AND tier.
func diffCoverageMatrix(specs []opcatalog.OperationSpec, rows []coverage.Capability) []string {
	var problems []string
	byID := make(map[string]coverage.Capability, len(rows))
	for _, row := range rows {
		if _, dup := byID[row.ID]; dup {
			problems = append(problems, fmt.Sprintf("coverage matrix holds two mcp-tool rows for %q", row.ID))
			continue
		}
		byID[row.ID] = row
	}
	seen := make(map[string]bool, len(specs))
	for _, spec := range specs {
		seen[spec.ID] = true
		row, ok := byID[spec.ID]
		if !ok {
			problems = append(problems, fmt.Sprintf(
				"%s: catalog spec with no `category: mcp-tool` row in docs/coverage-matrix.yaml", spec.ID))
			continue
		}
		if row.Tier != string(spec.Tier) {
			problems = append(problems, fmt.Sprintf(
				"%s: catalog tier %q, coverage-matrix tier %q", spec.ID, spec.Tier, row.Tier))
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !seen[id] {
			problems = append(problems, fmt.Sprintf(
				"coverage matrix declares mcp-tool %q with no catalog spec", id))
		}
	}
	return problems
}

func mcpToolRows(t *testing.T) []coverage.Capability {
	t.Helper()
	root, err := coverage.ModuleRoot()
	if err != nil {
		t.Fatalf("coverage.ModuleRoot(): %v", err)
	}
	all, err := coverage.LoadMatrix(filepath.Join(root, "docs", "coverage-matrix.yaml"))
	if err != nil {
		t.Fatalf("coverage.LoadMatrix: %v", err)
	}
	var rows []coverage.Capability
	for _, row := range all {
		if row.Category == "mcp-tool" {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		t.Fatal("coverage matrix declares no mcp-tool rows")
	}
	return rows
}

func TestAX03_ShadowCatalog_MatchesTheCoverageMatrixMCPRows(t *testing.T) {
	for _, problem := range diffCoverageMatrix(loadShadow(t).All(), mcpToolRows(t)) {
		t.Error(problem)
	}
}

// The gate about the gate: the same comparisons, fed corrupted inputs, must
// report each drift class. Without this, a passing run says nothing about
// whether the comparison looks at anything.
func TestAX03_HTTPAndMatrixGates_DetectDrift(t *testing.T) {
	specs := loadShadow(t).All()
	labsResources := httpContractResources(t, true)
	rows := mcpToolRows(t)

	t.Run("a declared HTTP resource the contract does not serve", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		perturbed[0].HTTPResource = "analyze/invented"
		if got := diffHTTPResources(perturbed, labsResources, true); len(got) == 0 {
			t.Fatal("an HTTP resource nothing serves was not reported")
		}
	})
	t.Run("an unclaimed HTTP resource that is not allow-listed", func(t *testing.T) {
		if got := diffHTTPResources(specs, append(labsResources, "analyze/smuggled"), true); len(got) == 0 {
			t.Fatal("a new unclaimed HTTP resource was not reported")
		}
	})
	t.Run("two operations claiming one HTTP resource", func(t *testing.T) {
		perturbed := append([]opcatalog.OperationSpec(nil), specs...)
		var first string
		for i := range perturbed {
			if perturbed[i].HTTPResource == "" {
				continue
			}
			if first == "" {
				first = perturbed[i].HTTPResource
				continue
			}
			perturbed[i].HTTPResource = first
			break
		}
		if got := diffHTTPResources(perturbed, labsResources, true); len(got) == 0 {
			t.Fatal("two operations claiming one HTTP resource was not reported")
		}
	})
	t.Run("a matrix row with no catalog spec", func(t *testing.T) {
		perturbed := append([]coverage.Capability(nil), rows...)
		perturbed = append(perturbed, coverage.Capability{
			ID: "zz_phantom", Category: "mcp-tool", Status: "shipped", Tier: "labs",
		})
		if got := diffCoverageMatrix(specs, perturbed); len(got) == 0 {
			t.Fatal("a matrix row with no catalog spec was not reported")
		}
	})
	t.Run("a catalog spec with no matrix row", func(t *testing.T) {
		if got := diffCoverageMatrix(specs, rows[1:]); len(got) == 0 {
			t.Fatal("a catalog spec with no matrix row was not reported")
		}
	})
	t.Run("a tier that disagrees with the matrix", func(t *testing.T) {
		perturbed := append([]coverage.Capability(nil), rows...)
		for i := range perturbed {
			if perturbed[i].Tier == "labs" {
				perturbed[i].Tier = "stable"
				break
			}
		}
		if got := diffCoverageMatrix(specs, perturbed); len(got) == 0 {
			t.Fatal("a matrix tier disagreeing with the catalog was not reported")
		}
	})
	t.Run("a duplicate matrix row", func(t *testing.T) {
		if got := diffCoverageMatrix(specs, append(append([]coverage.Capability(nil), rows...), rows[0])); len(got) == 0 {
			t.Fatal("a duplicated matrix row was not reported")
		}
	})
}

// The catalog must never disagree with the frozen 12-op set about how many
// Stable operations exist. mcp.StableOperations stays the single source; this
// only asserts the mirror did not lose one.
func TestAX03_ShadowCatalog_HoldsElevenStableMCPOperations(t *testing.T) {
	got := loadShadow(t).IDsWithTier(opcatalog.TierStable)
	want := mcp.StableMCPToolNames()
	if len(got) != len(want) {
		t.Fatalf("catalog holds %d Stable operations %v, mcp.StableMCPToolNames() has %d %v",
			len(got), got, len(want), want)
	}
}
