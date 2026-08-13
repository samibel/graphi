# graphi — capability coverage matrix

<!-- GENERATED FILE — do not edit by hand.
     Source of truth: docs/coverage-matrix.yaml
     Regenerate:      go run ./cmd/coverage -generate
     CI-enforced:     internal/coverage drift guard fails the build if this
                      matrix omits, adds, or mislabels a live capability
                      (registered parsers/analyzers, the maximal MCP registry, present surfaces). -->

Every row below is checked against the live registries by the `coverage-matrix`
CI gate (`internal/coverage`). A docs-only change that contradicts the code — a
missing capability, a phantom "shipped" entry, or a live capability marked
"planned" — breaks the build. **Status legend:** ✅ shipped · 🟡 partial · ⏳ planned.
**Stability tier (SCOPE-01):** 🟢 stable · 🧪 labs · ⛔ disabled. The `stable` set is
FROZEN to exactly the 12 operations below; the guard fails the build if a 13th
row is tagged stable or one is dropped.

> **`tier` is not GA.** The `tier` column answers exactly one machine question —
> *is this row one of the 12 frozen operations?* Parser and surface rows are
> structurally ineligible for `stable`, so `go`, `cli`, and `mcp` read `labs`
> despite being the entire GA scope. GA is a prose tier defined in
> [`stability-tiers.md`](stability-tiers.md).

**The 12 stable operations (frozen):** `index`, `agent_brief`, `callees`, `callers`, `change_risk`, `definition`, `explain_symbol`, `impact`, `neighborhood`, `references`, `related_files`, `search`.

**MCP profiles:** the default in-process `graphi mcp` binding advertises exactly **11 Stable tools**. Every binding then removes operations its concrete transport cannot execute; the current daemon binding exposes seven and honestly omits its four unwired agent-tool RPCs. `graphi mcp -labs` explicitly opts into the capability-gated Labs catalog; this matrix records its maximal **52-tool** union (41 Labs, 0 disabled), not a promise that every optional service or transport is wired. `index` is Stable lifecycle, not an MCP tool.

Total capabilities: **164**. See [`architecture-plan.md`](architecture-plan.md) for the design context.

## Parsers (23)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `bash` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `c` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `c_sharp` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `cpp` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `css` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `go` | 🧪 labs | ✅ shipped | EP-001 | Go AST→graph extractor (reference SymbolExtractor, go/ast, CGo-free). |
| `hcl` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `html` | ⛔ disabled | ⏳ planned | EP-001 | pure-Go grammar present but not subset-buildable in isolation (gotreesitter scanner co-located with grammar_subset_blade); deferred to graphi-broad. |
| `java` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `javascript` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `json` | 🧪 labs | ✅ shipped | EP-001 | stdlib structural parser (CGo-free). |
| `kotlin` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `lua` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `markdown` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `php` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `python` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `ruby` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `rust` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `sql` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `toml` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `tsx` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `typescript` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |
| `yaml` | 🧪 labs | ✅ shipped | EP-001 | pure-Go gotreesitter grammar (CGo-free, subset-tagged). |

## Analyzers (22)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `batched` | 🧪 labs | ✅ shipped | EP-004 | batched composite over impact+call-chain+metrics. |
| `call-chain` | 🧪 labs | ✅ shipped | EP-004 | caller/callee chain reconstruction. |
| `communities` | 🧪 labs | ✅ shipped | EP-017 | SW-104: SW-103 Louvain community detection surfaced behind the single dispatch table. |
| `compare-branches` | 🧪 labs | ✅ shipped | EP-018 | SW-107: graph-level branch diff over TWO already-built read-only graph states (materialized above the surface boundary) — added/removed/changed/moved entities + edges added/removed, keyed by canonical NodeId (not line ranges); detects signature/contract changes and correlates cross-file moves by path-independent symbol identity; pure local set-diff, zero engine egress (the engine never resolves a git ref). |
| `concept` | 🧪 labs | ✅ shipped | EP-004 | lexical-search-backed concept resolution (needs Searcher). |
| `conflicts-prs` | 🧪 labs | ✅ shipped | EP-018 | SW-106: inter-PR conflict detection over an enumerated PR set — textual overlap + shared file/symbol/high-centrality node + asymmetric contract-dependency edge check; entity→PRs inverted index, byte-stable pairwise report (zero engine egress; forge enumeration stays at the surface). |
| `contracts` | 🧪 labs | ✅ shipped | EP-005 | producer/consumer contract drift detection. |
| `critique-review` | 🧪 labs | ✅ shipped | EP-018 | SW-108 (capstone): deterministic graph-evidence critique of an EXISTING PR review — replays the EP-007 risk/blast/centrality/taint oracle over the touched set and runs a three-way diff (gap / over_flag / unsupported_claim) against the review; deterministic resolveRef anchoring with an honest unanchored tally (never guessed); NO LLM prose; total order type→NodeId→anchor; zero engine egress (the only egress is the surface review fetch). |
| `git-history` | 🧪 labs | ✅ shipped | EP-005 | churn / bus-factor / co-change signals. |
| `impact` | 🧪 labs | ✅ shipped | EP-004 | analyzer implementation reachable through the Labs generic dispatcher; the dedicated fixed-dispatch MCP/CLI impact operation is the Stable surface. |
| `interproc` | 🧪 labs | ✅ shipped | EP-005 | interprocedural fixpoint over per-procedure gen/kill summaries. |
| `metrics` | 🧪 labs | ✅ shipped | EP-004 | graph centrality / hub-bridge metrics. |
| `notebook-ingest` | 🧪 labs | ✅ shipped | EP-017 | SW-104: SW-100 notebook (.ipynb) cell provenance surfaced behind the single dispatch table. |
| `pdg` | 🧪 labs | ✅ shipped | EP-005 | program dependence graph (data + control dependence). |
| `pr-questions` | 🧪 labs | ✅ shipped | EP-007 | deterministic, no-LLM reviewer questions from findings. |
| `pr-risk` | 🧪 labs | ✅ shipped | EP-007 | deterministic per-region PR risk score (impact+taint). |
| `pr-signals` | 🧪 labs | ✅ shipped | EP-007 | hub/bridge/surprise signals on PR-changed code. |
| `suggest-reviewers` | 🧪 labs | ✅ shipped | EP-018 | SW-107: ranked candidate-reviewer recommender — file-granular ownership + recency-decayed churn over the touched files (fixed decay reference, never now()) plus symbol-granular affected-subgraph proximity (callers/callees/contract neighbors, centrality-weighted), folded into a fixed-integer composite with a transparent per-signal breakdown; deterministic total order (composite DESC, reviewer identity ASC); zero engine egress. |
| `taint` | 🧪 labs | ✅ shipped | EP-005 | flow-sensitive source→sink taint analysis. |
| `taint-query` | 🧪 labs | ✅ shipped | EP-017 | SW-104: SW-102 interprocedural taint verdict + flows surfaced behind the single dispatch table. |
| `triage-prs` | 🧪 labs | ✅ shipped | EP-018 | SW-105: single-pass graph-derived multi-PR triage ranking; reuses the EP-007 pr-risk kernel over an enumerated PR set (zero engine egress; forge enumeration stays at the surface). |
| `watcher-status` | 🧪 labs | ✅ shipped | EP-017 | SW-104: SW-101 filesystem-watcher health (honest per-root errors) surfaced behind the single dispatch table. |

## MCP tools (52)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `agent_brief` | 🟢 stable | ✅ shipped | EP-024 | SW-134: bounded, cited task-start context packet derived from the live graph and local memory store (start-here files, key symbols, known facts, risks). |
| `analyze` | 🧪 labs | ✅ shipped | EP-004 | run a named graph analyzer over the indexed graph. |
| `analyze_contracts` | 🧪 labs | ✅ shipped | EP-005 | dedicated tool for the contracts analyzer. |
| `analyze_githistory` | 🧪 labs | ✅ shipped | EP-005 | dedicated tool for the git-history analyzer. |
| `analyze_interproc` | 🧪 labs | ✅ shipped | EP-005 | dedicated tool for the interproc analyzer. |
| `analyze_pdg` | 🧪 labs | ✅ shipped | EP-005 | dedicated tool for the PDG analyzer. |
| `analyze_pr_questions` | 🧪 labs | ✅ shipped | EP-007 | dedicated tool for the pr-questions generator. |
| `analyze_pr_risk` | 🧪 labs | ✅ shipped | EP-007 | dedicated tool for the pr-risk scorer. |
| `analyze_pr_signals` | 🧪 labs | ✅ shipped | EP-007 | dedicated tool for the pr-signals detector. |
| `analyze_taint` | 🧪 labs | ✅ shipped | EP-005 | dedicated tool for the taint analyzer. |
| `callees` | 🟢 stable | ✅ shipped | EP-001 | structural query: callees. |
| `callers` | 🟢 stable | ✅ shipped | EP-001 | structural query: callers. |
| `change_impact` | 🧪 labs | ✅ shipped | - | P1 change intelligence (Change Risk 2.0): changed symbols, public-API subset, direct dependents with evidence, bounded transitive closure, covering tests via engine/testintel, co-change partners from bounded git history ('B usually changes with A — not in this change'), config files in the diff, explicit reasons, and a risk level (change_risk thresholds + one-step public-API escalation); confidence derived from consumed edge tiers, never invented; the frozen-stable change_risk operation keeps its bytes; byte-parity with `graphi change-impact`; Labs-only. |
| `change_risk` | 🟢 stable | ✅ shipped | EP-020 | SW-117: evidence-based low/medium/high/unknown blast-radius estimate from fan-in and dependent files, with diff targeting; CLI parity via `graphi change-risk`. |
| `compare_branches` | 🧪 labs | ✅ shipped | EP-018 | SW-107: graph-level structured diff of two branch states keyed by canonical NodeId — added/removed/changed/moved entities + edges, incl. detected signature/contract change (zero engine egress; states materialized above the surface boundary). |
| `compound` | 🧪 labs | ✅ shipped | EP-011 | compound / Cypher-style graph query composing traversals+filters (G1). |
| `conflicts_prs` | 🧪 labs | ✅ shipped | EP-018 | SW-106: inter-PR conflict detection over the enumerated PR set — textual / graph-semantic / asymmetric contract-dependency pairwise report (zero engine egress). |
| `critique_review` | 🧪 labs | ✅ shipped | EP-018 | SW-108 (capstone): deterministic graph-evidence critique of an existing PR review — gap / over_flag / unsupported_claim items with machine-readable evidence (blast-radius count, centrality, edge kinds, taint provenance, review-anchor) + an honest unanchored tally; NO LLM prose (zero engine egress; the review fetch is the only surface egress). |
| `definition` | 🟢 stable | ✅ shipped | EP-001 | structural query: definition. |
| `distill` | 🧪 labs | ✅ shipped | EP-012 | session distillation into a compact decision record. |
| `explain_symbol` | 🟢 stable | ✅ shipped | EP-020 | SW-115: compact, cited symbol-identity summary (definition + callers/callees/references) over the live graph; ambiguous references return candidates; CLI parity via `graphi explain-symbol`. |
| `find_clones` | 🧪 labs | ✅ shipped | EP-013 | structural clone-group detection from a JSON config (G4). |
| `graph_health` | 🧪 labs | ✅ shipped | - | P1 trust surface (PRD §17): canonical contract-§2 trust-report document (snapshot state, freshness facts, coverage, edge confidence tiers, resolution gaps, boundaries, optional exploratory-v1\|review-v1\|automated-change-v1 policy verdict) for repository or target scope; byte-parity with `graphi trust-report --json` through the single client.TrustReport composition; read-only fail-closed observer (missing evidence reads UNVERIFIED/UNAVAILABLE, never PASS); Labs-only pending a promotion decision. |
| `hotspots` | 🧪 labs | ✅ shipped | - | P2 git intelligence: churn × dependency-centrality file ranking (commits-in-window × (1 + graph edge endpoints), integer breakdown per row) with single-author bus-factor warnings for the ranked hotspots; history via the surface-boundary bounded `git log` provider (surfaces/gitlog — the engine stays exec-free), centrality via the compact BriefStats aggregate; typed unavailable without a git provider; byte-parity with `graphi hotspots`; Labs-only. |
| `impact` | 🟢 stable | ✅ shipped | EP-004 | dedicated default-profile entry for the frozen Stable impact operation; dispatch is fixed to StableClient.Impact with no generic analyzer selector. |
| `implementers` | 🧪 labs | ✅ shipped | EP-011 | structural query: types that implement/embed a symbol (G2). |
| `implements` | 🧪 labs | ✅ shipped | EP-011 | structural query: interfaces/types a symbol implements (G2). |
| `list_prs` | 🧪 labs | ✅ shipped | EP-018 | SW-105: read-only forge enumeration of open PRs (metadata only; no scoring, no comment posting). |
| `memory` | 🧪 labs | ✅ shipped | EP-012 | agent memory store/recall/forget operations. |
| `neighborhood` | 🟢 stable | ✅ shipped | EP-001 | structural query: k-hop neighborhood. |
| `overrides` | 🧪 labs | ✅ shipped | EP-011 | structural query: methods that override a symbol (G2). |
| `pr_comment` | 🧪 labs | ✅ shipped | EP-007 | render sticky PR review comment + optional merge gate. |
| `refactor` | 🧪 labs | ✅ shipped | EP-006 | apply a graph-aware refactor with an auditable change record. |
| `refactor_preview` | 🧪 labs | ✅ shipped | EP-006 | preview a graph-aware refactor (impact set, no mutation). |
| `references` | 🟢 stable | ✅ shipped | EP-001 | structural query: references. |
| `related_files` | 🟢 stable | ✅ shipped | EP-020 | SW-116: deterministic, evidence-cited read-first file ranking by graph proximity with direction filtering; CLI parity via `graphi related-files`. |
| `repo_overview` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: one-call repository summary — totals with edge-confidence tiers, directory tree, language mix, entry-point candidates, high-centrality symbols, test and generated/vendored areas, external boundaries, suggested next calls; the default call reads only the compact BriefStats/TrustStats aggregates, `communities` is the documented opt-in full-graph pass; byte-parity with `graphi repo-overview` through the single engine/agenttools/overview assembly; Labs-only. |
| `savings` | 🧪 labs | ✅ shipped | EP-003 | token-savings ledger readout (per-call/session/cumulative USD). |
| `search` | 🟢 stable | ✅ shipped | EP-001 | lexical / symbol search over the indexed graph. |
| `search_ast` | 🧪 labs | ✅ shipped | EP-013 | structural AST pattern query over the indexed graph (G3). |
| `search_hybrid` | 🧪 labs | ✅ shipped | - | P3 repository search: embedding-free hybrid ranking for multi-token queries — lexical retrieval re-ranked by identifier-segment matching (camelCase/snake_case aware), path relevance, and bounded graph degree; deterministic integer weight model, hash-stamped in every summary; no vector database, no egress; the optional search_semantic opt-in is untouched; byte-parity with `graphi search-hybrid`; Labs-only. |
| `search_semantic` | 🧪 labs | ✅ shipped | EP-001 | optional embedding search; `graphi index --semantic` generates+persists vectors, search reloads them (no re-embed); reports 'unavailable' cleanly when no embedder (OFF by default, FU-3 / SW-059+SW-061). |
| `skillgen` | 🧪 labs | ✅ shipped | EP-012 | deterministic skill generation from a procedure description. |
| `strict_query` | 🧪 labs | ✅ shipped | - | P1 strict query (PRD v1.0 §8 Phase 9): runs a Stable structural query unchanged, then withholds result edges below `minimum_tier` (confirmed\|derived\|heuristic) and reports the withheld count; a result emptied by the filter always carries an explicit limitation, so filtered emptiness never reads as proven emptiness. Optional fail-closed policy preflight blocks before the query runs. Byte-parity with `graphi query-strict` through the single client.ComposeStrictQuery composition; operations closed to the Stable structural set; Labs-only. |
| `subtypes` | 🧪 labs | ✅ shipped | EP-011 | structural query: subtypes (inherits+implements composed) (G2). |
| `suggest_reviewers` | 🧪 labs | ✅ shipped | EP-018 | SW-107: ranked candidate-reviewer recommendation from local graph ownership/churn + affected-subgraph proximity over the touched set, with a transparent per-signal breakdown (zero engine egress). |
| `supertypes` | 🧪 labs | ✅ shipped | EP-011 | structural query: supertypes (inherits+implements composed) (G2). |
| `symbol_context` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: unified single-call symbol view — definition site with optional token-budgeted source snippet, type-hierarchy relations, callers/callees/references, covering tests via a bounded reverse walk (per-node edge limit, global node budget, depth clamp — never a whole-graph scan), and a change_risk-consistent risk level; byte-parity with `graphi symbol-context` through the single engine/agenttools/symbolcontext assembly; Labs-only pending a promotion decision. |
| `task_context` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: free-text task → deterministically ranked, cited, token-budgeted context bundle (primary seeds, related symbols, callers/callees, nearby tests and configs, related-file roll-up, change_risk-consistent risk, recommended read order); ranking is a fixed integer weight model whose hash is stamped in every summary — no floats, no LLM estimates; byte-parity with `graphi task-context` through the single engine/agenttools/taskctx assembly; Labs-only. |
| `test_impact` | 🧪 labs | ✅ shipped | - | P1 test intelligence: diff/target → must_run (direct-call evidence at confirmed/derived tier), recommended (transitive reach, naming conventions, same-directory test files), probably_unaffected (remaining test-file universe, counted in full), unknown (unresolvable diff paths — never guessed); the symbol↔test mapping is DERIVED on demand by engine/testintel (bounded walks + exact SourcePath lookups — no materialized edges, frozen index output untouched); byte-parity with `graphi test-impact`; Labs-only. |
| `triage_prs` | 🧪 labs | ✅ shipped | EP-018 | SW-105: single-pass graph-derived ranked multi-PR triage over the enumerated PR set (zero engine egress). |
| `undo` | 🧪 labs | ✅ shipped | EP-006 | reverse a previously applied edit by undo token. |

## Surfaces (7)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `cli` | 🧪 labs | ✅ shipped | EP-008 | graphi CLI (query/search/analyze/edit/setup/parse). |
| `daemon` | 🧪 labs | ✅ shipped | EP-008 | Unix-socket daemon serving the shared client. |
| `github-action` | 🧪 labs | ✅ shipped | EP-007 | GitHub Action for the PR-review vertical (extensions/github-action). |
| `http` | 🧪 labs | ✅ shipped | EP-008 | loopback HTTP/SSE surface. |
| `mcp` | 🧪 labs | ✅ shipped | EP-008 | MCP stdio server (JSON-RPC 2.0, stdlib only). |
| `vscode` | 🧪 labs | ✅ shipped | EP-008 | VS Code extension (extensions/vscode). |
| `web` | 🧪 labs | ✅ shipped | EP-008 | React + Sigma web client (web/). |

## CLI subcommands (55)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `agent-brief` | 🧪 labs | 🟡 partial | - | bounded, cited task-start context packet for agents |
| `analyze` | 🧪 labs | ✅ shipped | - | run a registered analyzer over the graph |
| `change-impact` | 🧪 labs | ✅ shipped | - | P1 change intelligence (Change Risk 2.0): changed symbols, public API, dependents, covering tests, co-change partners, reasons, risk for a target or a piped `git diff` range; byte-parity with the change_impact MCP tool |
| `change-risk` | 🧪 labs | ✅ shipped | EP-020 | evidence-based local blast-radius estimate (low/medium/high/unknown) for a symbol, path, or unified diff; byte-parity with the change_risk MCP tool |
| `claude` | 🧪 labs | ✅ shipped | - | short-verb alias for `setup` (register the MCP server) |
| `compare` | 🧪 labs | ✅ shipped | - | name-based wrapper over compare-branches: resolves snapshot names plus the reserved `current` (live store) to paths; byte-identical diff output |
| `compare-branches` | 🧪 labs | ✅ shipped | - | graph-level diff of two graphi SQLite snapshots (paths, not git refs) |
| `compound` | 🧪 labs | ✅ shipped | - | compound / Cypher-style graph query |
| `conflicts-prs` | 🧪 labs | ✅ shipped | - | inter-PR conflict detection |
| `critique-review` | 🧪 labs | ✅ shipped | - | graph-evidence critique of an existing PR review |
| `daemon` | 🧪 labs | ✅ shipped | - | hot-index Unix-socket daemon lifecycle (start\|stop\|status) |
| `diagnose` | 🧪 labs | ✅ shipped | - | graph-derived diagnostics + suggested code-actions |
| `distill` | 🧪 labs | ✅ shipped | - | session distillation |
| `doctor` | 🧪 labs | ✅ shipped | EP-023 | read-only diagnostic checkup (binary, PATH, MCP clients, DB, privacy, local-first) |
| `explain-symbol` | 🧪 labs | ✅ shipped | EP-020 | compact, cited symbol identity summary; byte-parity with the explain_symbol MCP tool |
| `find-clones` | 🧪 labs | ✅ shipped | - | edge-profile clone detection |
| `help` | 🧪 labs | ✅ shipped | - | print the help blurb |
| `hotspots` | 🧪 labs | ✅ shipped | - | P2 git intelligence: churn × dependency-centrality file ranking with bus-factor warnings over the session repository's bounded git history; byte-parity with the hotspots MCP tool |
| `http` | 🧪 labs | ✅ shipped | - | loopback-only HTTP REST + SSE surface |
| `index` | 🟢 stable | ✅ shipped | - | ingest a repo into a durable store (optional --semantic embed pass) |
| `inline` | 🧪 labs | ✅ shipped | - | inline refactor over the edit saga (requires -root) |
| `list-prs` | 🧪 labs | ✅ shipped | - | read-only forge enumeration of open PRs |
| `mcp` | 🧪 labs | ✅ shipped | - | MCP stdio server (the agent-first surface) |
| `memory` | 🧪 labs | ✅ shipped | - | agent memory store\|recall\|forget |
| `parse` | 🧪 labs | ✅ shipped | - | parse a single file (the original SW-001 default) |
| `pr-comment` | 🧪 labs | ✅ shipped | - | sticky PR comment + optional risk-threshold merge gate |
| `privacy-audit` | 🧪 labs | ✅ shipped | - | local-first proof (CGo scan + canary egress guard) |
| `query` | 🧪 labs | ✅ shipped | - | structural query (callers\|callees\|references\|definition\|neighborhood) |
| `query-strict` | 🧪 labs | ✅ shipped | - | P1 strict-query prototype (PRD §28 option A): runs the unchanged stable query, then excludes result edges below -min-tier and wraps the canonical result in an envelope carrying the exclusion count — filtered emptiness always carries an explicit limitation, never bare emptiness; optional fail-closed -policy preflight (FAIL/UNKNOWN blocks the query with the trust exit code); no tier is ever rewritten, stable query semantics untouched |
| `rebuild` | 🧪 labs | ✅ shipped | - | flagless cold full re-index of the auto-managed per-repo graph (facade over the stable index lifecycle's --full pass) |
| `refactor` | 🧪 labs | ✅ shipped | - | commit a rename refactor through the edit saga |
| `refactor-preview` | 🧪 labs | ✅ shipped | - | impact-set preview of a refactor, no mutation |
| `related-files` | 🧪 labs | ✅ shipped | EP-020 | ranked, cited read-first file list; byte-parity with the related_files MCP tool |
| `repo-overview` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: one-call repository summary (structure, languages, entrypoints, central symbols, tests, boundaries, optional communities); byte-parity with the repo_overview MCP tool |
| `safe-delete` | 🧪 labs | ✅ shipped | - | reference-safety-gated delete over the edit saga (requires -root) |
| `savings` | 🧪 labs | ✅ shipped | - | session token-savings readout (requires -ledger) |
| `search` | 🧪 labs | ✅ shipped | - | lexical search; -semantic runs the optional embedding search |
| `search-ast` | 🧪 labs | ✅ shipped | - | structural AST pattern query |
| `search-hybrid` | 🧪 labs | ✅ shipped | - | P3 repository search: embedding-free hybrid search (identifier segments + path + bounded graph degree); byte-parity with the search_hybrid MCP tool |
| `setup` | 🧪 labs | ✅ shipped | - | register the MCP stdio server into local MCP clients' configs |
| `setup-embedder` | 🧪 labs | ✅ shipped | - | print the opt-in semantic-search instructions (offline) |
| `skillgen` | 🧪 labs | ✅ shipped | - | deterministic skill generation |
| `snapshot` | 🧪 labs | ✅ shipped | - | list/freeze/delete named per-repo graph states under <state>/snapshots (atomic tmp+rename builds; input to `graphi compare`) |
| `status` | 🧪 labs | ✅ shipped | - | read-only freshness report (repo/branch/drift/last-sync, --json; exit 0 current, 1 actionable, 2 error); opens store + sidecar mode=ro and never creates state |
| `suggest-reviewers` | 🧪 labs | ✅ shipped | - | ranked candidate-reviewer recommender |
| `symbol-context` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: unified single-call symbol view (definition + snippet, hierarchy, callers/callees/references, covering tests, risk); byte-parity with the symbol_context MCP tool |
| `sync` | 🧪 labs | ✅ shipped | - | flagless incremental update of the auto-managed per-repo graph (facade over the stable index lifecycle; branch-switch aware; matrix row labs because the stable-12 set is frozen) |
| `task-context` | 🧪 labs | ✅ shipped | - | P0 agent intelligence: free-text task → ranked, cited, token-budgeted context bundle with recommended read order; byte-parity with the task_context MCP tool |
| `test-impact` | 🧪 labs | ✅ shipped | - | P1 test intelligence: must_run/recommended/probably_unaffected/unknown test buckets for a target or a piped `git diff` range; byte-parity with the test_impact MCP tool |
| `triage-prs` | 🧪 labs | ✅ shipped | - | graph-derived multi-PR triage ranking |
| `trust-report` | 🧪 labs | ✅ shipped | - | P1 trust surface: repository/target trust report over the persisted TrustSnapshot (--json emits the canonical contract-§2 document; --target/--policy/--details/--limit); strict read-only observer that never creates state; byte-parity with the graph_health MCP tool through the single client.TrustReport composition |
| `ui` | 🧪 labs | ✅ shipped | - | short-verb alias for the zero-config index+serve flow |
| `undo` | 🧪 labs | ✅ shipped | - | reverse an applied edit by its undo token |
| `upgrade` | 🧪 labs | ✅ shipped | - | user-initiated self-update via the pinned install script |
| `version` | 🧪 labs | ✅ shipped | - | print version / commit / build date |

## Feature-Unit (5)

| id | tier | status | epic | note |
|---|---|---|---|---|
| `FU-1` | 🧪 labs | ✅ shipped | EP-001 | Cross-file / cross-package linker pass (SW-050, engine/link) — resolves selector calls/imports against the committed symbol table; wired into ingest with a Go resolver. |
| `FU-2` | 🧪 labs | ✅ shipped | EP-001 | Curated pure-Go language tier (20 gotreesitter grammars + 2 stdlib) + opt-in graphi-broad CGO flavor. |
| `FU-3` | 🧪 labs | ✅ shipped | EP-001 | Optional embedder graceful-skip path + semantic search (SW-059); resolves OQ6. |
| `FU-4` | 🧪 labs | ✅ shipped | EP-001 | Traceability story (SW-060): consolidated architecture-plan + CI-enforced coverage matrix (this matrix). |
| `FU-5` | 🧪 labs | ✅ shipped | EP-001 | Per-language cross-file/cross-package resolvers over engine/link (SW-063), with ingest dispatching the linker per language. Resolvers: Go, TypeScript family (ts/tsx/js), Python, Rust, Java, Kotlin, C#, C, C++, Ruby, PHP, Lua, Bash. SQL is an honest no-op (no provable cross-file refs at this tier → skip+count). Each is heuristic tier, deterministic, byte-identical full-vs-incremental, never confirmed, never fabricates. |
