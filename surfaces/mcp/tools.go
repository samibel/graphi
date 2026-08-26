package mcp

import (
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/query"
)

// Tool name constants are the SINGLE SOURCE OF TRUTH for the names of every MCP
// tool graphi's stdio surface can advertise. toolDescriptors() builds its
// JSON-RPC schemas from these constants (never from inline string literals), and
// ToolNames() exposes the canonical sorted set for the FU-4 capability coverage
// matrix drift guard (internal/coverage). This mirrors how query.Operations is
// the one place structural query names live and the descriptor loop iterates it —
// there is no second hand-maintained list to drift.
//
// The names here are wire-visible identifiers; changing one is a protocol change.
const (
	// Search / readout singletons.
	ToolSearch         = "search"
	ToolSearchSemantic = "search_semantic"
	ToolSavings        = "savings"
	// ToolImpact is the dedicated stable blast-radius analyzer. Unlike the Labs
	// generic analyzer dispatcher, callers cannot choose a different analyzer.
	ToolImpact = "impact"

	// Generic analyzer dispatch tool (SW-022).
	ToolAnalyze = "analyze"

	// EP-005 dedicated deep-analyzer tools (SW-033).
	ToolAnalyzeTaint       = "analyze_taint"
	ToolAnalyzePDG         = "analyze_pdg"
	ToolAnalyzeInterproc   = "analyze_interproc"
	ToolAnalyzeContracts   = "analyze_contracts"
	ToolAnalyzeGitHistory  = "analyze_githistory"
	ToolAnalyzePrRisk      = "analyze_pr_risk"
	ToolAnalyzePrSignals   = "analyze_pr_signals"
	ToolAnalyzePrQuestions = "analyze_pr_questions"

	// SW-038 edit/refactor command surface.
	ToolRefactorPreview = "refactor_preview"
	ToolRefactor        = "refactor"
	ToolUndo            = "undo"

	// SW-042 sticky PR-comment + merge-gate surface.
	ToolPrComment = "pr_comment"

	// ToolCompound runs a compound / Cypher-style graph query (EP-011 G1). It is
	// a singleton (not part of query.Operations) because its input is the query
	// text, not an op+symbol pair.
	ToolCompound = "compound"

	// SW-082 / SW-083 pattern-query singletons (input is a pattern/config, not an
	// op+symbol pair). Surface-exposed in SW-085.
	ToolSearchAST  = "search_ast"
	ToolFindClones = "find_clones"

	// EP-012 agent memory & skills.
	ToolMemory   = "memory"
	ToolDistill  = "distill"
	ToolSkillGen = "skillgen"

	// EP-018 multi-PR triage suite (SW-105). list_prs is the read-only forge
	// PR-enumeration tool (metadata only); triage_prs is the single-pass
	// graph-derived ranked-triage tool over the zero-egress engine analyzer.
	ToolListPRs   = "list_prs"
	ToolTriagePRs = "triage_prs"
	// SW-106 inter-PR conflict detection over the enumerated open-PR set.
	ToolConflictsPRs = "conflicts_prs"
	// SW-107 reviewer recommender (ranked candidates from local ownership/churn +
	// affected-subgraph proximity) and graph-level branch comparator (structured
	// diff keyed by canonical NodeId). Both are zero-egress engine analyzers.
	ToolSuggestReviewers = "suggest_reviewers"
	ToolCompareBranches  = "compare_branches"
	// SW-108 (EP-018 capstone) critique_review: deterministic graph-evidence critique
	// of an existing PR review (gap / over_flag / unsupported_claim). Zero-egress
	// engine analyzer; the only permitted egress is the surface review fetch.
	ToolCritiqueReview = "critique_review"

	// EP-024 agent-first task tool (SW-134).
	ToolAgentBrief = "agent_brief"

	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134).
	ToolExplainSymbol = "explain_symbol"
	ToolRelatedFiles  = "related_files"
	ToolChangeRisk    = "change_risk"

	// P1 trust surface (PRD §17, FR-3): the single Labs trust tool. It returns
	// the canonical contract §2 trust-report document through the shared
	// client.TrustReport composition, byte-identical to
	// `graphi trust-report --json`. Labs-only until a separate promotion
	// decision; the frozen Stable-12 set is untouched.
	ToolGraphHealth = "graph_health"

	// P1 strict query (PRD v1.0 §8 Phase 9): the Labs trust-aware query
	// wrapper. It runs a Stable query UNCHANGED, then withholds result edges
	// below the requested minimum confidence tier and reports how many it
	// withheld — so an agent can tell "no callers" from "no callers you asked
	// to see". Byte-identical to `graphi query-strict` through the shared
	// client.ComposeStrictQuery composition.
	//
	// The name was left open by PRD v1.0 §12 ("strict_query, trust_query oder
	// bestehendes Naming-Pattern?") and decided in the delta document §B2:
	// strict_query, the PRD's own prose spelling, matching the snake_case
	// tool-name convention (graph_health, explain_symbol, change_risk). The
	// CLI verb stays `query-strict` — CLI verbs are kebab-case here, and
	// renaming a shipped verb would be a gratuitous break.
	//
	// Labs-only. It adds no Stable operation and changes no Stable schema.
	ToolStrictQuery = "strict_query"

	// P0 agent intelligence: the unified single-call symbol view (definition
	// with optional token-budgeted snippet, hierarchy, callers/callees/
	// references, covering tests, risk level). Labs-only; the frozen Stable-12
	// set is untouched.
	ToolSymbolContext = "symbol_context"

	// P0 agent intelligence: free-text task → deterministically ranked,
	// token-budgeted context bundle (integer weight model, hash-stamped).
	// Labs-only.
	ToolTaskContext = "task_context"

	// P0 agent intelligence: the one-call "what is this repository" summary
	// from the compact graph aggregates (communities opt-in). Labs-only.
	ToolRepoOverview = "repo_overview"

	// P1 test intelligence: diff/target → must-run, recommended, and
	// probably-unaffected test buckets from the derived symbol↔test mapping.
	// Labs-only.
	ToolTestImpact = "test_impact"

	// P1 change intelligence ("Change Risk 2.0"): changed symbols, public-API
	// subset, direct/transitive dependents, covering tests, config changes,
	// explicit reasons, risk level. The frozen-stable change_risk operation
	// is untouched. Labs-only.
	ToolChangeImpact = "change_impact"

	// P2 git intelligence: churn × dependency-centrality hotspot ranking with
	// bus-factor warnings, over the bounded surface-boundary git provider.
	// Labs-only.
	ToolHotspots = "hotspots"

	// P3 repository search: embedding-free hybrid ranking (identifier
	// segments, path relevance, bounded graph degree). Labs-only.
	ToolSearchHybrid = "search_hybrid"

	// P2 architecture intelligence: the automatic community/layer view of the
	// graph — Louvain communities labeled by dominant package prefix, layered
	// by dependency direction. Labs-only.
	ToolArchitecture = "architecture"

	// P2 architecture intelligence: cycles, edges against the dominant
	// dependency direction, high-coupling pairs, and god modules on the
	// community graph. Labs-only.
	ToolArchitectureViolations = "architecture_violations"

	// P2 dead code: scored dead-code candidates with explicit exclusion
	// reasons, over the EP-015 dead_symbol diagnostic. Labs-only.
	ToolDeadCode = "dead_code"

	// P3 framework intelligence: the application-level view derived from
	// recorded framework annotations/decorators (routes, event handlers,
	// injections, components, configuration). Labs-only.
	ToolFrameworkMap = "framework_map"
)

// singletonToolNames are the non-structural-query tools in the maximal catalog.
// They are listed once here and consumed by both ToolNames() and descriptor
// construction so the canonical set cannot drift from what can be served.
//
// SW-225 (AX-05) gave this list a SECOND job: its ORDER is the advertised order
// of the singleton tools in tools/list. Descriptor bodies are now projected from
// engine/opcatalog, which is id-sorted by construction and therefore carries no
// advertisement order — but the order of the tools/list array is wire-observable
// and frozen by the AX-00 goldens. Rather than hand-maintain a second ordered
// name list beside this one, the projection reads THIS list (query.Operations
// first, then these), and the Stable profile's order is this same sequence
// filtered by IsStableMCPTool. ToolNames() sorts, so it is unaffected by the
// order here; changing it changes tools/list and moves an AX-00 golden.
var singletonToolNames = []string{
	ToolSearch,
	ToolSearchSemantic,
	ToolSearchAST,
	ToolFindClones,
	ToolSavings,
	ToolImpact,
	ToolAnalyze,
	ToolAnalyzeTaint,
	ToolAnalyzePDG,
	ToolAnalyzeInterproc,
	ToolAnalyzeContracts,
	ToolAnalyzeGitHistory,
	ToolAnalyzePrRisk,
	ToolAnalyzePrSignals,
	ToolAnalyzePrQuestions,
	ToolRefactorPreview,
	ToolRefactor,
	ToolUndo,
	ToolPrComment,
	ToolCompound,
	ToolMemory,
	ToolDistill,
	ToolSkillGen,
	ToolListPRs,
	ToolTriagePRs,
	ToolConflictsPRs,
	ToolSuggestReviewers,
	ToolCompareBranches,
	ToolCritiqueReview,
	ToolExplainSymbol,
	ToolRelatedFiles,
	ToolChangeRisk,
	ToolAgentBrief,
	ToolGraphHealth,
	ToolStrictQuery,
	ToolSymbolContext,
	ToolTaskContext,
	ToolRepoOverview,
	ToolTestImpact,
	ToolChangeImpact,
	ToolHotspots,
	ToolSearchHybrid,
	ToolArchitecture,
	ToolArchitectureViolations,
	ToolDeadCode,
	ToolFrameworkMap,
}

// StableOperations is the frozen SCOPE-01 (SW-111) set of graphi's 12 STABLE
// product operations — the exact capabilities graphi commits to as stable
// product surface. It is the SINGLE CODE source of the stability tier: the
// coverage matrix's `tier: stable` rows are cross-checked against this set by
// internal/coverage, MCP tool descriptions mark every advertised tool OUTSIDE
// this set `[labs]` (markLabs), and CLI help groups Stable vs Labs from it — so
// dispatch, the MCP tool list, CLI help, the coverage matrix, and the generated
// docs cannot disagree about what is stable.
//
// The set spans surfaces: `index` is the ingest lifecycle operation and is the
// only member not exposed as an MCP tool; `impact` is the stable analyzer behind
// a dedicated MCP tool whose analyzer name cannot be selected by the caller.
// Membership is by operation NAME (underscore spelling); the wire-level
// tool-name constants are unchanged. Freezing this set is the whole point of
// SCOPE-01 — adding a 13th or dropping one fails the coverage-matrix build. Keep
// it sorted.
var StableOperations = []string{
	"agent_brief",
	"callees",
	"callers",
	"change_risk",
	"definition",
	"explain_symbol",
	"impact",
	"index",
	"neighborhood",
	"references",
	"related_files",
	"search",
}

// stableOperationSet is the O(1) membership view of StableOperations.
var stableOperationSet = func() map[string]bool {
	m := make(map[string]bool, len(StableOperations))
	for _, op := range StableOperations {
		m[op] = true
	}
	return m
}()

// IsStableOperation reports whether name is one of the 12 frozen stable
// operations (SCOPE-01). It is the single membership check every surface uses to
// decide Stable vs Labs, so the taxonomy cannot diverge across MCP, CLI, and the
// coverage matrix.
func IsStableOperation(name string) bool { return stableOperationSet[name] }

// StableMCPToolNames returns the exact default MCP profile: every frozen stable
// operation except index, whose lifecycle is repository ingest rather than an
// MCP tools/call operation. The result is sorted and freshly allocated.
func StableMCPToolNames() []string {
	out := make([]string, 0, len(StableOperations)-1)
	for _, op := range StableOperations {
		if op != "index" {
			out = append(out, op)
		}
	}
	return out
}

// IsStableMCPTool reports membership in the default MCP profile. Keep the
// lifecycle-only exclusion centralized here so advertisement and dispatch use
// the same boundary.
func IsStableMCPTool(name string) bool {
	return name != "index" && IsStableOperation(name)
}

// labsPrefix marks a tool description as belonging to the Labs stability tier
// (SCOPE-01): kept in-tree and still advertised, but NOT part of the frozen
// 12-op stable product surface. Tool NAMES are frozen wire identifiers and never
// change; the tier marker lives in the human/agent-facing description only.
const labsPrefix = "[labs] "

// markLabs prefixes the description of every advertised tool that is NOT a stable
// operation (SCOPE-01) with labsPrefix, in place, and returns the slice. The
// stable 12 are left unmarked. Idempotent: an already-prefixed description is
// left alone. This makes the Stable/Labs taxonomy visible in the MCP tools/list
// descriptions without touching the frozen tool-name constants.
func markLabs(tools []map[string]any) []map[string]any {
	for _, t := range tools {
		name, _ := t["name"].(string)
		if IsStableOperation(name) {
			continue
		}
		if d, ok := t["description"].(string); ok && !strings.HasPrefix(d, labsPrefix) {
			t["description"] = labsPrefix + d
		}
	}
	return tools
}

// ToolNames returns the full, sorted, de-duplicated canonical set of every MCP
// tool the stdio surface can advertise across all wired capabilities: the
// structural query operations (query.Operations) plus the search/readout,
// generic + deep analyzer, edit, and PR-comment tools. The live tools/list
// response is a capability-gated SUBSET of this set (a tool is advertised only
// when its backing service is wired); this returns the maximal union, which is
// the capability surface the coverage matrix tracks. The result is a fresh slice
// the caller may mutate.
func ToolNames() []string {
	out := make([]string, 0, len(query.Operations)+len(singletonToolNames))
	out = append(out, query.Operations...)
	out = append(out, singletonToolNames...)
	sort.Strings(out)
	return dedupeSorted(out)
}

// readOnlyToolAnnotations returns the MCP tool-annotation set for a pure
// read-only, deterministic query tool (SW-085 AC4): it never mutates state
// (readOnlyHint / !destructiveHint), the same arguments always yield the same
// bytes (idempotentHint), and it touches no external/open world (!openWorldHint).
// The three new pattern-query tools all share this set.
func readOnlyToolAnnotations() map[string]any {
	return map[string]any{
		"readOnlyHint":    true,
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   false,
	}
}

// dedupeSorted removes adjacent duplicates from a sorted slice in place-ish,
// returning the compacted prefix. (query.Operations and the singletons are
// disjoint today; dedupe keeps ToolNames() robust if that ever changes.)
func dedupeSorted(s []string) []string {
	if len(s) < 2 {
		return s
	}
	w := 1
	for i := 1; i < len(s); i++ {
		if s[i] != s[w-1] {
			s[w] = s[i]
			w++
		}
	}
	return s[:w]
}
