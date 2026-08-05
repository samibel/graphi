# P1 Trust Surface — Evidence Index

**Created:** 2026-08-04 · **Owner:** samibel · **Scope:** P1 only — this index
lives beside the P1 plan documents and never touches the frozen P0 evidence
chain (`docs/rc/`, `docs/eval/`), per the scope guard in
[`docs/decisions/2026-08-p1-start-before-p0-go.md`](../decisions/2026-08-p1-start-before-p0-go.md).

**The rule, inherited from the P0 discipline:** a row is GREEN only with a
verifiable artifact behind it — a named test, file, or merged commit. OPEN
counts as not passed. Nothing here is inferred from "it feels done".

**Delivered in:** PR #95 (merge `0292e4d`, commits through `9a5e8db`) and
PR #97 (merge `a7ed59e`, commits `f6cc9a3` / `890c415` / `3c29c69`), plus the
section-46 docs commit `7d4d51f`.

---

## PRD §48 Definition of Done — row status

Grouped as §48 lists them. Evidence pointers name real tests/files in this
repository.

### Contract and governance

| Item | Status | Evidence |
|---|---|---|
| P0 GO dokumentiert | **OPEN** | P0 checklist stands at 17/18 open; no Go/No-Go held. P1 cannot reach its own GO before P0 does (§43). |
| P1 Owner und Reviewer benannt | **OPEN** | The PRD's own Owner field remains "noch festzulegen"; the start decision names `samibel` as the deciding owner but fills no reviewer role. |
| Status-vs-Trust ADR akzeptiert | GREEN | `docs/adr/0006-status-vs-trust-separation.md` (merged, PR #95) |
| Trust Terminology / JSON Schema v1 / Policy Spec v1 eingefroren | GREEN | `docs/plan/2026-08-graphi-p1-trust-contract-v1.md` §1–§3 (merged, PR #95) |

### Snapshot

| Item | Status | Evidence |
|---|---|---|
| TrustSnapshot implementiert, generation-gebunden, atomar publiziert, Digest geprüft | GREEN | `engine/trust/{types,serialize,state,read}.go`, `engine/ingest/trust_persist.go`; tests `engine/ingest/trust_persist_test.go`, `engine/trust/read_test.go` |
| Corruption fail-closed; alte Stores UNAVAILABLE | GREEN | corruption/generation-mismatch/old-store cases in `engine/ingest/trust_persist_test.go`; false-green pins in `engine/ingest/trust_falsegreen_test.go` |
| Parse Skips / Linker Stats / Type Resolver Health / External Boundaries persistiert | GREEN | snapshot facts (`engine/trust/types.go`) + sidecar rows (`engine/ingest/trust_evidence.go`, schema v3); tests `engine/ingest/trust_evidence_test.go` |
| Confidence Counts korrekt | GREEN | snapshot `TierCounts` pinned equal to `BriefStats.TierCounts` (`engine/ingest/trust_persist_test.go`) |
| Detail Evidence selektiv gelesen; kein Whole-Graph Hot-Path Scan | GREEN | generation-keyed ports `FileEvidence`/`PackageEvidence`/`ListFileEvidence`; aggregates via single-transaction `GROUP BY` (`core/graphstore/trust_aggregate.go`) |

### Assessment and policies

| Item | Status | Evidence |
|---|---|---|
| Repository / Symbol / File Assessment | GREEN | `engine/trust/{assess,scope,scopefacts}.go`; v1 symbol scope = owning-file evidence (documented) |
| Package Assessment | **OPEN** | deliberately deferred in v1 (contract leaves-open list); package-looking targets read TARGET_NOT_FOUND + SCOPE_EVIDENCE_UNAVAILABLE |
| exploratory-v1 / review-v1 / automated-change-v1 | GREEN | `engine/trust/policy.go`; sealed matrix `engine/trust/policy_matrix_test.go` |
| 0 False PASS in Fixtures | GREEN | red gates `TestNoFalsePass_MissingEvidence`, `TestNoFalsePass_AutomatedChange`, `TestVerdictAlwaysExplained` + pins in `policy_falsepass_test.go`, `scopefacts_attack_test.go` |

### Surfaces

| Item | Status | Evidence |
|---|---|---|
| `graphi trust-report` (+ `--json`, `--target`, `--policy`, bounded `--details`), Exit Codes dokumentiert | GREEN | `cmd/graphi/trust_report.go`; tests `trust_report_test.go` incl. the exit-code mapping table |
| MCP `graph_health` implementiert, Labs-gegated | GREEN | `surfaces/mcp/{tools,descriptors,toolcalls}.go`; gating + error-model pins in `surfaces/mcp/graphhealth_test.go` |
| CLI/MCP Parität 100 % | GREEN | byte-level parity pins over nine input combinations (`surfaces/mcp/graphhealth_test.go`, `cmd/graphi/trust_report_test.go`) — one shared composition (`surfaces/client/trust_report.go`) |
| Default MCP Output im Budget | GREEN | `TestGraphHealth_DefaultOutputWithinTokenBudget` (~380 estimated tokens vs the 2 000 target, 8 000 hard cap) |
| Strict Query implementiert, Stable-Semantik unverändert | GREEN (CLI only) | `cmd/graphi/query_strict.go`; pins in `query_strict_test.go` + `query_strict_attack_test.go` (incl. the filtered-emptiness limitation and the fail-closed preflight). **The MCP half is not built** — see the PRD-v1.0 row below. |

### PRD v1.0 delta rows (added 2026-08-05)

Registered on 2026-08-05: a second P1 PRD,
[`2026-08-graphi-p1-prd-v1.md`](2026-08-graphi-p1-prd-v1.md), reconciled by
[`2026-08-graphi-p1-prd-v1-delta.md`](2026-08-graphi-p1-prd-v1-delta.md). Rows it adds or
reopens:

| Item | Status | Evidence / what would discharge it |
|---|---|---|
| Wire contract matches PRD v1.0 (`UNVERIFIED`, `-v1` policy tokens, exit codes 0/1/2) | **OPEN** | delta §A. v0.8.0 ships `UNKNOWN`, bare policy tokens and a 5-way exit table, all built to the July PRD. Discharged by delta §E PR 2 plus the contract v1.1 amendment. |
| Capability Matrix (`typed-confirmed` / `cross-file-heuristic` / `intra-file-only` / `parse-only`) | **OPEN** | delta §B1. None of the four level strings exists in the repository; the substance is prose in `docs/language-support.md` only. Discharged by a registry-derived matrix plus the drift test PRD v1.0 §8 Phase 10 requires. |
| Strict Query reachable over MCP (`strict_query`, Labs) | **OPEN** | delta §B2. CLI ships; `surfaces/mcp/tools.go` registers no strict-query tool, so the PRD's primary persona (MCP agent) cannot reach it. Name decided in delta §B2. |
| `internal/repostatus` module (PRD v1.0 §6, §8 Phase 1) | **N/A — satisfied otherwise** | delta §C1. Shipped as `internal/freshness` + `/probe`; named in accepted ADR 0006. Not a wire contract; deliberately not renamed. Recorded so it is not filed as unmet. |

### Evaluation — the honest gap

| Item | Status | Evidence / what would discharge it |
|---|---|---|
| Trust Fixture Corpus vollständig (§36.1, 20 Fixtures) | **OPEN** | a large subset exists as Go test fixtures across the suites above, but no unified corpus artifact; closing it is plain Go test work, free in CI |
| ≥ 80 Policy-Fälle versiegelt | **OPEN** | 60+ sealed matrix cases exist (`policy_matrix_test.go`) plus adversarial pins; the formal ≥80 sealed count is not reached |
| ≥ 30 Agent Tasks ausgewertet (§36.3) | **OPEN** | requires an agent-task evaluation; can be run by the community or a future session |
| ≥ 10 Human Usability Tests (§36.4) | **OPEN** | community path: the `trust-surface feedback` issue template asks first-time users exactly the §36.4 questions — real evidence, at no cost |
| Policy Accuracy ≥ 98 % / Target Resolution ≥ 99 % | **OPEN** | needs the sealed evaluation runs above |
| Full-/Incremental Trust Parity 100 % | GREEN | `FactDigest` parity + evidence-row parity tests (`engine/ingest/trust_persist_test.go`, `trust_evidence_test.go`) |
| Performance Gates (Aggregate p95 ≤ 100 ms, Ingest-Overhead ≤ 5 %/10 %) | **OPEN** | the NFR gates target the P0 reference stress repo; no measurement on that repo has been run — no green is claimed from toy-fixture timings |
| Security Review grün | **OPEN** | not held. Note: Dependabot currently reports 8 findings (4 high, 4 moderate) on `main` — dependency-level, not trust-surface code, but they block "keine offenen High/Critical Findings" |
| Unabhängige Reproduktion | **OPEN** | a person other than the implementation owner (or a named clean-runner substitute, reported as weaker) |
| P1 Go/No-Go dokumentiert | **OPEN** | owner decision; requires the rows above and the P0 GO precondition |

---

## Known limitations of record

- **Residual crash window** (documented in `engine/trust/state.go`): a
  crash-window graph mutation preserving the entire recomputable aggregate
  distribution is indistinguishable from current by the recorded facts; the
  drift term catches the source-visible cases.
- **Windows path filter**: `NewParseFacts` filters absolute paths with slash
  semantics; Windows-absolute forms are untestable on the Linux runner and
  flagged for a Windows-aware hardening pass.
- **Remote transports**: HTTP/daemon clients return a typed
  `ErrTrustUnavailable` (unavailable-until-wired, the diagnose precedent).
- **Adversarial hardening**: five review passes proved and closed 17+
  false-green/laundering holes; every one is pinned by a regression test
  (`*_falsegreen_test.go`, `*_falsepass_test.go`, `*_attack_test.go` files).

## Non-claims

This index records implementation evidence. It does **not** claim P1 GO, does
not tick any P0 checklist box, and does not substitute for the PRD's human,
security or independent-reproduction requirements. The P0 GO precondition for
a P1 GO remains unmet.
