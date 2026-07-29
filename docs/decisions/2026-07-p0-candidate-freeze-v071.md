# Decision: the successor P0 candidate — correction release v0.7.1 (SW-136)

> ## ✅ IN EFFECT
>
> **The candidate of record is v0.7.1 at `80d67ed`**, the published, tagged and
> attested release. This record was completed at the follow-up commit
> [§0.1](#01-the-remaining-step-exactly) describes, once `release-dag.yml` run
> [`30473740673`](https://github.com/samibel/graphi/actions/runs/30473740673) had
> published it (`success`, 10m40s).
>
> Every field that only a published artifact could supply is now filled from that
> artifact — read, recomputed and cross-checked, never guessed. **One thing remains
> outstanding and says so in place:** the **sign-off** (§2, and §8 of the superseded
> record's convention — SW-136 is a build; only the human ship gate signs off).
> §7 reproducibility **has** been run and is recorded as a result, not an expectation.
>
> **This candidate has no measurements.** [§2.1](#21-what-this-candidate-has-measured-nothing)
> is the part to read before quoting any number at it.
>
> §10's marking **has been applied** to `docs/rc/evidence-index.yaml` in this same
> change: WP2, WP4 and M1 now read `STALE`.

**Status:** in effect · **Date:** 2026-07-29 (drafted) / **2026-07-29 (completed —
the freeze date, not back-dated)** · **Story:** SW-136 · **Risk:** high ·
**Sign-off:** OUTSTANDING (SW-136 is a build; the human ship gate signs off)

**Supersedes:**
[`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md)
(SW-131, candidate v0.7.0 at `5815db5`).

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

> **That step has now happened, in exactly that order.** PR #85 merged to `main` as
> `80d67ed`; `release-dag.yml` run
> [`30473740673`](https://github.com/samibel/graphi/actions/runs/30473740673)
> completed `success` and published `v0.7.1`; and *this* commit — a later one, as §0
> argues it must be — fills in §2–§7 and applies §10's marking. The reasoning above
> is kept rather than deleted, because it is the reason the two steps are separate
> and the trap it describes is one a future candidate move can still fall into.

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
| **Git commit SHA** | **`80d67ed586723ab22704cf7aada316138cb1360e`** — `git rev-list -n 1 v0.7.1` |
| **Short** | `80d67ed` |
| **Kind** | **merge commit of SW-136 into `main`** — confirmed: `git log -1 --format='%s %P'` → `Merge pull request #85 from samibel/sw-136-minimal-correction-candidate 2cbacf089adb7a7ceb0d05b2e9180e5ad1ec7fb2 3fba2f51ad8061490198ed96d80977a1b8783190` (two parents; second is `3fba2f5`, the last SW-136 commit) |
| **Branch** | `main` |
| **Tag** | **`v0.7.1`**, **annotated**, tagger `github-actions[bot] <github-actions[bot]@users.noreply.github.com>`, `2026-07-29T17:15:31Z`, message `Release v0.7.1 (release-dag, gated SHA 80d67ed586723ab22704cf7aada316138cb1360e)`. Confirmed with `git cat-file -p v0.7.1`; the tag object's `object` field is the SHA above, so **the tag names the gated SHA and not a retag**. |
| **Release version** | **0.7.1** — resolved by `release-dag.yml` from `CHANGELOG.md`'s first `## [X.Y.Z]` header. Confirmed as published. |
| **Binary digest** | **`sha256:be64c5c090ddf08a9798877aa96f0be84ed709ec2f71f883a4d39498d6828bed`** (`graphi-linux-amd64`, the reference platform) — all eight in [§3](#3-digests) |
| **Build command** | see [§4](#4-build-command) — unchanged from v0.7.0 |
| **Go version** | **go1.26.5** — confirmed by reading the published binary: `go version -m graphi-linux-amd64` → `go1.26.5` |
| **`CGO_ENABLED`** | **0** — confirmed inside the published binary: `build CGO_ENABLED=0` |
| **Build tags** | **the same 22 tags as v0.7.0**, confirmed from the published binary's `build -tags=`: the 21 `grammar_subset*` tags (`grammar_subset` + typescript, javascript, tsx, python, java, c, ruby, rust, php, c_sharp, kotlin, cpp, bash, sql, lua, css, yaml, toml, markdown, hcl) plus `webui_embed`. Also `-trimpath=true`, `-buildmode=exe`, `-compiler=gc`, `vcs=git`, `vcs.revision=80d67ed586723ab22704cf7aada316138cb1360e`, `vcs.modified=false`. |
| **Target platforms** | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` — all five published |
| **SBOM reference** | **`sbom.spdx.json`**, `sha256:14cbf3c0d07ae1ed48f170e0ca76d755002f9f89686a23ed24e67c5bb456eb18` — [§5](#5-sbom-reference) |
| **Attestation reference** | **verified — all 8 assets exit 0, negative control exits 1** — [§6](#6-attestation-reference) |
| **Release date** | **2026-07-29** — tag object `2026-07-29T17:15:31Z`; GitHub release published `2026-07-29T17:15:56Z`, not a draft, not a prerelease, and `repos/samibel/graphi/releases/latest` resolves to `v0.7.1` |
| **Freeze date** | **2026-07-29** — the date this record was **completed**, which happens to fall on the same day it was drafted and the same day the release published. Not back-dated: the completion commit is this one. |
| **Owner** | `samibel` — Graphi Maintainer (solo; ENG/EVAL/SEC/PROD are the same person, per the PRD's solo-substitution rule) |
| **Sign-off** | **OUTSTANDING** — §8 of the superseded v0.7.0 record's convention (not §8 of this record, which is ["What this record does not do"](#8-what-this-record-does-not-do)). This is **not** a field the published artifact can supply, so completing this record does not complete it: SW-136 is a build, and only the human ship gate signs off. It is stated as outstanding rather than filled. |

Every field above that a published artifact can supply is now filled from that
artifact. The single exception is **sign-off**, which no artifact supplies and which
therefore stays outstanding rather than being invented.

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

**Result: all three sources agree, asset for asset, for all eight assets.** The
published `SHA256SUMS`, an independent recomputation with `shasum -a 256` over the
downloaded assets, and the GitHub API's per-asset `digest` field were compared and
produced no discrepancy.

| Asset | `sha256` | bytes |
|---|---|---|
| `graphi-linux-amd64` | `be64c5c090ddf08a9798877aa96f0be84ed709ec2f71f883a4d39498d6828bed` | 33 350 676 |
| `graphi-linux-arm64` | `d3c8045787bd54a68d0e2ad2e73e94c0ca135887dbaafa42633f4bc09fb76f55` | 31 423 537 |
| `graphi-darwin-amd64` | `20954bb129859b18b24d0cb498f7027cb6fa264503e2afa2c82379a28daf29f7` | 34 035 152 |
| `graphi-darwin-arm64` | `d274ae856e378a1602921fa49109dc10673c3936d0ddc953419ac61b0eeaa1e3` | 32 354 242 |
| `graphi-windows-amd64.exe` | `a8125d0441c522fd58e4613931d4e229d9ea7895feafb6c7a5a6e94bb6f553ba` | 33 969 664 |
| `sbom.spdx.json` | `14cbf3c0d07ae1ed48f170e0ca76d755002f9f89686a23ed24e67c5bb456eb18` | 108 009 |
| `capability-manifest.json` | `fdcdd44893429740b70706853c786ec5b1335eefe337a4c32a255b12618da2ec` | 34 820 |
| `SHA256SUMS` | `b660ca0213a9871c2240765a34ccef9db5d1963f2f0569714ac0de8bc0e4b3d3` | 605 |

(`SHA256SUMS` indexes the other seven; its own digest is the GitHub API's.)

`docs/rc/evidence-index.yaml` carries **one** of these in `candidate.release_digest`
(`graphi-linux-amd64`, the reference platform), because the index schema has one
digest field. This section is the full statement; the index cites this record.

`capability-manifest.json` is **unchanged from v0.7.0** — confirmed with `cmp`, not
assumed: the v0.7.0 asset was downloaded and byte-compared against the v0.7.1 asset
and they are identical, both `fdcdd448…8da2ec`. It is also byte-identical to
`docs/capability-manifest.json` at `80d67ed` (`cmp` against `git show`), so it was
copied from the tree rather than built. The capability surface did not move,
consistent with [§11](#11-the-product-tree-relative-to-v070).

## 4. Build command

Unchanged from v0.7.0 — `release-dag.yml`'s build job, via
`go run ./cmd/release -dist dist -webui -version v0.7.1`, over the web UI built from
`web/` and copied to `surfaces/http/webui/dist`. `go.mod`, `go.sum` and the whole
product tree are byte-identical to v0.7.0 ([§11](#11-the-product-tree-relative-to-v070)),
so no input to the build changed except the version string.

## 5. SBOM reference

**`sbom.spdx.json`**, published as a release asset by the `sbom` stage of
`release-dag.yml`, digest `sha256:14cbf3c0d07ae1ed48f170e0ca76d755002f9f89686a23ed24e67c5bb456eb18`
([§3](#3-digests)).

| | |
|---|---|
| SPDX version | `SPDX-2.3`, `dataLicense: CC0-1.0` |
| Document name | `release-assets` |
| Namespace | `https://anchore.com/syft/dir/release-assets-16ce70eb-d398-4402-9880-35be51f7a334` |
| Generator | `Tool: syft-1.42.3` (Organization: Anchore, Inc), license list 3.28 |
| Created | `2026-07-29T17:14:27Z` |
| Packages | 72 |

The SBOM is attested ([§6](#6-attestation-reference)) but is **not** claimed to be
byte-reproducible — see [§7](#7-reproducibility).

## 6. Attestation reference

**Result: verified. All eight assets pass against the release workflow identity and
the v0.7.1 source digest, and the negative control fails as required.**

```sh
gh attestation verify "$asset" --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest 80d67ed586723ab22704cf7aada316138cb1360e
```

| Asset | exit |
|---|---|
| `graphi-linux-amd64` | **0** |
| `graphi-linux-arm64` | **0** |
| `graphi-darwin-amd64` | **0** |
| `graphi-darwin-arm64` | **0** |
| `graphi-windows-amd64.exe` | **0** |
| `sbom.spdx.json` | **0** |
| `capability-manifest.json` | **0** |
| `SHA256SUMS` | **0** |

**Negative control** — the same binary against the *superseded* candidate's source
digest must fail, and does:

```sh
gh attestation verify graphi-linux-amd64 --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest 5815db5b053c2bb1bf3119cdb9939c1dea03cc45   # → exit 1
```

That the control fails is what makes the eight passes mean something: the
verification is discriminating on the source digest, not returning 0 for anything
handed to it.

## 7. Reproducibility

**Result: all five published platform binaries were rebuilt bit-for-bit from
`80d67ed` on a different machine and a different host OS. No deviation.**

Run 2026-07-29 on macOS 26.5.2 / arm64 (Darwin 25.5.0), Go 1.26.5, Node v26.0.0 /
npm 11.12.1 — against a release built on `ubuntu-latest` with Node 20, by the
procedure in the superseded record's §7.

| Asset | published ([§3](#3-digests)) | local rebuild | |
|---|---|---|---|
| `graphi-linux-amd64` | `be64c5c0…828bed` | `be64c5c0…828bed` | MATCH |
| `graphi-linux-arm64` | `d3c80457…b76f55` | `d3c80457…b76f55` | MATCH |
| `graphi-darwin-amd64` | `20954bb1…af29f7` | `20954bb1…af29f7` | MATCH |
| `graphi-darwin-arm64` | `d274ae85…eaa1e3` | `d274ae85…eaa1e3` | MATCH |
| `graphi-windows-amd64.exe` | `a8125d04…f553ba` | `a8125d04…f553ba` | MATCH |

Compared both ways — `shasum -a 256` against the published `SHA256SUMS`, and `cmp`
against the downloaded assets byte-for-byte. `cmd/release` also reported
`reproducible webui-embedded build verified (sha256=e015bbd7…9bf0b)` — its own
internal two-build byte comparison — before cross-compiling the matrix.

The two load-bearing preconditions the superseded record documents were satisfied
**deliberately and verified before building**, not assumed:

1. **The checkout carried no tags.** `git clone --no-tags https://github.com/samibel/graphi.git`
   followed by `git checkout --detach 80d67ed…`; `git tag -l` in that clone returned
   **empty** (0 lines). Confirmation that this was the right condition comes from the
   artifact itself: the published binaries carry the pseudo-version
   `v0.0.0-20260729170520-80d67ed58672`, which is what Go stamps only when `v0.7.1`
   is *absent* from the workspace. A clone *with* tags stamps `v0.7.1` and every
   digest differs.
2. **A real clone, not a linked worktree.** `ls -ld .git` and `file .git` both
   returned a **directory**, not the `gitdir:` pointer file a linked worktree has.
   Building in a linked worktree yields `mod … (devel)` with no `vcs.*` settings and
   different digests.

The `vcs.modified` asymmetry documented at the two previous candidates is present
here too and was reproduced target for target: `graphi-linux-amd64` (built first,
while `dist/` is still empty and therefore invisible to git) carries
`vcs.modified=false` and a clean module version, while the four later targets see the
untracked `dist/` and carry `vcs.modified=true` with a `+dirty` module version —
verified by reading `go version -m` out of each *published* asset. The bit-for-bit
match across all five means the local rebuild reproduced that asymmetry exactly,
which is itself evidence the reproduction is faithful rather than coincidental.
Making the matrix uniform remains deliberately **out of scope** (a `release-dag.yml`
change, no demonstrated failure forces it) and is carried forward unchanged.

Not reproduced by the build command, by construction — unchanged in kind from the
superseded record:

- `sbom.spdx.json` — produced by `anchore/sbom-action` in a separate job; its digest
  is recorded ([§5](#5-sbom-reference)) and attested, but reproducing an SBOM
  generator's byte output is not a property this release claims.
- `SHA256SUMS` — the published file is the *complete* index written later by the
  publish job (binaries + SBOM + manifest); the build job's `dist/SHA256SUMS` covers
  only the five binaries. The five binary lines are byte-identical between the two;
  the files differ only by the two extra lines.
- `capability-manifest.json` — copied from the tree, not built; verified
  byte-identical to `docs/capability-manifest.json` at `80d67ed` ([§3](#3-digests)).

## 8. What this record does **not** do

- **It measures nothing** and moves no threshold. [§2.1](#21-what-this-candidate-has-measured-nothing).
  Moving the candidate and marking the superseded rows `STALE` — which this record,
  now complete, **does** do ([§10](#10-what-this-move-marks-stale-applied)) — subtracts
  evidence. It adds none.
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

- **Rule 2 is why this record existed in draft first.** It requires the move to be
  recorded *before it takes effect*. What it also requires — naming the new SHA — is
  what [§0](#0-what-this-record-is-and-why-it-cannot-be-finished-in-tree) shows cannot
  be done in the same commit. Neither half was relaxed: the record was written first,
  in draft, and completed at the follow-up commit. Both halves are now satisfied.
- **This is the third candidate change under manual STALE discipline** (`4e72637` →
  `fb3bf03` → `5815db5` → this). Each one strengthens the case for the deferred P0-E
  automatic staleness detector, which remains deferred.

## 10. What this move marks STALE (applied)

Per rule 3, the marking landed in the **same change** as the completed freeze record —
this commit, which is also the one that fills in [§2](#2-decision--the-successor-p0-candidate-prd-fr-1-field-set).
It could not have landed earlier, and doing so would have been false in a way
`go run ./cmd/evidence -check` cannot catch: the check only enforces that a `STALE`
row's `current` is non-empty (`internal/evidence/check.go:63-76`), so a premature
marking passes CI and still asserts that a candidate had been superseded when it had
not. **A green `-check` is therefore not evidence that this marking is right.** The
text below was written out in advance so the step was mechanical rather than a fresh
judgement, and so it could not be quietly dropped a third time; it was applied as
written.

Unlike the `fb3bf03` → `5815db5` move — where *nothing measured was lost, because
nothing had been measured* — **this move cost real, published measurements.** That
is the price of Outcome B, it was stated before it was paid, and it has now been paid.

| Row | Status before | Now | Why |
|---|---|---|---|
| **WP2** — Candidate & reproducible technical baseline | **PASS**, `sha: 5815db5…` | **STALE** | **P0's only PASS row**, and the move costs it. It was measured, honestly, against a candidate that is being superseded. Not re-pointed: the successor has no baseline until SW-143–145 run one. |
| **WP4** — §12.2 gate results | **FAIL**, `sha: 5815db5…` | **STALE** | A published FAIL is still evidence, and it is still lost as a statement about the successor. `freshness_p95` is unfixed, but "unfixed on the old candidate" is not a measurement of the new one. |
| **M1** — reproducible accuracy/performance raw baseline | **UNKNOWN**, `sha: 5815db5…` | **STALE** | Its performance half was measured against `5815db5`; its accuracy half was never measured on any candidate. STALE is the honest status for a row whose only delivered half is now about a superseded artifact. |
| **WP0** | UNKNOWN, prose cites `5815db5` | **UNKNOWN** (citation updated) | Describes a deliverable, not a measurement — calling it STALE would overstate what was lost. Same deliberate non-STALE edit SW-131 made. |
| **M0** | STALE, prose cites `5815db5` | **STALE** (re-marked) | Already stale across two moves; re-marked to name this record, **not** re-pointed and **not** moved toward green. A row stale across three candidate moves is not thereby closer to passing. |
| `candidate:` block | `sha: 5815db5…`, digest of v0.7.0 | `sha: 80d67ed…`, digest `be64c5c0…828bed` | Cited from this record now that it is complete, never from a guess. |

All six edits were applied exactly as written above. Nothing was re-pointed: no row
that was measured against `5815db5` now claims to be about `80d67ed`.

Plus, outside the index: the two published runs at
[`docs/eval/runs/2026-07-28-ubuntu-latest/`](../eval/runs/2026-07-28-ubuntu-latest/)
are now evidence about a superseded candidate. They are **not** deleted, **not**
re-labelled and **not** re-run in place — Delta PRD §6.1 requires the first honest
baseline to be preserved, and a red baseline remains useful evidence. **Every byte of
the raw data stays.** The `STALENESS-NOTICE.md` beside them has been completed at
this same commit to record that the candidate has now moved, per its own step 2.

**Rows deliberately left alone**, so the choice is not silent: **WP1, WP3, WP5–WP10,
M2–M5** never mentioned a candidate and were never measured against one. Calling them
STALE would overstate what was lost.

## 11. The product tree relative to v0.7.0

`git rev-parse <sha>:<path>`, re-run at completion against the **released** SHA
(`80d67ed`) rather than the pre-merge branch head — same result:

| Path | at `5815db5` (v0.7.0) | at `80d67ed` (v0.7.1) | |
|---|---|---|---|
| `core` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | **IDENTICAL** |
| `surfaces` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | **IDENTICAL** |
| `cmd/graphi` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | **IDENTICAL** |
| `go.mod` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | **IDENTICAL** |
| `go.sum` | `5618e2fe9c770f39398bb16279591703a212f87e` | `5618e2fe9c770f39398bb16279591703a212f87e` | **IDENTICAL** |
| `engine` | `00e960f44a2f1c55272366524fafd59f3c0c2e4f` | `24f7807b17db6e7fb31a3730fc17db88b36db24b` | **DIFFERS** — see below |

**`engine` differs, and the difference is two test files.** Stated plainly rather
than rounded to "identical", because the v0.7.0 record's equivalent section was a
clean six-for-six and this one is not:

```
$ git diff --name-status 5815db5 80d67ed -- engine
A  engine/agenttools/shape/partial_characterization_test.go   # SW-134
A  engine/scenario/partial_characterization_test.go           # SW-134

$ git diff --name-only 5815db5 80d67ed -- engine core surfaces cmd/graphi go.mod go.sum | grep -v '_test.go$'
(no output)
```

Independently corroborated by [§7](#7-reproducibility): the published
`graphi-linux-amd64` reproduced bit-for-bit from `80d67ed`. Its digest
(`be64c5c0…828bed`) is nonetheless *different* from v0.7.0's (`f91aa839…99d25`), and
that is expected rather than contradictory — the build stamps `-ldflags
…Version=v0.7.1` and `vcs.revision=80d67ed…`, both of which changed. **This is an
inference from the verified facts above (identical tree, identical build command
except `-version`), not a separately measured decomposition of the digest
difference.** No attempt was made to isolate the differing bytes, and none is claimed.

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

### 12.1 Verified at completion, against the published release

Re-derived on 2026-07-29 at the completion commit, once
[`v0.7.1`](https://github.com/samibel/graphi/releases/tag/v0.7.1) existed. These are
the checks that could not be run in-tree, and each one filled a field above:

```sh
git rev-list -n 1 v0.7.1        # → 80d67ed586723ab22704cf7aada316138cb1360e   (§2)
git cat-file -p v0.7.1          # → annotated, tagger github-actions[bot], 2026-07-29T17:15:31Z
gh run view 30473740673         # → conclusion success, headSha 80d67ed…, 17:05:23Z → 17:16:03Z
gh release view v0.7.1          # → published 2026-07-29T17:15:56Z, not draft, not prerelease
gh api repos/samibel/graphi/releases/latest --jq .tag_name   # → v0.7.1

# §3 — three independent sources, no discrepancy across 8 assets
gh release view v0.7.1 --json assets --jq '.assets[]|"\(.name) \(.digest)"'
shasum -a 256 <downloaded assets>   # vs the published SHA256SUMS
cmp <v0.7.0 capability-manifest.json> <v0.7.1 capability-manifest.json>   # → identical

# §2 — build facts read out of the published artifact, not the workflow file
go version -m graphi-linux-amd64
#   → go1.26.5; CGO_ENABLED=0; 22 tags; -trimpath=true;
#     vcs.revision=80d67ed…; vcs.modified=false

# §6 — 8 × exit 0, plus a negative control that must fail
gh attestation verify … --source-digest 80d67ed…                    # → 0 ×8
gh attestation verify … --source-digest 5815db5…                    # → 1  (control)

# §7 — tagless rebuild from a REAL clone
git clone --no-tags https://github.com/samibel/graphi.git && cd graphi
git checkout --detach 80d67ed…
git tag -l                      # → EMPTY (precondition 1)
file .git                       # → directory, not a worktree pointer (precondition 2)
(cd web && npm ci && npm run build) && rm -rf surfaces/http/webui/dist \
  && cp -R web/dist surfaces/http/webui/dist
CGO_ENABLED=0 go run ./cmd/release -dist dist -webui -version v0.7.1
#   → all 5 binaries bit-for-bit identical to the published assets
```

**Still not verified, and not claimable from any artifact:** the **sign-off**
([§2](#2-decision--the-successor-p0-candidate-prd-fr-1-field-set), and §8 of the
superseded v0.7.0 record's convention — not §8 of this record, which is
["What this record does not do"](#8-what-this-record-does-not-do)). SW-136 is a build;
the human ship gate signs off, and this record does not pre-empt it.

**Still not measured, deliberately:** every §12.2 gate on this candidate, including
gate 9 ([§2.1](#21-what-this-candidate-has-measured-nothing)). Completing a freeze
record produces no numbers. SW-143–145 do.

## 13. References

- Authorises this move: [`2026-07-p0-candidate-decision.md`](2026-07-p0-candidate-decision.md) (SW-135, Outcome B, D1)
- Proves the mechanism: [`../eval/p0/partial-outcome-diagnosis.md`](../eval/p0/partial-outcome-diagnosis.md) (SW-134, F1–F5)
- Supersedes (in effect): [`2026-07-p0-candidate-freeze-v070.md`](2026-07-p0-candidate-freeze-v070.md) (SW-131, v0.7.0 at `5815db5`) — §9 change control, §10 marking precedent, §11 product-tree identity, §7 reproduction procedure
- The release itself: [`v0.7.1`](https://github.com/samibel/graphi/releases/tag/v0.7.1) at `80d67ed`, cut by `release-dag.yml` run [`30473740673`](https://github.com/samibel/graphi/actions/runs/30473740673)
- Which superseded: [`2026-07-p0-candidate-freeze.md`](2026-07-p0-candidate-freeze.md) (SW-121, v0.6.7 at `fb3bf03`) and [`2026-07-m0-candidate-freeze.md`](2026-07-m0-candidate-freeze.md) (SW-116, `4e72637`, never published)
- The preserved baseline: [`../eval/runs/2026-07-28-ubuntu-latest/`](../eval/runs/2026-07-28-ubuntu-latest/) + its `STALENESS-NOTICE.md`
- Evidence index: `docs/rc/evidence-index.yaml` (source) → `docs/rc/evidence-index.md` (generated); `go run ./cmd/evidence -check`
- Release DAG: `.github/workflows/release-dag.yml`; ADR 0005; publish lock `.github/publish-lock.json` (`locked: false`, untouched by this story)
- Blocks until complete: SW-143–145 (Final Runs — the first baseline on this candidate)
