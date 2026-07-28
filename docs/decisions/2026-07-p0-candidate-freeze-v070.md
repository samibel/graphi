# Decision: the frozen P0 candidate — tagged release v0.7.0 at `5815db5` (SW-131)

This is the artifact every P0 measurement is bound to. It supersedes
[`2026-07-p0-candidate-freeze.md`](2026-07-p0-candidate-freeze.md), whose candidate
**v0.6.7 at `fb3bf03`** was published, tagged and attested — and is nevertheless
**unmeasurable by construction**, for the two mechanical reasons in §1. If you are
about to measure, benchmark, audit, or make a claim about graphi under P0, this file
names what you are measuring — and, just as importantly, what it does **not** contain.

**Status:** accepted · **Date:** 2026-07-28 · **Story:** SW-131 · **Risk:** high ·
**Sign-off:** PENDING (see §8)

---

## 1. Context — the blocker that forced the move

§9 of the superseded record is deliberately strict: *"The candidate SHA moves only for
a documented blocker fix … 'main has moved on' is never a reason."* This move qualifies
on those terms and on no others. The blocker is not drift, not convenience, and not
feature work. It is this:

> **The P0 candidate `fb3bf03` cannot be measured by the P0 harness, and the P0 harness
> cannot be run from the candidate. The frozen artifact is unmeasurable by construction,
> which defeats the entire purpose of freezing it.**

That is a single blocker with **two independent mechanical reasons**, and closing either
one alone does not lift it. SW-130 established both; both were re-derived for this record
(§12).

**Reason 1 — the harness does not exist at the candidate SHA.** `git ls-tree fb3bf03
cmd/eval/` returns **11** entries; the same command at the new candidate returns **48**.
The five files that carry every §12.2 measurement are absent at `fb3bf03`:

| Harness file | at `fb3bf03` | at `5815db5` | Measurement it carries |
|---|---|---|---|
| `cmd/eval/coldseries.go` | **ABSENT** | present | cold-index p50/p95, peak RSS, DB size |
| `cmd/eval/querylatency.go` | **ABSENT** | present | warm search / caller / callee / impact / agent-context p95 |
| `cmd/eval/incremental.go` | **ABSENT** | present | freshness p95, full-vs-incremental parity |
| `cmd/eval/stalls.go` | **ABSENT** | present | progress-stall p95 |
| `cmd/eval/rawexport.go` | **ABSENT** | present | the raw-sample export every claim must trace to |

**Reason 2 — the harness has no external-binary path, so it cannot be pointed at the
candidate either.** All **13** `flag.String`s in `cmd/eval/main.go` accept no `graphi`
executable: there is no `-graphi-binary` or equivalent external-subject flag.
`cmd/eval/fullrun.go` links the product packages into the harness process and times
`ing.IngestAll` **in-process**, so the thing measured is always the harness's own build,
never a supplied binary.

The obvious workaround — run the harness from `main` — is refused by the harness's own
provenance rule. With the harness built from a SHA that is not the candidate,
`candidateMatch = false`, and `coldgates.go`, `querygates.go`, `freshgates.go` and
`stallgates.go` each force every §12.2 gate to `UNKNOWN` **before a threshold is ever
compared**. That refusal is correct behaviour and is not being weakened here.

So P0-C's instruments are complete and cannot produce a single publishable number. This
story moves the candidate onto a release cut from `main`, where the candidate and its
instruments coexist for the first time. It runs **no measurement** — that is SW-130,
which this record unblocks.

### What this move invalidates

The honest answer is: **nothing measured, because nothing was ever measured.** That is
worth stating precisely rather than generously.

At the moment of this move, in `docs/rc/evidence-index.yaml`:

- **no** row reads `PASS`;
- **no** row carries a non-empty `evidence_uri` or `sha`;
- therefore **no** measurement, benchmark, accuracy score or performance number is lost.

What *is* invalidated is a set of **statements about the candidate** — prose in the
`current` and `next_action` columns that named `fb3bf03`, and which would otherwise
silently come to mean the new candidate. Those are enumerated and re-marked in §10.
Re-pointing them without re-measuring is precisely the substitution P0 exists to remove,
so they stay `STALE` and name their successor.

One further consequence, recorded so it is not discovered later: **SW-130's blocked state
is lifted by this record, not satisfied by it.** SW-130's AC-1 is unchanged and
unweakened; this story makes it satisfiable as written.

## 2. Decision — the frozen P0 candidate (PRD FR-1 field set)

| FR-1 field | Value |
|---|---|
| **Git commit SHA** | `5815db5b053c2bb1bf3119cdb9939c1dea03cc45` |
| **Short** | `5815db5` |
| **Kind** | true merge commit — `Merge pull request #79 from samibel/sw-131-cut-v070`; parents `8cf86b94e82c83b0f729e2d90e72c1ada131dc23` (prior `main`) and `08e425ba504e9ff6948e1b660840729549a346a9` (the CHANGELOG cut) |
| **Branch** | `main` (`origin/main` points at this commit) |
| **Tag** | `v0.7.0` — annotated tag object, peels to the SHA above; tagger `github-actions[bot]`, message `Release v0.7.0 (release-dag, gated SHA 5815db5b053c2bb1bf3119cdb9939c1dea03cc45)` |
| **Release version** | **0.7.0** — [github.com/samibel/graphi/releases/tag/v0.7.0](https://github.com/samibel/graphi/releases/tag/v0.7.0), published 2026-07-28T13:37:30Z, `draft: false`, `prerelease: false`, `targetCommitish` = the SHA above |
| **Binary digest** | see §3 — eight asset digests, **not** `UNKNOWN`. Reference platform `graphi-linux-amd64` = `sha256:f91aa839c5f246f3d20330bfb0771a2f11cc035859121661692a4b2429399d25` |
| **Build command** | see §4 |
| **Go version** | **go1.26.5** (`go.mod` `go 1.26.5`; the workflow uses `go-version-file: go.mod`; confirmed by reading the published binary with `go version -m`) |
| **`CGO_ENABLED`** | **0** (job-level `env: CGO_ENABLED: "0"`; confirmed as `build CGO_ENABLED=0` inside all five published binaries) |
| **Build tags** | `grammar_subset`, `grammar_subset_{typescript,javascript,tsx,python,java,c,ruby,rust,php,c_sharp,kotlin,cpp,bash,sql,lua,css,yaml,toml,markdown,hcl}`, `webui_embed` — 22 tags, confirmed as `build -tags=…` inside the published binary |
| **Target platforms** | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` — `internal/release.ReleaseTargets` order; `GOAMD64=v1` on the amd64 members |
| **SBOM reference** | §5 |
| **Attestation reference** | §6 |
| **Release date** | 2026-07-28 (publish 13:37:30Z; commit time 13:26:20Z, per the binaries' `vcs.time`) |
| **Freeze date** | 2026-07-28 |
| **Owner** | `samibel` — Graphi Maintainer (solo; roles ENG/EVAL/SEC/PROD are the same person, per the PRD's solo-substitution rule) |
| **Sign-off** | **PENDING** — see §8. This record does not claim a sign-off it does not have. |

No field above reads `UNKNOWN`. Every value is read back from the published artifact or
the immutable tag/commit objects, not transcribed from the workflow definition (§12).

## 3. Digests — the complete published asset set

Read from the release's own `SHA256SUMS`, independently recomputed from the downloaded
assets with `shasum -a 256`, and cross-checked against the GitHub API's per-asset
`digest` field. **All three agree**, asset for asset.

| Asset | sha256 |
|---|---|
| `graphi-linux-amd64` | `f91aa839c5f246f3d20330bfb0771a2f11cc035859121661692a4b2429399d25` |
| `graphi-linux-arm64` | `f8fe332a029e3d20263872dea4ef3bf04f1fa4b897afc50a340092261be9b124` |
| `graphi-darwin-amd64` | `761643e480e3a91670974c0be3e618432ca111018b20526a969d0445fd983faa` |
| `graphi-darwin-arm64` | `9140e73f90391f5c5122ae13951558c2e1a1402d4d19e3cae8c87932747e152d` |
| `graphi-windows-amd64.exe` | `3a7e17016a484a19862a39bf8cb7bbbd294c8d1e8b00c610414851b1185273ff` |
| `sbom.spdx.json` | `4761cf7c2cfa7d7979302dda18e8b50f9ba79540c85acfa23d1036f96f31f281` |
| `capability-manifest.json` | `fdcdd44893429740b70706853c786ec5b1335eefe337a4c32a255b12618da2ec` |
| `SHA256SUMS` (itself) | `ec5cb2526e627ccdc80ed8284281d84ed17c8b2d570207b8ed01d320e70400d1` |

`docs/rc/evidence-index.yaml` carries **one** of these in its `candidate.release_digest`
scalar — `graphi-linux-amd64`, the reference platform — because the index's schema has
one digest field. This table is the full statement; the index cites this record.

`capability-manifest.json` published as an asset is byte-identical to
`docs/capability-manifest.json` at `5815db5` (`cmp` exit 0, both
`fdcdd448…`), so the release's machine-readable capability claim is the one CI gated
(`cmd/coverage -check`). Its digest is **unchanged from v0.6.7** — expected, and
consistent with §11: the capability surface did not move between the two candidates.

## 4. Build command

The release runner's exact sequence (`.github/workflows/release-dag.yml`, job
*reproducible build + release matrix (same SHA)*, job-level `env: CGO_ENABLED: "0"`,
`runs-on: ubuntu-latest`, Node 20):

```sh
# checkout of the exact gated SHA, WITHOUT tags (actions/checkout@v4 default)
git clone --no-tags https://github.com/samibel/graphi.git && cd graphi
git checkout --detach 5815db5b053c2bb1bf3119cdb9939c1dea03cc45

# released binaries embed the web UI
(cd web && npm ci && npm run build)
rm -rf surfaces/http/webui/dist
cp -R web/dist surfaces/http/webui/dist

# the recorded release build command
CGO_ENABLED=0 go run ./cmd/release -dist dist -webui -version v0.7.0
```

`cmd/release` first proves the flavor reproducible (two builds, byte-compared — it
reported `reproducible webui-embedded build verified` on the rebuild in §7), then
cross-compiles the matrix. Per target it runs exactly
(`internal/release.CanonicalBuildArgs`, contract `internal/release.CanonicalBuildArgs/v1`):

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build \
  -trimpath -buildvcs=true \
  -tags "grammar_subset grammar_subset_typescript … webui_embed" \
  -ldflags "-X github.com/samibel/graphi/internal/version.Version=v0.7.0" \
  -o dist/graphi-<os>-<arch> ./cmd/graphi/
```

The workflow resolves `-version` from the gate job's `tag` output, which for this run was
`v0.7.0`; the literal above is that resolved value, not a template.

## 5. SBOM reference

- Release asset: <https://github.com/samibel/graphi/releases/download/v0.7.0/sbom.spdx.json>
  (`sha256:4761cf7c2cfa7d7979302dda18e8b50f9ba79540c85acfa23d1036f96f31f281`)
- Produced by the `sbom` job of run
  [30363482852](https://github.com/samibel/graphi/actions/runs/30363482852) — SPDX-JSON
  via `anchore/sbom-action@e22c389904149dbc22b58101806040fa8d37a610` (v0), over the
  assembled release binaries plus the gated capability manifest; workflow artifact
  `sbom-5815db5b053c2bb1bf3119cdb9939c1dea03cc45` (present, not expired)
- Scope caveat: the SBOM describes the *assembled release assets*, not the source tree.
  It is attested (§6) like every other asset.

## 6. Attestation reference

- Attestations index: <https://github.com/samibel/graphi/attestations>
- API entry for the reference binary:
  `GET /repos/samibel/graphi/attestations/sha256:f91aa839c5f246f3d20330bfb0771a2f11cc035859121661692a4b2429399d25`
  (returns 1 attestation)
- Produced by the `publish` job of run
  [30363482852](https://github.com/samibel/graphi/actions/runs/30363482852) via
  `actions/attest-build-provenance@e8998f949152b193b063cb0ec769d69d929409be` (v2) over
  `release-assets/*`

SLSA v1 provenance predicate (`predicateType: https://slsa.dev/provenance/v1`), read back
from the signing certificate:

| Property | Value |
|---|---|
| Builder ID | `https://github.com/samibel/graphi/.github/workflows/release-dag.yml@refs/heads/main` |
| Run invocation | `https://github.com/samibel/graphi/actions/runs/30363482852/attempts/1` |
| Source repository | `https://github.com/samibel/graphi` |
| **Source repository digest** | `5815db5b053c2bb1bf3119cdb9939c1dea03cc45` |
| Runner environment | `github-hosted` |
| OIDC issuer | `https://token.actions.githubusercontent.com` |

All **eight** assets verify against that workflow identity and that source digest
(`gh attestation verify`, exit 0 each). The same command with the *superseded* candidate
`fb3bf03` as `--source-digest` exits **1** — the binding is checked, not assumed, and the
negative control uses the exact candidate this record replaces.

### Why AC-1 is satisfied

AC-1 asks that v0.7.0 be cut in `CHANGELOG.md` and published through `release-dag.yml`
from a green `main` SHA, with every gate job running on that same SHA. All three hold:

1. **The cut happened and triggered the publish.** `## [0.7.0] - 2026-07-28` landed on
   `main` in `08e425b`, merged as `5815db5` (PR #79). `.github/publish-lock.json` reads
   `locked: false`, so the merge alone was sufficient: `release-dag.yml` tagged, built,
   SBOM'd, attested and published without further intervention.
2. **Every gate ran on that SHA and every one is green.** **15** workflow runs are bound
   to `5815db5` (event `push`, branch `main`) and **all 15 concluded `success`**:
   `test-gate`, `release-gate`, `release`, `cgo-conformance`, `lint`,
   `dependency-security`, `ledgeraudit`, `coverage-matrix`, `bench`, `corpus`, `eval`,
   `privacy-audit`, `canary`, `graphi-broad`, `release-dag`. This includes
   `privacy-audit`, the Linux netns gate that enforces the zero-outbound invariant a
   macOS workstation cannot check.
3. **The DAG's shape makes it unconditional, and the attestation binds it
   cryptographically.** `publish` `needs: [gate, build, sbom]`, and every job checks out
   `ref: ${{ github.sha }}`; all four jobs of run 30363482852 concluded `success`. Builder
   identity *is* `release-dag.yml@refs/heads/main`, `sourceRepositoryDigest` *is*
   `5815db5` — neither forgeable by a local build.

## 7. Reproducibility (PRD FR-1: "Ein Release-Binary kann aus dem Candidate reproduzierbar neu gebaut werden")

**Result: all five published platform binaries were rebuilt bit-for-bit from `5815db5`
on a different machine and a different host OS. No deviation.**

Re-derived 2026-07-28 on macOS 26.5.2 / arm64 (Darwin 25.5.0), Go 1.26.5, Node v26.0.0 —
against a release built on `ubuntu-latest` with Node 20. Every one of the five digests in
§3 matched:

| Asset | published (§3) | local rebuild | |
|---|---|---|---|
| `graphi-linux-amd64` | `f91aa839…99d25` | `f91aa839…99d25` | MATCH |
| `graphi-linux-arm64` | `f8fe332a…e9b124` | `f8fe332a…e9b124` | MATCH |
| `graphi-darwin-amd64` | `761643e4…983faa` | `761643e4…983faa` | MATCH |
| `graphi-darwin-arm64` | `9140e73f…47e152d` | `9140e73f…47e152d` | MATCH |
| `graphi-windows-amd64.exe` | `3a7e1701…5273ff` | `3a7e1701…5273ff` | MATCH |

The two facts SW-121 recorded as "worth writing down because either could plausibly have
broken" held again at this candidate: the Go cross-compile is host-independent under
`-trimpath` (a darwin/arm64 host produced bit-identical linux and windows binaries), and
the Vite web-UI bundle is byte-stable across Node majors — the embedded content-hashed
asset names `assets/index-B-Wcokm3.js` and `assets/index-Cg5Q50yJ.css` are identical
inside the Node-20-built published binary and the Node-26-built local one.

Two preconditions are load-bearing, and a reproduction attempt that skips either will
*fail* with a perfectly correct build. Both were satisfied deliberately and verified
before building:

1. **The checkout must carry no tags.** `actions/checkout@v4` fetches with `--no-tags`, so
   when the `build` job ran, `v0.7.0` did not yet exist in the workspace (the `publish`
   job pushes it afterwards). Go therefore stamped the module version as the
   pseudo-version `v0.0.0-20260728132620-5815db5b053c`, which is exactly what the
   published binaries carry. A clone *with* tags stamps `v0.7.0` instead and every digest
   differs. The rebuild used `git clone --no-tags …`; `git tag -l` in that clone returned
   **empty**.
2. **A linked `git worktree` stamps no VCS metadata at all.** Building in one yields
   `mod … (devel)` with no `vcs.*` settings and, again, different digests. The rebuild
   used a real clone — verified by `ls -ld .git` / `file .git` returning a **directory**,
   not the `gitdir:` pointer file a linked worktree has.

The `vcs.modified` asymmetry SW-121 documented is present at this candidate too, and was
reproduced exactly: `graphi-linux-amd64` (built first, while `dist/` is still empty and
therefore invisible to git) carries `vcs.modified=false` and a clean module version; the
four later targets see the untracked `dist/` directory and carry `vcs.modified=true` with
a `+dirty` module version. The bit-for-bit match across all five targets means the local
rebuild reproduced that asymmetry target for target — itself evidence that the
reproduction is faithful rather than coincidental. Making the matrix uniform would mean
writing `dist/` outside the work tree or ignoring it; that is a `release-dag.yml` change
and remains deliberately **out of scope** (no demonstrated failure forces it). It is
carried forward as a follow-up, unchanged from SW-121.

Not reproduced by the build command, by construction — unchanged in kind from the
superseded record:

- `sbom.spdx.json` — produced by `anchore/sbom-action` in a separate job; its digest is
  recorded and attested, but reproducing an SBOM generator's byte output is not a property
  this release claims.
- `SHA256SUMS` — the published file is the *complete* index written later by the publish
  job (binaries + SBOM + manifest), whereas the build job's `dist/SHA256SUMS` covers only
  the five binaries. The five binary lines are byte-identical between the two; the files
  differ only by the two extra lines.
- `capability-manifest.json` — copied from the tree, not built; verified byte-identical to
  `docs/capability-manifest.json` at `5815db5`.

## 8. Owner, date, sign-off

- **Owner:** `samibel` (Graphi Maintainer). The project is solo: ENG, EVAL, SEC and PROD in
  the PRD's role table are the same person. Per the spec's decision 3, a gate met by a solo
  substitute is labelled as such and never reported as the original.
- **Freeze date:** 2026-07-28. **Release date:** 2026-07-28.
- **Sign-off: PENDING.** SW-131 is a build; the human ship gate signs off. This record is
  written to be *checkable* before it is signed, not to assert an approval that has not
  happened. When the gate is passed, replace this line with the date and the name — do not
  back-date it.

## 9. Change-control and the STALE rule (PRD FR-1, execution plan §2.3)

The execution plan's rule stands verbatim and is inherited, not rewritten:

> *Der Candidate-SHA wird nach M0 nur über dokumentierte Blocker-Fixes bewegt. Jeder neue
> SHA invalidiert alle davon abhängigen Messungen und Artefakte.*

In force, for this and every future candidate — carried over unchanged from the superseded
record, because this move is an application of the rule, not an amendment to it:

1. **The candidate SHA moves only for a documented blocker fix**, and only onto a
   **published, tagged, attested** release. Convenience, drift and ordinary feature work
   are not grounds. "`main` has moved on" is never a reason. *This move's blocker is
   documented in §1 and is of the qualifying kind: the frozen artifact is unmeasurable by
   construction.*
2. **A move must be recorded before it takes effect** — in a successor to the prior file,
   naming the new SHA, the blocker that forced it, and the explicit list of measurements it
   invalidates. This file is that successor; §1 is that list.
3. **Every dependent evidence row is marked `STALE` in the same change.** Concretely, in
   `docs/rc/evidence-index.yaml`:
   - a row whose `current` or `evidence_uri` was stated or measured against the old
     candidate gets `status: STALE`;
   - its `current` must **name** the superseded candidate and the record that replaced it —
     `go run ./cmd/evidence -check` rejects a `STALE` row with an empty `current`, so the
     marking cannot be silent;
   - a row is **never** re-pointed at the new candidate by editing its SHA or URI in place.
     Re-pointing without re-measuring is the exact substitution P0 exists to remove;
   - `STALE`, like `UNKNOWN`, **counts as not passed** (plan §2.4). It exists to say *why*
     a row is not passed: measured, but against something else.
4. **Marking is manual and that is a known limitation.** The **automatic** candidate-
   staleness detector is P0-E and is deferred; until it exists, step 3 is a discipline
   enforced by review, and the only mechanical backstop is that a `STALE` row must explain
   itself. This move is the second candidate change performed under that manual
   discipline, which strengthens rather than weakens the case for P0-E.
5. **UNKNOWN is not a pass** (§2.4) — unchanged.

## 10. What this candidate move marks STALE, right now

As §1 records, **no** evidence row was `PASS` and **no** row carried an `evidence_uri` at
the time of the move, so nothing measured was lost. What existed was prose in the `current`
and `next_action` columns naming `fb3bf03` as the authoritative candidate, which would
have silently come to mean `5815db5`:

| Row | Status before | Status now | Why |
|---|---|---|---|
| **WP2** — Candidate & reproducible technical baseline | STALE (vs `4e72637`) | **STALE** (vs `fb3bf03`) | its `current` and `next_action` named `fb3bf03` / v0.6.7 as the candidate to baseline. Still never measured — on either candidate. Re-marked, **not** re-pointed. |
| **M0** — Wahrheit und Scope, exit gate | STALE (vs `4e72637`) | **STALE** (vs `fb3bf03`) | its `current` and `next_action` named `fb3bf03` as the candidate to hold the Go/No-Go against. The Day-10 M0 Go/No-Go was never held on any candidate. |

Neither row moves toward green. Both were already `STALE` from the `4e72637` → `fb3bf03`
move and remain `STALE`; what changes is **which** candidate superseded them and **which**
record says so. A row that has now been stale across two candidate moves is not thereby
closer to passing, and this record does not present it as such.

**WP0** is a deliberate non-STALE edit: its `current` cites the change-control record by
name, so that citation is updated to point here. It stays `UNKNOWN`, and its own text
already gives the reason — it describes a deliverable, not a measurement, so calling it
STALE would overstate what was lost.

Rows left `UNKNOWN` deliberately, so the choice is not silent: **WP4** and **M1** refer to
other *rows* ("blocked on WP2", "M0 not exited"), not to the candidate; **WP1, WP3,
WP5–WP10, M2–M5** never mentioned a candidate at all. They were never measured against
`fb3bf03`, so calling them STALE would overstate what was lost.

## 11. The product tree is byte-identical between the two candidates

This is the argument that the move is **sound**. It is stated with its limits attached,
because it is exactly the kind of claim that gets quietly promoted into something it is
not.

`git rev-parse <sha>:<path>` at both SHAs — re-run at the **real** candidate SHA
`5815db5`, not at any intermediate commit:

| Path | at `fb3bf03` | at `5815db5` | |
|---|---|---|---|
| `engine` | `00e960f44a2f1c55272366524fafd59f3c0c2e4f` | `00e960f44a2f1c55272366524fafd59f3c0c2e4f` | **IDENTICAL** |
| `core` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | `43d63ec270a8ee9e0a63b3b7ac890cc4f221f393` | **IDENTICAL** |
| `surfaces` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | `9948e9e06ee1496ef5bbb7bba1a4ddc10946ead1` | **IDENTICAL** |
| `cmd/graphi` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | `29cf42e4a0d786432d8b4c9bc707d3c8248dba01` | **IDENTICAL** |
| `go.mod` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | `d0284dab77505bd5d78a984bb120b3d09a9d9937` | **IDENTICAL** |
| `go.sum` | `5618e2fe9c770f39398bb16279591703a212f87e` | `5618e2fe9c770f39398bb16279591703a212f87e` | **IDENTICAL** |

A git tree hash is a recursive hash over the full subtree, so six equal hashes are a
complete statement about those six paths: **every byte of the product tree is the same at
both candidates.**

`git diff --name-only fb3bf03 5815db5`, reduced to top-level paths, differs in exactly:
`.github/workflows`, `CHANGELOG.md`, `cmd/corpus`, `cmd/eval`, `corpus/`, `docs/`,
`internal/corpus`, `internal/evalreport`, `internal/evidence` — the NFR-7 measurement-code
set and nothing else. No product path appears in that list.

**What this means:** the new candidate ships the *same product*. It differs only in
carrying the instruments that can measure it, plus corpus data, docs and CI. That is why
moving the candidate is safe rather than a fresh unknown.

**What this does NOT mean — and must never be presented as meaning:**

- It is **not a measurement.** It is a statement about source bytes, produced by `git`, not
  by running anything.
- It is **not evidence about performance.** No cold-index time, no query latency, no RSS,
  no DB size is implied. Identical source does not entail identical runtime behaviour on
  different hardware, and none of it has been observed on either candidate.
- It is **not evidence about accuracy.** No precision, recall, abstention or
  source-anchoring number follows from it.
- It is **not evidence for any §12.2 gate.** Every §12.2 gate remains `UNKNOWN`, and every
  affected evidence row remains `STALE` or `UNKNOWN` (§10). This section changes no row's
  status and is cited by none of them.

If a future document cites this section as support for a performance, accuracy or gate
claim, that citation is wrong on its face, and this paragraph is the reason.

## 12. Verification

Every fact above was re-derived on 2026-07-28 against the published artifact, not
transcribed from the workflow or copied from the superseded record:

```sh
# identity of the candidate
git rev-list -n 1 v0.7.0            # → 5815db5b053c2bb1bf3119cdb9939c1dea03cc45
git cat-file -p v0.7.0              # → annotated tag object at that SHA, tagger github-actions[bot]
git log -1 --format='%s %P' 5815db5 # → merge of PR #79; parents 8cf86b9 08e425b
gh release view v0.7.0 --json isDraft,isPrerelease,publishedAt,targetCommitish,url
                                    # → draft:false, prerelease:false, 2026-07-28T13:37:30Z

# every gate on that SHA
gh run list --commit 5815db5b053c2bb1bf3119cdb9939c1dea03cc45 --limit 40
                                    # → 15 runs, all conclusion=success, incl. release-dag + privacy-audit
gh run view 30363482852             # → 4/4 jobs success, push on main, headSha 5815db5

# digests, three independent ways
gh release download v0.7.0 --dir /tmp/v070
shasum -a 256 /tmp/v070/*           # → the digests in §3
cat /tmp/v070/SHA256SUMS            # → same seven lines
gh api repos/samibel/graphi/releases/tags/v0.7.0 --jq '.assets[] | "\(.name) \(.digest)"'
                                    # → same eight digests

# build metadata, read out of the published binary
go version -m /tmp/v070/graphi-linux-amd64
                                    # → go1.26.5, -tags=<22>, -trimpath=true, CGO_ENABLED=0,
                                    #   GOAMD64=v1, vcs.revision=5815db5…, vcs.time=2026-07-28T13:26:20Z,
                                    #   vcs.modified=false, mod v0.0.0-20260728132620-5815db5b053c

# attestation, with the superseded candidate as the negative control
for a in /tmp/v070/*; do
  gh attestation verify "$a" --repo samibel/graphi \
    --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
    --source-digest 5815db5b053c2bb1bf3119cdb9939c1dea03cc45
done                                # → exit 0 for all 8 assets
gh attestation verify /tmp/v070/graphi-linux-amd64 --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest fb3bf037e9b7fe05eda50514189caeff4c06679d
                                    # → exit 1 (negative control: the superseded candidate)

# the blocker, both mechanical reasons (§1)
git ls-tree fb3bf03 cmd/eval/ | wc -l    # → 11
git ls-tree 5815db5 cmd/eval/ | wc -l    # → 48
for f in coldseries querylatency incremental stalls rawexport; do
  git cat-file -e fb3bf03:cmd/eval/$f.go; done   # → all five ABSENT at fb3bf03, present at 5815db5
git show 5815db5:cmd/eval/main.go | grep -c 'flag\.String'   # → 13, none accepting a graphi binary

# product-tree byte-identity (§11)
for p in engine core surfaces cmd/graphi go.mod go.sum; do
  git rev-parse fb3bf03:$p; git rev-parse 5815db5:$p; done   # → pairwise equal, see §11

# reproducibility (§7) — tagless, real clone, NOT a linked worktree
git clone --no-tags https://github.com/samibel/graphi.git repro && cd repro
git checkout --detach 5815db5b053c2bb1bf3119cdb9939c1dea03cc45
git tag -l                          # → empty (precondition 1)
file .git                           # → directory, not a gitdir: pointer (precondition 2)
(cd web && npm ci && npm run build)
rm -rf surfaces/http/webui/dist && cp -R web/dist surfaces/http/webui/dist
CGO_ENABLED=0 go run ./cmd/release -dist dist -webui -version v0.7.0
shasum -a 256 dist/graphi-*         # → identical to the five binary digests in §3

# capability manifest identity
git show 5815db5:docs/capability-manifest.json > /tmp/cm.json
cmp /tmp/cm.json /tmp/v070/capability-manifest.json    # → exit 0
```

## 13. References

- Supersedes: [`docs/decisions/2026-07-p0-candidate-freeze.md`](2026-07-p0-candidate-freeze.md) (SW-121, candidate `fb3bf03`, v0.6.7)
- Which superseded: [`docs/decisions/2026-07-m0-candidate-freeze.md`](2026-07-m0-candidate-freeze.md) (SW-116, candidate `4e72637`, never published)
- Evidence index: `docs/rc/evidence-index.yaml` (source) → `docs/rc/evidence-index.md` (generated); `go run ./cmd/evidence -check`
- PRD: `docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md` — FR-1 (candidate freeze), FR-12 (evidence index, status values incl. `STALE`), NFR-7 (measurement code is not product code), §8.3, §12.2, §17
- Execution plan: `docs/plan/2026-07-graphi-9of10-execution-plan.md` §2.3 / §2.4 / §6 WP0
- Release DAG: `.github/workflows/release-dag.yml`; ADR 0005; publish lock `.github/publish-lock.json` (`locked: false`, untouched by this story)
- RC dossier: `docs/rc/focused-core-rc.md`
- Unblocks: SW-130 (P0 baseline measurement) — whose AC-1 is unchanged and unweakened by this record
