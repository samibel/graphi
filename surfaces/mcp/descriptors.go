package mcp

import (
	"fmt"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/surfaces/client"
)

// deepAnalyzerTools maps dedicated EP-005 MCP tool names → their analysis
// dispatcher name so each tool name routes through analysisCall after injecting
// the correct analyzer. The map is package-level so both toolsCall routing and
// toolDescriptors advertising can share a single source of truth.
var deepAnalyzerTools = map[string]string{
	ToolAnalyzeTaint:       "taint",
	ToolAnalyzePDG:         "pdg",
	ToolAnalyzeInterproc:   "interproc",
	ToolAnalyzeContracts:   "contracts",
	ToolAnalyzeGitHistory:  "git-history",
	ToolAnalyzePrRisk:      "pr-risk",
	ToolAnalyzePrSignals:   "pr-signals",
	ToolAnalyzePrQuestions: "pr-questions",
}

// deepAnalyzerDescriptors defines the MCP tool schema for each EP-005 deep
// analyzer. Each entry is appended verbatim to the tools/list response when
// the analysis service is available.
var deepAnalyzerDescriptors = []map[string]any{
	{
		"name":        ToolAnalyzeTaint,
		"description": "flow-sensitive taint analysis: finds source-to-sink data-flow paths through the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "traversal direction: reverse (dependents/blast radius — the default) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePDG,
		"description": "program dependence graph: computes data-dependence and control-dependence edges via reaching-definitions and post-dominance",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeInterproc,
		"description": "interprocedural analysis: Sharir-Pnueli fixpoint solver that computes procedure summaries over the call graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeContracts,
		"description": "contract drift detection: finds producer/consumer contracts and detects structural drift between linked API surfaces",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzeGitHistory,
		"description": "git-history signal analysis: computes churn scores, bus-factor risks, and co-change groups from commit history",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
	},
	{
		"name":        ToolAnalyzePrRisk,
		"description": "risk-scored PR diff (SW-039): maps changed nodes onto the graph and combines EP-004 impact with EP-005 taint signals into a deterministic, versioned per-region risk record. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrSignals,
		"description": "hub/bridge/surprise graph signals on PR-changed code (SW-040): annotates each changed node with hub (high fan-in/out over a configurable threshold), bridge (articulation point / cut-vertex between modules), and surprise (rarely-modified or unexpectedly-coupled region) signals. Consumes EP-004 metrics + EP-005 PDG/git-history; never recomputes centrality. Local-first: diff is a unified-diff string or simple ref form; NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
	{
		"name":        ToolAnalyzePrQuestions,
		"description": "deterministic, no-LLM reviewer questions from graph findings on PR-changed code (SW-041): applies a fixed rule/template set to the consumed SW-039 risk scores and SW-040 hub/bridge/surprise signals to emit targeted reviewer questions. Each question carries a non-empty evidence reference to the triggering node/edge/signal; identical input yields byte-identical output. Consumes the two sibling reports; never recomputes scoring or signals. Local-first: diff is a unified-diff string or simple ref form; NO LLM, NO remote fetch.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":       map[string]any{"type": "string", "description": "local-first PR diff: a unified-diff string or simple ref form (path:name / path#Lline / bare node id, one per line). No remote fetch."},
				"provenance": map[string]any{"type": "string", "description": "evidence redaction level: full (default) | summary"},
			},
			"required": []string{"diff"},
		},
	},
}

// toolDescriptors returns the immutable catalog for the current repository
// binding and selected profile. The cache also gives tools/call the exact same
// allow-list as tools/list without executing client operations during discovery.
func (s *Server) toolDescriptors() []map[string]any {
	binding := s.bound.Load()
	if binding == nil {
		return nil
	}
	s.catalogMu.Lock()
	defer s.catalogMu.Unlock()
	if s.catalogBinding == binding && s.catalog != nil {
		return s.catalog
	}
	var tools []map[string]any
	if s.labs {
		tools = maximalToolDescriptors()
	} else {
		tools = stableToolDescriptors()
	}
	tools = filterSupportedToolDescriptors(binding.client, tools)
	s.catalogBinding = binding
	s.catalog = tools
	return tools
}

// filterSupportedToolDescriptors applies the bound client's optional,
// side-effect-free capability report after the Stable/Labs profile has built
// its normal catalog. This is a second, binding-specific boundary: profile
// membership says whether graphi promises/allows a tool, while capability
// reporting says whether this concrete transport can actually execute it.
// Clients without a reporter retain the historical catalog contract.
func filterSupportedToolDescriptors(c client.Client, tools []map[string]any) []map[string]any {
	reporter, ok := c.(client.CapabilityReporter)
	if !ok {
		return tools
	}
	filtered := make([]map[string]any, 0, len(tools))
	for _, descriptor := range tools {
		name, ok := descriptor["name"].(string)
		if !ok || name == "" || !reporter.SupportsCapability(name) {
			continue
		}
		filtered = append(filtered, descriptor)
	}
	return filtered
}

// toolAdvertised is the dispatch-side half of the profile boundary: a caller
// cannot invoke a tool omitted from this binding's tools/list response.
func (s *Server) toolAdvertised(name string) bool {
	for _, descriptor := range s.toolDescriptors() {
		if descriptor["name"] == name {
			return true
		}
	}
	return false
}

// stableToolDescriptors is deliberately static and side-effect free. The
// shipped Runtime wires every stable port, so its default profile is exactly
// StableOperations minus lifecycle-only index; partially wired bindings are
// narrowed later through CapabilityReporter.
func stableToolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(StableOperations)-1)
	for _, op := range query.Operations {
		if !IsStableMCPTool(op) {
			continue
		}
		props := map[string]any{
			"symbol": map[string]any{"type": "string", "description": "symbol (node) id to query"},
		}
		if op == query.OpNeighborhood {
			props["depth"] = map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("hop depth (clamped to MaxNeighborhoodDepth=%d)", query.MaxNeighborhoodDepth),
			}
		}
		tools = append(tools, map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": []string{"symbol"}},
			"annotations": readOnlyToolAnnotations(),
		})
	}
	tools = append(tools,
		map[string]any{
			"name":        ToolSearch,
			"description": "lexical and symbol search over the indexed graph",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "search query (symbol token or free-text)"},
					"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
				},
				"required": []string{"symbol"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		impactToolDescriptor(),
		map[string]any{
			"name":        ToolExplainSymbol,
			"description": "return a compact, cited symbol identity and immediate neighborhood",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "qualified id, file:line anchor, or bare name"},
					"limit":  map[string]any{"type": "integer", "description": "maximum returned items"},
				},
				"required": []string{"symbol"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolRelatedFiles,
			"description": "return a deterministically ranked read-first file list around an anchor",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target":    map[string]any{"type": "string", "description": "symbol id, file path, or diff anchor"},
					"direction": map[string]any{"type": "string", "description": "dependencies | dependents | both"},
					"limit":     map[string]any{"type": "integer", "description": "maximum returned files"},
				},
				"required": []string{"target"},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolChangeRisk,
			"description": "return an evidence-based change-risk assessment for a target or diff",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target": map[string]any{"type": "string", "description": "symbol id or file path"},
					"diff":   map[string]any{"type": "string", "description": "unified diff or line-oriented refs"},
					"limit":  map[string]any{"type": "integer", "description": "maximum returned items"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		},
		map[string]any{
			"name":        ToolAgentBrief,
			"description": "return a bounded, cited task-start context packet",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{"type": "string", "description": "optional topic: symbol, path, or subsystem"},
				},
			},
			"annotations": readOnlyToolAnnotations(),
		},
	)
	return tools
}

func impactToolDescriptor() map[string]any {
	return map[string]any{
		"name":        ToolImpact,
		"description": "stable impact analysis: traverse forward dependencies or reverse dependents/blast radius",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "reverse (default blast radius) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget (0 = analyzer default)"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	}
}

// maximalToolDescriptors builds the complete Stable+Labs descriptor registry
// without consulting the bound client. It must remain pure: tools/list is
// discovery, not permission to dial a daemon, auto-start a process, enumerate a
// forge, or execute an analyzer. toolDescriptors applies the optional, pure
// CapabilityReporter filter after this registry is complete. A third-party
// Client without that optional reporter retains the full Client-contract Labs
// catalog for backwards compatibility.
func maximalToolDescriptors() []map[string]any {
	tools := make([]map[string]any, 0, len(query.Operations)+2)
	for _, op := range query.Operations {
		props := map[string]any{
			"symbol": map[string]any{"type": "string", "description": "symbol (node) id to query"},
		}
		required := []string{"symbol"}
		if op == query.OpNeighborhood {
			props["depth"] = map[string]any{
				"type":        "integer",
				"description": fmt.Sprintf("hop depth (clamped to MaxNeighborhoodDepth=%d)", query.MaxNeighborhoodDepth),
			}
		}
		tools = append(tools, map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		})
	}
	tools = append(tools, map[string]any{
		"name":        ToolSearch,
		"description": "lexical and symbol search over the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "search query (symbol token or free-text)"},
				"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
			},
			"required": []string{"symbol"},
		},
	})
	// Optional semantic search (SW-059). Advertised whenever the search tool is —
	// it is always callable through the client and cleanly reports "unavailable"
	// (typed graceful-skip) when no embedder is configured.
	tools = append(tools, map[string]any{
		"name":        ToolSearchSemantic,
		"description": "optional semantic (embedding) search over the indexed graph; reports 'unavailable' cleanly when no embedder is configured (OFF by default)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "semantic search query (free-text)"},
				"depth":  map[string]any{"type": "integer", "description": "maximum number of results (default 100)"},
			},
			"required": []string{"symbol"},
		},
	})
	// SW-085 pattern-query tools. They ride the in-process query.Service and reuse
	// the canonical engine serializers; CapabilityReporter independently filters
	// them from bindings without that service. Per AC4 they carry the explicit
	// annotation set: read-only, idempotent, non-destructive, closed-world.
	tools = append(tools, map[string]any{
		"name":        ToolSearchAST,
		"description": "structural AST pattern search (SW-082): match nodes by kind/name/parent_kind; returns node identity + parent context only, never a file body",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{"type": "string", "description": "JSON AstPattern, e.g. {\"kind\":\"function\",\"name\":{\"regex\":\"^handle_\"}}"},
				"limit":   map[string]any{"type": "integer", "description": "maximum number of matches (applied after the canonical sort)"},
			},
			"required": []string{"pattern"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolFindClones,
		"description": "clone-group detection (SW-083): reports exact/renamed/structural clone groups derived from the AST edge sets; deterministic and bounded by max_groups",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"config": map[string]any{"type": "string", "description": "optional JSON CloneConfig (threshold, max_groups, clone_kinds, min_edges); empty uses engine defaults"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// Savings readout (SW-020). Binding-specific availability is filtered later.
	tools = append(tools, map[string]any{
		"name":        ToolSavings,
		"description": "token-savings ledger readout: per-call / per-session / cumulative USD with anti-gaming cap flags",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})
	// Analyzers (SW-022). Binding-specific availability is filtered later.
	tools = append(tools, impactToolDescriptor(), map[string]any{
		"name":        ToolAnalyze,
		"description": "run a named graph analyzer (e.g. impact forward/reverse blast-radius reachability) over the indexed graph",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"analyzer":  map[string]any{"type": "string", "description": "analyzer name (e.g. impact)"},
				"symbol":    map[string]any{"type": "string", "description": "symbol (node) id to analyze"},
				"direction": map[string]any{"type": "string", "description": "traversal direction for directional analyzers: reverse (dependents/blast radius — the default) | forward (dependencies)"},
				"max_nodes": map[string]any{"type": "integer", "description": "output budget on reached nodes (0 = analyzer default)"},
			},
			"required": []string{"analyzer", "symbol"},
		},
	})
	// EP-005 (SW-033): include one dedicated tool per deep analyzer.
	tools = append(tools, deepAnalyzerDescriptors...)
	// SW-038 edit/refactor command surface.
	tools = append(tools, editToolDescriptors...)
	// SW-042 sticky PR-comment + merge-gate surface.
	tools = append(tools, map[string]any{
		"name":        ToolPrComment,
		"description": "render the assembled PR-review findings (risk + hub/bridge/surprise signals + reviewer questions) into one sticky Markdown comment and evaluate the optional risk-threshold merge gate; offline dry-run by default",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff":           map[string]any{"type": "string", "description": "local-first unified-diff or simple ref string (required)"},
				"pr":             map[string]any{"type": "string", "description": "PR reference rendered in the comment header (e.g. owner/repo#42)"},
				"provenance":     map[string]any{"type": "string", "description": "evidence redaction level: summary (default; safe for public comments) | full"},
				"gate_enabled":   map[string]any{"type": "boolean", "description": "enable the optional risk-threshold merge gate"},
				"gate_threshold": map[string]any{"type": "integer", "description": "risk threshold in fixed-point units (1/1000) the worst region must EXCEED to BLOCK (default 700)"},
				"publish":        map[string]any{"type": "boolean", "description": "upsert the sticky comment through the host (default false: offline dry-run, render+gate only)"},
			},
			"required": []string{"diff"},
		},
	})
	// EP-011 G1 compound query (singleton descriptor; input is query text).
	tools = append(tools, map[string]any{
		"name":        ToolCompound,
		"description": "compound / Cypher-style graph query composing traversals, filters, and projections in one request (SEED/HOP/WHERE/MAXDEPTH text form)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "compound query text: SEED <id> then HOP <in|out|both> [<kind>] lines, optional WHERE KIND <kind>"},
			},
			"required": []string{"query"},
		},
	})
	// EP-012 agent memory & skills. Binding-specific availability is filtered later.
	tools = append(tools, map[string]any{
		"name":        ToolMemory,
		"description": "scoped agent memory: store, recall, forget, list, or export notes in scopes and notebooks with provenance",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op":             map[string]any{"type": "string", "description": "operation: store | recall | forget | list | export"},
				"scope":          map[string]any{"type": "string", "description": "memory scope"},
				"notebook":       map[string]any{"type": "string", "description": "memory notebook"},
				"tags":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "tags for store"},
				"payload":        map[string]any{"type": "string", "description": "payload for store"},
				"mem_id":         map[string]any{"type": "string", "description": "entry id for forget or overwrite"},
				"kind":           map[string]any{"type": "string", "description": "entry kind for store: architecture | command | convention | decision | risk | dependency | workflow"},
				"source":         map[string]any{"type": "string", "description": "provenance source for store"},
				"confidence":     map[string]any{"type": "string", "description": "confirmed | derived | heuristic"},
				"evidence":       map[string]any{"type": "string", "description": "optional file:line citation"},
				"limit":          map[string]any{"type": "integer", "description": "max entries for list"},
				"export_to_path": map[string]any{"type": "string", "description": "REJECTED (SAFE-01): the transport never writes server-side files; export returns the payload in the response's `export` field — omit this argument"},
			},
			"required": []string{"op"},
		},
	})
	tools = append(tools, map[string]any{
		"name":        ToolDistill,
		"description": "deterministic, non-LLM session distillation: compress a session trace into a reusable artifact",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"session_id":      map[string]any{"type": "string", "description": "session identifier"},
				"decisions":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"risks":           map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"open_questions":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"file_references": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"session_id"},
		},
	})
	tools = append(tools, map[string]any{
		"name":        ToolSkillGen,
		"description": "deterministic, non-LLM skill generation: turn a repeatable procedure into a Markdown skill artifact",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string", "description": "skill name"},
				"trigger":     map[string]any{"type": "string", "description": "skill trigger phrase"},
				"description": map[string]any{"type": "string", "description": "skill description"},
			},
			"required": []string{"name", "trigger"},
		},
	})
	// EP-018 multi-PR triage suite (SW-105).
	tools = append(tools, map[string]any{
		"name":        ToolListPRs,
		"description": "list open pull requests of the configured repo with read-only forge metadata (number, title, author, base/head refs, head SHA, changed files, additions/deletions, mergeable). Discovery/metadata ONLY — no graph scoring, no comment posting. The forge enumeration is the suite's only outbound path.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
	})
	tools = append(tools, map[string]any{
		"name":        ToolTriagePRs,
		"description": "single-pass graph-derived PR triage: enumerate open PRs, then rank them by blast radius, touched high-centrality nodes, ownership concentration, churn, and test-coverage-of-touched-code, folded into a fixed-integer composite. Deterministic total order (composite DESC, PR number ASC). Scoring is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolConflictsPRs,
		"description": "inter-PR conflict detection: enumerate open PRs, then report which PR PAIRS collide over the local graph — textual overlap (overlapping changed line ranges in the same file), shared file/symbol/high-centrality node, and the asymmetric contract-dependency case (one PR mutates a contract that another PR's changed entities depend on via graph edges, flagged even with NO textual overlap). Deterministic pairwise report (pairs by ascending PR number, canonical within-pair entity order). Detection is a zero-egress pass over the local graph; the forge is touched only for enumeration.",
		"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-107: suggest_reviewers.
	tools = append(tools, map[string]any{
		"name":        ToolSuggestReviewers,
		"description": "suggest reviewers for a change: resolve the touched symbol/file set from a local-first PR diff (or line-oriented refs), then rank candidate reviewers from graph ownership + recency-decayed churn over the touched files plus affected-subgraph proximity (callers/callees/contract neighbors) of the touched symbols. Each candidate carries a transparent per-signal breakdown (ownership/recency-decayed-churn/subgraph-proximity) with honest file-vs-symbol granularity labels. Deterministic total order (composite DESC, reviewer identity ASC). Zero-egress pass over the local graph + git history.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"diff": map[string]any{"type": "string", "description": "unified diff or line-oriented refs (path:name | path#Lline | node id) of the change"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-107: compare_branches.
	tools = append(tools, map[string]any{
		"name":        ToolCompareBranches,
		"description": "compare two branches at the GRAPH level: given two branch refs (states materialized above the surface boundary), report the structured diff of entities/symbols/contracts added/removed/changed plus edges added/removed and entities moved across files — keyed by stable canonical graph identity (NodeId), not line ranges. Detects signature/contract changes (a contract node whose dependency surface changed) and correlates moves by path-independent symbol identity. Deterministic per-group order. Zero-egress pure local set-diff; the engine never resolves a git ref.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base": map[string]any{"type": "string", "description": "base branch ref"},
				"head": map[string]any{"type": "string", "description": "head branch ref"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-018 SW-108 (capstone): critique_review.
	tools = append(tools, map[string]any{
		"name":        ToolCritiqueReview,
		"description": "critique an EXISTING PR review against the knowledge graph: replay the single-PR risk/blast-radius/centrality/taint signals as a ground-truth oracle over the PR's touched set, then emit a structured, graph-evidence-grounded critique with three item types — gap (a high-risk touched entity the review never mentioned: blast-radius count + centrality + contributing edge kinds + taint provenance), over_flag (a review-flagged entity the graph shows is a low-centrality leaf below the risk threshold), and unsupported_claim (a review comment asserting impact to an anchorable target with NO connecting graph edge). Comment→entity matching is DETERMINISTIC anchoring (file:line/symbol → NodeId); unanchorable comments/claims are counted in an honest unanchored tally, never guessed. NO LLM prose. Deterministic total order (type → entity NodeId → review-anchor). The review is fetched at the surface boundary (or supplied inline); the critique itself is a zero-egress pass over the local graph.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pr_number": map[string]any{"type": "integer", "description": "PR number to fetch the existing review for (when no inline review is supplied)"},
				"diff":      map[string]any{"type": "string", "description": "the PR's touched set: unified diff or line-oriented refs (path:name | path#Lline | node id)"},
				"review":    map[string]any{"type": "string", "description": "inline existing-review JSON ({verdict, comments:[{id,path,line,symbol,claim_targets}]}); takes precedence over the surface fetch"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-020 agent-first task tools (SW-115 / SW-116 / SW-117) plus EP-024 (SW-134). Advertised
	// unconditionally: they require only the engine/agenttools packages, not a
	// separate service. Each descriptor uses the hardened six-facet
	// template (purpose, when-to-use, when-not-to-use, input shape, read-only,
	// partial-possible) and carries explicit read-only annotations.
	tools = append(tools, map[string]any{
		"name":        ToolExplainSymbol,
		"description": "explain_symbol: return a compact, cited symbol-identity summary (qualified name, kind, declaring file:line, direct callers/callees). Purpose: answer 'what is this symbol?' in one call. When to use: the agent has a symbol reference and needs identity + immediate neighborhood without reading source. When NOT to use: for broad 'what should I read first?' questions (use related_files) or risk scoring (use change_risk). Input shape: a single symbol reference (qualified id, file:line, or bare name). Read-only: true. Partial results possible: neighbor lists may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "symbol reference: qualified id, file:line anchor, or bare name"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolRelatedFiles,
		"description": "related_files: return a deterministically ranked 'read these first' file list for a symbol, file, or diff anchor. Purpose: answer 'what should I read first?' in one call. When to use: the agent needs a scoped, evidence-backed file list before editing or reviewing. When NOT to use: for a single symbol's identity (use explain_symbol) or for risk scoring (use change_risk). Input shape: a single anchor plus optional direction (dependencies | dependents | both). Read-only: true. Partial results possible: ranked file list may truncate.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target":    map[string]any{"type": "string", "description": "anchor: symbol id, file path, or diff line-oriented refs"},
				"direction": map[string]any{"type": "string", "description": "dependencies | dependents | both (default)"},
			},
			"required": []string{"target"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolChangeRisk,
		"description": "change_risk: return an evidence-based low/medium/high/unknown risk assessment for a symbol, file, or diff target. Purpose: answer 'how risky is it to touch this?' in one call. When to use: before proposing or reviewing a change, to gauge blast radius and coverage. When NOT to use: when you only need a file list (use related_files) or a symbol summary (use explain_symbol). Input shape: a target symbol/file or a local-first diff. Read-only: true. Partial results possible: evidence may be truncated, and the tool returns unknown rather than guessing.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "symbol id or file path to evaluate"},
				"diff":   map[string]any{"type": "string", "description": "local-first unified diff or line-oriented refs (alternative to target)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// EP-024 agent_brief: bounded task-start context packet.
	tools = append(tools, map[string]any{
		"name":        ToolAgentBrief,
		"description": "agent_brief: return a bounded, cited task-start context packet (project identity, start-here files, key symbols, known facts, hotspots, suggested next MCP calls) in Markdown with embedded canonical JSON. Purpose: give an agent a scoped, cited starting context without reading source blindly. When to use: at the beginning of a task or when entering a new subsystem. When NOT to use: when you already have a specific symbol to explain (use explain_symbol) or a file list to read (use related_files). Input shape: optional topic (symbol, path, or subsystem). Read-only: true. Partial results possible: sections may be empty if underlying analyzers are not yet wired.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "optional topic: symbol id, file path, or subsystem name"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// Central stability-tier marking (single source: StableOperations in
	// tools.go) — every advertised tool outside the frozen 12-op stable set is
	// prefixed [labs]; descriptor literals never carry the tag by hand.
	return markLabs(tools)
}

// editToolDescriptors defines the MCP tool schema for the SW-038 edit/refactor
// command surface (refactor-preview, refactor, undo). Each routes through the
// shared client; the surface holds no engine logic.
var editToolDescriptors = []map[string]any{
	{
		"name":        ToolRefactorPreview,
		"description": "preview a graph-aware refactor: resolve the target via the query layer and return the EP-004 impact set (blast radius + planned edits) WITHOUT mutating",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|signature_change (extract|move are NOT implemented and fail closed with a typed error before any read or write — SAFE-01)"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolRefactor,
		"description": "apply a graph-aware refactor through the shared atomic edit saga and return an auditable change record (operation, target, before/after, actor, timestamp, undo token)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":             map[string]any{"type": "string", "description": "refactor kind: rename|signature_change (extract|move are NOT implemented and fail closed with a typed error before any read or write — SAFE-01)"},
				"target_symbol":    map[string]any{"type": "string", "description": "resolved node id of the symbol to refactor"},
				"old_name":         map[string]any{"type": "string", "description": "current spelling of the symbol"},
				"new_name":         map[string]any{"type": "string", "description": "replacement spelling"},
				"destination_file": map[string]any{"type": "string", "description": "destination file (move only)"},
				"actor":            map[string]any{"type": "string", "description": "request identity recorded on the change record (default \"mcp\")"},
			},
			"required": []string{"kind", "target_symbol", "old_name", "new_name"},
		},
	},
	{
		"name":        ToolUndo,
		"description": "reverse a previously applied edit by its undo token, restoring the prior graph + source and recording the reversal as its own auditable change record",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"undo_token": map[string]any{"type": "string", "description": "the undo token returned by a prior refactor"},
				"actor":      map[string]any{"type": "string", "description": "request identity recorded on the reversal record (default \"mcp\")"},
			},
			"required": []string{"undo_token"},
		},
	},
}
