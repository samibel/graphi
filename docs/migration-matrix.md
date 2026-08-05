# graphi — P2 architecture migration matrix

<!-- GENERATED FILE — do not edit by hand.
     Source of truth: docs/migration-matrix.yaml
     Regenerate:      go run ./cmd/archmatrix -generate
     CI-enforced:     internal/archmatrix drift guard fails the build if a live
                      surfaces/client.Client method or error sentinel is missing
                      from this matrix, or if the matrix names one that is gone. -->

This is the ARCH-P0 inventory for the P2 modularization: every method of the
broad `surfaces/client.Client` contract, the bounded context that will own it,
the application service it moves to, and the phase that moves it.

The method set is derived from the live interface by reflection and the sentinel
set from the package source, so this file cannot drift from the code: adding a
method to the legacy client without deciding its owning context fails CI.

**Implementation legend:** ✅ full — executes the real operation · ⛔ unavailable — refuses with a typed sentinel, doing no work · 🟡 typed-skip — returns a typed graceful-skip payload with no error.

> `unavailable` counts are the compatibility debt the PRD forbids growing: no new
> capability may be added by appending a method here and stubbing it out in the
> remote clients.

## Summary

29 methods across 5 bounded contexts.

| Context | Target service | Phase | Methods | `unavailable` on HTTP | `unavailable` on daemon |
|---|---|---|---|---|---|
| Graph Read (graphread) | `app/graphread` | 4 | 12 | 2 | 5 |
| Code Change (codechange) | `app/codechange` | 5 | 5 | 5 | 5 |
| Review & Forge (review) | `app/review` | 6 | 7 | 7 | 7 |
| Knowledge (knowledge) | `app/knowledge` | 7 | 4 | 4 | 4 |
| Operations & Capability (operations) | `app/operations` | 7 | 1 | 1 | 0 |

## Graph Read — `app/graphread` (phase 4)

| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |
|---|---|---|---|---|---|---|
| `Analyze` | ✅ full | ✅ full | ✅ full | cli, daemon, http, mcp, tui | `ErrAnalysisUnavailable` | Named analyzer dispatch. |
| `ChangeRisk` | ✅ full | ✅ full | ⛔ unavailable | cli, http, mcp | `ErrAgentToolsUnavailable`, `ErrBadInput` | EP-020 agent tool. HTTP serves it but rejects diff targeting as bad input rather than stubbing the whole method. |
| `Compound` | ✅ full | ✅ full | ✅ full | daemon, http, mcp | — | Compound/Cypher-style query; byte-identical to a fixed Query. |
| `Diagnose` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli | `ErrDiagnosticUnavailable` | Read-only diagnostics. Direct also owns surface-side generated-marker detection, which is filesystem I/O the application service must inherit deliberately. |
| `ExplainSymbol` | ✅ full | ✅ full | ⛔ unavailable | cli, http, mcp | `ErrAgentToolsUnavailable` | EP-020 agent tool over the shared query/search deps. |
| `FindClones` | ✅ full | ✅ full | ✅ full | cli, daemon, http, mcp | — | Clone-group detection; singleton pattern query. |
| `Query` ⭐ | ✅ full | ✅ full | ✅ full | cli, daemon, http, mcp, tui | — | Structural query core; the canonical encoder is engine/query.Marshal. |
| `RelatedFiles` | ✅ full | ✅ full | ⛔ unavailable | cli, http, mcp | `ErrAgentToolsUnavailable` | EP-020 agent tool. |
| `Search` | ✅ full | ✅ full | ✅ full | cli, daemon, http, mcp, tui | `ErrSearchUnavailable` | Lexical/symbol search. |
| `SearchAST` | ✅ full | ✅ full | ✅ full | cli, daemon, http, mcp | — | Structural AST pattern query; rides the query.Marshal encoder. |
| `SemanticSearch` | ✅ full | ✅ full | 🟡 typed-skip | cli, http, mcp | — | Optional embedder. The unconfigured path is a TYPED unavailable response with no error and zero network — it must not be folded into a sentinel. |
| `TrustReport` ⭐ | ✅ full | ⛔ unavailable | ⛔ unavailable | mcp | `ErrTrustUnavailable` | The only method returning a verdict and state alongside bytes. Its encoder lives at surface rank (surfaces/client/trust_report.go), so the application service must take ownership of that composition. |

## Code Change — `app/codechange` (phase 5)

| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |
|---|---|---|---|---|---|---|
| `Inline` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli | `ErrEditUnavailable` | Edit operation. |
| `Refactor` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, mcp | `ErrEditUnavailable` | Commits through the shared edit applier and persists an audit record; the saga and undo-token semantics are the migration risk. |
| `RefactorPreview` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, mcp | `ErrEditUnavailable` | Impact set BEFORE mutation; must remain non-mutating. |
| `SafeDelete` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli | `ErrEditUnavailable` | Edit operation. |
| `Undo` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, mcp | `ErrEditUnavailable` | Reverses a recorded change by undo token. |

## Review & Forge — `app/review` (phase 6)

| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |
|---|---|---|---|---|---|---|
| `CompareBranches` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrAnalysisUnavailable`, `ErrCompareUnavailable` | Needs a branch-state materializer; the egress boundary must stay in the outer adapter. |
| `ConflictsPRs` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrAnalysisUnavailable`, `ErrForgeUnavailable` | Local conflict detection. |
| `CritiqueReview` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrAnalysisUnavailable`, `ErrReviewFetchUnavailable` | Local critique; needs a review fetcher. |
| `ListPRs` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrForgeUnavailable` | Forge enumeration; metadata only, no scoring. |
| `PrComment` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, mcp | `ErrReviewUnavailable` | The one publish path. Dry-run rendering must stay egress-free and separable from publishing. |
| `SuggestReviewers` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrAnalysisUnavailable` | Local analysis over a supplied diff. |
| `TriagePRs` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrAnalysisUnavailable`, `ErrForgeUnavailable` | Local ranking over already-materialized PR metadata. |

## Knowledge — `app/knowledge` (phase 7)

| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |
|---|---|---|---|---|---|---|
| `Brief` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, http, mcp | `ErrBriefUnavailable` | The only method returning two payloads (JSON + Markdown). **Open decision: Knowledge or Graph Read? The PRD lists Brief under the Knowledge context, but agent_brief is one of the 12 frozen stable read operations and shares agentDeps with ExplainSymbol/RelatedFiles/ChangeRisk, which are Graph Read. Placement decides which service owns the stable-12 encoder.** |
| `Distill` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, daemon, http, mcp | `ErrDistillUnavailable` | Serialized by a plain json.Marshal helper in direct.go, unlike every other path — the application service must not silently change those bytes. |
| `Memory` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, daemon, http, mcp | `ErrExportPathRejected`, `ErrMemoryUnavailable` | Carries the SAFE-01 export-path rejection; that refusal is a safety contract, not a capability gap. |
| `SkillGen` | ✅ full | ⛔ unavailable | ⛔ unavailable | cli, daemon, http, mcp | `ErrSkillGenUnavailable` | Same plain-marshal caveat as Distill. |

## Operations & Capability — `app/operations` (phase 7)

| Method | Direct | HTTP | Daemon | Surfaces today | Refusal sentinels | Note |
|---|---|---|---|---|---|---|
| `Savings` | ✅ full | ⛔ unavailable | ✅ full | cli, daemon, mcp | `ErrSavingsUnavailable` | Savings-ledger readout. Capability reporting and runtime status join this context in phase 7 but are not Client methods today. |

## ⭐ Phase 3 differential pilots

These use cases are migrated first, behind the compatibility facade, to prove
the legacy and application paths agree before the bulk migration starts: `Query`, `TrustReport`.

## Open decisions

These placements are the plan of record but need maintainer sign-off before the
phase that acts on them begins.

| Method | Proposed context | Question |
|---|---|---|
| `Brief` | knowledge | Knowledge or Graph Read? The PRD lists Brief under the Knowledge context, but agent_brief is one of the 12 frozen stable read operations and shares agentDeps with ExplainSymbol/RelatedFiles/ChangeRisk, which are Graph Read. Placement decides which service owns the stable-12 encoder. |

## Error sentinel inventory

Every exported `Err…` in `surfaces/client`. The refactor must preserve these
identities: an unwired capability stays fail-closed, and no unavailable path may
quietly become a success.

| Sentinel | Kind | Note |
|---|---|---|
| `ErrAgentToolsUnavailable` | capability | EP-020 agent tools not available on this transport. |
| `ErrAnalysisUnavailable` | capability | Analysis service not wired. |
| `ErrBadInput` | validation | HTTP 400: the request was rejected as malformed or unsupported on this surface. |
| `ErrBriefUnavailable` | capability | Brief not available on this transport. |
| `ErrCompareUnavailable` | capability | Branch-state materializer not wired. |
| `ErrDiagnosticUnavailable` | capability | Diagnostics not available on this transport. |
| `ErrDistillUnavailable` | capability | Distiller not wired. |
| `ErrEditUnavailable` | capability | Edit applier/recorder not wired. |
| `ErrExportPathRejected` | safety | SAFE-01: a server-side export path is refused outright. This is a safety contract that must survive the migration unchanged, NOT a capability gap to be filled. |
| `ErrForgeUnavailable` | capability | Forge enumerator not wired. |
| `ErrMemoryUnavailable` | capability | Memory store not wired. |
| `ErrReviewFetchUnavailable` | capability | Review fetcher not wired. |
| `ErrReviewUnavailable` | capability | Review service not wired. |
| `ErrSavingsUnavailable` | capability | Savings ledger not wired. |
| `ErrSchemaMismatch` | transport | HTTP client and server disagree on the schema version. |
| `ErrSearchUnavailable` | capability | Search service absent. |
| `ErrSkillGenUnavailable` | capability | Skill generator not wired. |
| `ErrStrictQueryBlocked` | validation | Strict-query composition refused to answer under its own policy. |
| `ErrStrictQueryInput` | validation | Strict-query composition rejected the input. |
| `ErrTrustUnavailable` | capability | Trust reporting not available on this transport. |
| `ErrUnavailable` | transport | HTTP 503 from the loopback server. |
| `ErrUnreachable` | transport | HTTP client could not reach the loopback server. |

22 sentinels total (capability: 15 · safety: 1 · transport: 3 · validation: 3).
