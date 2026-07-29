<!-- REGISTRATION HEADER (SW-132, 2026-07-29) — added by this repository around the
     supplied document. Nothing in this header is part of the supplied text. The
     supplied text starts at the marker line that closes this header and runs
     unaltered to the end of the file. -->

# Registration record — P0 Completion Delta PRD

**This section was added by the repository (SW-132, 2026-07-29). It is not part of the
supplied document.** The supplied text begins at the marker line below and runs to the end
of the file, byte-for-byte as received.

## Provenance — SUPPLIED VERBATIM, not reconstructed

The document below was **supplied verbatim by the product owner** as a file and copied in
unchanged (`cp`). It was **not reconstructed** from the documents that cite it, and it was
**not authored fresh** in the repository (the SW-120 precedent, where a PRD was written
anew and labelled as such). These three are different classes of authority and a later
reader must not mistake one for another: what follows is the product owner's own text.

The distinction is load-bearing. The predecessor case is recorded in the project backlog
for 2026-07-16: a PRD whose source text existed nowhere on disk was **not** reconstructed
from the plan that cited it, because *"reconstructing it from the plan that cites it was
rejected as fabrication"*. This document did not need that judgement — the source text was
produced.

**Verbatim proof.** The supplied body is byte-identical to the source file: 41 483 bytes,
1 593 lines, `sha256:9e94f1deb554dfcfb1555dc9c49ec3eb5a93ab7db6d5e0781390d57e9666d75c`.
Re-verify at any time, without needing the original:

```bash
sed -n '/^<!-- BEGIN SUPPLIED DOCUMENT/,$p' docs/plan/2026-07-graphi-p0-completion-delta-prd.md \
  | tail -n +2 | shasum -a 256
# → 9e94f1deb554dfcfb1555dc9c49ec3eb5a93ab7db6d5e0781390d57e9666d75c
```

**Nothing in the body was corrected.** Typography, heading levels (SW-132 is an `###`
heading while every other story is `##`), wording and the artifact paths it names —
including `docs/eval/p0/baseline-v070.md`, which does not exist under that path — are
preserved exactly as supplied. Where the repository disagrees with the document, the
disagreement is recorded in the completion checklist, never by editing the body.

## Registration facts

| Field | Value |
|---|---|
| Registered path | `docs/plan/2026-07-graphi-p0-completion-delta-prd.md` — the path the document itself recommends in its header ("Recommended repository path") |
| Registered by | SW-132 — "Register the P0 Completion Delta PRD", 2026-07-29 |
| Provenance | Supplied verbatim by the product owner (see above) |
| Snapshot date of the supplied text | 2026-07-28 (as stated in the document's own header) |
| P0 completion owner | **Graphi Maintainer (`samibel`)** — the solo maintainer, the single completion owner this document's SW-132 requires. Named in the completion checklist, §"Completion owner". |
| Completion checklist (SW-132..SW-149) | [`docs/plan/2026-07-graphi-p0-completion-checklist.md`](2026-07-graphi-p0-completion-checklist.md) — derived from §8 and §9 of the document below |

## Where this document sits in the authority chain

- [`docs/plan/2026-07-graphi-9of10-execution-plan.md`](2026-07-graphi-9of10-execution-plan.md)
  — the **execution plan**: programme structure, WP0–WP10, M0–M5, timeline. It owns
  everything outside the P0 scope.
- [`docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md`](2026-07-graphi-p0-proof-and-truth-prd.md)
  — the **parent P0 PRD**: a precisification of plan milestones M0–M2. It owns the P0
  contract — gates, thresholds, scorers, acceptance criteria.
- **This document** — the **Delta PRD**: only the *remaining* work needed to complete P0.
  It adds no new gate vocabulary and moves no threshold; where it names a gate it is the
  parent PRD's gate. It does **not** replace the parent PRD, and it does not reopen work
  the parent PRD already closed (see its §2.1, which lists that work as inherited input).
- [`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml) → generated
  [`docs/rc/evidence-index.md`](../rc/evidence-index.md) — the **evidence source of
  truth**: the per-gate dashboard `go run ./cmd/evidence -check` enforces. Gate status is
  read there, never here.

**Conflict rule:** if this document and the parent PRD state different numbers for the same
gate, the parent PRD's number stands and the difference is a defect in this document, not a
licence to relax the gate. If this document and the evidence index disagree about whether a
gate has passed, the evidence index stands.

<!-- BEGIN SUPPLIED DOCUMENT — verbatim, sha256 9e94f1deb554dfcfb1555dc9c49ec3eb5a93ab7db6d5e0781390d57e9666d75c, 41483 bytes, 1593 lines. Everything below this line is the product owner's supplied text, unaltered. Do not edit below this line. -->
# PRD — Graphi P0 Completion: Remaining Work to “Proof and Truth”

**Document type:** Delta PRD / Completion Plan  
**Product:** Graphi  
**Program:** P0 — Proof and Truth  
**Status:** Draft for implementation  
**Snapshot date:** 2026-07-28  
**Repository snapshot:** `samibel/graphi` at `main` commit `afa1b686de381dd455ab08e4bf33aaf9420d6aab`  
**Frozen measurement candidate:** `v0.7.0` at `5815db5b053c2bb1bf3119cdb9939c1dea03cc45`  
**Reference binary digest:** `sha256:f91aa839c5f246f3d20330bfb0771a2f11cc035859121661692a4b2429399d25`  
**Parent PRD:** `docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md`  
**Candidate record:** `docs/decisions/2026-07-p0-candidate-freeze-v070.md`  
**Evidence source of truth:** `docs/rc/evidence-index.yaml`  
**Recommended repository path:** `docs/plan/2026-07-graphi-p0-completion-delta-prd.md`

---

## 1. Purpose

This PRD defines only the remaining work required to complete Graphi P0.

It does not restate or reopen work that is already complete unless that work becomes stale because the frozen candidate changes. It converts the current incomplete state into a bounded execution plan with explicit dependencies, deliverables, stop rules, acceptance criteria, and a final Go/No-Go decision.

The central goal is:

> Produce independently reproducible evidence that the frozen Graphi Go core meets the P0 accuracy, performance, parity, claim-integrity, and reproducibility contracts.

P0 is not complete because instruments exist. P0 is complete only when the instruments produce valid evidence against the final frozen candidate and every blocking gate is PASS.

---

## 2. Current State

### 2.1 Completed and not to be rebuilt

The following work is considered complete at the start of this PRD:

- The full P0 PRD exists in the repository.
- A published candidate is frozen on Graphi `v0.7.0`.
- Candidate SHA, platform binaries, digests, build contract, CGo-free status, SBOM, and attestations are documented.
- Five real Go repositories are pinned for scheduled/reference measurement:
  - `google/uuid`
  - `samber/lo`
  - `spf13/cobra`
  - `gin-gonic/gin`
  - `grpc/grpc-go`
- `kubernetes/kubernetes` is pinned as the manual stress target with more than 10,000 Go files.
- The reference runner and reference scenario are defined.
- The evaluation harness can measure:
  - ten cold index runs,
  - p50 and p95,
  - peak RSS,
  - DB size,
  - OOM behavior,
  - at least 1,000 query executions per class,
  - freshness,
  - at least 100 incremental changes,
  - progress stalls,
  - raw sample export,
  - environment capture,
  - aggregate reproduction,
  - CPU, heap, allocation, and I/O profiles after failed gates.
- The `eval-full` workflow now checks out and measures the frozen candidate instead of the dispatch branch.
- Evidence statuses support `PASS`, `FAIL`, `UNKNOWN`, and `STALE`.
- A PASS without evidence and candidate provenance is rejected.

These assets are inputs to this plan. They are not proof that P0 has passed.

### 2.2 Remaining blockers

P0 remains incomplete because:

1. No authoritative complete baseline has been accepted into the evidence index.
2. The current reference scenario has produced non-successful Stable operation outcomes:
   - `explain_symbol` returns `partial`
   - `change_risk` returns `partial`
3. The Gold Corpus does not exist.
4. The Gold ontology and annotation guide are not frozen.
5. Symbol and edge accuracy have not been measured against independent ground truth.
6. The 100 sealed task set does not exist.
7. Double annotation and inter-rater agreement have not been completed.
8. The P0 accuracy scorers have not been executed.
9. Performance and parity gates have not passed in two consecutive full runs.
10. Public claim cleanup has not received an independent reviewer confirmation.
11. A final clean-run reproduction by an independent reviewer has not been recorded.
12. Candidate sign-off is still pending.
13. The P0 Go/No-Go has not been held.
14. The evidence pack has not been frozen.

---

## 3. Outcome

At completion, Graphi must have one final P0 candidate and one immutable evidence pack proving:

- the measured artifact is exactly identified,
- the repository inputs are exactly pinned,
- raw samples reproduce every reported number,
- Stable Go operations meet the accuracy gates,
- performance gates are met in the reference scenario,
- full and incremental indexing converge to the same graph,
- the system abstains correctly when it cannot prove an answer,
- public claims do not exceed the evidence,
- a clean independent run reproduces the result,
- two consecutive full runs are green,
- a formal Go decision has been signed.

---

## 4. Scope

### 4.1 In scope

- Go GA core
- CGo-free default build
- CLI Stable operations
- MCP stdio Stable tools
- reference scenario on `grpc-go`
- measurements across the five pinned Go repositories
- manual Kubernetes stress evidence
- Gold Corpus and scoring
- operation-level accuracy reporting
- Stable operation corrections derived from measured failures
- performance corrections derived from profiles
- full/incremental parity
- claim integrity
- evidence generation
- independent reproduction
- P0 Go/No-Go

### 4.2 Out of scope

- new Stable operations
- new GA languages
- promotion of Java, Kotlin, TypeScript, Python, or other Preview languages
- new Labs tools
- Taint GA
- Refactoring GA
- SaaS
- SSO/RBAC
- billing
- cloud indexing
- cross-repository search
- full third-party dependency indexing
- 90-day focused RC
- paid pilots
- retention study
- broad business validation
- six-reviewer 9/10 audit
- redesigning Graphi around the podcast
- lowering gates to turn red evidence green

The broader execution plan may require these later. They are not part of completing the technical P0 defined by the parent PRD.

---

## 5. Success Metrics

### 5.1 Accuracy gates

| Metric | Required result |
|---|---:|
| Symbol Precision | ≥ 98% |
| Symbol Recall | ≥ 95% |
| Edge Precision | ≥ 95% |
| Edge Recall | ≥ 90% |
| Source-anchor Precision | ≥ 99% |
| Correct abstention | ≥ 95% |
| High-confidence false statements | ≤ 1% |
| Sealed task success | ≥ 90 / 100 |
| Inter-rater agreement, Cohen’s Kappa | ≥ 0.85 |

### 5.2 Performance and reliability gates

| Metric | Required result |
|---|---:|
| Cold index p50 | ≤ 90 s |
| Cold index p95 | ≤ 120 s |
| Peak RSS | ≤ 2 GB |
| OOM on 8 GB host | 0 |
| Reference DB size | ≤ 300 MB |
| Progress-stall p95 | ≤ 2 s |
| Warm search p95 | ≤ 100 ms |
| Caller/callee/impact p95 | ≤ 200 ms |
| Agent context p95 | ≤ 500 ms |
| Freshness p95 | ≤ 2 s |
| Full/incremental parity | 100% |
| Consecutive complete green runs | 2 |
| Independent clean reproduction | PASS |

### 5.3 Evidence and claim gates

- Every PASS includes:
  - evidence URI,
  - final candidate SHA or digest,
  - corpus version,
  - runner class,
  - scorer version,
  - date,
  - owner,
  - sign-off.
- UNKNOWN is not PASS.
- STALE is not PASS.
- No public claim contradicts the final candidate or measured evidence.
- No current quantitative claim lacks its measurement context.
- Candidate sign-off is complete.
- Final P0 Go/No-Go is recorded.

---

## 6. Execution Strategy

### 6.1 Principle: preserve the first honest baseline

Do not fix the product, harness, scenario, or threshold before the current `v0.7.0` candidate has produced and preserved an authoritative first baseline.

A red baseline is useful evidence.

The first accepted baseline must show the actual product state, including:

- `partial` Stable operation outcomes,
- failed thresholds,
- unexpected resource use,
- parity problems,
- UNKNOWN gates and why they are unknown.

### 6.2 Principle: one bounded candidate correction cycle

After the first baseline and Gold pilot identify defects:

1. classify every blocking finding,
2. write regression tests or ground-truth evidence,
3. batch the minimal required corrections,
4. cut one correction candidate,
5. rerun all stale evidence.

Recommended version if production or measurement code changes:

```text
v0.7.1
```

Do not repeatedly move the candidate after every small defect.

A second candidate move is allowed only for:

- a proven P0 blocking product defect,
- a proven measurement-integrity defect,
- a High/Critical security issue,
- a failure that makes required evidence impossible to produce.

“Main moved on” is not a reason.

### 6.3 Parallelism rule

Only independent work may run in parallel.

Allowed:

- CI baseline execution while the Gold ontology is designed,
- external claims review while annotation work proceeds,
- corpus annotation while scorer infrastructure is built against synthetic fixtures.

Not allowed:

- product fixes before first baseline evidence is preserved,
- scoring against Gold data before the ontology is frozen,
- changing thresholds after results are visible,
- publishing final claims before the final candidate is frozen,
- final reproduction before all gates are green.

---

## 7. Critical Path

```text
SW-132 Authoritative v0.7.0 baseline
        ↓
SW-133 Stable partial-result diagnosis
        ↓
SW-134 Candidate correction decision
        ↓
[optional] SW-135 Minimal correction candidate v0.7.1
        ↓
SW-136 Gold ontology and annotation guide
        ↓
SW-137 Gold pilot and Kappa validation
        ↓
SW-138 Full Gold Corpus and sealed tasks
        ↓
SW-139 Accuracy scorers and operation report
        ↓
SW-140 Measured correction loop
        ↓
SW-141 Final performance/parity run
        ↓
SW-142 Two consecutive complete green runs
        ↓
SW-143 Independent reproduction and claims review
        ↓
SW-144 Evidence freeze, sign-off, P0 Go/No-Go
```

Gold Corpus work may start while SW-132 and SW-133 run, but final scoring and candidate correction converge on the same final candidate.

---

# 8. Work Packages and Stories

## WP-R0 — Program Control and P0 Delta Authority

### Objective

Make this completion plan the authoritative list of remaining P0 work without replacing the parent PRD.

### Story SW-132 — Register the P0 Completion Delta

#### Deliverables

- Commit this Delta PRD.
- Add a link from the parent P0 PRD.
- Add a link from the evidence index or focused-core RC document.
- Mark completed parent-PRD items as inherited, not reopened.
- Define a single P0 completion owner.
- Define the independent reviewer requirement.
- Create a P0 completion checklist mapped to the story IDs below.

#### Acceptance criteria

- There is one visible list of remaining P0 work.
- Every remaining Definition-of-Done item maps to a story.
- No completed item is duplicated as new implementation work.
- The relationship between parent PRD, Delta PRD, execution plan, and evidence index is explicit.
- Conflicting scope is resolved in writing.

---

## WP-R1 — Authoritative Baseline and Stable Outcome Diagnosis

### Objective

Produce the first honest, complete measurement record for the frozen `v0.7.0` candidate and diagnose the Stable operation `partial` outcomes without changing gates.

## Story SW-133 — Run and Preserve the Authoritative v0.7.0 Baseline

### Required execution

Dispatch the current `eval-full` workflow after merge from `main`, but make every measurement run against candidate:

```text
5815db5b053c2bb1bf3119cdb9939c1dea03cc45
```

Measure all five scheduled Go repositories:

- uuid
- lo
- cobra
- gin
- grpc-go

Only `grpc-go` is the reference scenario allowed to produce P0 performance PASS/FAIL verdicts. The other repositories publish comparative measurements and must state why their P0 gates are UNKNOWN.

### Required artifacts

For every repository and measurement class:

- evaluation report JSON,
- raw sample directory,
- aggregate reproduction result,
- environment capture,
- candidate provenance,
- repository pin verification,
- harness wall-clock cost,
- exit status,
- profiles when a gate fails.

### Acceptance criteria

- `candidate_match = true`.
- Worktree is clean.
- Runner role is the documented reference class.
- All five pinned SHAs are verified.
- Ten cold runs are complete for every matrix repository.
- Query sample floors are met.
- Incremental sample floor is met.
- Raw sample aggregation reproduces every published statistic exactly.
- Failure of one repository does not cancel the others.
- Artifacts remain available and are referenced in a run record.
- The baseline is recorded even when red.
- No threshold is changed.
- No repository is removed.
- No scenario is simplified.

### Output

`docs/eval/p0/baseline-v070.md`

The report must include a compact table:

| Repository | Candidate valid | Pins valid | Cold | Query | Incremental | Stalls | Overall |
|---|---:|---:|---:|---:|---:|---:|---:|

---

## Story SW-134 — Diagnose `explain_symbol` and `change_risk` Partial Outcomes

### Problem

The reference scenario currently exits non-zero because the Stable operations:

- `explain_symbol`
- `change_risk`

return `partial`.

The plan must not assume this is a product bug. It can be:

- correct fail-closed behavior,
- missing graph data,
- a resolver degradation,
- an incorrect reference expectation,
- an ambiguous target,
- a stale or incomplete index,
- an adapter/surface problem,
- an evaluation harness classification defect.

### Required analysis

For each operation:

1. reproduce the result locally and in CI,
2. record the exact request and complete response,
3. identify the first layer that introduces `partial`,
4. trace the result through:
   - parser,
   - ingestion,
   - linker,
   - type resolver,
   - graph store,
   - engine operation,
   - CLI/MCP adapter,
   - evaluation classifier,
5. verify whether all available evidence is present,
6. determine whether `partial` is contractually correct,
7. create a regression test before any correction.

### Classification

Each finding must be assigned one category:

- **P0-PRODUCT:** Stable operation is incorrect.
- **P0-MEASUREMENT:** Harness or classifier is incorrect.
- **P0-SCENARIO:** Reference expectation is invalid.
- **P0-DOCUMENTATION:** Behavior is correct but contract is unclear.
- **P0-UNKNOWN:** More evidence required.

### Acceptance criteria

- Root cause is proven, not guessed.
- Reproduction is deterministic.
- The affected symbol and repository pin are recorded.
- Expected behavior is derived from the Stable contract.
- A regression test exists for a confirmed defect.
- No production code is changed inside the diagnosis story.
- A correction recommendation and candidate impact are documented.

### Output

`docs/eval/p0/stable-partial-diagnosis.md`

---

## Story SW-135 — Decide Whether the Candidate Must Move

### Decision outcomes

#### Outcome A — Retain v0.7.0

Allowed only when:

- the product behavior is correct,
- no measurement-integrity code must change,
- the scenario can be corrected without invalidating the measurement contract,
- all changes are documentation-only and do not alter the measured instrument.

#### Outcome B — Cut one correction candidate

Required when:

- a Stable product defect must be fixed,
- a measurement-integrity defect must be fixed,
- a required regression test changes the candidate harness,
- the existing candidate cannot produce valid P0 evidence.

### Acceptance criteria

- Decision log created.
- All known blocking baseline findings are considered together.
- Candidate does not move for convenience.
- Evidence affected by the decision is marked STALE.
- A retained candidate has a written justification.
- A moved candidate receives a complete successor freeze record.

### Output

`docs/decisions/2026-07-p0-candidate-correction-decision.md`

---

## Story SW-136 — Optional Minimal Correction Candidate

This story is created only when SW-135 selects Outcome B.

### Scope

Only corrections directly supported by:

- baseline evidence,
- diagnosis evidence,
- Gold pilot failures,
- security blockers.

No unrelated features, cleanup, or refactoring.

### Requirements

- Regression test before code correction.
- Minimal diff.
- No Stable surface expansion.
- No GA scope expansion.
- No threshold changes.
- No repository removal.
- Product tree changes listed explicitly.
- Build and release through the existing release DAG.
- New candidate freeze record.
- Complete artifact digests, SBOM, attestations, and rebuild evidence.
- All old candidate-dependent evidence marked STALE.

### Recommended version

`v0.7.1`, unless SemVer requires a different version.

### Acceptance criteria

- Every code change maps to a P0 blocking finding.
- All CGo-free and zero-egress gates remain green.
- Product and harness changes are independently reviewed.
- Candidate sign-off fields are populated except final P0 program sign-off.
- Baseline is rerunnable on the new candidate.

---

## WP-R2 — Gold Corpus and Independent Ground Truth

### Objective

Create sufficient independent ground truth to measure accuracy instead of inferring it from unit tests or self-authored fixtures.

## Story SW-137 — Freeze Gold Ontology and Annotation Guide

### Deliverables

Recommended structure:

```text
corpus/gold/v1/
├── ontology.yaml
├── annotation-guide.md
├── repositories.yaml
├── schema/
│   ├── symbol.schema.json
│   ├── edge.schema.json
│   └── task.schema.json
└── decisions/
```

### Ontology must define

#### Symbol classes

- package
- file
- function
- method
- type
- interface
- struct
- variable
- constant
- receiver method
- test
- benchmark
- entry point

#### Edge classes

- defines
- calls
- references
- imports
- implements
- embeds
- other relation only when already part of the Stable Graphi contract

#### Confidence-independent truth

The Gold answer must state whether the relation exists in the source, not whether Graphi currently discovers it.

#### Negative and abstention cases

- symbol does not exist,
- relationship does not exist,
- relationship cannot be determined from indexed local source,
- ambiguous target,
- external-only target,
- unsupported construct.

### Annotation rules

- Never seed Gold answers from Graphi output.
- Graphi may be used only after an annotation is sealed, for discrepancy investigation.
- Every record carries:
  - repository,
  - pinned SHA,
  - file,
  - line range,
  - qualified identity,
  - annotator,
  - status,
  - adjudication state.
- Historical decisions are append-only.

### Acceptance criteria

- Ontology has no unresolved critical ambiguity.
- Annotation guide contains at least two examples per important relation class.
- Negative and abstention examples are included.
- Schema validation is automatic.
- Annotation changes are auditable.
- Ontology and guide are frozen before the pilot is scored.

---

## Story SW-138 — Build the Gold Pilot

### Pilot size

- at least 100 symbols,
- at least 200 edges,
- at least 10 sealed tasks,
- at least 20% double annotated.

### Repository distribution

The pilot must include at least:

- one small library,
- one web/API project,
- one multi-package project.

Recommended:

- uuid
- gin
- grpc-go

### Required difficult cases

- pointer and value receivers,
- interface satisfaction,
- embedded interfaces,
- embedded structs,
- generics,
- method values,
- method expressions,
- identical names in different packages,
- external calls,
- generated files,
- build-tagged files,
- negative and not-found queries.

### Inter-rater process

- Annotation A is sealed.
- Annotation B is completed independently.
- Conflicts are listed automatically.
- An adjudicator resolves conflicts.
- Cohen’s Kappa is computed.
- If Kappa < 0.85:
  - do not scale the corpus,
  - revise the guide,
  - retrain annotators,
  - repeat affected pilot annotations.

### Acceptance criteria

- Pilot size reached.
- 20% double annotation reached.
- Kappa ≥ 0.85.
- Schemas validate.
- No Gold answer depends on Graphi output.
- All conflicts are adjudicated.
- Pilot tasks remain sealed until scorer execution.

---

## Story SW-139 — Scale to the Full Gold Corpus

### Required size

- at least 1,000 symbols,
- at least 2,000 edges,
- 100 sealed real coding tasks,
- at least 20% double annotation across the full corpus.

### Sampling strategy

The corpus must be stratified, not convenience sampled.

Strata include:

- repository size,
- package depth,
- relation type,
- exported/unexported symbols,
- methods/functions,
- interfaces/implementations,
- generated/non-generated,
- build-tagged/non-tagged,
- positive/negative,
- internal/external boundaries,
- simple/ambiguous qualified names.

### Sealed task categories

At least:

- find the correct definition,
- identify callers,
- identify callees,
- find references,
- explain symbol,
- construct a relevant neighborhood,
- estimate structural impact,
- identify related files,
- create an agent brief,
- assess structural change risk,
- distinguish not-found from empty,
- abstain where evidence is insufficient.

### Acceptance criteria

- Minimum record counts reached.
- Distribution report proves coverage across strata.
- At least 20% double annotated.
- Kappa remains ≥ 0.85.
- All disagreements are adjudicated.
- 100 tasks remain sealed from implementation authors until scoring.
- Corpus version is immutable after scoring begins.
- Any later correction creates `v2`, never silently edits `v1`.

---

## WP-R3 — Accuracy Scoring and Operation-Level Reporting

### Objective

Measure every P0 accuracy metric and prevent good aggregate numbers from hiding weak Stable operations.

## Story SW-140 — Implement Versioned Gold Scorers

### Required scorers

- symbol precision,
- symbol recall,
- edge precision,
- edge recall,
- source-anchor precision,
- correct abstention,
- high-confidence false statement rate,
- sealed task success.

### Required output dimensions

Every score must be breakable down by:

- repository,
- operation,
- symbol kind,
- edge kind,
- confidence tier,
- generated/non-generated,
- build-tagged/non-tagged,
- local/external boundary,
- positive/negative case.

### Scorer rules

- Scorer implementation is separate from production logic.
- Scorers never call Graphi code to determine expected answers.
- Matching rules are documented and versioned.
- Partial credit is forbidden unless defined before results are visible.
- Missing results and wrong results are counted separately.
- Abstention is correct only when the Gold record permits abstention.
- A heuristic result reported as confirmed is a high-confidence error.

### Recommended output

```text
docs/eval/p0/gold-v1/
├── summary.json
├── operation-breakdown.json
├── repository-breakdown.json
├── errors.jsonl
├── scorer-manifest.json
└── report.md
```

### Acceptance criteria

- Synthetic scorer fixtures cover PASS and FAIL behavior.
- Raw Graphi responses are preserved.
- Re-running the scorer produces byte-identical reports.
- Every aggregate can be reproduced from record-level results.
- No overall PASS can hide an operation below its required minimum.
- Scorer version is included in evidence.

---

## Story SW-141 — Execute the First Sealed Accuracy Run

### Requirements

- Use the currently selected candidate from SW-135/SW-136.
- Use Gold Corpus `v1`.
- Do not inspect sealed task answers before the run.
- Preserve all raw requests and responses.
- Record candidate, corpus, runner, and scorer provenance.

### Output classification

For every failed Gold record:

- parser miss,
- symbol identity miss,
- linker miss,
- type resolver degradation,
- store miss,
- operation logic defect,
- adapter defect,
- anchor defect,
- ranking defect,
- incorrect confidence,
- incorrect abstention,
- unsupported by contract,
- Gold defect.

### Acceptance criteria

- All required metrics are produced.
- Every failed record has a machine-readable category or `unclassified`.
- No threshold changes after the run.
- The evidence index remains FAIL/UNKNOWN where gates are not met.
- Results are preserved even when poor.

---

## WP-R4 — Measured Correction Loop

### Objective

Fix only defects proven by the baseline or Gold evaluation.

## Story SW-142 — Prioritize and Correct Measured Failures

### Priority order

1. High-confidence false statements
2. Wrong source anchors
3. Wrong symbol identities
4. False positive edges
5. Missing critical edges
6. Incorrect abstention
7. Stable operation classification defects
8. Performance regressions
9. Low-value ranking defects

### Required workflow per correction

```text
Gold or baseline failure
→ minimal reproduction
→ regression test
→ root cause
→ minimal implementation correction
→ focused test
→ full suite
→ Gold rescore
→ performance check
→ parity check
```

### Constraints

- No speculative framework support.
- No new operation.
- No new language.
- No Labs expansion.
- No broad refactor unless a profile or defect proves it necessary.
- No special-casing repository names.
- No encoding Gold answers into production.
- No threshold relaxation.

### Exit criteria

- All accuracy gates pass.
- Every fixed failure has a regression test.
- No correction degrades another operation below its gate.
- Performance remains within limits.
- Full/incremental parity remains 100%.
- Candidate is re-frozen once after the correction batch if code changed.

---

## WP-R5 — Performance, Parity, Recovery, and Stress Evidence

### Objective

Prove the final candidate meets performance and consistency requirements after accuracy corrections.

## Story SW-143 — Run the Final Reference Performance Baseline

### Reference scenario

- repository: `grpc-go`
- exact pinned SHA from `corpus/manifest.json`
- runner class: documented reference runner
- final candidate SHA
- clean worktree
- cold cache contract as defined by the harness

### Measurements

- ten cold runs,
- p50 and p95,
- peak RSS,
- DB size,
- OOM check,
- at least 1,000 query executions per class,
- progress-stall distribution,
- at least 100 incremental changes,
- freshness p50/p95,
- update p50/p95.

### Acceptance criteria

All parent-PRD performance thresholds pass.

A failed gate automatically produces required profiles.

No performance result is accepted when:

- candidate mismatch,
- dirty worktree,
- wrong runner class,
- pin mismatch,
- insufficient samples,
- aggregate mismatch,
- incomplete run.

---

## Story SW-144 — Complete Full/Incremental Parity and Recovery Matrix

### Required change classes

- add file,
- modify file,
- delete file,
- rename symbol,
- move symbol,
- rename package,
- add call,
- remove call,
- change interface,
- add implementation,
- remove implementation,
- change build tag,
- replace generated file,
- change external import,
- branch switch,
- interrupted full pass,
- restart and recovery.

### Comparison scope

Compare:

- nodes,
- edges,
- IDs,
- evidence,
- confidence,
- source anchors,
- relevant metadata,
- external node cleanup,
- stale linker edge cleanup.

### Acceptance criteria

- 100% final-state parity.
- Repeated incremental application is idempotent.
- Recovery converges to the same state as a fresh full index.
- No orphaned external nodes.
- No stale edges.
- Every mismatch is blocking.

---

## Story SW-145 — Run the Manual Kubernetes Stress Scenario

### Purpose

Provide scale evidence and identify safety boundaries. This is not allowed to replace the reference scenario.

### Requirements

- Explicit manual dispatch.
- Pinned Kubernetes SHA.
- Named machine:
  - CPU,
  - RAM,
  - OS,
  - filesystem,
  - Go version.
- Candidate provenance.
- Full raw resource data.
- Progress-stall output.
- Graceful cancellation observation.
- No scheduled hosted-runner execution.

### Stop rules

Stop and record FAIL/BOUNDARY when:

- memory threatens host stability,
- progress becomes unobservable,
- disk growth exceeds documented limits,
- cancellation cannot release resources,
- the process enters a repeated restart spiral.

### Acceptance criteria

- Result is published honestly as PASS, FAIL, or BOUNDARY.
- No universal performance claim is derived from this one machine.
- Findings create future scale tickets, not P0 threshold exceptions.
- A safety failure blocks P0 only when it violates P0 reliability or local-system safety.

---

## WP-R6 — Claims, Reproduction, and Closeout

### Objective

Prove that public positioning matches the final artifact and that a second party can reproduce the result.

## Story SW-146 — Independent Claims Review

### Reviewer task

An external reviewer must inspect public surfaces without maintainer coaching and answer:

- Which language is GA?
- Which operations are Stable?
- What is Preview?
- What is Labs?
- What is Source-only?
- Are external dependencies indexed?
- Are external nodes navigable?
- What does deterministic mean?
- What does confirmed mean?
- What does Zero Egress guarantee?
- Which performance scenario produced each number?
- Does Graphi claim to replace CodeQL, Semgrep, or Sourcegraph?

### Surfaces

- README
- website
- tutorial
- CLI help
- capability manifest
- Stability Tiers
- feature inventory
- coverage matrix
- benchmark pages
- release notes
- security/privacy docs

### Acceptance criteria

- Reviewer distinguishes all four stability levels correctly.
- Reviewer does not infer universal multi-language guarantees.
- Reviewer understands local dependency boundaries.
- Quantitative claims include context.
- Any misunderstanding caused by the docs is corrected.
- Review notes are preserved as evidence.

---

## Story SW-147 — Two Consecutive Complete Green Runs

### Run A

- final candidate,
- final Gold Corpus,
- final scorers,
- reference runner,
- full accuracy,
- performance,
- parity,
- recovery,
- evidence generation.

### Run B

- clean environment,
- no changed thresholds,
- same candidate,
- same pins,
- same corpus,
- same scorers,
- same reference contract.

### Acceptance criteria

- Both runs are complete.
- Both runs are green.
- Run B does not reuse mutable output from Run A.
- All report values reproduce from raw samples.
- No evidence row is UNKNOWN or STALE for a blocking P0 gate.
- No unexplained variation changes a verdict.
- Any expected timing variation is reported.

---

## Story SW-148 — Independent Clean Reproduction

### Independent reviewer

A person other than the implementation owner must:

1. obtain the final candidate,
2. verify digest,
3. obtain the pinned corpus,
4. run the documented entry point,
5. reproduce the reports,
6. verify the evidence index,
7. sign the reproduction record.

For a solo-maintainer project, acceptable reviewers include:

- an external open-source contributor,
- a design partner engineer,
- a contracted reviewer,
- an independent maintainer from another project.

An AI-only review does not satisfy the independent-human sign-off requirement.

### Acceptance criteria

- Reviewer did not participate in the relevant implementation batch.
- Commands are followed from documentation.
- No undocumented maintainer intervention is needed.
- Deviations are visible.
- Reproduction verdict is PASS.
- Reviewer signs the record.

### Output

`docs/eval/p0/independent-reproduction.md`

---

## Story SW-149 — Freeze the Evidence Pack and Hold P0 Go/No-Go

### Evidence pack contents

```text
docs/eval/p0/final/
├── candidate/
├── baseline/
├── gold/
├── performance/
├── parity/
├── recovery/
├── stress/
├── claims-review/
├── independent-reproduction/
├── evidence-index.yaml
└── p0-final-report.md
```

### Final report

Must contain:

- candidate identity,
- scope,
- corpus,
- scorer version,
- all gate results,
- all limitations,
- known non-blocking findings,
- claims permitted after P0,
- claims still forbidden,
- sign-offs,
- final decision.

### Go decision

P0 receives GO only when:

- all accuracy gates PASS,
- all reference performance gates PASS,
- parity is 100%,
- two complete consecutive runs PASS,
- independent reproduction PASS,
- claims review PASS,
- candidate sign-off complete,
- no blocking evidence row is UNKNOWN or STALE,
- no open High/Critical security finding affects the P0 scope.

### No-Go decision

P0 remains NO-GO if any blocking gate is:

- FAIL,
- UNKNOWN,
- STALE,
- missing evidence,
- missing sign-off.

### Acceptance criteria

- Evidence pack is immutable or content-addressed.
- Evidence index points to final artifacts.
- Candidate SHA and digest are consistent everywhere.
- Final decision is signed and dated.
- Parent PRD Definition of Done is updated item by item.
- P1 does not start before P0 GO.

---

# 9. Recommended Story Order

## Mandatory sequence

| Order | Story | Result |
|---:|---|---|
| 1 | SW-132 | Delta PRD and control |
| 2 | SW-133 | First preserved v0.7.0 baseline |
| 3 | SW-134 | Stable partial diagnosis |
| 4 | SW-135 | Candidate retain/move decision |
| 5 | SW-136 | Optional correction candidate |
| 6 | SW-137 | Gold ontology and guide |
| 7 | SW-138 | Gold pilot and Kappa |
| 8 | SW-139 | Full Gold Corpus |
| 9 | SW-140 | Scorers |
| 10 | SW-141 | First sealed accuracy run |
| 11 | SW-142 | Measured corrections |
| 12 | SW-143 | Final performance baseline |
| 13 | SW-144 | Parity and recovery |
| 14 | SW-145 | Kubernetes stress evidence |
| 15 | SW-146 | Claims review |
| 16 | SW-147 | Two green full runs |
| 17 | SW-148 | Independent reproduction |
| 18 | SW-149 | Evidence freeze and Go/No-Go |

## Safe parallel lane

While SW-133 and SW-134 run:

- SW-137 may begin.
- Reviewer recruitment for SW-138, SW-146, and SW-148 may begin.
- Scorer schemas may be prototyped on synthetic data.

Do not start SW-141 before SW-139 and SW-140 are complete.

---

# 10. Ticket Template

Every implementation ticket created from this PRD must include:

```markdown
## P0 finding

Evidence URI:
Candidate:
Repository and pin:
Operation:
Observed result:
Expected result:
Why this blocks P0:

## Scope

Included:
Excluded:

## Acceptance criteria

- [ ] Regression evidence exists before production correction
- [ ] No threshold changed
- [ ] No repository removed
- [ ] Stable scope unchanged
- [ ] CGo-free build preserved
- [ ] Zero-egress contract preserved
- [ ] Full suite green
- [ ] Gold rescore performed
- [ ] Performance checked
- [ ] Parity checked
- [ ] Evidence index updated only from artifacts

## Candidate impact

- [ ] Candidate retained
- [ ] Candidate move required
- [ ] Existing evidence marked STALE
```

---

# 11. Data Contracts

## 11.1 Gold symbol record

```json
{
  "schema_version": 1,
  "id": "gold-symbol-000001",
  "repository": "grpc-go",
  "repository_sha": "PIN_FROM_MANIFEST",
  "kind": "method",
  "name": "Serve",
  "qualified_name": "google.golang.org/grpc.(*Server).Serve",
  "file": "server.go",
  "start_line": 826,
  "end_line": 900,
  "exists": true,
  "supports_abstention": false,
  "annotator": "annotator-a",
  "status": "sealed"
}
```

## 11.2 Gold edge record

```json
{
  "schema_version": 1,
  "id": "gold-edge-000001",
  "repository": "grpc-go",
  "repository_sha": "PIN_FROM_MANIFEST",
  "kind": "calls",
  "source_symbol_id": "gold-symbol-000101",
  "target_symbol_id": "gold-symbol-000202",
  "exists": true,
  "source_evidence": {
    "file": "example.go",
    "line": 42
  },
  "annotator": "annotator-a",
  "status": "sealed"
}
```

## 11.3 Task record

```json
{
  "schema_version": 1,
  "id": "gold-task-001",
  "repository": "grpc-go",
  "repository_sha": "PIN_FROM_MANIFEST",
  "operation": "callers",
  "prompt": "Which local symbols call X?",
  "target_symbol_id": "gold-symbol-000202",
  "expected_record_ids": [
    "gold-edge-000001"
  ],
  "allowed_outcome": "answer",
  "sealed": true
}
```

---

# 12. Evidence Rules

1. A green CI check is not automatically P0 evidence.
2. A PASS row requires an immutable artifact.
3. Raw samples are preserved.
4. Reports are derived, never hand-maintained.
5. Every report records:
   - candidate,
   - repository pin,
   - runner,
   - environment,
   - harness version,
   - scorer version.
6. A candidate move marks dependent results STALE.
7. A Gold Corpus edit after scoring creates a new version.
8. An instrument change requires rerunning affected evidence.
9. A threshold change requires a new PRD decision and invalidates comparisons.
10. Failed and negative results remain visible.

---

# 13. Risks and Mitigations

## Risk: Candidate churn

**Mitigation:** Preserve first baseline, batch corrections, allow one bounded correction release.

## Risk: Gold Corpus encodes current Graphi behavior

**Mitigation:** Independent annotation, sealed records, Graphi output prohibited during initial annotation.

## Risk: Solo maintainer cannot provide independent annotation

**Mitigation:** Recruit a contributor or paid reviewer before full corpus work. Start with a pilot to bound cost.

## Risk: Excellent precision hides poor recall

**Mitigation:** Separate precision, recall, abstention, and high-confidence error gates.

## Risk: Overall score hides a weak operation

**Mitigation:** Operation-level breakdown; a blocking operation cannot be averaged away.

## Risk: Benchmark overfitting to grpc-go

**Mitigation:** Comparative measurements across five repos and separate Kubernetes stress evidence.

## Risk: Kubernetes resource use destabilizes the machine

**Mitigation:** Manual-only run, named machine, hard stop rules, no scheduled hosted-runner job.

## Risk: Partial is incorrectly treated as failure or success

**Mitigation:** Contract-first diagnosis in SW-134 before any correction.

## Risk: Gold annotations become public test answers

**Mitigation:** Keep sealed holdout tasks private or access-controlled while publishing aggregate results.

## Risk: Evidence index drifts from artifacts

**Mitigation:** Generate from machine-readable manifests and fail the honesty/freshness check.

## Risk: New feature work interrupts P0

**Mitigation:** Freeze GA scope; unrelated work does not enter the correction candidate.

---

# 14. Stop Rules

Stop implementation and record the result when:

- a required repository pin no longer resolves,
- candidate provenance fails,
- a run uses the wrong runner role,
- raw samples do not reproduce the report,
- annotator agreement remains below 0.85 after guide revision,
- a correction requires expanding Stable scope,
- a performance correction reduces accuracy below threshold,
- parity is not 100%,
- the final candidate changes after Run A,
- independent reproduction requires undocumented maintainer intervention,
- a High/Critical finding affects the P0 artifact.

Do not:

- lower thresholds,
- drop grpc-go,
- replace real repositories with synthetic fixtures,
- count UNKNOWN as PASS,
- use Kubernetes as the only proof,
- declare P0 complete from unit tests,
- start P1 because the remaining P0 work is inconvenient.

---

# 15. Definition of Done

P0 Completion is done when every box below is true:

## Candidate and baseline

- [ ] First authoritative v0.7.0 baseline preserved.
- [ ] `explain_symbol` partial outcome diagnosed.
- [ ] `change_risk` partial outcome diagnosed.
- [ ] Candidate retain/move decision recorded.
- [ ] Final candidate frozen.
- [ ] Candidate sign-off complete.

## Gold Corpus

- [ ] Ontology frozen.
- [ ] Annotation guide frozen.
- [ ] Pilot has at least 100 symbols.
- [ ] Pilot has at least 200 edges.
- [ ] Pilot has at least 10 tasks.
- [ ] At least 20% double annotation.
- [ ] Kappa ≥ 0.85.
- [ ] Full corpus has at least 1,000 symbols.
- [ ] Full corpus has at least 2,000 edges.
- [ ] 100 sealed tasks exist.

## Accuracy

- [ ] Symbol Precision ≥ 98%.
- [ ] Symbol Recall ≥ 95%.
- [ ] Edge Precision ≥ 95%.
- [ ] Edge Recall ≥ 90%.
- [ ] Source-anchor Precision ≥ 99%.
- [ ] Correct abstention ≥ 95%.
- [ ] High-confidence false statements ≤ 1%.
- [ ] At least 90/100 sealed tasks pass.
- [ ] Every blocking failure has a regression test or accepted No-Go disposition.

## Performance and reliability

- [ ] Cold index p50 ≤ 90 s.
- [ ] Cold index p95 ≤ 120 s.
- [ ] Peak RSS ≤ 2 GB.
- [ ] No OOM on 8 GB host.
- [ ] Reference DB size ≤ 300 MB.
- [ ] Progress-stall p95 ≤ 2 s.
- [ ] Warm search p95 ≤ 100 ms.
- [ ] Caller/callee/impact p95 ≤ 200 ms.
- [ ] Agent context p95 ≤ 500 ms.
- [ ] Freshness p95 ≤ 2 s.
- [ ] Full/incremental parity = 100%.
- [ ] Recovery converges to a fresh full index.
- [ ] Kubernetes stress result published honestly.

## Truth and reproducibility

- [ ] Claims review passed.
- [ ] Two consecutive full runs passed.
- [ ] Independent reproduction passed.
- [ ] Every PASS has evidence URI and candidate identity.
- [ ] No blocking gate is UNKNOWN.
- [ ] No blocking gate is STALE.
- [ ] Evidence pack frozen.
- [ ] Final report signed.
- [ ] P0 Go/No-Go held.
- [ ] Decision is GO before P1 begins.

---

# 16. Final Product Decision

This plan intentionally prioritizes evidence over expansion.

The remaining P0 work is not:

> Build more Graphi.

It is:

> Prove exactly how well the existing Graphi Go core works, correct only what the evidence disproves, and freeze a result another engineer can reproduce.

The completion sequence is:

```text
Preserve baseline
→ diagnose partial outcomes
→ decide candidate
→ build independent Gold Corpus
→ score
→ fix measured defects
→ prove performance and parity
→ run twice
→ reproduce independently
→ freeze evidence
→ Go/No-Go
```
