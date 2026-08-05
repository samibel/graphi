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
| Strict Query implementiert, Stable-Semantik unverändert | GREEN | `cmd/graphi/query_strict.go`; pins in `query_strict_test.go` + `query_strict_attack_test.go` (incl. the filtered-emptiness limitation and the fail-closed preflight); the MCP half is `strict_query`, see the PRD-v1.0 row below. |

### PRD v1.0 delta rows (added 2026-08-05)

Registered on 2026-08-05: a second P1 PRD,
[`2026-08-graphi-p1-prd-v1.md`](2026-08-graphi-p1-prd-v1.md), reconciled by
[`2026-08-graphi-p1-prd-v1-delta.md`](2026-08-graphi-p1-prd-v1-delta.md). Rows it adds or
reopens:

| Item | Status | Evidence / what would discharge it |
|---|---|---|
| Wire contract matches PRD v1.0 (`UNVERIFIED`, `-v1` policy tokens, exit codes 0/1/2) | GREEN | delta §A, discharged. `engine/trust/prdv1_wire_test.go` pins all three values plus the rejection of the superseded bare names; `cmd/graphi/trust_report_test.go` pins the 0/1/2 table and the "no non-PASS verdict ever exits 0" property; contract amended to v1.1 (§1.5, §2.1). |
| Capability Matrix (`typed-confirmed` / `cross-file-heuristic` / `intra-file-only` / `parse-only`) | GREEN | delta §B1, discharged. `engine/trust/capability.go` grades; `surfaces/client/trust_report.go` derives from the live registries at read time (not persisted — the snapshot digest contract and `schema_version: 1` both forbid it). Drift tests re-derive every expectation from the same registries (`surfaces/client/capability_test.go`); `core/parse.UndeclaredSymbolCapability` fails the build for a language registered without a declaration (`core/parse/capability_test.go`, planted-offender proof included). |
| Strict Query reachable over MCP (`strict_query`, Labs) | GREEN | delta §B2, discharged. `ToolStrictQuery` registered Labs-only; CLI and MCP share one composition (`surfaces/client/query_strict.go`). Pins in `surfaces/mcp/strictquery_test.go`: byte parity over five input shapes, gating in both halves (absent from the Stable catalog AND dispatch-rejected), `[labs]` marking, the closed operation set, and the withheld-count/limitation red gate on real mixed-tier data. |
| `internal/repostatus` module (PRD v1.0 §6, §8 Phase 1) | **N/A — satisfied otherwise** | delta §C1. Shipped as `internal/freshness` + `/probe`; named in accepted ADR 0006. Not a wire contract; deliberately not renamed. Recorded so it is not filed as unmet. |

### Evaluation — the honest gap

| Item | Status | Evidence / what would discharge it |
|---|---|---|
| Trust Fixture Corpus vollständig (§36.1, 20 Fixtures) | **OPEN** | the sealed matrix now carries 27 *snapshot-level* situations (`policy_matrix_test.go`) and the privacy fixture adds a hostile *repository*, but §36.1 asks for 20 unified repository fixtures and no such single artifact exists — the fixtures remain distributed across `engine/ingest`, `surfaces/client` and `surfaces/mcp` test files. Honest partial: not claimed green. |
| ≥ 80 Policy-Fälle versiegelt | GREEN | 81 sealed cases — 27 fixture situations × 3 built-in policies (`engine/trust/policy_matrix_test.go`), each pinning the exact verdict and the exact canonically-sorted finding-code list, plus the adversarial pins in `policy_falsepass_test.go` / `scopefacts_attack_test.go`. Situations 21–27 pin boundaries the first twenty left implicit (type errors vs degradation, partial vs total degradation, derived-only vs heuristic-only, file vs package scope). |
| ≥ 30 Agent Tasks ausgewertet (§36.3) | **OPEN** | requires an agent-task evaluation; can be run by the community or a future session |
| ≥ 10 Human Usability Tests (§36.4) | **OPEN** | community path: the `trust-surface feedback` issue template asks first-time users exactly the §36.4 questions — real evidence, at no cost |
| Policy Accuracy ≥ 98 % / Target Resolution ≥ 99 % | **OPEN** | needs the sealed evaluation runs above |
| Privacy-Fixture + Output-Scanner (0 Source Bytes, 0 Secrets) | GREEN | `surfaces/client/privacy_test.go`: a repository with secrets in code, prompt-injection text in comments/literals/**a filename**, an over-long path, a binary blob and a fail-closed parse skip; every trust-report variant (incl. `--details`, both policies, adversarial targets) and every strict-query envelope is scanned. Found and closed a real gap: emitted paths were count-capped but not length-bounded (`trust.MaxPathLength`). Residual risk documented below. |
| Full-/Incremental Trust Parity 100 % | GREEN | `FactDigest` parity + evidence-row parity tests (`engine/ingest/trust_persist_test.go`, `trust_evidence_test.go`) |
| Performance Gates (Aggregate p95 ≤ 100 ms, Ingest-Overhead ≤ 5 %/10 %) | **OPEN** | the NFR gates target the P0 reference stress repo; no measurement on that repo has been run — no green is claimed from toy-fixture timings |
| Security Review grün | **OPEN** | not held. Note: Dependabot currently reports 8 findings (4 high, 4 moderate) on `main` — dependency-level, not trust-surface code, but they block "keine offenen High/Critical Findings" |
| Unabhängige Reproduktion | **OPEN** | a person other than the implementation owner (or a named clean-runner substitute, reported as weaker) |
| P1 Go/No-Go dokumentiert | **OPEN** | owner decision; requires the rows above and the P0 GO precondition |

---

## Known limitations of record

- **A filename can address the agent.** Trust documents name the files the
  parser skipped — a skipped file a reader cannot name is not actionable — so a
  repository can put chosen text in front of an agent by naming a file after it
  (PRD §9 lists filenames as an injection vector and requires paths be escaped
  and bounded, not omitted). What is guaranteed: such text arrives as DATA in a
  known field, JSON-escaped, repository-relative and length-bounded
  (`trust.MaxPathLength`), never as document prose and never with the file's
  contents. Pinned by `TestPrivacy_PathsAreBoundedAndRelative`.

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
