# Decision: the WP-J11 flip gate — nine conditions, owner-measured

- **Date:** 2026-08-20
- **Scope:** the WP-J11 default flip of the JVM binders
  (`GRAPHI_JVM_TYPERESOLVE` from opt-in to default-on) and, by extension, every
  future wave's identical-flip decision
- **Method:** the independent review record's R1 ("WP-J11 default flip is
  blocked; nine conditions, zero met") and the language-GA execution-plan
  cutover checklist (`docs/plan/2026-08-graphi-language-ga-execution-plan-v1.md`
  §4) were reconciled into a single, machine-checkable list; the index-migration
  story is added because the flip changes graph bytes in stores users already
  have and a flip that silently invalidates a user's index is not a flip that
  can be shipped
- **Story:** W1.e / SW-178 — ratifies ADR 0008 D1–D9 and states this gate;
  reads alongside [`2026-08-language-ga-independent-review.md`](2026-08-language-ga-independent-review.md)
  (the reviewed record), [`../adr/0008-jvm-declared-type-resolution.md`](../adr/0008-jvm-declared-type-resolution.md)
  (the ADR this gate ratifies), and the W1.f / SW-179 story file (the flip
  itself, which is owner-gated on this gate being green end-to-end)
- **Status:** Accepted as a measurement instrument. Not a decision to flip —
  SW-179 is. This document is the instrument SW-179 measures against, not a
  substitute for the owner's call.

## 1. Why this gate is a separate document

R1's "nine conditions, zero met" was a verdict, not a checklist: the review
recorded *that* no condition was met, not *what* measuring each one would look
like. SW-179 has to decide, not judge, so each condition is restated below as a
**checkable statement** naming the artefact the check consumes and the command
that runs it. Where the language-GA execution-plan §4 cutover checklist is
stronger (the "one line in `engine/semantic/semantic.go`" change, the
prose-and-matrix binding), this gate borrows its wording rather than restating
it under a second label — a gate that contradicts itself is one a reviewer
cannot check.

Three terms, used in every condition below, are not redefined per condition:

- **"PASS with URI and sha"** — the evidence-index honesty rule
  (`docs/rc/evidence-index.yaml`'s header: a row reads PASS only if it carries
  BOTH a non-empty `evidence_uri` AND a non-empty `sha`). `cmd/evidence -check`
  enforces it; `cmd/coverage -check` enforces it again at the GA-language layer.
- **"green" / "non-red"** — the relevant gate's own success criterion; nothing
  is "green" by the absence of a measurement.
- **"UNKNOWN is not a pass"** — the rule a row or verdict reads UNKNOWN, STALE
  or FAIL is NOT PASS, and a green row without a measured red-without-fix is
  itself not a pass either.

## 2. The nine conditions

Each condition is one bullet, with the artefact, the check, and the recorded
state on 2026-08-20 (today, all nine are NOT MET — that is the gate's value:
it turns SW-179 into a decision rather than a judgement call).

### C1 — ADR 0008 ratified D1–D9

**Artefact:** [`../adr/0008-jvm-declared-type-resolution.md`](../adr/0008-jvm-declared-type-resolution.md)
**Check:** the ADR's `Status:` reads **Accepted**; the dated ratification block
at the top of the ADR records D1, D2, D3, D4, D5, D6, D7, D8, D9 each
**Accepted** (or Accepted-as-reformulated / Accepted-as-re-scoped /
Accepted-as-ruled where the wave changed the ruling), with the reason, the
evidence and the dated amendment stated in the block; no ruling left in an
intermediate state.
**Recorded state (2026-08-20):** **MET.** Ratification block added today (SW-178);
D1/D3/D4/D7 unchanged; D2/D5/D6/D8/D9 recorded with the reformulation, scope
change and rule from Wave 0 / Wave 1. The table above shows each ruling's
amended status and the binding evidence.
**Why the ADR alone isn't enough.** A ratified ADR is necessary but not
sufficient — it tells the flip *what* the JVM binder does, not *that the
binder is sound, calibrated and measured*. Conditions C2–C8 measure the
binder; C9 protects the user's index from the flip.

### C2 — every `GA-LANG-{java,kotlin}-*` evidence row reads PASS with URI and sha

**Artefact:** `docs/rc/evidence-index.yaml` (the GA-LANG-java-* and
GA-LANG-kotlin-* rows, plus the §"The GA-language axis" ordering block).
**Check:** `go run ./cmd/evidence -check` exit 0 with every `GA-LANG-java-*`
and `GA-LANG-kotlin-*` row reading PASS, evidence_uri non-empty, sha
non-empty, sha being a 40-char git blob sha (not a placeholder). UNKNOWN,
STALE and FAIL count as NOT passed; a row whose sha is the literal placeholder
counts as NOT passed regardless of status.
**Recorded state (2026-08-20):** **NOT MET.** The 18 rows are born UNKNOWN
(SW-174, W1.a); none have an artefact. SW-175 (binding rate / histogram),
SW-176 (WP-J7 real-repo matrix), SW-177 (G7 perf baselines) and the
outstanding G1/G3/G5/G6/G8 work each consume a row but none of them have
flipped a single row to PASS yet. The full pass list is
`grep '^  - id: GA-LANG-' docs/rc/evidence-index.yaml | awk '{print $4}'`.
**This is AC-7.** The flip gate leans on `cmd/evidence -check` rather than
duplicating its parsing; the check is reproducible from any clean checkout.
**No row may read PASS without a measured red-without-fix** where the row's
gate is an assertion; SW-174's header documents the rule.

### C3 — ground-truth soundness: zero `JVMSOUND-0xx` counterexamples on the attested candidate

**Artefact:** the WP-J9 differential ground-truth CI job
(`.github/workflows/jvm-groundtruth.yml`) and its pinned fixtures.
**Check:** the most recent CI run against the *attested* candidate
(`internal/parityreport.CandidateSHA:74`, today `3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`)
reports zero `JVMSOUND-0xx` defects; the job's exit 0 has not been inverted by
a hand-written waiver. An OPEN defect is a stop-ship; the gate does not
provide a waivering route.
**Recorded state (2026-08-20):** **NOT MET.** Two open JVM defects — **JVMSOUND-003**
and **JVMSOUND-004** — are reproduced wrong-confirmed-edge defects, open and
deliberately unfixed; see `projects/graphi/backlog.md`, "Found by the SW-172
signature-aware JVM oracle". They are invisible today only because the JVM
registrants are default-OFF, which is precisely what the WP-J11 flip changes;
**flipping them on turns two known D5 stop-ships into GA defects in the same
commit.** They must be FIXED, or disclosed under D8, before C3 reads MET.
SW-179 AC-9 is where the disclosure obligation is discharged.

### C4 — real-repo parity (G4) for Java and Kotlin: PASS or PARTIAL with defects filed and pinned (D8)

**Artefact:** the WP-J7 real-repository matrix, `docs/rc/parity-matrix-real-repo.md`
(the JVM section), two dispatches (`parity-matrix-jvm-wpj7-run-l.json` and
`-run-m.json` or their successors).
**Check:** the two dispatches agree at verdict AND per-row count AND snapshot
digest granularity (`-verdict-diff` and `-counts-diff` at exit 0); every
FAIL or PARTIAL verdict is filed as `PARITY-0xx` and pinned by a test that
goes red-with-instructions the moment the defect closes; per the D8
re-scope, no PARTIAL is allowed for a defect the JVM code path can exhibit.
**Recorded state (2026-08-20):** **NOT MET** for two reasons that pre-date
SW-176 itself and SW-176 makes no worse. (1) The product tree has diverged
from `parityreport.CandidateSHA` `3b8d43f` — `internal/parity` sets
`ProductDiffEmpty = false`, which the provenance gate reads as "this run cannot
be published"; the owed candidate move is an owner decision that predates
SW-176 and SW-178. (2) The matrix is INCOMPLETE on `jvm_change_import_shadowing`
and `jvm_mixed_dir_change_receiver_type` (PARITY-COV-001): no pin exhibits the
two shapes, so `-verdict-diff` returns 2 rather than 0 and AC-2 is unsatisfiable
from this branch. Either settle the two rows or move the candidate.

### C5 — legible abstention shipped on CLI and MCP (SW-171)

**Artefact:** the W0.g / SW-171 outcome — `internal/coverage.PackageEvidence`
named-skip populations, the Labs `strict_query` envelope's `Limitations`
roll-up, and the `trust-report` carry of the JVM skip histogram.
**Check:** `cmd/graphi doctor` reports the JVM named skips at info severity;
`graphi trust-report --json` carries `abstention.registrants` populated for
the JVM when any `*.java`/`*.kt` source exists; `cmd/graphi query
-strict_query` returns the per-package / per-file named-skip roll-up rather
than an empty outcome; SW-171 ships at **recommend-ship** or later, and the
shipped commit is the one the gate is checked against.
**Recorded state (2026-08-20):** **NOT MET.** SW-171 reads `recommend-ship` and
awaits human ship (`projects/graphi/stories/SW-171/story.md`). The two
structural reasons R5 named — `query.Result` is a frozen Stable-12 contract
and the per-body counters are not site-attributable — are resolved by the
**per-package / per-file roll-up** SW-171 builds (not by leaking counters into
Stable-12), which is the right shape. The gate does not reopen that choice.
**This is AC-5.** Legible abstention is one of the two easily-overlooked
conditions the SW-178 brief names; the other is the index-migration story
(C9).

### C6 — every JVM perf + budget gate G7 reads PASS, with the four suites over the JVM corpus

**Artefact:** `docs/eval/runs/...` raw runs (SW-177, W1.d), the budgets
derived from them, and the G7 `GA-LANG-{java,kotlin}-G7` row's evidence_uri +
sha.
**Check:** all four perf suites (cold index, query latency, freshness /
incremental, progress stalls) have a JVM run; the freshness suite's
`EVALFRESH-001` (Go-only `modifiableGoFile`) is closed so the fourth suite
runs at all; the budgets derived from the runs are inside
`docs/bench/lang-budget.md`'s allocation and the whole-binary budget gate;
the G7 row reads PASS with URI + sha. **Measured rather than asserted** per the
language-GA programme plan §2 G7's "publish the measurement" rule.
**Recorded state (2026-08-20):** **NOT MET.** SW-177 measured three of the
four suites over guava at the W0.f-5 candidate (cold, query latency, stalls),
and the matrix row stays UNKNOWN for two measured reasons: EVALFRESH-001
(aborts on guava with `the index contains no modifiable Go source files`)
and EVALBUDGET-001 (no JVM budget was derived because the run is on
`local-sandbox`, the DECLARED COMPARISON class). The measured local-to-CI
projection for `agent_brief` straddles the 500 ms gate; recorded as a
projection, NO verdict drawn.

**Amended (2026-08-22, SW-191):** **STILL NOT MET, for a shorter list of
reasons.** Both named blockers are closed. EVALFRESH-001: the change sequence
is language-scoped (`cmd/eval/sourcefamily.go`) and `-incremental-changes 100`
now exits 0 on every JVM, python and TS-family pin — guava 99/100, okio 73/100,
kotlinx.serialization 97/100, flask 100/100, ky 98/100, express 100/100, with
the cobra control at 100/100. EVALBUDGET-001: `docs/eval/hero-budgets.json`'s
`historical_ceilings` block is now READ by `cmd/eval/budgets.go`, so a
comparison-class run is bounded by a non-ratcheting ceiling instead of refused.
What is still missing is what it always was: a **reference-class**
(`ubuntu-latest`) dispatch at the current candidate, on a clean tree, with a
fresh URI + sha per pin. The remaining freshness shortfalls (okio, ky, guava)
are parser-coverage gaps in the Kotlin / TypeScript / Java extractors, recorded
on the G7 rows, not harness gaps.

### C7 — capability surface (G8) and `cmd/coverage -check` bind java/kotlin at their declared capability

**Artefact:** `docs/coverage-matrix.yaml` (the `category: ga-language` rows
when the flip lands), `docs/language-support.md`, `docs/stability-tiers.md`,
`internal/coverage/galang.go`'s `CheckGALanguages`.
**Check:** `go run ./cmd/coverage -check` exit 0; if java/kotlin are being
flipped, the corresponding `category: ga-language` rows exist with the
declared capability matching what `engine/trust/capability.go` derives at
runtime; **the prose (language-support.md, stability-tiers.md) is updated to
match the matrix**, with the matrix winning on disagreement (per
`stability-tiers.md`'s "the matrix wins" rule). The naming rule — every
user-facing GA mention of a language carries its capability level beside it,
e.g. `Java — GA (cross-file-heuristic)`, never bare "GA" — has been swept
across every surface and the sweep is reproducible (the SW-150 sweep-the-class
lesson applies).
**Recorded state (2026-08-20):** **NOT MET.** No java/kotlin `ga-language` row
exists in `docs/coverage-matrix.yaml` (the matrix has exactly one such row,
`go`, at `capability: typed-confirmed`). Adding either row before C2 reads
MET would convert a green matrix into a red build — `galang.go:129-131`
violates on every non-PASS evidence row, which is the ordering constraint
SW-174's header records. The flip is the row's addition; it is not
prerequisite to C2.

### C8 — kill switches survive the flip (D4)

**Artefact:** `engine/semantic/semantic.go:35-48` (the registration site);
`TestCapabilities_JVMOptInFlip` and its kill-switch sibling.
**Check:** `TestCapabilities_JVMOptInFlip` exit 0 with the JVM registrants
live (`GRAPHI_JVM_TYPERESOLVE=1`) and the capability derivation reports
java/kotlin at `typed-confirmed`; the kill-switch sibling exits 0 with
`GRAPHI_NO_TYPERESOLVE_JAVA=1` (or its per-language equivalent) and the
capability derivation reports java/kotlin at `cross-file-heuristic`. **The
kill-switch test must be demonstrated capable of failing** — a green test that
cannot go red proves nothing (the SW-170 / SW-152 lesson).
**Recorded state (2026-08-20):** **PARTIAL.** Both tests exist and pass
under the current default-off regime; the kill-switch sibling has been
demonstrated capable of failing (per its own review). The test does NOT yet
exercise the **post-flip** default-on regime because the flip has not
happened. C8 reads MET only AFTER the one-line flip and a green run of both
tests under it.

### C9 — the **index-migration story**: a version bump + a documented re-index path

**Artefact:** `engine/ingest/warmstart.go`'s `ingestSemanticsVersion`
constant (currently `11`); `docs/HOWTO.md` §5.x (the re-index section);
SW-179 AC-3; SW-179 AC-4's kill-switch test on a pre-flip store; the
`cmd/graphi status` / `graphi rebuild` surface that surfaces the migration
to the user.
**Check:** THREE parts, each load-bearing:

1. **Version bump.** `ingestSemanticsVersion` increases by one (the next free
   value is `12`). The bump is recorded with the rule that flips the stamp,
   one sentence, in the same style as the existing 1-to-11 history
   (`warmstart.go:13-78`). The reason must name what changes graph bytes —
   today: the JVM registrants move from default-off to default-on, so a
   pre-flip store has `*.java`/`*.kt` files whose confirmed `calls` /
   `references` / `implements` edges were never minted. A flipped binary
   greeting that store with "up to date" would serve bytes the old binary
   would never produce — exactly the failure mode `ingestSemanticsVersion`
   exists to prevent.
2. **Re-index path, documented and tested.** The documented path is:
   `graphi rebuild` (the flagless cold full re-index facade over the
   `--full` pass, per `docs/coverage-matrix.yaml:794`); it is reachable from
   the same CLI every user already has; it does NOT require deleting the
   store manually (an undocumented requirement would be the same shape as
   the W0.b migration race). `docs/HOWTO.md:279-304` (the warm-start stamp
   and the "restart graphi after changing the root `.graphi/ignore.json`"
   section) is the precedent for the wording.
3. **Pre-flip store test.** A test opens a pre-flip store (a real
   `core/graphstore` instance populated by the pre-flip binary on a fixture
   JVM file set), runs the documented re-index path under the post-flip
   binary, and asserts the post-flip graph is the post-flip binary's graph
   (not the pre-flip binary's graph, not a hybrid). The test is the
   "untested migration is the shape that produced the W0.b migration race"
   sentence from SW-179 AC-3's test notes, in code.

**Recorded state (2026-08-20):** **NOT MET.** No version bump; no documented
re-index path; no pre-flip store test. The story is **stated here, not
implemented** — building the version bump, the re-index path and the test is
SW-179's change, where the graph bytes actually move. The reason the story
is in *this* gate rather than SW-179's description is precisely to keep the
flip from happening until the migration story is visible — **a flip whose
implementation invisibly invalidates a user's index is not a flip that can
be shipped.** A flip with a documented re-index path the user can run is.
**This is AC-4.** The durable shape of "what does a flip cost the user" is
asked and answered here, so SW-179's commit cannot quietly decide it.

## 3. The nine, as a single checkable list

For tooling, the nine conditions in machine-readable form (the form SW-179's
"check condition by condition" assertion is supposed to consume rather than
re-parse prose):

```
C1 — ADR 0008 ratified D1–D9                      (status: MET 2026-08-20)
C2 — every GA-LANG-{java,kotlin}-* row PASS       (status: NOT MET)
C3 — zero JVMSOUND-0xx counterexamples            (status: NOT MET, 003/004 open)
C4 — JVM real-repo parity PASS or ruled PARTIAL   (status: NOT MET)
C5 — legible abstention shipped (SW-171)          (status: NOT MET, recommend-ship)
C6 — G7 perf + budget PASS, four suites           (status: NOT MET, 3 of 4 + budget)
C7 — coverage matrix + prose bind the flip        (status: NOT MET, no row)
C8 — kill switches survive the flip               (status: PARTIAL, not yet flipped)
C9 — index-migration story (version + re-index)   (status: NOT MET, story stated)
```

The status line records 2026-08-20; SW-179 will replace it with its own
check-date. A condition reading UNKNOWN/STALE on the row or the verdict,
or NOT MET on the gate, is NOT PASS, and the flip does not happen.

## 4. What the gate does not decide

- **The owner decision.** Every condition is a check the owner can read and
  the harness can run; *whether the conditions collectively warrant the flip*
  is the owner's call. The gate exists so that call is a decision rather than
  a judgement.
- **The flip itself.** SW-179 performs the flip; this gate is measured
  before SW-179 starts.
- **The Java/Kotlin capability levels.** D2's measurement is supplied here
  (in the ADR ratification block); the level (`cross-file-heuristic` vs
  `typed-confirmed`) is set by SW-179's flips and the `ga-language` rows it
  adds. A flip that moves Java to `typed-confirmed` does NOT change Kotlin's
  level unless Kotlin's own GA-LANG-kotlin-* rows carry it (per the D7
  no-split ruling, withdrawn by the PO; see independent review R7).
- **The candidate move.** C4's condition 1 names the owed candidate move as
  a blocker; performing the move is its own decision with its own ADR
  (`docs/decisions/2026-08-parity-candidate-move-adr0009.md` and successors),
  and SW-178 does not perform it.
- **The D9 residual-owner rule.** The ADR ratification block defers the
  residual-owner fix (or a product-wide unowned-from-node assertion) to the
  WP-J11 flip, on the strength of a measured delta of zero; C1 names the
  fix as something the flip must decide, not something this story decides.
- **The other languages.** Every condition is JVM-specific. The wave-2/3
  languages (Python, TypeScript family, the cross-file-heuristic residual)
  reuse this gate's shape but with their own evidence rows and their own
  binder soundness; that reuse is SW-180's template, not SW-178's.

## 5. Cross-references

- Independent review record: [`2026-08-language-ga-independent-review.md`](2026-08-language-ga-independent-review.md)
  (§2 R1, §3 priority order 1–6, §4 follow-ups)
- ADR 0008 (this gate ratifies): [`../adr/0008-jvm-declared-type-resolution.md`](../adr/0008-jvm-declared-type-resolution.md)
- Plan §4 cutover checklist (the source of the "one line in semantic.go" and
  the "matrices win on disagreement" rules):
  [`../plan/2026-08-graphi-language-ga-execution-plan-v1.md`](../plan/2026-08-graphi-language-ga-execution-plan-v1.md)
- Plan §6 GA-language axis (the ordering constraint that requires evidence
  rows to exist before the matrix row): same plan, §6
- ADR 0011 ("An `imports` edge targets the package's source files",
  `docs/adr/0011-imports-edge-targets-package-source-files.md`) — precedent
  for an index-migration story written as an ADR amendment, not a release note
- W0.f-5 / SW-166 (the LINK-001 fix) — the change that moved the candidate
  to `3b8d43f` and is therefore the candidate SW-179 dispatches against
- The story this gate was built under: `projects/graphi/stories/SW-178/story.md`
- The story this gate gates: `projects/graphi/stories/SW-179/story.md`
- The kill switch: `engine/semantic/semantic.go:35-48`,
  `TestCapabilities_JVMOptInFlip` and its kill-switch sibling
- The semantics stamp: `engine/ingest/warmstart.go:13-78`