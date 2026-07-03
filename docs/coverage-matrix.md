# graphi — capability coverage matrix

<!-- GENERATED FILE — do not edit by hand.
     Source of truth: docs/coverage-matrix.yaml
     Regenerate:      go run ./cmd/coverage -generate
     CI-enforced:     internal/coverage drift guard fails the build if this
                      matrix omits, adds, or mislabels a live capability
                      (registered parsers/analyzers, advertised MCP tools, present surfaces). -->

Every row below is checked against the live registries by the `coverage-matrix`
CI gate (`internal/coverage`). A docs-only change that contradicts the code — a
missing capability, a phantom "shipped" entry, or a live capability marked
"planned" — breaks the build. **Legend:** ✅ shipped · 🟡 partial · ⏳ planned.

Total capabilities: **136**. See [`architecture-plan.md`](architecture-plan.md) for the design context.

## Parsers (23)

| id | status | epic | note |
|---|---|---|---|
| `bash` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `c` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `c_sharp` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `cpp` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `css` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `go` | ✅ shipped | EP-001 | Go AST→graph extractor (reference SymbolExtractor, go/ast, CGo-free). |
| `hcl` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `html` | ⏳ planned | EP-001 | pure-Go grammar present but not subset-buildable in isolation (gotreesitter scanner co-located with grammar_subset_blade); deferred to graphi-broad. |
| `java` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `javascript` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `json` | ✅ shipped | EP-001 | stdlib structural parser (CGo-free). |
| `kotlin` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `lua` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `markdown` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `php` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `python` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `ruby` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `rust` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `sql` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `toml` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `tsx` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `typescript` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `yaml` | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |

## Analyzers (22)

| id | status | epic | note |
|---|---|---|---|
| `batched` | ✅ shipped | EP-004 | batched composite over impact+call-chain+metrics. |
| `call-chain` | ✅ shipped | EP-004 | caller/callee chain reconstruction. |
| `communities` | ✅ shipped | EP-017 | SW-104: SW-103 Louvain community detection surfaced behind the single dispatch table. |
| `compare-branches` | ✅ shipped | EP-018 | SW-107: graph-level branch diff over TWO already-built read-only graph states (materialized above the surface boundary) — added/removed/changed/moved entities + edges added/removed, keyed by canonical NodeId (not line ranges); detects signature/contract changes and correlates cross-file moves by path-independent symbol identity; pure local set-diff, zero engine egress (the engine never resolves a git ref). |
| `concept` | ✅ shipped | EP-004 | lexical-search-backed concept resolution (needs Searcher). |
| `conflicts-prs` | ✅ shipped | EP-018 | SW-106: inter-PR conflict detection over an enumerated PR set — textual overlap + shared file/symbol/high-centrality node + asymmetric contract-dependency edge check; entity→PRs inverted index, byte-stable pairwise report (zero engine egress; forge enumeration stays at the surface). |
| `contracts` | ✅ shipped | EP-005 | producer/consumer contract drift detection. |
| `critique-review` | ✅ shipped | EP-018 | SW-108 (capstone): deterministic graph-evidence critique of an EXISTING PR review — replays the EP-007 risk/blast/centrality/taint oracle over the touched set and runs a three-way diff (gap / over_flag / unsupported_claim) against the review; deterministic resolveRef anchoring with an honest unanchored tally (never guessed); NO LLM prose; total order type→NodeId→anchor; zero engine egress (the only egress is the surface review fetch). |
| `git-history` | ✅ shipped | EP-005 | churn / bus-factor / co-change signals. |
| `impact` | ✅ shipped | EP-004 | forward/reverse blast-radius reachability. |
| `interproc` | ✅ shipped | EP-005 | interprocedural fixpoint over per-procedure gen/kill summaries. |
| `metrics` | ✅ shipped | EP-004 | graph centrality / hub-bridge metrics. |
| `notebook-ingest` | ✅ shipped | EP-017 | SW-104: SW-100 notebook (.ipynb) cell provenance surfaced behind the single dispatch table. |
| `pdg` | ✅ shipped | EP-005 | program dependence graph (data + control dependence). |
| `pr-questions` | ✅ shipped | EP-007 | deterministic, no-LLM reviewer questions from findings. |
| `pr-risk` | ✅ shipped | EP-007 | deterministic per-region PR risk score (impact+taint). |
| `pr-signals` | ✅ shipped | EP-007 | hub/bridge/surprise signals on PR-changed code. |
| `suggest-reviewers` | ✅ shipped | EP-018 | SW-107: ranked candidate-reviewer recommender — file-granular ownership + recency-decayed churn over the touched files (fixed decay reference, never now()) plus symbol-granular affected-subgraph proximity (callers/callees/contract neighbors, centrality-weighted), folded into a fixed-integer composite with a transparent per-signal breakdown; deterministic total order (composite DESC, reviewer identity ASC); zero engine egress. |
| `taint` | ✅ shipped | EP-005 | flow-sensitive source→sink taint analysis. |
| `taint-query` | ✅ shipped | EP-017 | SW-104: SW-102 interprocedural taint verdict + flows surfaced behind the single dispatch table. |
| `triage-prs` | ✅ shipped | EP-018 | SW-105: single-pass graph-derived multi-PR triage ranking; reuses the EP-007 pr-risk kernel over an enumerated PR set (zero engine egress; forge enumeration stays at the surface). |
| `watcher-status` | ✅ shipped | EP-017 | SW-104: SW-101 filesystem-watcher health (honest per-root errors) surfaced behind the single dispatch table. |

## MCP tools (41)

| id | status | epic | note |
|---|---|---|---|
| `analyze` | ✅ shipped | EP-004 | run a named graph analyzer over the indexed graph. |
| `analyze_contracts` | ✅ shipped | EP-005 | dedicated tool for the contracts analyzer. |
| `analyze_githistory` | ✅ shipped | EP-005 | dedicated tool for the git-history analyzer. |
| `analyze_interproc` | ✅ shipped | EP-005 | dedicated tool for the interproc analyzer. |
| `analyze_pdg` | ✅ shipped | EP-005 | dedicated tool for the PDG analyzer. |
| `analyze_pr_questions` | ✅ shipped | EP-007 | dedicated tool for the pr-questions generator. |
| `analyze_pr_risk` | ✅ shipped | EP-007 | dedicated tool for the pr-risk scorer. |
| `analyze_pr_signals` | ✅ shipped | EP-007 | dedicated tool for the pr-signals detector. |
| `analyze_taint` | ✅ shipped | EP-005 | dedicated tool for the taint analyzer. |
| `callees` | ✅ shipped | EP-001 | structural query: callees. |
| `callers` | ✅ shipped | EP-001 | structural query: callers. |
| `change_risk` | 🟡 partial | EP-020 | SW-117: evidence-based change risk assessment; scaffold: contract shape and surface wiring present. |
| `compare_branches` | ✅ shipped | EP-018 | SW-107: graph-level structured diff of two branch states keyed by canonical NodeId — added/removed/changed/moved entities + edges, incl. detected signature/contract change (zero engine egress; states materialized above the surface boundary). |
| `compound` | ✅ shipped | EP-011 | compound / Cypher-style graph query composing traversals+filters (G1). |
| `conflicts_prs` | ✅ shipped | EP-018 | SW-106: inter-PR conflict detection over the enumerated PR set — textual / graph-semantic / asymmetric contract-dependency pairwise report (zero engine egress). |
| `critique_review` | ✅ shipped | EP-018 | SW-108 (capstone): deterministic graph-evidence critique of an existing PR review — gap / over_flag / unsupported_claim items with machine-readable evidence (blast-radius count, centrality, edge kinds, taint provenance, review-anchor) + an honest unanchored tally; NO LLM prose (zero engine egress; the review fetch is the only surface egress). |
| `definition` | ✅ shipped | EP-001 | structural query: definition. |
| `distill` | ✅ shipped | EP-012 | session distillation into a compact decision record. |
| `explain_symbol` | 🟡 partial | EP-020 | SW-115: compact symbol-identity summary; scaffold: contract shape and surface wiring present. |
| `find_clones` | ✅ shipped | EP-013 | structural clone-group detection from a JSON config (G4). |
| `implementers` | ✅ shipped | EP-011 | structural query: types that implement/embed a symbol (G2). |
| `implements` | ✅ shipped | EP-011 | structural query: interfaces/types a symbol implements (G2). |
| `list_prs` | ✅ shipped | EP-018 | SW-105: read-only forge enumeration of open PRs (metadata only; no scoring, no comment posting). |
| `memory` | ✅ shipped | EP-012 | agent memory store/recall/forget operations. |
| `neighborhood` | ✅ shipped | EP-001 | structural query: k-hop neighborhood. |
| `overrides` | ✅ shipped | EP-011 | structural query: methods that override a symbol (G2). |
| `pr_comment` | ✅ shipped | EP-007 | render sticky PR review comment + optional merge gate. |
| `refactor` | ✅ shipped | EP-006 | apply a graph-aware refactor with an auditable change record. |
| `refactor_preview` | ✅ shipped | EP-006 | preview a graph-aware refactor (impact set, no mutation). |
| `references` | ✅ shipped | EP-001 | structural query: references. |
| `related_files` | 🟡 partial | EP-020 | SW-116: deterministic ranked read-first file list; scaffold: contract shape and surface wiring present. |
| `savings` | ✅ shipped | EP-003 | token-savings ledger readout (per-call/session/cumulative USD). |
| `search` | ✅ shipped | EP-001 | lexical / symbol search over the indexed graph. |
| `search_ast` | ✅ shipped | EP-013 | structural AST pattern query over the indexed graph (G3). |
| `search_semantic` | ✅ shipped | EP-001 | optional embedding search; `graphi index --semantic` generates+persists vectors, search reloads them (no re-embed); reports 'unavailable' cleanly when no embedder (OFF by default, FU-3 / SW-059+SW-061). |
| `skillgen` | ✅ shipped | EP-012 | deterministic skill generation from a procedure description. |
| `subtypes` | ✅ shipped | EP-011 | structural query: subtypes (inherits+implements composed) (G2). |
| `suggest_reviewers` | ✅ shipped | EP-018 | SW-107: ranked candidate-reviewer recommendation from local graph ownership/churn + affected-subgraph proximity over the touched set, with a transparent per-signal breakdown (zero engine egress). |
| `supertypes` | ✅ shipped | EP-011 | structural query: supertypes (inherits+implements composed) (G2). |
| `triage_prs` | ✅ shipped | EP-018 | SW-105: single-pass graph-derived ranked multi-PR triage over the enumerated PR set (zero engine egress). |
| `undo` | ✅ shipped | EP-006 | reverse a previously applied edit by undo token. |

## Surfaces (8)

| id | status | epic | note |
|---|---|---|---|
| `cli` | ✅ shipped | EP-008 | graphi CLI (query/search/analyze/edit/setup/parse). |
| `daemon` | ✅ shipped | EP-008 | Unix-socket daemon serving the shared client. |
| `github-action` | ✅ shipped | EP-007 | GitHub Action for the PR-review vertical (extensions/github-action). |
| `http` | ✅ shipped | EP-008 | loopback HTTP/SSE surface. |
| `mcp` | ✅ shipped | EP-008 | MCP stdio server (JSON-RPC 2.0, stdlib only). |
| `tui` | ✅ shipped | EP-008 | terminal UI surface. |
| `vscode` | ✅ shipped | EP-008 | VS Code extension (extensions/vscode). |
| `web` | ✅ shipped | EP-008 | React + Sigma web client (web/). |

## CLI subcommands (37)

| id | status | epic | note |
|---|---|---|---|
| `analyze` | ✅ shipped | - | run a registered analyzer over the graph |
| `claude` | ✅ shipped | - | short-verb alias for `setup` (register the MCP server) |
| `compare-branches` | ✅ shipped | - | graph-level diff of two graphi SQLite snapshots (paths, not git refs) |
| `compound` | ✅ shipped | - | compound / Cypher-style graph query |
| `conflicts-prs` | ✅ shipped | - | inter-PR conflict detection |
| `critique-review` | ✅ shipped | - | graph-evidence critique of an existing PR review |
| `daemon` | ✅ shipped | - | hot-index Unix-socket daemon lifecycle (start\|stop\|status) |
| `diagnose` | ✅ shipped | - | graph-derived diagnostics + suggested code-actions |
| `distill` | ✅ shipped | - | session distillation |
| `find-clones` | ✅ shipped | - | edge-profile clone detection |
| `help` | ✅ shipped | - | print the help blurb |
| `http` | ✅ shipped | - | loopback-only HTTP REST + SSE surface |
| `index` | ✅ shipped | - | ingest a repo into a durable store (optional --semantic embed pass) |
| `inline` | ✅ shipped | - | inline refactor over the edit saga (requires -root) |
| `list-prs` | ✅ shipped | - | read-only forge enumeration of open PRs |
| `mcp` | ✅ shipped | - | MCP stdio server (the agent-first surface) |
| `memory` | ✅ shipped | - | agent memory store\|recall\|forget |
| `parse` | ✅ shipped | - | parse a single file (the original SW-001 default) |
| `pr-comment` | ✅ shipped | - | sticky PR comment + optional risk-threshold merge gate |
| `privacy-audit` | ✅ shipped | - | local-first proof (CGo scan + canary egress guard) |
| `query` | ✅ shipped | - | structural query (callers\|callees\|references\|definition\|neighborhood) |
| `refactor` | ✅ shipped | - | commit a rename refactor through the edit saga |
| `refactor-preview` | ✅ shipped | - | impact-set preview of a refactor, no mutation |
| `safe-delete` | ✅ shipped | - | reference-safety-gated delete over the edit saga (requires -root) |
| `savings` | ✅ shipped | - | session token-savings readout (requires -ledger) |
| `search` | ✅ shipped | - | lexical search; -semantic runs the optional embedding search |
| `search-ast` | ✅ shipped | - | structural AST pattern query |
| `setup` | ✅ shipped | - | register the MCP stdio server into local MCP clients' configs |
| `setup-embedder` | ✅ shipped | - | print the opt-in semantic-search instructions (offline) |
| `skillgen` | ✅ shipped | - | deterministic skill generation |
| `suggest-reviewers` | ✅ shipped | - | ranked candidate-reviewer recommender |
| `triage-prs` | ✅ shipped | - | graph-derived multi-PR triage ranking |
| `tui` | ✅ shipped | - | interactive terminal surface |
| `ui` | ✅ shipped | - | short-verb alias for the zero-config index+serve flow |
| `undo` | ✅ shipped | - | reverse an applied edit by its undo token |
| `upgrade` | ✅ shipped | - | user-initiated self-update via the pinned install script |
| `version` | ✅ shipped | - | print version / commit / build date |

## Feature-Unit (5)

| id | status | epic | note |
|---|---|---|---|
| `FU-1` | ✅ shipped | EP-001 | Cross-file / cross-package linker pass (SW-050, engine/link) — resolves selector calls/imports against the committed symbol table; wired into ingest with a Go resolver. |
| `FU-2` | ✅ shipped | EP-001 | Curated pure-Go language tier (20 gotreesitter grammars + 2 stdlib) + opt-in graphi-broad CGO flavor. |
| `FU-3` | ✅ shipped | EP-001 | Optional embedder graceful-skip path + semantic search (SW-059); resolves OQ6. |
| `FU-4` | ✅ shipped | EP-001 | Traceability story (SW-060): consolidated architecture-plan + CI-enforced coverage matrix (this matrix). |
| `FU-5` | ✅ shipped | EP-001 | Per-language cross-file/cross-package resolvers over engine/link (SW-063), with ingest dispatching the linker per language. Resolvers: Go, TypeScript family (ts/tsx/js), Python, Rust, Java, Kotlin, C#, C, C++, Ruby, PHP, Lua, Bash. SQL is an honest no-op (no provable cross-file refs at this tier → skip+count). Each is heuristic tier, deterministic, byte-identical full-vs-incremental, never confirmed, never fabricates. |
