
# P1 Trust Contract v1

| | |
|---|---|
| **Status** | v1 — frozen on merge; changes require a version bump and a recorded decision |
| **Date** | 2026-08-03 |
| **Owner** | samibel |
| **Work package / story** | P1 Trust Surface — WP1.0 "Contract and Architecture", story P1-001 "Freeze Trust Contract" |
| **Derived from** | The registered PRD [`docs/plan/2026-07-graphi-p1-trust-surface-prd.md`](2026-07-graphi-p1-trust-surface-prd.md) only (registration hash `sha256:58cce857b2657def19876bc0090f443ef74e5cb6383da2b2853b0347922bec8f`), §§ 11, 13, 15, 16, 25, 26, 35 |
| **Evidence companions** | [`docs/plan/2026-08-graphi-p1-code-baseline-audit.md`](2026-08-graphi-p1-code-baseline-audit.md) (code baseline), [`core/model/edge.go`](../../core/model/edge.go) (tier constants) |

## 0. What this document freezes

This is the WP1.0 contract freeze for the P1 Trust Surface. It freezes, as v1:

1. the trust terminology (§1),
2. the `graphi trust-report --json` wire contract (§2),
3. the built-in policy specification v1 with its rule-to-finding-code binding (§3),
4. the error model (§4).

It also records what v1 deliberately leaves open (§5) and the change procedure (§6).

It freezes **contracts, not implementations**: per the companion audit, P1 is 0 %
implemented, so nothing below describes existing behavior except where a code file is
quoted as the source of a definition. Everything normative is derived from the registered
PRD; where the PRD's German wording is load-bearing it is quoted verbatim. Additions this
document makes beyond the PRD are explicitly marked (**minted in v1** for finding codes,
*v1 clarification* for contract notes).

## 1. Terminology (frozen)

### 1.1 Confidence tiers

The tier vocabulary is not defined by this document — it already exists in code as a
closed, construction-enforced enum. `core/model/edge.go` is the single source of truth;
v1 adopts it verbatim:

```go
// ConfidenceTier is the closed provenance vocabulary reconciled with
// architecture §7.3. The story's HIGH/MEDIUM/LOW are illustrative only; the
// canonical, persisted set is {heuristic, derived, confirmed}. Any value outside
// this set is rejected on construction and on deserialization.
type ConfidenceTier string

const (
	// TierHeuristic — produced by a heuristic/best-effort signal.
	TierHeuristic ConfidenceTier = "heuristic"
	// TierDerived — derived from structured analysis (e.g. resolved symbols).
	TierDerived ConfidenceTier = "derived"
	// TierConfirmed — confirmed by an authoritative source.
	TierConfirmed ConfidenceTier = "confirmed"
)
```

— quoted from [`core/model/edge.go`](../../core/model/edge.go) (lines 15–28 at freeze).

User-facing meanings, per PRD §11.1:

| Tier | Meaning (PRD §11.1) |
|---|---|
| `confirmed` | The relationship was confirmed by a stronger semantic source such as go/types. |
| `derived` | The relationship was deterministically derived from local, unambiguous symbol information. |
| `heuristic` | The relationship was assigned by language-specific or selector-based heuristics. |

Frozen consequences (PRD §19): the wire contract emits counts for exactly these three
tiers; external edges remain `heuristic`; linker edges are never relabeled `confirmed`;
typechecker-confirmed edges remain recognizable as `confirmed`; ratios are derived from
counts and counts stay the primary evidence; a high confirmed ratio alone never produces
PASS.

### 1.2 Coverage limit (PRD §11.2)

An observed area in which Graphi does not promise complete structural navigation.
Examples (non-exhaustive): an external dependency, a skipped file, a degraded package, an
unresolved reference, an ambiguous reference, an unsupported language capability.

### 1.3 Trust fact (PRD §11.3)

A fact deterministically derived from the graph, the ingest, a resolver, or status.

### 1.4 Trust policy (PRD §11.4)

A versioned rule set that evaluates trust facts for a named use case.

### 1.5 Trust verdict (PRD §11.5)

Closed set, exactly four values:

```text
PASS | WARN | FAIL | UNKNOWN
```

### 1.6 Snapshot state (PRD §11.6, semantics from §18)

Closed set, exactly four values:

| State | Meaning (PRD §18) |
|---|---|
| `CURRENT` | Graph exists, warm-startable, no drift, snapshot generation equals graph generation. |
| `STALE` | Source drift, snapshot generation does not match, or graph changed after the snapshot. |
| `INCOMPLETE` | Full-pass marker set, aborted ingest, or incomplete snapshot. |
| `UNAVAILABLE` | No graph, no snapshot, incompatible old graph version, or trust data not migratable. |

### 1.7 Scope (PRD §11.7)

Closed set, exactly five values:

```text
repository | package | file | symbol | result-set
```

### 1.8 Terminology notes (*v1 clarifications*)

- **`PARTIAL` is not a v1 verdict and not a v1 snapshot state.** PRD §7.5 mentions
  "Partial Evidence → PARTIAL" once; §11.5/§11.6 do not include it, and the closed sets
  above govern. Partial evidence is expressed through findings and limitations; ingest
  incompleteness through snapshot state `INCOMPLETE`; unresolvable or unsupported scopes
  through verdict `UNKNOWN`.
- Verdicts qualify policy assessments; snapshot states qualify snapshots. The two enums
  never mix in one field.
- Missing evidence is never a positive signal (PRD §7.5): no snapshot → `UNAVAILABLE`;
  wrong generation → `STALE`; unresolvable scope or unsupported data source → `UNKNOWN`.

## 2. JSON contract v1 — `graphi trust-report --json`

### 2.1 Command (PRD §15, FR-1)

```bash
graphi trust-report [--json] [--details] \
  [--target <symbol|path|package>] \
  [--policy exploratory|review|automated_change]
```

v1 freezes the JSON contract only. The human rendering and the exit-code table are
specified by PRD §15 but not frozen by this document (see §5).

### 2.2 Normative shape (PRD §16, FR-2)

The PRD §16 example is reproduced below and is the **normative v1 shape**: the set of
properties shown, at the nesting shown, is the v1 property set (values are illustrative).

```json
{
  "schema_version": 1,
  "snapshot_version": "trust-v1",
  "snapshot_state": "CURRENT",
  "graph_generation": {
    "id": "7d2f...",
    "source_commit": "afa1b686de381dd455ab08e4bf33aaf9420d6aab",
    "profile": "balanced",
    "binary_commit": "afa1b686de381dd455ab08e4bf33aaf9420d6aab"
  },
  "freshness": {
    "current": true,
    "drift": {
      "added": 0,
      "changed": 0,
      "removed": 0
    }
  },
  "scope": {
    "kind": "repository",
    "id": ""
  },
  "coverage": {
    "files_discovered": 1431,
    "files_indexed": 1428,
    "files_skipped": 3,
    "packages_total": 84,
    "packages_degraded": 2
  },
  "edge_evidence": {
    "confirmed": 82410,
    "derived": 11203,
    "heuristic": 7891
  },
  "resolution": {
    "resolved_external": 1204,
    "skipped": 318,
    "ambiguous": 24,
    "dropped_intents": 51
  },
  "boundaries": [
    {
      "code": "EXTERNAL_NOT_NAVIGABLE",
      "severity": "info",
      "count": 1204
    }
  ],
  "policy": {
    "name": "review",
    "version": 1,
    "verdict": "WARN"
  },
  "limitations": [
    {
      "code": "TYPECHECK_DEGRADED",
      "severity": "warning",
      "count": 2,
      "action": "inspect degraded packages"
    }
  ]
}
```

Field register (one row per frozen property):

| Property | Type | Content |
|---|---|---|
| `schema_version` | integer | Wire schema version; `1` at freeze. |
| `snapshot_version` | string | Snapshot format identifier (`"trust-v1"`). |
| `snapshot_state` | string | One of the §1.6 closed set. |
| `graph_generation` | object | `id`, `source_commit`, `profile`, `binary_commit` — all strings. |
| `freshness` | object | `current` (bool), `drift` (`added`/`changed`/`removed`, integers). |
| `scope` | object | `kind` (one of the §1.7 closed set), `id` (string, may be empty). |
| `coverage` | object | `files_discovered`, `files_indexed`, `files_skipped`, `packages_total`, `packages_degraded` — integers. |
| `edge_evidence` | object | Exactly the three tier keys `confirmed`, `derived`, `heuristic` — integers. |
| `resolution` | object | `resolved_external`, `skipped`, `ambiguous`, `dropped_intents` — integers. |
| `boundaries` | array | Objects `{code, severity, count}`. |
| `policy` | object | `name`, `version` (integer), `verdict` (one of the §1.5 closed set). |
| `limitations` | array | Objects `{code, severity, count, action}`. |

### 2.3 Contract rules (PRD §16 "Contract-Regeln")

1. Every defined top-level property is always present.
2. Empty arrays, never `null`.
3. Counts are non-negative.
4. Canonical sort for lists.
5. Map outputs are converted into sorted structures before serialization wherever byte
   parity is required.
6. Breaking changes bump `schema_version`.
7. New additive fields may be added within the same major version where the existing
   compatibility rule allows it.
8. The JSON contains no absolute private paths, except when the user explicitly requests
   details and the existing CLI convention permits it.
9. Repository paths remain normalized and relative.

*v1 clarification:* the presence rule follows the existing `statusReport` convention —
"Every field is always present; empty strings / zero values are used instead of omission"
(`cmd/graphi/status.go:25-27`). In particular, when no `--policy` is requested, `policy`
is still present with zero values, never omitted.

### 2.4 Schema version constant

The schema version constant will live in code as `trustReportJSONSchemaVersion`,
following the `statusJSONSchemaVersion` convention (`cmd/graphi/status.go:23`):

```go
// trustReportJSONSchemaVersion versions the `graphi trust-report --json`
// document. Bump only on breaking changes to the shape or value domain.
const trustReportJSONSchemaVersion = 1
```

At freeze its value is `1` and it is the single source of the wire field
`schema_version`.

### 2.5 Boundary and limitation codes on the wire

The minimum `boundaries[].code` vocabulary is PRD §23's set:

```text
EXTERNAL_NOT_NAVIGABLE
CROSS_REPOSITORY_UNAVAILABLE
DEPENDENCY_INTERNALS_UNKNOWN
DYNAMIC_RUNTIME_UNKNOWN
```

`limitations[].code` uses the same style; the §16 example uses `TYPECHECK_DEGRADED`.
*v1 clarification:* codes are SCREAMING_SNAKE_CASE ASCII identifiers; adding a code is
additive (no schema bump); removing or renaming a code is breaking.

## 3. Policy specification v1 (PRD §25, §26)

### 3.0 Ground rules (frozen)

- Facts and policy are separate: `Facts + Scope + Policy → Verdict`; a policy never
  alters or hides facts (PRD §7.3).
- Policies are implemented as code or static versioned data (PRD §25).
- **Not permitted** (PRD §25 "Nicht zulässig", reinforced by §44): a user-supplied
  executable expression; dynamic scripting; an LLM deciding the verdict; a silent
  threshold change without a version bump.
- **Versioning rule:** any threshold or rule change bumps the policy version
  (`exploratory-v1` → `exploratory-v2`, …). The policy version appears in the output.
  Identical facts plus identical policy yield an identical verdict. Every rule is bound
  to a finding code (PRD §25 acceptance criteria — the binding is the tables below).
- Fail closed: missing evidence never produces PASS (PRD §25.3, §7.5).

The three built-in policies are `exploratory-v1`, `review-v1`, `automated-change-v1`.
Their rules are reproduced verbatim from PRD §25 (German original is normative), each
bound to a finding code. Code sources: **§26** (the PRD's finding-code list), **§27**
(defined in the PRD's target-resolution section, adopted here), or **minted in v1**
(no PRD code existed; minted in the same style — §26's list is explicitly examples).

### 3.1 exploratory-v1 (PRD §25.1)

Purpose (PRD): understand a codebase, find files, form hypotheses, plan follow-up
queries.

| # | Rule (PRD §25.1, verbatim) | English gloss | Finding code | Code source |
|---|---|---|---|---|
| E1 | "Graph muss vorhanden sein." | A graph must exist. | `GRAPH_UNAVAILABLE` | **minted in v1** |
| E2 | "stale Graph → WARN oder FAIL nach Drift." | Stale graph → WARN or FAIL depending on drift. | `GRAPH_STALE` | §26 |
| E3 | "heuristische Edges erlaubt." | Heuristic edges are allowed (visible, non-blocking). | `HEURISTIC_EDGES_PRESENT` (info) | **minted in v1** |
| E4 | "unresolved und ambiguous werden sichtbar." | Unresolved and ambiguous references are made visible. | `UNRESOLVED_REFERENCE_IN_SCOPE`, `AMBIGUOUS_REFERENCE_IN_SCOPE` | §26 |
| E5 | "Parse Skips erzeugen WARN." | Parse skips produce WARN. | `PARSE_SKIPPED_IN_SCOPE` | §26 |
| E6 | "externe Boundaries erzeugen INFO/WARN." | External boundaries produce INFO/WARN. | `EXTERNAL_BOUNDARY_REACHED` | §26 |
| E7 | "fehlender Snapshot → UNKNOWN." | Missing snapshot → UNKNOWN. | `SNAPSHOT_MISSING` | §26 |

Note on E4/E5: the default scope of `exploratory-v1` is `repository`, so the `IN_SCOPE`
codes fire against the repository scope.

### 3.2 review-v1 (PRD §25.2)

Purpose (PRD): PR review, change impact, risk analysis, reviewer questions.

| # | Rule (PRD §25.2, verbatim) | English gloss | Finding code | Code source |
|---|---|---|---|---|
| R1 | "Graph muss current sein." | Graph must be current. | `GRAPH_STALE` (not current); `GRAPH_UNAVAILABLE` (absent) | §26; **minted in v1** |
| R2 | "Target muss auflösbar sein." | Target must be resolvable. | `TARGET_NOT_FOUND`; `TARGET_AMBIGUOUS` | §26; §27 (adopted) |
| R3 | "Parse Skip im Target Scope → FAIL." | Parse skip in the target scope → FAIL. | `PARSE_SKIPPED_IN_SCOPE` | §26 |
| R4 | "degraded Package im Target Scope → WARN oder FAIL nach Schwere." | Degraded package in the target scope → WARN or FAIL by severity. | `PACKAGE_DEGRADED` | §26 |
| R5 | "Ambiguous References im Scope → WARN." | Ambiguous references in scope → WARN. | `AMBIGUOUS_REFERENCE_IN_SCOPE` | §26 |
| R6 | "rein heuristic kritischer Pfad → WARN." | Purely heuristic critical path → WARN. | `HEURISTIC_ONLY_PATH` | §26 |
| R7 | "externe Boundaries im Pfad → WARN." | External boundaries in the path → WARN. | `EXTERNAL_BOUNDARY_REACHED` | §26 |
| R8 | "fehlende Scope-Evidenz → UNKNOWN." | Missing scope evidence → UNKNOWN. | `SCOPE_EVIDENCE_UNAVAILABLE` | §26 |

### 3.3 automated-change-v1 (PRD §25.3)

Purpose (PRD): preparing an autonomous change, safe delete, inline, automated
refactorings.

| # | Rule (PRD §25.3, verbatim) | English gloss | Finding code | Code source |
|---|---|---|---|---|
| A1 | "Graph current." | Graph must be current. | `GRAPH_STALE`; `GRAPH_UNAVAILABLE` | §26; **minted in v1** |
| A2 | "Snapshot current." | Snapshot must be current. | `SNAPSHOT_MISSING`; `SNAPSHOT_STALE` | §26; **minted in v1** |
| A3 | "Target eindeutig." | Target must be unambiguous. | `TARGET_AMBIGUOUS`; `TARGET_NOT_FOUND` | §27 (adopted); §26 |
| A4 | "kein Parse Skip im Scope." | No parse skip in scope. | `PARSE_SKIPPED_IN_SCOPE` | §26 |
| A5 | "kein degraded Package im Scope." | No degraded package in scope. | `PACKAGE_DEGRADED` | §26 |
| A6 | "keine Ambiguity im Scope." | No ambiguity in scope. | `AMBIGUOUS_REFERENCE_IN_SCOPE` | §26 |
| A7 | "keine unresolved References im relevanten Scope." | No unresolved references in the relevant scope. | `UNRESOLVED_REFERENCE_IN_SCOPE` | §26 |
| A8 | "kritische Beziehungen mindestens derived; bevorzugt confirmed." | Critical relationships at least `derived`; `confirmed` preferred. | `HEURISTIC_ONLY_PATH` | §26 |
| A9 | "externe Boundary mit möglicher Verhaltensabhängigkeit → FAIL oder explizite Human Approval." | External boundary with possible behavioral dependence → FAIL or explicit human approval. | `EXTERNAL_BOUNDARY_REACHED` | §26 |
| A10 | "fehlende Evidenz → FAIL oder UNKNOWN, niemals PASS." | Missing evidence → FAIL or UNKNOWN, never PASS. | `SCOPE_EVIDENCE_UNAVAILABLE` | §26 |

### 3.4 Finding-code registry v1

The union of all codes bound above. PRD §26's list is explicitly examples
("Beispiele"); the registry below is the closed v1 set.

| Code | Source | Bound in |
|---|---|---|
| `GRAPH_STALE` | PRD §26 | E2, R1, A1 |
| `SNAPSHOT_MISSING` | PRD §26 | E7, A2 |
| `PARSE_SKIPPED_IN_SCOPE` | PRD §26 | E5, R3, A4 |
| `PACKAGE_DEGRADED` | PRD §26 | R4, A5 |
| `AMBIGUOUS_REFERENCE_IN_SCOPE` | PRD §26 | E4, R5, A6 |
| `UNRESOLVED_REFERENCE_IN_SCOPE` | PRD §26 | E4, A7 |
| `HEURISTIC_ONLY_PATH` | PRD §26 | R6, A8 |
| `EXTERNAL_BOUNDARY_REACHED` | PRD §26 | E6, R7, A9 |
| `TARGET_NOT_FOUND` | PRD §26 | R2, A3 |
| `SCOPE_EVIDENCE_UNAVAILABLE` | PRD §26 | R8, A10 |
| `TARGET_AMBIGUOUS` | PRD §27 (adopted; not in the §26 list) | R2, A3 |
| `GRAPH_UNAVAILABLE` | **minted in v1** | E1, R1, A1 |
| `SNAPSHOT_STALE` | **minted in v1** (sibling of §26 `SNAPSHOT_MISSING`; cf. §35 `ErrTrustSnapshotStale`) | A2 |
| `HEURISTIC_EDGES_PRESENT` | **minted in v1** (info-severity visibility finding; §26 forbids a verdict without findings or an explicit all-checks-passed list) | E3 |

Adding a code to the registry is a v1-compatible additive change; removing, renaming, or
rebinding a code requires a policy version bump and a document version bump (§6).

## 4. Error model v1 (PRD §35, verbatim)

Error classes:

```text
ErrTrustSnapshotUnavailable
ErrTrustSnapshotStale
ErrTrustSnapshotCorrupt
ErrTrustSchemaUnsupported
ErrTrustTargetNotFound
ErrTrustTargetAmbiguous
ErrTrustScopeUnsupported
ErrTrustPolicyUnknown
ErrTrustDetailLimit
ErrTrustSelectiveLookupUnavailable
```

Surface mapping (PRD §35):

- **CLI:** clear error message; documented exit code; no stack traces by default.
- **MCP:** typed tool errors; no empty success responses on operational errors.

## 5. What v1 deliberately leaves open

Frozen means the shape above and nothing more. The following are known dimensions that v1
intentionally does not freeze, with the audit findings that justify deferring them:

1. **Node-level evidence.** The v1 wire shape carries edge-tier counts
   (`edge_evidence`) and file/package coverage counts only. Nodes carry no confidence
   tier in the model, and the only tier aggregation in the store is edge-level
   (`GROUP BY confidence_tier`, `core/graphstore/brief_aggregate.go:124` — audit, "What
   exists beyond §3"). Any node-level evidence is a future additive proposal.
2. **Kind × tier cross-tab.** PRD §13.8 defines `EdgesByKind` and `EdgesByTier` as
   separate maps; neither map nor a cross-tab is part of the §16 shape, and the audit
   records the store gap explicitly ("no kind×tier cross-tab" — audit, gap 2 "Store
   aggregation"). Left open.
3. **External package grouping.** PRD §13.9's `Packages` and `TopBoundaries` are not in
   the v1 shape; `boundaries[]` carries `code`/`severity`/`count` only. The audit records
   that no external-boundary count or listing exists and that external edges are ordinary
   heuristic-tier edges recognizable only via a node-kind join on the target (audit,
   gap 2). Grouping boundaries by external package is left open.
4. **Result-set scope details.** The scope name `result-set` is frozen (§1.7); its
   semantics are not. The PRD itself qualifies it "soweit technisch verfügbar" (§7.4),
   and the audit records that no confidence-tier filter and no strict mode exist anywhere
   on the query path (audit, Verdict), with only result-scoped tallies available for
   reuse (`engine/agenttools/shape/shape.go:72-88`). Details are deferred to the strict-
   query prototype work (FR-14 / WP1.7).

Also specified by the PRD but **not frozen by this document**: CLI exit codes and human
rendering (§15), the MCP `graph_health` surface (§17), persistence keys and sidecar
schemas (§14), and performance gates (§30). They freeze in their own WP1.0 artifacts or
later work packages.

## 6. Change procedure

> **Pending amendment (2026-08-05).** A second P1 PRD —
> [`2026-08-graphi-p1-prd-v1.md`](2026-08-graphi-p1-prd-v1.md), v1.0 — was registered on
> 2026-08-05 and contradicts this document in two frozen places: the fourth verdict
> (§1.5 `UNKNOWN` → `UNVERIFIED`) and the policy tokens on the CLI flag (§2.1
> `exploratory|review|automated_change` → `exploratory-v1|review-v1|automated-change-v1`).
> The owner (`samibel`) decided that **PRD v1.0 wins and this document follows**. The
> reconciliation, including what is *not* changed and why, is
> [`2026-08-graphi-p1-prd-v1-delta.md`](2026-08-graphi-p1-prd-v1-delta.md) §A.
>
> Per the procedure below this is a terminology change and therefore a document version
> bump: **§1.5 and §2.1 are amended in v1.1, landing with the code change (delta §E, PR 2).**
> Until that PR lands, §1.5 and §2.1 below still describe the shipped v0.8.0 behavior — the
> freeze is not silently edited ahead of the code. The exit-code change (delta §A3) needs
> no amendment here: §2.1 and §5 already leave the exit-code table unfrozen.

- **Wire contract:** breaking change → bump `trustReportJSONSchemaVersion` (and the wire
  `schema_version`); additive fields → allowed within the major version per §2.3 rule 7.
- **Policies:** any rule or threshold change → bump that policy's version; the version is
  part of the output. No silent changes.
- **Terminology and this document:** any change → a new document version (v2, …) plus a
  recorded decision, per the status line. Silent edits are not permitted.
- **Tier enum:** owned by code (`core/model/edge.go`); changing the closed set
  {`heuristic`, `derived`, `confirmed`} is outside P1 scope entirely.

