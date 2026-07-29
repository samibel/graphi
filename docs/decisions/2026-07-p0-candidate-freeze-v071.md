# Decision: the successor P0 candidate — correction release v0.7.1 (SW-136)

> ## ⚠️ PREPARED — **NOT IN EFFECT**
>
> **The candidate of record is still v0.7.0 at `5815db5`**
> ([`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md)).
> This record does not move it, and reading it as though it had is the one
> misreading it is written to prevent.
>
> A freeze record can only be completed **after** the release it freezes exists —
> see [§0](#0-what-this-record-is-and-why-it-cannot-be-finished-in-tree). Every
> field below that only a published artifact can supply reads
> **`PENDING — supplied at tag time`**, never a guess and never `UNKNOWN`
> (`UNKNOWN` would claim it was looked for and not found).
>
> **Nothing in `docs/rc/evidence-index.yaml` is marked `STALE` by this record.**
> The prepared marking, with its replacement text, is [§10](#10-what-this-move-will-mark-stale-prepared-not-applied).

**Status:** prepared, not in effect · **Date:** 2026-07-29 · **Story:** SW-136 ·
**Risk:** high · **Sign-off:** PENDING (SW-136 is a build; the human ship gate signs off)

**Supersedes, once in effect:**
[`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md)
(SW-131, candidate v0.7.0 at `5815db5`). Until then it supersedes nothing.

---

## 0. What this record is, and why it cannot be finished in-tree

The move is authorised. [`2026-07-p0-candidate-decision.md`](2026-07-p0-candidate-decision.md)
(SW-135) selected **Outcome B** and named one forcing defect, **D1**. SW-136 has
corrected D1. What is missing is the release.

**The ordering is structural, not an omission.** §9 rule 1 of the superseded record
requires the candidate to move *"only onto a **published, tagged, attested**
release"*, and rule 2 requires the move to be recorded *"in a successor to the prior
file, **naming the new SHA**"*. Those two together are unsatisfiable in a single
commit, for a mechanical reason:

- the release SHA is produced by `release-dag.yml`, which triggers on a push to
  `main` and tags the exact commit its gates ran on;
- a record naming that SHA must therefore be written **at a later commit** than the
  SHA it names;
- so a candidate can never cite itself. This is not new: SW-131's freeze record does
  not exist at `5815db5`, the candidate it freezes. The evidence index at the
  candidate SHA names the *previous* candidate.

That is the trap this section exists to keep the project from walking into again.
Inventing a SHA, back-dating a digest, or marking rows `STALE` against a release
that does not exist would each produce a record that is checkable and **false** —
the precise failure P0 exists to remove. So this record is written to be
**completable**, not to be complete.

### 0.1 The remaining step, exactly

There is **no manual tagging step in this project**, and performing one would be
wrong: `release-dag.yml` is the only workflow permitted to push tags or create
releases, and a hand-cut tag would bypass every gate the DAG carries (`gate → build
→ sbom → publish`). AC-4's *"no hand-built artifact"* is a prohibition on exactly
that.

The remaining step is therefore **merging SW-136 to `main`**. `CHANGELOG.md` already
carries the `## [0.7.1]` header, so the DAG resolves `tag=v0.7.1`,
`introduced=true`, and — with `.github/publish-lock.json` reading `locked: false` —
publishes. Then, and only then, this record is completed at a follow-up commit and
§10's marking is applied.

### 0.2 What is already true, and checkable, today

| Claim | Where |
|---|---|
| D1 is corrected, minimally, in measurement code only | [§1.2](#12-what-sw-136-changed), [§11](#11-the-product-tree-relative-to-v070) |
| The regression test was committed **before** the correction | [§12](#12-verification) |
| The correction recovers exactly the 25 executions the diagnosis attributes to `partial` | [§12](#12-verification) |
| No byte of the shipped product tree changed | [§11](#11-the-product-tree-relative-to-v070) |
| Gate 9 has **no** reading on either candidate | [§2.1](#21-what-this-candidate-has-measured-nothing) |

---

## 1. Context — the blocker that forces the move

### 1.1 The blocker

Inherited verbatim from SW-135's decision record §3.1:

> **D1 — the agent-tools countability rule at `cmd/eval/querylatency.go:430-435`
> omits `partial` from the `allowed` set of `explain_symbol`, `change_risk` and
> `related_files`, while the fourth member of the same FR-8 pool declares it
> countable.**

It clears the §6.2 move bar twice over — as a proven measurement-integrity defect
(**C2**) and, independently, as a failure that makes required evidence impossible to
produce (**C4**). The C4 half is what makes retention impossible rather than merely
untidy: the shortfall is `|{s ∈ sample : items(op, s) > 10}|`, every term of which is
machine-independent, so **re-running the baseline at `5815db5` any number of times
produces 975 again**. It reproduced in both published runs, on an Intel Xeon Platinum
8573C and an AMD EPYC 9V74, and on 4 of the 5 pinned repositories.

The full argument, including why Outcome A was tested seriously and fails, is in the
decision record and is not restated here.

### 1.2 What SW-136 changed

Two commits, in this order, because the order is an acceptance criterion:

| # | Commit | Contents |
|---|---|---|
| 1 | `ddfe70a` | `cmd/eval/partialoutcome_regression_test.go` (new, **RED** at this commit); freezes SW-134's historical `allowed` sets and repoints the two published-tally replays at them |
| 2 | `5783e07` | the correction: `allowed` becomes `["found","partial"]` for `explain_symbol` / `change_risk` and `["found","empty","partial"]` for `related_files`; updates the one SW-134 test that pinned the asymmetry |

Nothing else. In particular **F4 is not corrected** — see [§8](#8-what-this-record-does-not-do).

## 2. Decision — the successor P0 candidate (PRD FR-1 field set)

| FR-1 field | Value |
|---|---|
| **Git commit SHA** | **PENDING — supplied at tag time.** `git rev-list -n 1 v0.7.1` |
| **Short** | PENDING |
| **Kind** | expected: merge commit of SW-136 into `main` — confirm with `git log -1 --format='%s %P'` |
| **Branch** | `main` |
| **Tag** | **PENDING** — expected `v0.7.1`, annotated, tagger `github-actions[bot]`; confirm with `git cat-file -p v0.7.1` |
| **Release version** | **0.7.1** — resolved by `release-dag.yml` from `CHANGELOG.md`'s first `## [X.Y.Z]` header, which reads `## [0.7.1]` on this branch. This is the one release-bound field that is already determined, because the workflow reads it from a tracked file. |
| **Binary digest** | **PENDING** — see [§3](#3-digests) |
| **Build command** | see [§4](#4-build-command) — unchanged from v0.7.0 |
| **Go version** | expected **go1.26.5** (`go.mod` is byte-identical to v0.7.0's, and the workflow uses `go-version-file: go.mod`); confirm by reading the published binary |
| **`CGO_ENABLED`** | expected **0** (job-level `env`); confirm inside the published binaries |
| **Build tags** | expected the same 22 tags as v0.7.0 (`grammar_subset*`, `webui_embed`); confirm |
| **Target platforms** | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` |
| **SBOM reference** | **PENDING** — [§5](#5-sbom-reference) |
| **Attestation reference** | **PENDING** — [§6](#6-attestation-reference) |
| **Release date** | **PENDING** |
| **Freeze date** | **PENDING** — the date this record is completed, not the date it was drafted |
| **Owner** | `samibel` — Graphi Maintainer (solo; ENG/EVAL/SEC/PROD are the same person, per the PRD's solo-substitution rule) |
| **Sign-off** | **PENDING** — [§8](#8-owner-date-sign-off) of the superseded record's convention: this record does not claim a sign-off it does not have, and is not back-dated |

Every `PENDING` above is a field only the published artifact can supply. None is a
value that was looked for and not found, which is why none reads `UNKNOWN`.

### 2.1 What this candidate has measured: **nothing**

Stated before anyone asks. **A new candidate has no measurements until it is
measured** (AC-9). Concretely:

- **Gate 9 (`agent_context_p95`) has no reading on this candidate.** Not PASS, not
  FAIL, not "expected to pass". `UNKNOWN`, and `UNKNOWN` is not a PASS (PRD §8.2).
- **On v0.7.0, gate 9's `UNKNOWN` is now final** — a closed fact about a candidate
  that could not reach FR-8's floor, not a pending reading.
- **The nine other §12.2 verdicts published against v0.7.0 do not carry over.**
  Eight PASS and one FAIL (`freshness_p95`, ~3.2× budget) are statements about
  `5815db5` measured by the pre-correction instrument. They are not evidence about
  this release, in either direction.
- **The correction does not predict a direction, and what it does predict is
  upward.** The corrected pool will contain the executions the old one excluded —
  systematically the largest answers — and the harness still measures at an item cap
  of 10 against a shipped default of 20 ([§8](#8-what-this-record-does-not-do)). Both
  push the measured distribution up. Anyone expecting a number near the old
  undersampled 471.250 ms is expecting the wrong thing.

Producing an actual reading is the Final Runs slice (SW-143–145): two complete runs
on the reference runner class with `candidate_match` true in every
`environment.json`, and `sufficient: true` for the pool in both. Only then is a p95
compared with 500 ms.

## 3. Digests

**PENDING — supplied at tag time.** The complete published asset set (5 binaries +
`sbom.spdx.json` + `capability-manifest.json` + `SHA256SUMS`), read from the
release's own `SHA256SUMS`, independently recomputed with `shasum -a 256` from the
downloaded assets, and cross-checked against the GitHub API's per-asset `digest`
field — all three must agree, asset for asset, as they did for v0.7.0.

`docs/rc/evidence-index.yaml` carries **one** of these in `candidate.release_digest`
(`graphi-linux-amd64`, the reference platform), because the index schema has one
digest field. This section is the full statement; the index cites this record.

`capability-manifest.json` is expected to be **unchanged from v0.7.0**
(`fdcdd44893429740b70706853c786ec5b1335eefe337a4c32a255b12618da2ec`): the capability
surface did not move, consistent with [§11](#11-the-product-tree-relative-to-v070).
Confirm with `cmp`, do not assume.

## 4. Build command

Unchanged from v0.7.0 — `release-dag.yml`'s build job, via
`go run ./cmd/release -dist dist -webui -version v0.7.1`, over the web UI built from
`web/` and copied to `surfaces/http/webui/dist`. `go.mod`, `go.sum` and the whole
product tree are byte-identical to v0.7.0 ([§11](#11-the-product-tree-relative-to-v070)),
so no input to the build changed except the version string.

## 5. SBOM reference

**PENDING** — the `sbom` stage of `release-dag.yml` publishes `sbom.spdx.json` as a
release asset. Its digest belongs in [§3](#3-digests).

## 6. Attestation reference

**PENDING** — verify every asset against the release workflow identity and the
source digest, with a superseded candidate as a **negative control** that must fail:

```sh
gh attestation verify "$asset" --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest <v0.7.1 SHA>          # → exit 0 for all 8 assets
gh attestation verify dist/graphi-linux-amd64 --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest 5815db5b053c2bb1bf3119cdb9939c1dea03cc45   # → exit 1, negative control
```

## 7. Reproducibility

**PENDING** — a tagless rebuild from a real clone (not a linked worktree) at the
frozen SHA must reproduce the published binary digests bit-for-bit, by the procedure
in the superseded record's §7. It is expected to hold, since the product tree and
both module files are byte-identical to a release that already reproduced
bit-for-bit on a different host OS — but *expected* is not *verified*, and this
section stays PENDING until it is run.

## 8. What this record does **not** do

- **It does not move the candidate.** [§0](#0-what-this-record-is-and-why-it-cannot-be-finished-in-tree).
- **It marks nothing `STALE`.** [§10](#10-what-this-move-will-mark-stale-prepared-not-applied).
- **It measures nothing** and moves no threshold. [§2.1](#21-what-this-candidate-has-measured-nothing).
- **It does not correct F4.** The harness resolves an omitted item cap to **10**
  (`engine/scenario/fixture.go:292,295,297`) while every shipped surface resolves it
  to `shape.DefaultMaxItems` = **20**, so the instrument times a configuration no
  default-configured user runs, and every answer whose natural size lands in 11–20
  items is `partial` to the instrument and `found` to a user.

  **This is a deliberate, and contestable, scope decision, recorded as such.**
  SW-135's decision record §5 step 1 lists F4's correction as something to do *"in
  the same change"*, and §3.2 says it *"will be corrected in the same cycle"*. It is
  not corrected here. The reasons, in order of weight:

  1. **The story's acceptance criteria bound the diff.** AC-3: *"The correction diff
     shall touch only what the named defect requires. Unrelated changes shall be
     split out."* Out of scope: *"Correcting any defect other than the one SW-135
     named."* SW-135 named exactly one defect, D1, and §3.2 of the same record states
     plainly that F4 does not force this move.
  2. **Two corrections make the next baseline unattributable.** D1 changes *which*
     executions are counted; F4 changes *what work* each execution does. Batched, any
     movement in the corrected p95 has two possible causes and the diagnosis's own
     residual (§10.2: how many of the 25 survive at a cap of 20 is *not derivable*
     from the published artifacts) becomes unanswerable by measurement.
  3. **F4 corrupts no published verdict.** It feeds exactly one gate,
     `agent_context_p95`, and that gate has no verdict on either candidate.

  **The cost is real and is not hidden:** correcting F4 later means a further
  candidate move and a further full baseline re-run, which is exactly what Delta PRD
  §6.2 exists to prevent. That tension is not resolved here. It is on `backlog.md`,
  and whoever plans SW-142/SW-143–145 must decide it deliberately rather than inherit
  it. **A baseline run against v0.7.1 will still be measured at an item cap of 10 and
  is therefore still systematically optimistic relative to the shipped default** —
  that limitation must be carried into any reading of gate 9.
- **It does not re-tier or re-scope the 12 frozen Stable operations.** `partial`
  under truncation remains designed, documented, GA-frozen behaviour;
  `corpus/hero/hero-17-explain-symbol-partial.yaml` is unchanged.

## 9. Change-control and the STALE rule

**Inherited verbatim and unamended** from
[`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md) §9.
This move is an application of the rule, not an amendment to it, so it is cited
rather than restated: rules 1–5 (blocker-only moves onto a published/tagged/attested
release; record before effect; dependent rows marked `STALE` in the same change,
never re-pointed; marking is manual and that is a known limitation; `UNKNOWN` is not
a pass) are in force here word for word.

Two notes specific to this move:

- **Rule 2 is why this record exists in draft.** It requires the move to be recorded
  *before it takes effect*. What it also requires — naming the new SHA — is what
  [§0](#0-what-this-record-is-and-why-it-cannot-be-finished-in-tree) shows cannot be
  done in the same commit. The resolution is not to relax either half, but to write
  the record now and complete it at the follow-up commit.
- **This is the third candidate change under manual STALE discipline** (`4e72637` →
  `fb3bf03` → `5815db5` → this). Each one strengthens the case for the deferred P0-E
  automatic staleness detector, which remains deferred.

## 10. What this move **will** mark STALE (prepared, not applied)

Per rule 3, the marking lands in the **same change** as the completed freeze record —
i.e. at the follow-up commit that fills in [§2](#2-decision--the-successor-p0-candidate-prd-fr-1-field-set), **not here**.
Applying it now would be false in a way `go run ./cmd/evidence -check` cannot catch:
the check only enforces that a `STALE` row's `current` is non-empty, so a premature
marking would pass CI and still assert that a candidate had been superseded when it
had not. The text is written out here so the step is mechanical rather than a fresh
judgement, and so it cannot be quietly dropped a third time.

Unlike the `fb3bf03` → `5815db5` move — where *nothing measured was lost, because
nothing had been measured* — **this move costs real, published measurements.** That
is the price of Outcome B and it is stated before it is paid.

| Row | Status today | Becomes | Why |
|---|---|---|---|
| **WP2** — Candidate & reproducible technical baseline | **PASS**, `sha: 5815db5…` | **STALE** | **P0's only PASS row**, and the move costs it. It was measured, honestly, against a candidate that is being superseded. Not re-pointed: the successor has no baseline until SW-143–145 run one. |
| **WP4** — §12.2 gate results | **FAIL**, `sha: 5815db5…` | **STALE** | A published FAIL is still evidence, and it is still lost as a statement about the successor. `freshness_p95` is unfixed, but "unfixed on the old candidate" is not a measurement of the new one. |
| **M1** — reproducible accuracy/performance raw baseline | **UNKNOWN**, `sha: 5815db5…` | **STALE** | Its performance half was measured against `5815db5`; its accuracy half was never measured on any candidate. STALE is the honest status for a row whose only delivered half is now about a superseded artifact. |
| **WP0** | UNKNOWN, prose cites `5815db5` | **UNKNOWN** (citation updated) | Describes a deliverable, not a measurement — calling it STALE would overstate what was lost. Same deliberate non-STALE edit SW-131 made. |
| **M0** | STALE, prose cites `5815db5` | **STALE** (re-marked) | Already stale across two moves; re-marked to name this record, **not** re-pointed and **not** moved toward green. A row stale across three candidate moves is not thereby closer to passing. |
| `candidate:` block | `sha: 5815db5…`, digest of v0.7.0 | new SHA + digest | Cited from this record once it is complete, never from a guess. |

Plus, outside the index: the two published runs at
[`docs/eval/runs/2026-07-28-ubuntu-latest/`](../eval/runs/2026-07-28-ubuntu-latest/)
become evidence about a superseded candidate. They are **not** deleted, **not**
re-labelled and **not** re-run in place — Delta PRD §6.1 requires the first honest
baseline to be preserved, and a red baseline remains useful evidence. A
`STALENESS-NOTICE.md` notice already sits beside them recording what is true *today*
(the instrument that produced them has been corrected); it is completed at the same
follow-up commit.

**Rows deliberately left alone**, so the choice is not silent: **WP1, WP3, WP5–WP10,
M2–M5** never mentioned a candidate and were never measured against one. Calling them
STALE would overstate what was lost.

## 11. The product tree relative to v0.7.0

`git rev-parse <sha>:<path>`, run at `5815db5` and at this branch's head:

| Path | at `5815db5` | at SW-136 head | |
|---|---|---|---|
| `core` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | **IDENTICAL** |
| `surfaces` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | **IDENTICAL** |
| `cmd/graphi` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | **IDENTICAL** |
| `go.mod` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | **IDENTICAL** |
| `go.sum` | `5618e2fe9c770f39398bb16279591703a212f87e` | `5618e2fe9c770f39398bb16279591703a212f87e` | **IDENTICAL** |
| `engine` | `00e960f44a2f1c55272366524fafd59f3c0c2e4f` | `24f7807b17db…` | **DIFFERS** — see below |

**`engine` differs, and the difference is two test files.** Stated plainly rather
than rounded to "identical", because the v0.7.0 record's equivalent section was a
clean six-for-six and this one is not:

```
$ git diff --name-status 5815db5 HEAD -- engine
A  engine/agenttools/shape/partial_characterization_test.go   # SW-134
A  engine/scenario/partial_characterization_test.go           # SW-134

$ git diff --name-only 5815db5 HEAD -- engine core surfaces cmd/graphi go.mod go.sum | grep -v '_test.go$'
(no output)
```

Both were added by **SW-134's diagnosis**, which changed no production code, and
neither ships: `_test.go` files are excluded from a non-test build, and
`go list -deps ./cmd/graphi | grep -c engine/scenario` returns **0** — `engine/scenario`
is the harness fixture and is in no shipped binary's dependency graph despite living
under `engine/`.

The complete set of top-level paths that changed between the two candidates is
`.github/workflows`, `CHANGELOG.md`, `cmd/eval`, `docs/`, `engine/agenttools`
(test only), `engine/scenario` (test only) — the NFR-7 measurement-code set, docs and
CI. **No production file appears in that list.**

**What this means:** this release ships the *same product* as v0.7.0. It differs only
in the instrument that measures it.

**What this does NOT mean** — the v0.7.0 record's §11 warnings apply here unchanged
and are inherited, not weakened:

- It is **not a measurement**. It is a statement about source bytes, produced by
  `git`, not by running anything.
- It is **not evidence about performance or accuracy**, on either candidate.
- It is **not evidence for any §12.2 gate**. Every §12.2 gate on this candidate is
  `UNKNOWN` ([§2.1](#21-what-this-candidate-has-measured-nothing)).
- In particular it is **not** a reason to carry v0.7.0's eight PASS verdicts across.
  Identical product bytes measured by a *different instrument* is precisely the
  situation in which the old numbers are not the new numbers.

## 12. Verification

Re-derived on 2026-07-29 on branch `sw-136-minimal-correction-candidate`. Everything
here is checkable **now**, in-tree, without a release:

```sh
# the correction, and its bound
git show 5783e07 -- cmd/eval/querylatency.go
#   → allowed ["found","partial"] / ["found","empty","partial"]; not_found,
#     ambiguous and error still uncountable; no other operation touched

# AC-2: the regression test precedes the correction, in the commit history
git log --oneline --reverse ddfe70a^..HEAD -- cmd/eval
#   → ddfe70a (test, RED) then 5783e07 (correction)
git stash && git checkout ddfe70a -- . && CGO_ENABLED=0 go test ./cmd/eval/ -run TestAgentContextPool_EveryOperationCountsPartial
#   → FAIL at the test-only commit

# the correction is load-bearing, not cosmetic (mutation check)
#   revert the two allowed sets in cmd/eval/querylatency.go, then:
CGO_ENABLED=0 go test ./cmd/eval/ -run 'AgentContextPool|CorrectedRule'
#   → 4 tests FAIL, incl. "the correction recovers 0 executions, want the 25"
#     and "pooled 975 executions, want FR-8's floor of 1000", both runs

# the recovery, against the PUBLISHED tallies rather than a re-run
CGO_ENABLED=0 go test ./cmd/eval/ -run TestPublishedBaseline_CorrectedRuleRecoversAllTwentyFiveExecutions -v
#   → 16 + 5 + 4 + 0 = 25 recovered; 975 → 1000 in run-a and run-b

# SW-134's diagnosis still checks out after the correction
CGO_ENABLED=0 go test ./cmd/eval/ -run TestPublishedBaseline_PartialAloneExplainsThe25ExecutionShortfall
#   → PASS, replayed through the frozen historical sets

# the product tree (§11)
for p in engine core surfaces cmd/graphi go.mod go.sum; do
  git rev-parse 5815db5:$p; git rev-parse HEAD:$p; done
git diff --name-only 5815db5 HEAD -- engine core surfaces cmd/graphi go.mod go.sum | grep -v '_test.go$'
go list -deps ./cmd/graphi | grep -c engine/scenario     # → 0

# the release the DAG would cut from this branch
grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md   # → ## [0.7.1] - 2026-07-29
git show main:CHANGELOG.md | grep -m1 -E '^## \[[0-9]+\.[0-9]+\.[0-9]+\]'  # → ## [0.7.0]
#   → release-dag resolves tag=v0.7.1, introduced=true
jq .locked .github/publish-lock.json                        # → false (publication open)

# the project's gates
CGO_ENABLED=0 go test ./... ; gofmt -l . ; go vet ./... ; CGO_ENABLED=0 go build ./...
go run ./cmd/layerguard ; go run ./cmd/coverage -check ; go run ./cmd/evidence -check
go test -json ./... | go run ./cmd/testgate -stdin -producer-exit-code=0
```

**Not verified, because it cannot be until the release exists:** every field marked
`PENDING` in [§2](#2-decision--the-successor-p0-candidate-prd-fr-1-field-set)–[§7](#7-reproducibility),
and the marking in [§10](#10-what-this-move-will-mark-stale-prepared-not-applied).

## 13. References

- Authorises this move: [`2026-07-p0-candidate-decision.md`](2026-07-p0-candidate-decision.md) (SW-135, Outcome B, D1)
- Proves the mechanism: [`../eval/p0/partial-outcome-diagnosis.md`](../eval/p0/partial-outcome-diagnosis.md) (SW-134, F1–F5)
- Supersedes once in effect: [`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md) (SW-131, v0.7.0 at `5815db5`) — §9 change control, §10 marking precedent, §11 product-tree identity
- Which superseded: [`2026-07-p0-candidate-freeze.md`](2026-07-p0-candidate-freeze.md) (SW-121, v0.6.7 at `fb3bf03`) and [`2026-07-m0-candidate-freeze.md`](2026-07-m0-candidate-freeze.md) (SW-116, `4e72637`, never published)
- The preserved baseline: [`../eval/runs/2026-07-28-ubuntu-latest/`](../eval/runs/2026-07-28-ubuntu-latest/) + its `STALENESS-NOTICE.md`
- Evidence index: `docs/rc/evidence-index.yaml` (source) → `docs/rc/evidence-index.md` (generated); `go run ./cmd/evidence -check`
- Release DAG: `.github/workflows/release-dag.yml`; ADR 0005; publish lock `.github/publish-lock.json` (`locked: false`, untouched by this story)
- Blocks until complete: SW-143–145 (Final Runs — the first baseline on this candidate)
