package mcp

import (
	"reflect"
	"testing"
)

// TestCharacterization_ToolNames_Snapshot is the SW-110 (TEST-01) AC3 snapshot of
// the MCP tool surface: it pins the exact, sorted, canonical set of tool names the
// stdio surface can advertise (ToolNames()) as of the characterization baseline.
//
// It is intentionally a full-set equality against a frozen literal so that any
// SCOPE-01 (the Stable/Labs/Disabled tiering) or SAFE-01 (fail-closing the
// dangerous ones) change to the advertised surface shows up here as a REVIEWED
// diff — a tool added, removed, or renamed cannot slip through unnoticed. Tool
// names are frozen wire identifiers (see tools.go), so a diff here is a protocol
// change and must be deliberate.
func TestCharacterization_ToolNames_Snapshot(t *testing.T) {
	want := []string{
		"agent_brief",
		"analyze",
		"analyze_contracts",
		"analyze_githistory",
		"analyze_interproc",
		"analyze_pdg",
		"analyze_pr_questions",
		"analyze_pr_risk",
		"analyze_pr_signals",
		"analyze_taint",
		"callees",
		"callers",
		"change_risk",
		"compare_branches",
		"compound",
		"conflicts_prs",
		"critique_review",
		"definition",
		"distill",
		"explain_symbol",
		"find_clones",
		"implementers",
		"implements",
		"list_prs",
		"memory",
		"neighborhood",
		"overrides",
		"pr_comment",
		"refactor",
		"refactor_preview",
		"references",
		"related_files",
		"savings",
		"search",
		"search_ast",
		"search_semantic",
		"skillgen",
		"subtypes",
		"suggest_reviewers",
		"supertypes",
		"triage_prs",
		"undo",
	}
	got := ToolNames()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MCP ToolNames() snapshot drifted (intentional scope change? update this baseline):\n got  = %#v\n want = %#v", got, want)
	}
}
