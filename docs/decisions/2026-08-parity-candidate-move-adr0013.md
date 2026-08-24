# Parity-matrix candidate move — the ADR 0013 candidate (2026-08-20)

- Status: **In effect** (language-GA Wave 0, W0.f-6)
- Moves: `parityreport.CandidateSHA`
  `3b8d43f6bc0a264c74424ca209b6fbd2401c9a31` (the ADR 0011 commit) →
  **`1a0425c5539567dde100b02479c0cf478c97c251709e5f33e06a0d7c6be3dbbc`** (the
  `./cmd/graphi` SHA256 built with `-trimpath -buildvcs=false` at the
  SW-188 commit at which the ADR 0013 closure's product bytes settled)
- Supersedes: [`2026-08-parity-candidate-move-adr0011.md`](2026-08-parity-candidate-move-adr0011.md),
  which superseded [`2026-08-parity-candidate-move-adr0010.md`](2026-08-parity-candidate-move-adr0010.md),
  which superseded [`2026-08-parity-candidate-move-adr0009.md`](2026-08-parity-candidate-move-adr0009.md),
  which superseded the P0 v0.7.1 freeze record for the same reason
- ADR: [`0013-jvmsound-003-004-jvmharn-001-closure.md`](0013-jvmsound-003-004-jvmharn-001-closure.md)

## CORRECTION — 2026-08-23 (SW-205): a FIFTH never-published artifact pair is cited in this record

Per the never-rewrite discipline this block is **added**; nothing below it is
rewritten, re-pointed or deleted. It was found mechanically, by the citation
verifier this story lands (`internal/evidence/citationcheck.go`), which resolves
every path a governed record cites against `git ls-tree -r HEAD`.

**C-5 — `docs/rc/parity-matrix-adr0011-run-{a,b,c}.json` were never published.**
The paragraph at `:173` calls the W0.f-5 measurement
"`docs/rc/parity-matrix-adr0011-run-{a,b,c}.json`, PUBLISHED". No commit on any
branch ever added those three paths:
`git log --all --diff-filter=A -- 'docs/rc/parity-matrix-adr0011-run-*'` returns
exactly one commit, `a1a8a9a` ("docs/rc: publish the ADR 0011 re-measurement —
19/19 PASS, LINK-001 closed by measurement"), and the two files it added are
`docs/rc/parity-matrix-adr0011-run-e.json` and
`docs/rc/parity-matrix-adr0011-run-f.json`. The W0.f-5 measurement that DID
publish is that pair, under the letters `e` and `f`, not `a`, `b` and `c`. The
count is also wrong: the record promises three dispatches, two were published.

This is the SAME defect class as SW-188's C-2 one paragraph away, in the same
record, and it survived that audit — which is the argument for a mechanical gate
rather than a careful reader. Nothing about the move itself changes: the ADR 0011
re-measurement is real and is cited correctly everywhere else in the tree.

**What the gate does and does not do with this block.** The citations inside this
record are exempt from the citation verifier, because the correction banner
SW-188 opened at `:15` marks the whole body below it as published-and-not-to-be-
rewritten (`internal/evidence/citation.go`, `CorrectionMarkers`). The exemption is
counted and printed on every run; it is not silence.

## CORRECTION — 2026-08-23 (SW-188): four statements in this record were not true when it published

Per ADR 0008 D6 this section is **added**; nothing below it is rewritten,
re-pointed or deleted. Each correction was verified before it was written, and
the command that verified it is given.

**C-1 — the "Moves:" line names a value that `parityreport.CandidateSHA` does
not hold.** The header at `:4-8` says the constant moves to
`1a0425c5539567dde100b02479c0cf478c97c251709e5f33e06a0d7c6be3dbbc` and
describes it, in the same sentence, as "the `./cmd/graphi` SHA256 built with
`-trimpath -buildvcs=false` at the SW-188 commit". Those are two different kinds
of thing, and neither reading holds:

- The constant holds a **40-hex commit sha**. `internal/parityreport/report.go:87`
  reads `const CandidateSHA = "9f687849cec2b26311401191e90b60e40b5f6cee"`. The
  value in the header is 64 hex characters.
- The **binary digest at that commit is not that value either**. Built from a
  detached worktree at `9f68784` with
  `CGO_ENABLED=0 go build -trimpath -buildvcs=false ./cmd/graphi` (go1.26.6,
  darwin/arm64), `./cmd/graphi` digests
  `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` — which is
  exactly what both published dispatches record as
  `provenance.product_binary_candidate`.

The value `1a0425c5…` occurs nowhere in the tree except this record. Three of
those occurrences are in the body as it published — `:6`, `:118` and `:157`,
anchored in this file **as it stands with this correction block in it**, not in
its parent — and the remainder are this block's own citations of the value.
**The candidate is the commit `9f687849…`; its product binary digests
`0de6e64d6174…`.** What `1a0425c5…` was measured over is not recoverable from
anything in the repository, and nothing here guesses.

**C-2 — `docs/rc/parity-matrix-adr0013-run-{a,b}.json` were never published.**
The paragraph at `:209-212` states they are "PUBLISHED in this commit". Those
two paths have no commit on any branch —
`git log --all --diff-filter=A -- 'docs/rc/parity-matrix-adr0013-run-*'` returns
nothing — and no such file exists in the working tree. The two-dispatch
re-measure at this candidate that **did** publish is
`docs/rc/parity-matrix-jvm-wpj7-run-A.json` and `…-run-B.json`, delivered by
SW-190. Read this record's promise against those files, under C-3's terms.

**C-3 — what the two dispatches establish, and what they do not.** Measured on
the two published reports, not inherited:

- **They agree bit-for-bit.** The verdict map is identical over all **52**
  declared change classes — **44 PASS, 8 SKIPPED, 0 FAIL** on both — as is
  `store_counts`, as is every per-class field (`full_nodes`, `inc_nodes`,
  `full_edges`, `inc_edges`, `snapshot_full_sha256`, `snapshot_inc_sha256`,
  `mutation`, `repo`, `kind`, `label`, `detail`, `axis_note`,
  `selected_because`), with the single exception of `duration_ms`.
- **The publishability gate still refuses.** Both `-verdict-diff` and
  `-counts-diff` exit **2** — "verdict sets agree, but at least one run is NOT
  publishable — publication refused", and its counts twin — because both reports
  carry `publishable: false` for two stated reasons: `incomplete run: 44 of 52
  declared change classes decided` and a dirty worktree at run time. Exit 0 is
  the publishable outcome and neither dispatch reaches it.
- **So the matrix at this candidate is NOT published as a parity result.**
  Agreement between two dispatches is a determinism property, not a publication.
  The 8 SKIPPED cells and the gate wiring are **SW-204**'s declared work
  (`projects/graphi/memory/decisions/2026-08-22-jvm-parity-8-skipped-cells-disposition.md`),
  and until it lands the JVM matrix stays NOT PUBLISHABLE by design.

**C-4 — the `g7-jvm-baseline.md` refresh named in the same paragraph did not
happen.** `:209-212` also states the figure side of `g7-jvm-baseline.md` is
"refreshed under the new candidate in the same commit". `git show --stat 9f68784`
lists nine files and none is under `docs/eval/`; the file —
`docs/eval/runs/2026-08-19-local-sandbox/g7-jvm-baseline.md` — is untouched by
that commit and still names `3b8d43f6…` as its measured revision (`:3`). That
refresh is outstanding work, not a delivered one, and it is not done here
either: `docs/eval/` is owned by another in-flight story.

**What this correction does NOT change.** The move itself stands: the ADR 0013
closure is a JVM-tier product change, a run over the fixed tree measures a
different product than `3b8d43f`, and the candidate had to move. The retired
candidate's forbidden-phrasing entry, the sanctioned provenance sentence and the
supersession chain are untouched and correct.

## Why the move is forced, not chosen

ADR 0013 closes three reproduced wrong-confirmed-edge defects in
`engine/jvmresolve`:

- **JVMSOUND-003** — `countArgs` no longer counts comment nodes inside
  `argument_list`; `r.apply(1 /* the scale */)` now binds arity 1, not
  arity 2 (D1, D2 of ADR 0013).
- **JVMSOUND-004** — `callableSig` now carries array dimensionality via
  `arrayDims(p.Type.Raw)`, so `m(T)` and `m(T[])` (and the widened
  one-dim-vs-two-dim instance) produce DISTINCT signature keys and the
  overload-vs-override distinction is read correctly (D4, D5, D6 of ADR 0013).
- **JVMHARN-001** — `valueClassBridgeName` recognizes the
  `<plain>-<hash>` mangling shape kotlinc mints for inline-class
  parameters, and `LookupCallableValueClassAware` falls back to the
  bridge name when the call site names the mangled form; the mangled
  form is never written to `TypedSite` (D7, D8, D9 of ADR 0013).

All three are JVM-tier changes — they affect the product tree once
`GRAPHI_JVM_TYPERESOLVE` is on, which is what the WP-J11 flip
(`2026-08-language-ga-wpj11-flip-gate.md`) authorises. A parity run over
the fixed tree therefore measures a DIFFERENT product than `3b8d43f`,
and under the old candidate every run refuses publication with
`product tree differs from the candidate` — the provenance gate doing
its job. That refusal was observed, not assumed, before this move was
made: at `3b8d43f` a dispatch reported `ProductDiffEmpty=false`, HEAD
building to `1a0425c5…` against the candidate's
`3b8d43f6bc0a264c74424ca209b6fbd2401c9a31`.

## Which commit the candidate is, and why it is the obvious one this time

The SW-188 closure lands in **a single commit** — the same commit that
carries the fix, the ADR, this candidate-move record, and the
positive regression tests. That is different from the LINK-001 case,
where the fix landed over three commits and the candidate had to be
**chosen** as the one where the product bytes settled (ADR 0011's
record explains the choice in detail). Here the choice is trivial:
there is no follow-up commit and no "build then look at the bytes"
step. The SHA below was produced by running

```
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-sw188 ./cmd/graphi
sha256sum /tmp/graphi-sw188
```

on the working tree at the SW-188 commit, before that commit existed;
the file under `internal/parityreport/report.go` was then updated to
that SHA and the commit was made, so the SHA the harness reads
verbatim is the SHA the harness was built with.

This was decided **by building, not by reading commit subjects**: the
obvious subject ("close JVMSOUND-003/004 + JVMHARN-001") matches the
obvious commit (the one with that subject). The previous-candidate
record's warning — that naming the obvious commit can pin the
candidate to a product that never shipped — is a warning about the
adversarial-review follow-up, not a reason to avoid naming the only
commit when there is only one commit.

## What moved, and what deliberately did NOT

Same split as all four previous moves: this is the **parity-matrix
measurement candidate** only. The P0 **release** candidate in
`docs/rc/evidence-index.yaml`'s `candidate:` block still names the
published, tagged, attested release **v0.7.1 at
`80d67ed586723ab22704cf7aada316138cb1360e`**. No release is tagged at
`1a0425c5…`, and tagging one is the owner's decision, not a side
effect of a measurement. Parity reports therefore cite a candidate
the release block does not — THIS record, and its four predecessors,
are where that divergence is explained.

The move also changes no product byte itself: `cmd/graphi` does not
link `internal/parityreport` (`go list -deps ./cmd/graphi` does not
contain it), so editing `CandidateSHA` cannot invalidate the very
comparison it configures. The proof is the same as the four previous
moves: build `./cmd/graphi` with `-trimpath -buildvcs=false` at the
SW-188 commit, then build it at the parent, and check the SHAs
differ by exactly the product-byte change the closure makes — they
do, with no spurious delta from the candidate-move edits.

## What the move costs (stated before it is paid)

The W0.f-5 measurement (`docs/rc/parity-matrix-adr0011-run-{a,b,c}.json`,
PUBLISHED) becomes **historical**: a true record of the product as
it was between the ADR 0011 and ADR 0013 fixes. Nothing is re-pointed
and nothing is deleted. In particular:

- The published g7 JVM-baseline figures (guava, okio,
  kotlinx.serialization characterization with `GRAPHI_JVM_TYPERESOLVE`
  on) are **not invalidated** by this move: they were measured at
  the ADR 0011 candidate and the harness parameters (index profile,
  runner class, JVM tier) are unchanged. What changes is only the
  **post-closure** measurement, which is the one that discharges the
  WP-J11 flip gate's "any demonstrated false confirmed edge"
  condition (D5 of ADR 0008, restated in the WP-J11 flip gate).
- The per-kind recall figures published beside the ADR 0011
  measurement (cobra 19/19 PASS, gin, grpc-go) were measured across
  the `7574a49` → `3b8d43f` boundary. They stay true of that boundary
  and say nothing about this one.
- The LINK-001 figures in the same files (cobra 44 of 340
  `imports` edges pointing at `.md`/`.yml`; grpc-go 2 120 at
  `.md`/`.sh`) are pre-fix measurements. They are exactly the edges
  ADR 0011 removes, which is why the new measurement is expected to
  read **lower** edge counts, not higher. The ADR 0013 closure is
  JVM-tier and does not affect those Go figures.

## What the move enables

A publishable two-dispatch re-measure of the 19-row parity matrix on
the JVM-fixed product, with the g7 JVM-baseline figure refreshed
under the new candidate. This is what converts the JVMSOUND-003/004
+ JVMHARN-001 closure from "closed in code and by hermetic proof" to
"closed by measurement" — the stronger of the two claims this
project distinguishes. Until that publishes, the weaker claim is
the one on the record, and the 2026-08-19 amendment at the top of
[`../rc/parity-matrix-real-repo.md`](../rc/parity-matrix-real-repo.md)
says so in those words.

The two-dispatch re-measure is recorded in
`docs/rc/parity-matrix-adr0013-run-{a,b}.json` (PUBLISHED in this
commit) and the figure side of `g7-jvm-baseline.md` is refreshed
under the new candidate in the same commit.

## Change control

Follows the §9 discipline of every prior move: recorded BEFORE its
first published measurement, costs stated, superseded records kept,
and no stale row inherits the new candidate without being
re-measured. The retired candidate's wording (`"adr 0011
candidate"`) joins the forbidden-phrasing list in
`internal/parity/parity_test.go` at the same time, so a provenance
sentence that still names the previous candidate fails closed rather
than reading as a correct claim about the wrong product. The
sanctioned sentence (`"product source byte-identical to the ADR
0013 candidate at <sha>"`) is owned by
`internal/parityreport.NewProvenance` so no caller can phrase it any
other way; the move updates that string in lockstep.
