package mcp

import (
	"fmt"
	"strings"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/trust"
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
		// Optimistic catalog for a session whose binding is still in flight:
		// profile membership is static, only the binding-specific capability
		// narrowing must wait for the bound client. Not cached — the bound
		// catalog replaces it, announced via notifications/tools/list_changed.
		if s.labs {
			return maximalToolDescriptors()
		}
		return stableToolDescriptors()
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

// legacyStableToolDescriptors is deliberately static and side-effect free. The
// shipped Runtime wires every stable port, so its default profile is exactly
// StableOperations minus lifecycle-only index; partially wired bindings are
// narrowed later through CapabilityReporter.
//
// SW-225 (AX-05): this is the LEGACY source. stableToolDescriptors() selects
// between it and the catalog projection; see descriptors_projected.go.
func legacyStableToolDescriptors() []map[string]any {
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

// legacyMaximalToolDescriptors builds the complete Stable+Labs descriptor
// registry without consulting the bound client. It must remain pure: tools/list
// is discovery, not permission to dial a daemon, auto-start a process, enumerate
// a forge, or execute an analyzer. toolDescriptors applies the optional, pure
// CapabilityReporter filter after this registry is complete. A third-party
// Client without that optional reporter retains the full Client-contract Labs
// catalog for backwards compatibility.
//
// SW-225 (AX-05): this is the LEGACY source. maximalToolDescriptors() selects
// between it and the catalog projection; see descriptors_projected.go.
func legacyMaximalToolDescriptors() []map[string]any {
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
		descriptor := map[string]any{
			"name":        op,
			"description": "structural query: " + op,
			"inputSchema": map[string]any{"type": "object", "properties": props, "required": required},
		}
		// SW-241 (AX-12): the read-only annotations used to be a Stable-profile
		// exclusive, so a `-labs` session lost hints a default one always got.
		// The maximal profile adopted them — but ONLY for the tools that
		// actually diverged. query.Operations also carries five Labs-only
		// structural queries (implementers, implements, overrides, subtypes,
		// supertypes) that the Stable profile never advertises, so they have no
		// Stable form to adopt; annotating them here would be an unrequested
		// wire change riding along with a collapse, which is exactly the class
		// of silent widening this program exists to prevent.
		if IsStableMCPTool(op) {
			descriptor["annotations"] = readOnlyToolAnnotations()
		}
		tools = append(tools, descriptor)
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
		"annotations": readOnlyToolAnnotations(),
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
	// separate service.
	//
	// SW-241 (AX-12): these four used to carry a SECOND, longer six-facet
	// description and a narrower input schema here, while the Stable profile
	// advertised the terse form above. The maximal profile adopted the
	// Stable-profile advertisement — one descriptor per tool, and the shipped
	// default profile did not move a byte.
	//
	// Stated plainly, because it is the cost of the direction chosen: `-labs`
	// sessions GAIN explain_symbol's `limit`, related_files' `limit` and
	// change_risk's `limit`, and LOSE the longer six-facet prose. The prose was
	// never advertised to a default session, so no client that has it today
	// loses it in the shipped profile; the alternative direction would have
	// stripped a documented argument from that shipped profile instead, which
	// the story forbids.
	tools = append(tools, map[string]any{
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
	})
	tools = append(tools, map[string]any{
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
	})
	tools = append(tools, map[string]any{
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
	})
	// EP-024 agent_brief: bounded task-start context packet.
	tools = append(tools, map[string]any{
		"name":        ToolAgentBrief,
		"description": "return a bounded, cited task-start context packet",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol": map[string]any{"type": "string", "description": "optional topic: symbol, path, or subsystem"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P1 trust surface (PRD §17, FR-3): graph_health, the single Labs trust
	// tool. It rides the shared client.TrustReport composition (ONE assembly,
	// ONE encoder), so the returned document is byte-identical to
	// `graphi trust-report --json` for the same inputs. The input schema is the
	// PRD §17 shape verbatim; the [labs] prefix comes from markLabs below.
	// Read-only annotations hold strictly: the composition is a pure observer
	// (read-only store opens, no indexing, no daemon start, no network) and
	// degrades fail-closed to the UNAVAILABLE document when no graph exists.
	tools = append(tools, map[string]any{
		"name":        ToolGraphHealth,
		"description": "graph_health: return the canonical trust-report document for the repository or a target scope — snapshot state (CURRENT|STALE|INCOMPLETE|UNAVAILABLE), freshness facts, coverage, edge confidence tiers (confirmed/derived/heuristic), resolution gaps, external boundaries, and an optional policy verdict. Purpose: agent preflight — answer 'how far may I trust graph evidence for the planned action?' before acting on query results. When to use: before a risky task, to decide whether to use, supplement, or reject graph evidence; with a policy (" + strings.Join(trust.PolicyIDs(), " | ") + ") for a reproducible fail-closed verdict. When NOT to use: for rebuild/freshness advice alone (use `graphi status`) or to run the query itself. Input shape: optional target (symbol | repository-relative path | package), optional policy, optional bounded details. Read-only: true — never indexes, never starts a daemon, no network; missing evidence reads UNVERIFIED/UNAVAILABLE, never PASS. Partial results possible: details lists are bounded by limit.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "optional symbol id, qualified name, repository-relative path or package"},
				// Enum and prose both derive from trust.PolicyIDs so a policy
				// version bump reaches the agent-facing schema automatically —
				// a hand-copied list here would advertise a token the resolver
				// no longer accepts.
				"policy":  map[string]any{"type": "string", "enum": trust.PolicyIDs(), "description": "optional trust policy"},
				"details": map[string]any{"type": "boolean", "description": "include bounded supporting evidence"},
				"limit":   map[string]any{"type": "integer", "description": "maximum detail items"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P1 strict query (PRD v1.0 §8 Phase 9): strict_query, the Labs trust-aware
	// query wrapper. It rides the shared client.ComposeStrictQuery composition,
	// so the envelope is byte-identical to `graphi query-strict` for the same
	// inputs. Read-only annotations hold: it runs one Stable query underneath
	// and filters its result — no indexing, no daemon start, no network.
	//
	// `operation` is constrained to the Stable structural operations. The tool
	// exists to make an EXISTING answer's confidence legible; letting it select
	// arbitrary analyzers would make it a second query surface.
	tools = append(tools, map[string]any{
		"name":        ToolStrictQuery,
		"description": "strict_query: run a structural query and WITHHOLD every result edge below a minimum confidence tier, reporting how many were withheld. Purpose: let an agent act only on evidence at or above a chosen strength, without having to filter and re-tally results itself. When to use: before a change or a definitive claim, when heuristic edges would be unsafe to treat as facts — pair it with graph_health for the repository-level verdict. When NOT to use: for exploration or recall (the Stable query tools return everything, which is what you want there). Input shape: operation (" + strings.Join(strictQueryOperations(), " | ") + "), symbol id, optional depth, optional minimum_tier (confirmed | derived | heuristic; default heuristic), optional policy preflight. Read-only: true — never indexes, never starts a daemon, no network. CRITICAL for interpreting the result: an EMPTY result with excluded_edges > 0 is filtered, NOT proven empty — the envelope says so in `limitations`, and reading it as \"no such relationships\" is a false negative. A blocked policy preflight returns an error and runs no query.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation":    map[string]any{"type": "string", "enum": strictQueryOperations(), "description": "structural query operation to run"},
				"symbol":       map[string]any{"type": "string", "description": "symbol (node) id to query"},
				"depth":        map[string]any{"type": "integer", "description": "neighborhood hop depth (ignored by other operations)"},
				"minimum_tier": map[string]any{"type": "string", "enum": []string{"confirmed", "derived", "heuristic"}, "description": "lowest confidence tier admitted into the result (default heuristic)"},
				"policy":       map[string]any{"type": "string", "enum": trust.PolicyIDs(), "description": "optional fail-closed trust preflight; a non-PASS/WARN verdict blocks the query"},
			},
			"required": []string{"operation", "symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P0 agent intelligence: symbol_context, the unified single-call symbol
	// view. It rides the shared engine/agenttools/symbolcontext assembly
	// through the labs client facade, so CLI and MCP emit byte-identical
	// envelopes. Read-only annotations hold: bounded graph reads plus local
	// disk reads for the definition snippet — no indexing, no daemon start,
	// no network.
	tools = append(tools, map[string]any{
		"name":        ToolSymbolContext,
		"description": "symbol_context: return the unified single-call symbol view — definition site with an optional token-budgeted source snippet, type-hierarchy relations (implementers/implements/overrides/subtypes/supertypes), callers, callees, references, the test files that exercise the symbol (bounded reverse walk), and a change_risk-consistent risk level, every claim cited. Purpose: replace the explain_symbol + callers + callees + references + change_risk round-trips with ONE call when an agent is about to work on a symbol. When to use: the agent has a concrete symbol and wants its full working context in one response. When NOT to use: for 'what should I read first?' (use related_files/agent_brief) or free-text task scoping (use task_context when available). Input shape: symbol reference plus optional depth (test-walk hops, 1-3), limit (item cap), token_budget (snippet tokens; negative disables). Read-only: true. Partial results possible: item cap and walk bounds truncate; limits.next says how to widen.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"symbol":       map[string]any{"type": "string", "description": "symbol reference: qualified id, repo-relative path, or bare name"},
				"depth":        map[string]any{"type": "integer", "description": "test-walk hop depth 1-3 (default 2)"},
				"limit":        map[string]any{"type": "integer", "description": "item cap (default 20)"},
				"token_budget": map[string]any{"type": "integer", "description": "snippet token budget (default 700; negative disables the snippet)"},
			},
			"required": []string{"symbol"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P0 agent intelligence: task_context, the free-text task scoper. One
	// call returns a deterministically ranked, token-budgeted context bundle;
	// the ranking is an integer weight model whose hash is stamped into every
	// summary (no LLM estimates, no floats). Read-only.
	tools = append(tools, map[string]any{
		"name":        ToolTaskContext,
		"description": "task_context: turn a free-text task description into a ranked, cited, token-budgeted context bundle — primary symbols (seeds), related symbols, callers, callees, nearby tests and configuration files, a related-file roll-up, a change_risk-consistent risk level, and a recommended read order, with source snippets under a hard token budget. Ranking is a deterministic integer weight model (hash stamped in the summary), never an LLM guess. Purpose: answer 'where do I start for this task?' in one call, replacing search + related_files + explain round-trips. When to use: at task start, with the task phrased in a few words. When NOT to use: when you already know the exact symbol (use symbol_context) or need repository orientation (use agent_brief / repo_overview when available). Input shape: task text plus optional limit (item cap) and token_budget (snippet tokens; negative disables). Read-only: true. Partial results possible: bounded reads and the item cap truncate; limits.next says how to widen.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task":         map[string]any{"type": "string", "description": "free-text task description, or an exact symbol/path"},
				"limit":        map[string]any{"type": "integer", "description": "item cap (default 40)"},
				"token_budget": map[string]any{"type": "integer", "description": "snippet token budget (default 1200; negative disables snippets)"},
				"version":      map[string]any{"type": "integer", "description": "task_context version: 0/1 = lexical-seeded (default), 2 = retrieval-seeded with claim_type on every evidence item (SW-264)"},
			},
			"required": []string{"task"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P0 agent intelligence: repo_overview, the one-call repository summary.
	// The default call reads only the compact aggregates; `communities` is
	// the documented opt-in full-graph pass. Read-only.
	tools = append(tools, map[string]any{
		"name":        ToolRepoOverview,
		"description": "repo_overview: return the one-call 'what is this repository?' summary — node/edge/file totals with edge-confidence tiers, a directory tree ranked by symbol count, the language mix, entry-point candidates (go-main probe, meta flags, cmd/ path heuristic), the highest-centrality symbols, test and generated/vendored areas, external boundaries, optional dependency communities, and concrete suggested next calls. Purpose: orient an agent in an unfamiliar repository in one response, right after indexing. When to use: first call in a new repository, or when scoping where a subsystem lives. When NOT to use: for task-specific context (use task_context) or a known symbol (use symbol_context). Input shape: optional limit (item cap) and communities (opt-in full-graph community detection — the only non-aggregate read). Read-only: true. Partial results possible: every section is row-capped and the item cap truncates lowest-value sections first.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit":       map[string]any{"type": "integer", "description": "item cap (default 60)"},
				"communities": map[string]any{"type": "boolean", "description": "opt into the full-graph community pass (default false)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P1 test intelligence: test_impact. The mapping is derived on demand
	// (bounded walks + naming/package heuristics) — no materialized edges, so
	// the frozen index output is untouched. Read-only.
	tools = append(tools, map[string]any{
		"name":        ToolTestImpact,
		"description": "test_impact: given a unified diff or a symbol/file target, bucket the repository's tests — must_run (a direct call edge proves the test exercises a changed symbol), recommended (transitive reach, naming conventions, same-directory test files), probably_unaffected (the remaining test-file universe, counted in full), and unknown (diff paths with no graph symbols — never guessed). Purpose: run seven tests instead of the whole suite, with evidence for why. When to use: after editing, before running tests; pipe `git diff <range>` in for range-based selection. When NOT to use: for the risk grade itself (use change_impact) or coverage of a single known symbol (symbol_context lists its tests). Input shape: exactly one of target or diff, plus optional depth (walk hops 1-3) and limit. Read-only: true. Partial results possible: bounded walks make the buckets a superset-safe lower bound; limits.truncated says when.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "symbol id or file path (alternative to diff)"},
				"diff":   map[string]any{"type": "string", "description": "unified diff text (alternative to target)"},
				"depth":  map[string]any{"type": "integer", "description": "reverse-walk hop depth 1-3 (default 2)"},
				"limit":  map[string]any{"type": "integer", "description": "item cap (default 20)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P1 change intelligence: change_impact ("Change Risk 2.0"). A separate
	// labs operation — the frozen-stable change_risk envelope keeps its bytes.
	tools = append(tools, map[string]any{
		"name":        ToolChangeImpact,
		"description": "change_impact: the Change Risk 2.0 assessment for a unified diff or a symbol/file target — changed symbols, their public-API subset, direct dependents with evidence, the bounded transitive closure, the tests covering the change, configuration files riding the diff, explicit machine-checkable reasons ('public interface changed', 'no test directly covers X', dependent counts), and a risk level (change_risk's thresholds, plus a one-step escalation when exported surface changed). The confidence distribution is derived from the consumed edge tiers, never invented. Purpose: one call answers 'how risky is this change and why?'. When to use: before proposing or reviewing a change set; pipe `git diff <range>` in for ranges. When NOT to use: for the stable low/medium/high quick check (change_risk) or test selection alone (test_impact). Input shape: exactly one of target or diff, plus optional depth (1-3) and limit. Read-only: true. Partial results possible: bounded walks make dependent/test counts lower bounds; limits.truncated says when.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"target": map[string]any{"type": "string", "description": "symbol id or file path (alternative to diff)"},
				"diff":   map[string]any{"type": "string", "description": "unified diff text (alternative to target)"},
				"depth":  map[string]any{"type": "integer", "description": "transitive-walk hop depth 1-3 (default 2)"},
				"limit":  map[string]any{"type": "integer", "description": "item cap (default 20)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P2 git intelligence: hotspots. History comes from the surface-boundary
	// bounded `git log` provider; the engine stays exec-free. Read-only.
	tools = append(tools, map[string]any{
		"name":        ToolHotspots,
		"description": "hotspots: rank the repository's files by churn × dependency centrality — 'this file changed 12 times in the window AND has 43 graph edge endpoints' — with per-file breakdowns, single-author bus-factor warnings for the ranked hotspots, and a concrete next call. A far better where-to-refactor signal than cyclomatic complexity alone. Purpose: answer 'where does this repository hurt?' in one call. When to use: planning refactors, onboarding into maintenance, prioritizing review attention. When NOT to use: for a specific change's risk (change_impact) or test selection (test_impact). Input shape: optional max_commits (history window bound, default 1000) and limit (item cap). Read-only: true — one bounded local `git log` plus compact graph aggregates; no network. Partial results possible: the window is bounded by max-commits/max-age; without git history the tool returns a typed unavailable outcome.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"max_commits": map[string]any{"type": "integer", "description": "history window bound (default 1000)"},
				"limit":       map[string]any{"type": "integer", "description": "item cap (default 20)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P3 repository search: search_hybrid, embedding-free multi-token ranking.
	// Read-only; the optional semantic search stays a separate opt-in.
	tools = append(tools, map[string]any{
		"name":        ToolSearchHybrid,
		"description": "search_hybrid: rank symbols for a multi-token free-text query WITHOUT embeddings — lexical retrieval re-ranked by identifier-segment matching (camelCase/snake_case aware), path relevance, and bounded graph degree, with the per-signal breakdown in every reason ('authentication token validation' ranks TokenValidator ahead of accidental substring hits). Deterministic integer weight model, hash-stamped in the summary; no vector database, no model, no egress. Purpose: better multi-word discovery than plain lexical search, before reaching for the optional semantic search. When to use: exploratory multi-word queries where plain search returns noise. When NOT to use: exact names (search / definition) or task scoping (task_context). Input shape: query text plus optional limit. Read-only: true. Partial results possible: retrieval and degree reads are bounded; the item cap truncates lowest scores first.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":   map[string]any{"type": "string", "description": "free-text multi-token query"},
				"limit":   map[string]any{"type": "integer", "description": "item cap (default 20)"},
				"version": map[string]any{"type": "integer", "description": "search_hybrid version: 0/1 = lexical (default, byte-identical to SW-257 §7.2), 2 = retrieval-rendered with explain fields + summary fingerprints (SW-264)"},
			},
			"required": []string{"query"},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P2 architecture intelligence: architecture + architecture_violations.
	// Both consume the deterministic Louvain partition (SW-103) over the
	// symbol-only projection — one documented whole-graph pass per call.
	tools = append(tools, map[string]any{
		"name":        ToolArchitecture,
		"description": "architecture: the automatic architecture view of the code graph — deterministic Louvain communities labeled by their dominant package prefix, layered by dependency DIRECTION (edge majority between communities; foundation = layer 1), each row listing member counts and its strongest depends-on/used-by neighbors with edge counts, plus the top inter-community dependencies. No LLM classification — every line is a graph fact. Purpose: answer 'what are this repository's real modules and which way do they depend?' in one call. When to use: orientation beyond repo_overview, before refactor planning, to see whether intended layering matches reality. When NOT to use: for violations themselves (architecture_violations) or file-tree structure (repo_overview). Input shape: optional limit (item cap). Read-only: true — one node and one edge catalog read (detection needs the whole graph by definition). Partial results possible: community and dependency rows are capped; cyclic communities get 'layer ?' and a pointer to architecture_violations.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "item cap (default 60)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	tools = append(tools, map[string]any{
		"name":        ToolArchitectureViolations,
		"description": "architecture_violations: detect architecture violations on the community dependency graph — dependency CYCLES (community A → B → A with edge counts along the loop), UNEXPECTED dependencies (edges against the dominant direction, e.g. 3 edges storage → domain against a 120-edge domain → storage), HIGH-COUPLING pairs (≥3 edges both ways — no clean layering possible), and GOD MODULES (≥50% of all inter-community edges while touching ≥60% of communities). Pinned integer thresholds are quoted in every finding; a violation-free graph returns an explicit cited 'clean' item, never an empty shrug. Purpose: make unusual dependency directions visible before they calcify. When to use: architecture reviews, CI-adjacent hygiene checks, after large refactors. When NOT to use: for the layer view itself (architecture) or single-change risk (change_impact). Input shape: optional limit (item cap). Read-only: true — one node and one edge catalog read. Partial results possible: per-category rows are capped (8 cycles, 12 back-edges, 8 coupling pairs).",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "item cap (default 60)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P2 dead code: dead_code — the agent-facing view of the EP-015
	// dead_symbol diagnostic, with scores and visible exclusions.
	tools = append(tools, map[string]any{
		"name":        ToolDeadCode,
		"description": "dead_code: precise dead-code candidates — symbols with ZERO live inbound references (calls/references/implements/inherits/overrides), each scored by a pinned integer signal model (exported API and dynamic-dispatch methods score lower, penalties quoted in the reason) — plus the exclusions made VISIBLE with their reasons: framework/language entry points (annotations, main, test paths, overrides, decorators), test fixtures, generated/vendored paths, and exported API without usage evidence. Far better than 'references == 0': every candidate says why it is safe and every exclusion says why it is not dead. Purpose: find safely deletable code with evidence. When to use: cleanup passes, before refactors, dead-weight audits. When NOT to use: for a guarded delete itself (CLI safe-delete) or a single symbol's liveness (symbol_context). Input shape: optional limit (item cap). Read-only: true — one node + one edge catalog read (the analysis needs every edge) plus selective hydration. Partial results possible: candidate and exclusion rows are capped; limits.truncated says when.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "item cap (default 40)"},
			},
		},
		"annotations": readOnlyToolAnnotations(),
	})
	// P3 framework intelligence: framework_map — the application-graph view
	// derived from parser-recorded annotations; no new parsing, no guessing.
	tools = append(tools, map[string]any{
		"name":        ToolFrameworkMap,
		"description": "framework_map: the application-level view of the repository derived from the framework annotations and decorators already recorded in the graph — HTTP ROUTES (@GetMapping, NestJS @Get, [HttpGet]), EVENT handlers (@EventListener, @KafkaListener, @EventPattern, @Scheduled), INJECTION points (@Autowired, @Inject), DI-managed COMPONENTS (@RestController, @Service, @Injectable, Angular @Component), and CONFIGURATION units (@Configuration, @Bean, @Module). Providers: spring (Java/Kotlin), nest (TypeScript/JavaScript), dotnet (C#). Every fact cites its annotation and definition site verbatim — no LLM classification, no new parsing. Purpose: see the application graph (endpoints, listeners, wiring) on top of the code graph in one call. When to use: orienting in an annotated service codebase, enumerating endpoints before a change, finding event consumers. When NOT to use: for call-graph structure (callers/callees) or repositories without annotation metadata — Go and Python sources record none, and the tool says so honestly. Input shape: optional limit (item cap). Read-only: true — one node catalog read, no edges. Partial results possible: per-category rows are capped; limits.truncated says when.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "integer", "description": "item cap (default 60)"},
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

// strictQueryOperations is the closed operation set strict_query accepts: the
// Stable structural query operations, minus the lifecycle op `index`. Derived
// from StableMCPToolNames so a change to the frozen set reaches this schema
// automatically, and filtered to structural queries so the tool cannot become a
// second dispatcher for analyzers or agent tools.
func strictQueryOperations() []string {
	ops := make([]string, 0, len(query.Operations))
	for _, op := range query.Operations {
		if IsStableMCPTool(op) {
			ops = append(ops, op)
		}
	}
	return ops
}

// isStrictQueryOperation reports membership in the closed strict_query
// operation set. Shared by the input schema and the handler so the two cannot
// disagree about what the tool accepts.
func isStrictQueryOperation(op string) bool {
	for _, allowed := range strictQueryOperations() {
		if op == allowed {
			return true
		}
	}
	return false
}
