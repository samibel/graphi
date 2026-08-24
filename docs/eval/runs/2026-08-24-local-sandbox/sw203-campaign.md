# SW-203 (W5.q) — perf + binary-size campaign for the intra/parse residual

**Date:** 2026-08-24 · **Runner class:** `local-sandbox` (comparison role, not the
reference class) · **Measured at:** `bca6cf27cdbb6aef8cddfdf91219134bd9f21a0e`
(`main`) · **Candidate cited by the harness:** `80d67ed586723ab22704cf7aada316138cb1360e`

The six languages in scope are the intra/parse residual: **css, hcl, json,
markdown, toml, yaml**.

> ## Headline: the perf half did not run, and this document says so rather than estimating it.
>
> The four perf suites are specified against *each language's corpus pin*. **No
> corpus pin exists for any of the six languages at `main`.** The campaign
> therefore has one head, not two: the **binary-size campaign ran in full and is
> reported below with measured numbers**; the **perf campaign did not run at
> all**, and every `GA-LANG-<lang>-G7` row stays **UNKNOWN**.
>
> Per AC-3 and AC-5 a missing measurement does not become a number. Nothing in
> this document is estimated, extrapolated, or carried over from another
> language.

---

## 1. Captured environment

Every number below was produced on this machine, in this state. No claim in this
document is made about any other CPU family, OS or runner class — the SW-177
lesson (a "reproduced across CPU families" claim the captured environment did
not support) is the reason this section is first rather than last.

| | |
|---|---|
| CPU | Apple M2 Max (12 physical / 12 logical cores) |
| Memory | 68,719,476,736 B (64 GiB) |
| OS | macOS 26.6.1 (build 25G76), Darwin 25.6.0 arm64 |
| Go | go1.26.6 darwin/arm64, `GOTOOLCHAIN=auto` (the toolchain control pins it per leg) |
| Power | AC power, battery charged |
| Thermal | no thermal warning level recorded (`pmset -g therm`) |
| Load average during the campaign | 1-minute load sampled three times: 4.45, 4.87, 7.86 on 12 logical cores → **the machine was NOT quiesced** |
| Binary-size target | `linux/amd64`, `GOAMD64=v1`, `CGO_ENABLED=0`, **cross-built from darwin/arm64** |
| Host leg | `darwin/arm64`, `CGO_ENABLED=0` |

Machine-readable copy: [`binary-size/raw/environment.txt`](binary-size/raw/environment.txt).

**Two environment caveats that bound what may be read out of this document.**

1. The load average of ~5 on 12 cores means wall-clock numbers here carry real
   contention. The harness states the same thing in its own words in every
   exported `report.json`: *"Uncontrolled. This is a development machine: page
   cache, background indexing and competing processes are not quiesced… Cold
   here means 'a store that did not exist before', nothing stronger."*
2. The binary-size figures for `linux/amd64` are **cross-built**. SW-177 left
   open whether a cross-built `linux/amd64` binary is byte-identical to a
   natively built one, and that question is **still open** — this campaign did
   not test it either. Every **delta** below is same-method and therefore robust
   to any cross-versus-native offset; the **absolute** headroom figure in §4 is
   not, and is labelled accordingly.

---

## 2. The perf campaign: what blocked it, mechanically

`cmd/eval -full-run` takes the *name of a manifest entry*. Asking it for any of
the six languages' pins produces:

```
$ go run ./cmd/eval -manifest corpus/manifest.json -full-run bootstrap -runner-class local-sandbox
eval: -full-run "bootstrap" not in manifest (have: cobra, uuid, lo, gin, grpc-go,
kubernetes, flask, sinatra, ky, express, guava, okio, kotlinx.serialization, antlr4,
retrofit, tier1-fixture-go, tier1-fixture-hero-go, tier1-fixture-hero-jvm,
tier1-fixture-hero-python, tier1-fixture-hero-typescript, cjson, Newtonsoft.Json,
nlohmann_json, lua-resty-core, composer, serde, bash-abstention, sql-abstention,
tier1-fixture-hero-bash, tier1-fixture-hero-c-cpp, tier1-fixture-hero-csharp,
tier1-fixture-hero-lua, tier1-fixture-hero-php, tier1-fixture-hero-ruby,
tier1-fixture-hero-rust, tier1-fixture-hero-sql)
exit status 2
```

The 36 entries of `corpus/manifest.json` at `main` carry `language` values
`bash, c, c_sharp, cpp, csharp, go, java, javascript, jvm, kotlin, lua, php,
python, ruby, rust, sql, typescript`. **None of css, hcl, json, markdown, toml
or yaml appears.** There is nothing for a suite to run over, and no substitute
was invented: running the four suites over some *other* repository and labelling
the result "css" would be the exact defect this story exists to avoid.

**Where the pins were expected to come from.** SW-201 (W5.o) is the pin-landing
story and its ticket is at `status: done`, but **its commits are not on `main`**:

```
$ git merge-base --is-ancestor 6958775 main   # SW-201 r2
→ SW-201 NOT on main
$ git branch -a --contains 6958775
  sw-201-w5o-corpus-pins-v3-intra-parse
$ git log --oneline main ^sw-201-w5o-corpus-pins-v3-intra-parse | wc -l
      22        # the branch is 22 commits BEHIND main
$ gh pr list --state open        # no PR proposes to land it
```

SW-203 did not merge, cherry-pick or rebase that branch. It is 22 commits behind
`main` and its diff against `main` deletes ~7,000 lines that `main` has, so
pulling it here would be a regression dressed as a dependency. The gap is
reported instead — see the follow-up in §7.

**The consequence, stated exactly.** For all six languages: `cold_index`,
`query_latency`, `progress_stalls` and `incremental/freshness` are **NOT
MEASURED**. No per-language run directory is published under this date, because
an empty directory shaped like a run directory is worse than an absent one. No
entry is added to `docs/eval/hero-budgets.json`, because a budget frozen from a
run that did not happen is the failure mode AC-3 names.

---

## 3. The binary-size campaign — measured

The gated artifact is the one `internal/bench` actually builds:
`internal/release.CanonicalBuildArgs`' contract — `-trimpath -buildvcs=true
-tags '<the 21 registered subset tags>' -ldflags '-X …version.Version=dev'` —
under `CGO_ENABLED=0`. The tag list is *derived* from
`internal/release.DefaultGrammarSubsetTags` by every script here rather than
retyped, so it cannot drift from the source of truth.

### 3.1 The noise budget, measured before any cost is quoted (AC-8)

Three controls, in order of strength:

| control | result | file |
|---|---|---|
| same build, 3 dispatches | **byte-identical** — 34,244,965 B, sha256 `dde6278a5bf4a45f…` on all three | [`noise-control.txt`](binary-size/raw/noise-control.txt) |
| identical tag **set**, reversed tag **order** | **same size** 34,244,965 B, *different* sha256 `79cccb73fdb07767…` — layout moved, size did not | [`noise-control.txt`](binary-size/raw/noise-control.txt) |
| additivity: drop(toml)+drop(lua) versus drop(toml,lua) | 4,590 + 65,418 = 70,008 measured jointly as 74,200 → residual **+4,192 B** | [`additivity-control.txt`](binary-size/raw/additivity-control.txt) |
| additivity: drop(css)+drop(yaml) versus drop(css,yaml) | 28,077 + 153,654 = 181,731 measured jointly as 177,667 → residual **−4,064 B** | [`additivity-control.txt`](binary-size/raw/additivity-control.txt) |

The two additivity residuals are +4,192 B and −4,064 B: both within one
4,096-byte page-alignment quantum, in opposite directions.

> **Stated noise budget: ±4,096 B on any single-language marginal cost.**
> A marginal cost at or below ~4 KB is **not resolvable by this method** and is
> reported as such rather than as a number. This budget is measurement-derived,
> not chosen.

### 3.2 Non-vacuity: each "without-`<lang>`" build really is without that language

`go tool nm` on each binary, counting the upstream `subsetBlobFS_<lang>` embed
symbols ([`embedded-blob-check.txt`](binary-size/raw/embedded-blob-check.txt)):

```
graphi-baseline          embeds=20   bash,c,c_sharp,cpp,css,hcl,java,javascript,kotlin,lua,
                                     markdown,php,python,ruby,rust,sql,toml,tsx,typescript,yaml
graphi-no-css            embeds=19   …css absent, the other 19 present
graphi-no-hcl            embeds=19   …hcl absent
graphi-no-markdown       embeds=19   …markdown absent
graphi-no-toml           embeds=19   …toml absent
graphi-no-yaml           embeds=19   …yaml absent
graphi-no-kotlin         embeds=19   …kotlin absent
```

20 embeds in the baseline is exactly `release.ExpectedGrammarBlobs()`. Each
removal drops **exactly one**, and it is the intended one.

That the check *counts* is shown rather than asserted
([`red-green-non-vacuity.txt`](binary-size/raw/red-green-non-vacuity.txt)): the
identical recipe is run with a removal that **cannot** take effect — the
misspelled tag `grammar_subset_cssx`, which is not in the set — and then with
the real one.

```
                          bytes        sha256(16)        langs_embedded  css_present
rg-baseline               34,244,965   2d0a817dc9a34a3d       20              1
rg-red   (no-op removal)  34,244,965   2d0a817dc9a34a3d       20              1   ← RED
rg-green (real removal)   34,216,888   d9560271a79c3e49       19              0   ← GREEN
```

The RED build is **byte-identical and sha-identical** to the baseline: a removal
that did nothing measures as nothing. The GREEN build lands on 34,216,888 B —
the same figure route A reports for css — and css is gone. Had RED and GREEN
looked alike, every marginal cost in this campaign would have been meaningless.

(The baseline's *size* is stable at 34,244,965 B across every run in this
campaign; its *sha256* is not, because `-buildvcs=true` stamps the commit and
dirty flag into the binary. Size is the measured quantity; sha is only used
within a single run to tell two builds apart.)

### 3.3 Per-language marginal cost versus the stated budget (AC-4)

The **budget is the blob allocation published in
[`bench/lang-budget.md`](../../../../bench/lang-budget.md)'s per-language table**.
It is quoted here, **not widened**, exactly as SW-177 declined to widen the gate
around Kotlin's 337,236 B blob.

Route A — the shipped default rebuilt with and without one
`grammar_subset_<lang>` tag; `linux/amd64`; baseline **34,244,965 B**
([`leg-a-tag-route.txt`](binary-size/raw/leg-a-tag-route.txt)):

| lang | without-`<lang>` build | **marginal cost** | stated blob budget | cost ÷ budget | verdict |
|---|---:|---:|---:|---:|---|
| css      | 34,216,888 | **28,077 B** | 14,325 | 1.96× | over the blob figure |
| hcl      | 34,200,607 | **44,358 B** | 20,132 | 2.20× | over the blob figure |
| markdown | 34,154,993 | **89,972 B** | 36,259 | 2.48× | over the blob figure |
| toml     | 34,240,375 | **4,590 B** (≤ noise) | 5,317 | ~0.86× | **not resolvable** — see §3.1 |
| yaml     | 34,091,311 | **153,654 B** | 25,479 | **6.03×** | over the blob figure |
| json     | — | see route B | *no blob* | — | stdlib parser, carries no subset tag |
| *kotlin (SW-177 cross-check)* | *33,859,091* | *385,874 B* | *337,236* | *1.14×* | *reproduces SW-177's 385,418 B to within 456 B* |

Route B — the shipped default rebuilt with the language's **registration**
removed from `core/parse.RegisterDefaults`; `linux/amd64`, `-buildvcs=false` on
both halves so the dirty-tree VCS stamp cannot contaminate the delta; baseline
**34,245,005 B** ([`leg-b-registration-route.txt`](binary-size/raw/leg-b-registration-route.txt)):

| lang | build | **marginal cost** | why this route |
|---|---:|---:|---|
| **json** | 34,243,315 | **1,690 B (≤ noise budget)** | json is a stdlib parser with **no** `grammar_subset_json` tag, so route A is structurally inapplicable to it — there is no tag to remove |
| css (cross-check) | 34,206,183 | 38,822 B | route A gave 28,077 B for the same language; the extra ~10.7 KB is graphi's own `core/parse/parser_css.go`, which route A leaves linked |

Route B is therefore the **larger and more complete** figure, and route A is the
figure comparable with SW-177's Kotlin measurement. Both are published rather
than one being presented as "the" number.

Host-platform leg (`darwin/arm64`, baseline 33,104,098 B —
[`host-platform-leg.txt`](binary-size/raw/host-platform-leg.txt)): css 18,128 ·
hcl 34,944 · markdown 69,536 · toml 16,928 · yaml 137,280 · kotlin 383,856. It
is measured because `internal/bench/harness.go`'s `buildBinary` passes **no**
`GOOS`/`GOARCH` and so gates a *host* binary, while `bench/bench-budget.yml`
pins its baseline to `go1.26.6/linux-amd64`. On CI the host *is* linux/amd64 and
the two coincide; on a developer machine they do not, and this campaign measured
both rather than letting one stand for the other.

### 3.4 Findings

1. **`yaml` costs 6.03× its published blob allocation** — 153,654 B against
   25,479 B. Upstream `yaml_scanner.go` is 2,096 lines and is gated by
   `grammar_subset_yaml`, so it leaves with the tag. This is the
   Kotlin-337-KB-shaped finding the story anticipated on a different language:
   **the budget is stated as it stands and is not widened to fit.** Closing the
   gap is a separate decision, not this story's.
2. **Every blob figure in `bench/lang-budget.md` understates the true binary
   cost**, in the same direction and for the same reason SW-177 gave for Kotlin.
   The measured ratios are 0.86× to 6.03×; blob size is not a proxy for
   marginal cost and should stop being read as one.
3. **`toml` (4,590 B) and `json` (1,690 B) sit at or below the ±4,096 B noise
   budget.** They are reported as unresolvable rather than as small numbers.

---

## 4. The whole-binary budget gate

`bench/bench-budget.yml` reads `binary_size_bytes: baseline 32,509,872, budget
34,250,000`. Neither is moved by this story.

```
measured (linux/amd64, cross-built, HEAD bca6cf2)   34,244,965 B
budget                                              34,250,000 B
                                                    ------------
headroom                                                 5,035 B  = 0.0147 % of budget
```

For comparison, SW-177 recorded 87,074 B (0.25 %) of headroom at candidate
`3b8d43f`. **The headroom has fallen by 94 % — from 87,074 B to 5,035 B.**

The six intra/parse languages together cost **322,341 B** (route A for the five
tagged languages, route B for json) — **64× the remaining headroom**.

**How far this may be read.** The gate is *met* on this cross-built artifact.
It is **not** asserted to be met on the natively built `ubuntu-latest` artifact
CI measures, because the cross-versus-native question SW-177 opened is still
untested (§1, caveat 2), and 5,035 B is well inside the size of an unknown of
that kind. Whether the gate is genuinely met on the runner that enforces it
needs one CI dispatch — carried as a follow-up in §7, not answered here.

---

## 5. The platform control (AC-6) — run, not assumed

### 5.1 Toolchain leg

The same tree (`80d67ed`, v0.7.1), built twice with the toolchain **pinned
explicitly** per leg, and each binary's actual toolchain read back out of its
buildinfo rather than assumed ([`toolchain-control.txt`](binary-size/raw/toolchain-control.txt)):

| tree | toolchain (from `go version -m`) | measured | SW-177 published | agreement |
|---|---|---:|---:|---|
| `80d67ed` | go1.26.5 | 32,741,344 | 32,741,344 | **exact** |
| `80d67ed` | go1.26.6 | 32,750,198 | 32,750,198 | **exact** |
| `bca6cf2` (HEAD) | go1.26.6 | 34,244,965 | — | — |

```
toolchain alone   +8,854 B   = +0.0270 %      → the +0.03 % AC-6 names, reproduced
code alone        +1,494,767 B = +4.564 %     (HEAD versus 80d67ed at the same toolchain)
```

Both of SW-177's figures reproduce **byte-for-byte** a year-quarter later on a
different machine. The toolchain is isolated at +0.03 % and the growth is
attributed to code.

> **A trap worth recording.** The first attempt used the ambient
> `GOTOOLCHAIN=auto`. `go version` printed go1.26.6, the build ran, and it
> produced 32,741,344 B — which is SW-177's **go1.26.5** row. `go version -m` on
> the binary showed `go1.26.5`: Go had silently switched *down* to satisfy
> `80d67ed`'s `go 1.26.5` directive. A leg labelled "go1.26.6" would have been
> false while every visible signal said it was fine. Both legs are now pinned and
> both are verified from buildinfo. This is precisely the AC-7 shape — a claim
> the captured environment does not support — caught by checking rather than by
> luck.

### 5.2 Perf leg — cobra and grpc-go through the same four suites

Two Go pins, **four suites**, **three dispatches** each = **24 leaf run
directories** under [`platform-control/`](platform-control/), each reproduced
with `cmd/eval -aggregate`.

The four suites are four separate harness invocations, mirroring
`.github/workflows/eval-full.yml`, because one invocation publishes at most one
of `{cold_index, incremental}` — a `-cold-runs` invocation prints *"incremental
not measured by this run"* and an `-incremental-changes` invocation prints
*"cold_index not published by this run"*.

**Every one of the 24: `-full-run` exit 0 and `-aggregate` exit 0.**

| pin | cold index p50 (9 samples) | db size | structural p95 | search p95 | agent_tools p95 | freshness | stall p95 / max |
|---|---|---|---|---|---|---|---|
| cobra | 193–213 ms | 1,413,120 B, identical in all 9 | 0.77–0.82 ms | 0.68–0.72 ms | 14.66–15.03 ms | **8/8 changes completed, 0 failed** in all 3 dispatches | 0.025–0.030 s / 0.040–0.045 s |
| grpc-go | 11,196–11,407 ms | 29,556,736 B, identical in all 9 | 0.34–0.39 ms | 2.78–3.55 ms | 319.94–326.85 ms | **8/8 changes completed, 0 failed** in all 3 dispatches | 0.001 s / **8.298–8.415 s** |

The cobra control the story asks for — *"still 8/8 changes completed"* — holds,
in all three dispatches, and grpc-go matches it. Cold-index spread across nine
samples each is **10.4 % on cobra** (193–213 ms; one 213 ms outlier on an
unquiesced machine against a 193 ms floor) and **1.9 % on grpc-go**
(11,196–11,407 ms) — the smaller workload is the noisier one in relative terms,
which is why no wall-clock budget is frozen from either. DB size is
byte-identical across all nine samples per pin. The harness, the machine and the
toolchain are steady enough that a *language-scale* delta would have been
attributable to that language.

**A finding from the control itself.** On grpc-go the progress-stall gate reads
`p95 = 0.001 s` while `max = 8.30–8.42 s`, reproducibly across all three
dispatches. That is a **~8,400× gap between the percentile the gate reads and
the tail a user would feel** — the same "percentile gate hides a tail" shape
this story's test notes name for `progress_stall_p95` on Go. It is reported, not
fixed: acting on a profile is out of scope here. Follow-up in §7.

### 5.3 The aggregate-all sweep (AC-2)

Every checked-in leaf run directory in `docs/eval/runs` (a leaf being a
directory containing `run.json`) re-run through `cmd/eval -aggregate`
([`aggregate-all-sweep.txt`](aggregate-all-sweep.txt)):

```
sweep totals: exit0=107  exit1(discrepancy)=0  exit2(unreadable)=1  exit3(incomplete)=0
UNREADABLE(2) docs/eval/runs/2026-08-20-Darwin-ARM64/apple-m2-max
```

108 leaf directories in total: 84 already checked in (83 exit 0, 1 exit 2) plus
the 24 this campaign added, all 24 exit 0.

**0 discrepancies**, which is what AC-2 asks. The single exit-2 is **proven
pre-existing**, not asserted to be: that directory is byte-identical to `main`
(`git diff --stat main -- docs/eval/runs/2026-08-20-Darwin-ARM64/` is empty), it
was last written by SW-192, and it fails because it holds an `f5_measurement`
raw series rather than a perf run — `evalreport: parse raw series
f5_measurement: json: cannot unmarshal object into Go struct field
RawSampleSet.repo of type string`. All 24 directories this campaign added exit 0.

---

## 6. Per-language grade (AC-5)

`GA-LANG-<lang>-G7` flips to PASS only if **all four suites ran**, **the budgets
are stated**, and **the binary-size gate is met**. The first condition fails for
all six.

| lang | 4 perf suites | binary-size measured | budget stated | **G7** | reason carried in the row |
|---|---|---|---|---|---|
| css | **not run** | 28,077 B (A) / 38,822 B (B) | yes | **UNKNOWN** | no v3 corpus pin at `main` |
| hcl | **not run** | 44,358 B | yes | **UNKNOWN** | no v3 corpus pin at `main` |
| json | **not run** | ≤ 4,096 B (noise) | yes | **UNKNOWN** | no v3 corpus pin at `main`; SW-201 additionally dispositioned json as a permanent no-pin abstention |
| markdown | **not run** | 89,972 B | yes | **UNKNOWN** | no v3 corpus pin at `main` |
| toml | **not run** | ≤ 4,096 B (noise) | yes | **UNKNOWN** | no v3 corpus pin at `main` |
| yaml | **not run** | 153,654 B (**6.03× budget**) | yes | **UNKNOWN** | no v3 corpus pin at `main`; and the marginal cost breaches the stated allocation |

Six UNKNOWN rows, each with a reason that names a mechanism. That is the
outcome, and it is the honest one.

### 6.1 Why those reasons are *here* and not in the rows' `current:` fields

AC-5 asks for the SW-203 reason to be carried in each row's `current:` field in
`docs/rc/evidence-index.yaml`. **That edit was made, verified line-scoped to
exactly the six G7 rows, and then reverted**, because landing it would have added
a gate failure that is not pre-existing:

- `docs/rc/evidence-index.md` is a **pure render** of the YAML, and
  `cmd/evidence -check` fails if the two drift (`cmd/evidence/main.go:136-138`).
- The only writer of that `.md` is `cmd/evidence -generate`, and it **refuses to
  write while any citation does not resolve** (`cmd/evidence/main.go:93-110`).
- Seven citations do not resolve at `main` today: the `GA-LANG-{c, c_sharp, cpp,
  lua, php, ruby, rust}-G5` rows record blob sha `71fd5829…` for
  `corpus/manifest.json`, whose sha at HEAD is `e8b050a4…`. This is
  **pre-existing and proven so**: `go run ./cmd/evidence -generate` on the
  *unmodified* `main` tree exits 1 with exactly those seven.

So `docs/rc/evidence-index.yaml` is **frozen for every story** until those seven
are resolved: any row edit strands the `.md`, and the `.md` cannot be
regenerated. The two ways out are both forbidden here — re-recording shas for a
file this story did not touch would re-attest evidence it did not verify, and
grandfathering the seven would be exempting a guard to go green. SW-203
therefore reports the blocker instead of routing around it, and the six rows keep
their SW-185 birth text.

The intended row text is preserved verbatim in the story's verification record
so the follow-on can land it unchanged the moment the citation red clears.

---

## 7. Follow-ups this campaign files

1. **SW-201's pins are not on `main`.** The ticket is `done`, the branch is 22
   commits behind `main`, and no PR proposes to land it. Until the pins land,
   SW-203's perf half cannot run and the six G7 rows cannot move. **This is the
   single blocking dependency.**
2. **The whole-binary gate has 5,035 B (0.0147 %) of headroom** on the
   cross-built artifact — down 94 % from SW-177's 87,074 B. One further grammar
   the size of yaml's *scanner alone* would breach it.
3. **The cross-versus-native question is still open**, and now matters far more
   than it did at 0.25 % headroom. One CI dispatch on `ubuntu-latest` settles
   whether the gate is genuinely met.
4. **`bench/lang-budget.md`'s per-language table lists blob sizes**, which
   understate true marginal cost by 0.86×–6.03×. The table is left as published
   (D6: amendments are added, never rewritten); the amendment beside it now says
   so with per-language numbers.
5. **`progress_stall_p95` hides an 8.4 s tail on grpc-go**, reproducibly. The
   percentile the gate reads is 0.001 s.
6. **`docs/rc/evidence-index.yaml` is frozen for every story** until the seven
   pre-existing `corpus/manifest.json` sha-mismatches are resolved — see §6.1.
   This blocks SW-203's row update and will block every other row update the
   same way, which makes it more urgent than its "owned by nobody" status
   suggests.
