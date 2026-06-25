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

Total capabilities: **76**. See [`architecture-plan.md`](architecture-plan.md) for the design context.

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

## Analyzers (13)

| id | status | epic | note |
|---|---|---|---|
| `batched` | ✅ shipped | EP-004 | batched composite over impact+call-chain+metrics. |
| `call-chain` | ✅ shipped | EP-004 | caller/callee chain reconstruction. |
| `concept` | ✅ shipped | EP-004 | lexical-search-backed concept resolution (needs Searcher). |
| `contracts` | ✅ shipped | EP-005 | producer/consumer contract drift detection. |
| `git-history` | ✅ shipped | EP-005 | churn / bus-factor / co-change signals. |
| `impact` | ✅ shipped | EP-004 | forward/reverse blast-radius reachability. |
| `interproc` | ✅ shipped | EP-005 | interprocedural Sharir-Pnueli procedure summaries. |
| `metrics` | ✅ shipped | EP-004 | graph centrality / hub-bridge metrics. |
| `pdg` | ✅ shipped | EP-005 | program dependence graph (data + control dependence). |
| `pr-questions` | ✅ shipped | EP-007 | deterministic, no-LLM reviewer questions from findings. |
| `pr-risk` | ✅ shipped | EP-007 | deterministic per-region PR risk score (impact+taint). |
| `pr-signals` | ✅ shipped | EP-007 | hub/bridge/surprise signals on PR-changed code. |
| `taint` | ✅ shipped | EP-005 | flow-sensitive source→sink taint analysis. |

## MCP tools (27)

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
| `compound` | ✅ shipped | EP-011 | compound / Cypher-style graph query composing traversals+filters (G1). |
| `definition` | ✅ shipped | EP-001 | structural query: definition. |
| `implementers` | ✅ shipped | EP-011 | structural query: types that implement/embed a symbol (G2). |
| `implements` | ✅ shipped | EP-011 | structural query: interfaces/types a symbol implements (G2). |
| `neighborhood` | ✅ shipped | EP-001 | structural query: k-hop neighborhood. |
| `overrides` | ✅ shipped | EP-011 | structural query: methods that override a symbol (G2). |
| `pr_comment` | ✅ shipped | EP-007 | render sticky PR review comment + optional merge gate. |
| `refactor` | ✅ shipped | EP-006 | apply a graph-aware refactor with an auditable change record. |
| `refactor_preview` | ✅ shipped | EP-006 | preview a graph-aware refactor (impact set, no mutation). |
| `references` | ✅ shipped | EP-001 | structural query: references. |
| `savings` | ✅ shipped | EP-003 | token-savings ledger readout (per-call/session/cumulative USD). |
| `search` | ✅ shipped | EP-001 | lexical / symbol search over the indexed graph. |
| `search_semantic` | ✅ shipped | EP-001 | optional embedding search; `graphi index --semantic` generates+persists vectors, search reloads them (no re-embed); reports 'unavailable' cleanly when no embedder (OFF by default, FU-3 / SW-059+SW-061). |
| `subtypes` | ✅ shipped | EP-011 | structural query: subtypes (inherits+implements composed) (G2). |
| `supertypes` | ✅ shipped | EP-011 | structural query: supertypes (inherits+implements composed) (G2). |
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

## Feature-Unit (5)

| id | status | epic | note |
|---|---|---|---|
| `FU-1` | ✅ shipped | EP-001 | Cross-file / cross-package linker pass (SW-050, engine/link) — resolves selector calls/imports against the committed symbol table; wired into ingest with a Go resolver. |
| `FU-2` | ✅ shipped | EP-001 | Curated pure-Go language tier (20 gotreesitter grammars + 2 stdlib) + opt-in graphi-broad CGO flavor. |
| `FU-3` | ✅ shipped | EP-001 | Optional embedder graceful-skip path + semantic search (SW-059); resolves OQ6. |
| `FU-4` | ✅ shipped | EP-001 | Traceability story (SW-060): consolidated architecture-plan + CI-enforced coverage matrix (this matrix). |
| `FU-5` | ✅ shipped | EP-001 | Per-language cross-file/cross-package resolvers over engine/link (SW-063), with ingest dispatching the linker per language. Resolvers: Go, TypeScript family (ts/tsx/js), Python, Rust, Java, Kotlin, C#, C, C++, Ruby, PHP, Lua, Bash. SQL is an honest no-op (no provable cross-file refs at this tier → skip+count). Each is heuristic tier, deterministic, byte-identical full-vs-incremental, never confirmed, never fabricates. |
