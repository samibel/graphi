# P1 PRD v1.0 — Delta against the shipped trust surface

> ## AMENDMENT — 2026-08-19 (SW-171): a seventh additive v1 field, `abstention`
>
> Per D6 this amendment is **added**; nothing below is rewritten or re-pointed.
> It exists because this repository's own guard says it must: the register test
> at `surfaces/client/trust_report_test.go` pins the exact top-level property
> count and states that *"a field appearing here is a contract decision that must
> be argued in the delta document, never drift that slips in"*. This is that
> argument.
>
> **What was added.** `trust_report_doc.abstention`
> (`surfaces/client/trust_report.go`), an additive field under
> `schema_version: 1` per contract §2.3 rule 7. It carries the semantic binders'
> **named skip counters** — what a registrant REFUSED to bind, under which
> reason — as `{available, unavailable_reason, scope, languages[]}`.
>
> **Why it exists.** The JVM binder skips every receiver it cannot type from a
> declared form, under a named counter, rather than guessing (ADR 0008;
> `engine/jvmresolve/body_java.go:68-78`). None of that reached a user, so an
> agent asking `callers` on a Java method got a confident list and no signal
> that call sites had been refused. Silent under-reporting is the confidence
> laundering the surrounding rules forbid.
>
> **Composed at read time, not persisted in the snapshot — the SAME reasoning
> §B1 recorded for `capabilities`, and it is restated rather than assumed.**
> Adding a field to `trust.Snapshot` changes its canonical `Encode` bytes, which
> **is** the digest contract, and would force `SnapshotSchemaVersion` to 2
> against the `schema_version: 1` PRD v1.0 §6 itself fixes. The counters are
> therefore persisted in their own generation-bound sidecar table
> (`trust_language_skips`, ingest schema 5) and composed into the document at
> read time. Measured, not assumed: indexing one fixture with a binary at the
> parity candidate `3b8d43f` and with one carrying this change produced
> **byte-identical** `nodes` + `edges` rows, on a Go repository and on a Java one
> with the binder opted in.
>
> **The scope limit is part of the field, not a footnote.** The counters are
> **repository-global per language** and carry NO file, package, symbol or
> call-site attribution — for `java_receiver_untyped` and
> `java_receiver_external` the callee is undeterminable by definition, so no
> site exists to attribute them to even in principle. The `scope` property
> restates this inside every document, and the `strict_query` roll-up restates
> it inside every notice, so the numbers cannot travel without their limit.
> A reader must not take a package roll-up for a per-symbol accounting.
>
> **Fail-closed presence.** `available: false` with a stated
> `unavailable_reason` means the record could not be read; `available: true`
> with an empty `languages` list means it was read and holds nothing. Those are
> different answers and are never collapsed — a silent absence here would read
> as an all-clear.
>
> **What did NOT change.** `query.Result` and the Stable-12 wire contract are
> byte-untouched (`engine/query` carries no diff);
> `parityreport.CandidateSHA` is unmoved; no published parity row or evidence
> row was touched. Product bytes DID move (the change touches compiled files),
> so a parity dispatch is unpublishable until the next scheduled candidate move
> — expected and precedented, see the Wave 0 handoff §8 and its 2026-08-19
> amendment.

| | |
|---|---|
| **Status** | Binding reconciliation. Records an owner decision; supersedes nothing on its own. |
| **Date** | 2026-08-05 |
| **Owner** | samibel |
| **Subject** | [`docs/plan/2026-08-graphi-p1-prd-v1.md`](2026-08-graphi-p1-prd-v1.md) (registration hash `sha256:cc538e6c1498c6dd9df9ed057a4029061fb9d73cae49310a09221f105a96e468`) |
| **Measured against** | The tree at `19acd90`, released as v0.8.0 — the P1 trust surface built from the July PRD |
| **Method** | Go source and shipped documents only. Every row below names a file and, where a value is quoted, its line. No row is inferred from a plan document. |

---

## 0. Why this document exists

The product owner supplied a **second P1 PRD** on 2026-08-05: *„Graphi P1: Trust &
Coverage Intelligence" v1.0*. It is a condensed rewrite of the July PRD
([`2026-07-graphi-p1-trust-surface-prd.md`](2026-07-graphi-p1-trust-surface-prd.md),
2 901 lines) covering the same product bet in eleven sections and eleven phases.

Two facts make a reconciliation necessary rather than optional:

1. **Phases 0–9 of PRD v1.0 already shipped.** They were built against the *July* PRD and
   released as v0.8.0 (`f12b759` … `06719ca`, PRs #94/#95/#97/#98). Read as a forward plan,
   PRD v1.0 would restate finished work as open work.
2. **PRD v1.0 changes three wire values** that the July PRD fixed and that v0.8.0 shipped.
   Those are not restatements; they are contract changes to a released surface.

### The owner decision this document records

> **Where PRD v1.0 and [`2026-08-graphi-p1-trust-contract-v1.md`](2026-08-graphi-p1-trust-contract-v1.md)
> contradict each other, PRD v1.0 is binding and the contract is amended to follow.**
> Decided by the owner (`samibel`) on 2026-08-05.

That decision is what makes §A below a legitimate amendment rather than a regression. It
does **not** extend to §C: PRD v1.0's *implementation suggestions* (which packages to
create, which file names to use) are build guidance, not contracts, and are not binding
where the shipped structure already satisfies the requirement by other means. §C states
each such case and why.

### What this document does not do

It does not grant P1 GO, does not supply the missing P0 GO, and ticks no P0 checklist box.
The P0 GO precondition named by PRD v1.0 §8 Phase 0 remains unmet; the deliberate deviation
is recorded in [`docs/decisions/2026-08-p1-start-before-p0-go.md`](../decisions/2026-08-p1-start-before-p0-go.md).

---

## A. Wire-contract divergences — PRD v1.0 wins, code changes

Three values differ. Each is externally visible: an agent or a script consuming the Labs
surface can observe all three.

| # | PRD v1.0 requires | v0.8.0 ships | Shipped at | Contract conflict? |
|---|---|---|---|---|
| **A1** | Verdict `UNVERIFIED` | Verdict `UNKNOWN` | `engine/trust/assess.go:37` | **Yes** — contract §1.5 freezes the closed set as `PASS \| WARN \| FAIL \| UNKNOWN` |
| **A2** | Policy tokens `exploratory-v1`, `review-v1`, `automated-change-v1` | `exploratory`, `review`, `automated_change` | `engine/trust/policy.go:141-145` | **Yes** — contract §2.1 spells the CLI flag as `--policy exploratory\|review\|automated_change` |
| **A3** | Exit codes `0` = PASS, `1` = WARN, `2` = FAIL **or** UNVERIFIED | `0` PASS · `1` WARN · `2` operational error · `3` FAIL · `4` UNKNOWN/unavailable | `cmd/graphi/trust_report.go:149` (`trustExitCode`) | **No** — contract §2.1 and §5 explicitly do *not* freeze the exit-code table; it follows whichever PRD §15/§6 governs |

### A1 — `UNKNOWN` → `UNVERIFIED`

PRD v1.0 §5 defines the fourth verdict as `UNVERIFIED` and gives it a sharper meaning than
the July PRD's `UNKNOWN`: *"wird verwendet, wenn die notwendige Evidenz fehlt oder nicht
generation-bound ist"*. The rename is therefore not cosmetic — `UNVERIFIED` names the
absence of *verifiable* evidence, which is what the fail-closed policies actually detect.

Scope: the `Verdict` enum, every policy rule that yields it, the sealed policy matrix, the
false-PASS pins, both surfaces, and the docs. The Go identifier is renamed
(`VerdictUnknown` → `VerdictUnverified`) rather than aliased: an alias would let
un-migrated call sites keep compiling while emitting the old wire value.

**Not affected:** snapshot state `UNAVAILABLE` / `STALE` / `INCOMPLETE` / `CURRENT`
(contract §1.6). PRD v1.0 does not touch that enum, and the two enums must not merge —
contract §1.8 pins that they "never mix in one field".

### A2 — policy tokens gain the `-v1` suffix

PRD v1.0 §6 lists the policy names as `exploratory-v1`, `review-v1`, `automated-change-v1`
and uses them in every example (§8 Phases 6–8).

**The shipped output already carries the version separately.** `PolicyRef` is
`{name, version}` (`engine/trust/assess.go:82-83`), so `review` + `version: 1` is
`review-v1` decomposed. The realignment therefore changes the **accepted input token
only** — `PolicyRef` keeps its shape, and the emitted document keeps both fields. This
satisfies PRD v1.0 without inventing a third representation of the same fact.

Unknown tokens stay a usage error with no implicit default, per PRD v1.0 §8 Phase 6
("entsteht ein Usage-Fehler und keine implizite Default-Policy") — which matches the
shipped behavior at `cmd/graphi/trust_report.go:50-52`, where a missing flag value is an
error precisely so a dropped `--policy` cannot launder a gate into a friendlier exit code.

### A3 — exit-code table collapses to 0/1/2

PRD v1.0 §6 fixes three codes and refers usage errors to "dem bereits bestehenden
CLI-Konventionsmuster", which in this repository is exit 2 (`cmd/graphi/status.go`,
`doctor.go`, `trust_report.go` all use 2 for operational and usage failures).

> **Recorded concern, then implemented as specified.** The new table maps FAIL,
> UNVERIFIED and usage errors all onto 2. An agent shelling out to `trust-report` can then
> no longer distinguish "the policy blocked me" from "I passed a bad flag" by exit code
> alone — a loss of discriminating power at exactly the fail-closed edge the surface
> exists to defend. The owner was informed and chose PRD v1.0.
>
> **Mitigation carried into the implementation:** usage and operational errors write to
> stderr and emit **no** JSON document; FAIL and UNVERIFIED always emit the document with
> its `verdict` field. The distinction stays machine-detectable — through the document
> rather than the exit code. This is recorded here so a later reader does not mistake the
> collapse for an oversight.

### A0 — confirmed identical, no work

PRD v1.0 §6 names three further contract values. All three already match and are listed so
a reader does not have to re-derive them:

| Value | PRD v1.0 §6 | Shipped |
|---|---|---|
| Schema version | `schema_version: 1` | `SnapshotSchemaVersion = 1`, `trustReportJSONSchemaVersion = 1` |
| Persistence key | `trust.snapshot.v1` | `MetaSnapshot = "trust.snapshot.v1"` (`engine/trust/types.go:47`) |
| MCP tool name | `graph_health` | `ToolGraphHealth = "graph_health"` (`surfaces/mcp/tools.go:94`) |

---

## B. Scope items never built

Both are listed in PRD v1.0 §3 "In Scope (MVP)". Neither exists in v0.8.0.

### B1 — Capability Matrix

PRD v1.0 §3: *"Pro Sprache maschinenlesbar ausgeben, ob sie `typed-confirmed`,
`cross-file-heuristic`, `intra-file-only` oder `parse-only` unterstützt."* §5 lists
`Capability Level` as a term and §8 Phase 10 requires it be *"mit automatisiertem
Drift-Test an die tatsächlichen Language Capabilities"* bound.

**Status: absent.** None of the four level strings occurs anywhere in the repository
(`*.go`, `*.md`, `*.yaml`, `*.json`). The *substance* exists as prose in
[`docs/language-support.md`](../language-support.md) — Go alone gets type-checker-confirmed
edges, Preview languages resolve cross-file at heuristic tier only, some languages have no
registered resolver — but nothing machine-readable and nothing drift-tested.

**Two decisions of record for the implementation:**

1. **Derived, not hand-written.** The level per language is computed from the live
   registries — `core/parse.Registry.Languages()` (`core/parse/registry.go:105`) for the
   parser set, and the registered resolver set in `engine/link` for cross-file capability
   (which needs a small additive accessor: `Linker.resolvers` is unexported today,
   `engine/link/link.go:156`). A hand-maintained table would drift the first time a
   grammar is added, which is precisely what PRD v1.0 §8 Phase 10 forbids.
2. **Composed at read time, not persisted in the snapshot.** PRD v1.0 §5 sketches
   `capabilities` under `facts`, which are snapshot-persisted. Persisting it here would be
   wrong on three counts: capability is a property of the **binary**, not of the graph
   generation, so a persisted copy can go stale against the running binary; adding a field
   to `trust.Snapshot` changes the canonical `Encode` bytes, which **is** the digest
   contract (`engine/trust/serialize.go`, `Digest` over exactly those bytes) and would
   invalidate every stored snapshot plus the full/incremental `FactDigest` parity pins;
   and it would force `SnapshotSchemaVersion` to 2, contradicting PRD v1.0 §6's own
   `schema_version: 1`. The field is therefore emitted in the composed trust-report
   document (`surfaces/client/trust_report.go`), where both CLI and MCP already read it
   from one shared composition. **This is a deliberate deviation from the §5 sketch,
   taken to keep the §6 contract PRD v1.0 itself states.**

### B2 — Strict Query as a Labs MCP tool

PRD v1.0 §3 and §8 Phase 9 require a Labs **MCP** tool, naming `strict_query` with the
final name to be settled in contract review (§12: *"Wie heißt das Strict-Query-Labs-Tool
final (`strict_query`, `trust_query` oder bestehendes Naming-Pattern)?"*).

**Status: half built.** The CLI surface ships as `graphi query-strict`
(`cmd/graphi/query_strict.go`) with min-tier filtering, an excluded count, the
filtered-emptiness limitation and a fail-closed policy preflight. `surfaces/mcp/tools.go`
registers **no** strict-query tool — MCP agents, the PRD's primary persona, cannot reach it.

**Name decided here, as §12 requires:** **`strict_query`**. It is the name PRD v1.0 uses
in its own prose and matches the repository's `snake_case` tool-name convention
(`graph_health`, `explain_symbol`, `change_risk`). The CLI verb `query-strict` stays as it
is — CLI verbs are kebab-case in this repository and renaming a shipped verb would be a
gratuitous break.

The implementation moves the strict-query composition into `surfaces/client/` so CLI and
MCP share one byte-identical composition, mirroring `surfaces/client/trust_report.go`.
That is the pattern PRD v1.0 §8 Phase 7 mandates ("keinen parallelen Client anlegen").

---

## C. PRD v1.0 statements deliberately not implemented

The owner decision in §0 covers **contracts**. These three are not contracts, and each is
already satisfied. Recording them prevents a later reader from filing them as unmet.

### C1 — `internal/repostatus/` (PRD v1.0 §6, §8 Phase 1)

PRD v1.0 §6 lists `internal/repostatus/` as a new module and Phase 1 specifies
`model.go` / `read.go` inside it with a `Report` struct and `Read(ctx, Input)`.

**Already satisfied under a different name.** The extraction happened in `f12b759` as
`internal/freshness` (vocabulary, `Report`/`SyncStamp`/`IndexState`/`Drift`) plus
`internal/freshness/probe` (the observer, `probe.Compute(ctx, root, dbPath, metaDir)`).
`cmd/graphi/status.go` consumes it; the status JSON contract and exit codes are unchanged
and pinned by `cmd/graphi/status_test.go` and `status_lockstate_test.go`.

**Not renamed, for four reasons:**

- **Nothing about it is externally visible.** No wire value, no flag, no JSON field. §6
  lists it under "Vorgesehene neue Module/Dateien" — a build plan for work not yet done,
  not an API contract.
- **The two-package split is load-bearing.** It exists to break the import cycle
  `ingest → trust → freshness → ingest` (documented in the `internal/freshness` package
  doc). A rename that merged them would reintroduce the cycle PRD v1.0 §11 tells the agent
  to report rather than work around.
- **[`docs/adr/0006-status-vs-trust-separation.md`](../adr/0006-status-vs-trust-separation.md)
  (Accepted) names `internal/freshness` in its Decision 2** and forbids a second drift
  implementation. Renaming would require superseding an accepted ADR to gain nothing.
- **PRD v1.0 §6 and §11 forbid the churn it would cause:** "Keine globale
  Repo-Refaktorierung im Rahmen von P1" and "Keine Repository-weiten Renames". Six
  non-test files outside the package import it, including three in `engine/trust`.

### C2 — the §5 "Kanonisches JSON-Schema auf Feature-Ebene"

PRD v1.0 §5 sketches a document nested as `snapshot` / `freshness` / `facts` /
`assessment`. The shipped document (`trustReportDoc`,
`surfaces/client/trust_report.go:64-80`) is flat with 15 top-level fields.

**Not restructured.** The PRD labels the sketch itself *"auf Feature-Ebene"* — a
feature-level illustration, which in this document set means conceptual rather than
normative. Every fact the sketch names is present in the shipped document, and the shipped
document carries more (`limitations`, `checks_passed`, `scope_evidence`, per-boundary
severity). Reshaping it would be pure form change: it would break the CLI↔MCP byte-parity
pins over nine input combinations, invalidate the frozen wire shape in contract §2.2, and
add no information. The one §5 field that is genuinely absent — `capabilities` — is
delivered by B1.

### C3 — Stable-12

PRD v1.0 §3 puts "Änderung der Stable-12-MCP-Verträge" out of scope and §11 forbids it.
Shipped state complies: `mcp.StableOperations` is the frozen twelve, `graph_health` is
Labs-only, and `internal/coverage/stable.go` guards both the twelve and the eleven-tool
default MCP profile. **No change, and every PR in this delta must keep those guards green.**

---

## D. Evaluation rows — unchanged, still open

PRD v1.0 §8 Phase 10 and §4 restate evaluation gates the July PRD already carried. Their
status is tracked in [`2026-08-graphi-p1-evidence-index.md`](2026-08-graphi-p1-evidence-index.md)
and this document does not restate it row by row. Two notes on where PRD v1.0 *differs*:

- **Policy golden set.** PRD v1.0 §4 asks for "mindestens 60 versiegelte Fälle"; the July
  PRD §36.2 asked for ≥ 80. `engine/trust/policy_matrix_test.go` holds 60+. The stricter
  number governs — a PRD rewrite lowering an evidence bar is not a reason to discard
  evidence already committed to. The ≥ 80 row stays OPEN.
- **Snapshot overhead.** PRD v1.0 §4 tightens the full-index p95 overhead gate to ≤ 3 %
  (July PRD: 5 %/10 % depending on pass type). The row stays OPEN either way — no
  measurement on the P0 reference stress repo has been run, and no green is claimed from
  toy-fixture timings.

---

## E. Execution order

Each row below is one reviewable PR. Sequential; a PR starts only when the previous one is
green. Every PR states its effect on Stable-12, which must be "none".

| PR | Content | Delta rows |
|---|---|---|
| 1 | Register PRD v1.0, this delta, contract §6 amendment note, evidence-index rows | — |
| 2 | Wire-contract realignment | A1, A2, A3 |
| 3 | Capability Matrix | B1 |
| 4 | `strict_query` Labs MCP tool | B2 |
| 5 | Evidence closure (fixture corpus, ≥ 80 sealed cases, A/B performance, privacy fixture) | D |

PR 2 makes the trust surface's Labs wire contract **breaking**. It is recorded in
`CHANGELOG.md` under `[Unreleased]` as a breaking Labs change and bumps the trust contract
document to v1.1 with an amendment log, per contract §6 ("any change → a new document
version plus a recorded decision").
