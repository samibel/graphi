package mcp

import (
	"sort"

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
)

// singletonToolNames are the non-structural-query tools advertised behind a
// capability probe. They are listed once here and consumed by both ToolNames()
// and toolDescriptors() so the canonical set cannot drift from what is served.
var singletonToolNames = []string{
	ToolSearch,
	ToolSearchSemantic,
	ToolSavings,
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
	ToolSearchAST,
	ToolFindClones,
	ToolMemory,
	ToolDistill,
	ToolSkillGen,
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
