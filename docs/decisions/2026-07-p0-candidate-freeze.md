# Decision: the frozen P0 candidate — tagged release v0.6.7 at `fb3bf03` (SW-121)

This is the artifact every P0 measurement is bound to. It supersedes
[`2026-07-m0-candidate-freeze.md`](2026-07-m0-candidate-freeze.md), whose candidate
`4e72637` was never published and whose release digest therefore reads `UNKNOWN`.
If you are about to measure, benchmark, audit, or make a claim about graphi under
P0, this file names what you are measuring — and, just as importantly, what it does
**not** contain.

**Status:** accepted · **Date:** 2026-07-28 · **Story:** SW-121 · **Risk:** high ·
**Sign-off:** PENDING (see §8)

---

## 1. Context

`docs/decisions/2026-07-m0-candidate-freeze.md` froze `4e72637` on 2026-07-16. By
2026-07-27 `main` was **99 commits and 8 tags** further on (v0.5.1 … v0.6.6), and the
frozen candidate's published release digest still read `UNKNOWN` — for a plain reason
recorded in that file: **nothing was ever published from that SHA**. Measuring it
would have proven the quality of an artifact nobody installs, violating PRD §8.3
("one candidate, one truth") and §17 ("candidate drift → NO-GO") before P0 began.

One premise of SW-121 as written was already out of date when the story was built.
It said a release still "needs to be *cut*". It does not: **v0.6.7 was published on
2026-07-27T15:14:34Z from `fb3bf03`**, by run
[30278205371](https://github.com/samibel/graphi/actions/runs/30278205371) of
`release-dag.yml`, before this story started. This record therefore **freezes the P0
candidate on that existing release**. No release was cut, no tag pushed, no
CHANGELOG version bumped and no publish performed by SW-121; every fact below is read
back from public, immutable evidence and re-verified locally (§11).

## 2. Decision — the frozen P0 candidate (PRD FR-1 field set)

| FR-1 field | Value |
|---|---|
| **Git commit SHA** | `fb3bf037e9b7fe05eda50514189caeff4c06679d` |
| **Short** | `fb3bf03` |
| **Kind** | true merge commit — `Merge pull request #76 from samibel/claude/graphi-index-analysis-h5tfvu` |
| **Branch** | `main` (`origin/main` points at this commit) |
| **Tag** | `v0.6.7` — annotated tag object, peels to the SHA above; tagger `github-actions[bot]`, message `Release v0.6.7 (release-dag, gated SHA fb3bf037e9b7fe05eda50514189caeff4c06679d)` |
| **Release version** | **0.6.7** — [github.com/samibel/graphi/releases/tag/v0.6.7](https://github.com/samibel/graphi/releases/tag/v0.6.7), published 2026-07-27T15:14:34Z, `draft: false`, `prerelease: false` |
| **Binary digest** | see §3 — eight asset digests, **not** `UNKNOWN`. Reference platform `graphi-linux-amd64` = `sha256:6fd561c27a728a8c4085afe80e9b592c2f3019a27ccfb1861d6cb13d29b21b94` |
| **Build command** | see §4 |
| **Go version** | **go1.26.5** (`go.mod` `go 1.26.5`; the workflow uses `go-version-file: go.mod`; confirmed by reading the published binary with `go version -m`) |
| **`CGO_ENABLED`** | **0** (job-level `env: CGO_ENABLED: "0"`; confirmed as `build CGO_ENABLED=0` inside the published binary) |
| **Build tags** | `grammar_subset`, `grammar_subset_{typescript,javascript,tsx,python,java,c,ruby,rust,php,c_sharp,kotlin,cpp,bash,sql,lua,css,yaml,toml,markdown,hcl}`, `webui_embed` — 22 tags, confirmed as `build -tags=…` inside the published binary |
| **Target platforms** | `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, `windows/amd64` — `internal/release.ReleaseTargets` order; `GOAMD64=v1` on the amd64 members |
| **SBOM reference** | §5 |
| **Attestation reference** | §6 |
| **Release date** | 2026-07-27 (publish 15:14:34Z; commit time 15:04:22Z) |
| **Freeze date** | 2026-07-28 |
| **Owner** | `samibel` — Graphi Maintainer (solo; roles ENG/EVAL/SEC/PROD are the same person, per the PRD's solo-substitution rule) |
| **Sign-off** | **PENDING** — see §8. This record does not claim a sign-off it does not have. |

### What this candidate contains — and what it does not

The candidate is `fb3bf03`, **not** the current local `main`. Two stories have since
been merged locally and are **not in the candidate**:

| Story | In `fb3bf03`? | Effect on the shipped `graphi` binary |
|---|---|---|
| SW-120 — commit the P0 PRD under the plan's authority | **no** | none — three documentation files only (`docs/README.md`, `docs/plan/2026-07-graphi-9of10-execution-plan.md`, `docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md`) |
| SW-122 — Go corpus manifest v3 (six pinned Go repos, stress target) | **no** | none — corpus data (`corpus/manifest.json`, `corpus/README.md`), its loader and harness (`internal/corpus`, `cmd/corpus`), two CI workflows and `CHANGELOG.md`; measurement code, never product code (NFR-7) |

Neither changes `engine/`, `core/`, `surfaces/` or `cmd/graphi`, so the *measurable
artifact* is unaffected. That is a reason the freeze is sound, **not** a licence to
say "the candidate is main". It is not. Any P0 measurement that needs the SW-122
corpus runs the SW-122 harness **against the `fb3bf03` binary**; it does not
re-point the candidate at a newer SHA.

## 3. Digests — the complete published asset set

Read from the release's own `SHA256SUMS`, independently recomputed from the
downloaded assets, and cross-checked against the GitHub API's per-asset digest. All
three agree.

| Asset | sha256 |
|---|---|
| `graphi-linux-amd64` | `6fd561c27a728a8c4085afe80e9b592c2f3019a27ccfb1861d6cb13d29b21b94` |
| `graphi-linux-arm64` | `a76415e35303cb8a953b17d344d561e9c45b7ace7758d88026728ec5a703f0b9` |
| `graphi-darwin-amd64` | `fefed6a6654afd127ec8f4f0ab1c1eac0d58bdc8fd50f1fa0b9578d17626994f` |
| `graphi-darwin-arm64` | `6341f812bfd44eee1b7d02b7bf61729a7ef7ac10f3b60454c5cef1b7481cb093` |
| `graphi-windows-amd64.exe` | `323f5515046db13fa303ca5ec66ff508608fc27312dd56150970bcb7deb8e579` |
| `sbom.spdx.json` | `bf93922359d345e80c8e878c9489796e8d25738a4951586cde61b8607aedd759` |
| `capability-manifest.json` | `fdcdd44893429740b70706853c786ec5b1335eefe337a4c32a255b12618da2ec` |
| `SHA256SUMS` (itself) | `b59d001d177d0d4bff2c35273baf5f5afb74b65b88d0ccebe27fb91e71ea14a6` |

`docs/rc/evidence-index.yaml` carries **one** of these in its `candidate.release_digest`
scalar — `graphi-linux-amd64`, the reference platform — because the index's schema has
one digest field. This table is the full statement; the index cites this record.

`capability-manifest.json` published as an asset is byte-identical to
`docs/capability-manifest.json` at `fb3bf03`, so the release's machine-readable
capability claim is the one CI gated (`cmd/coverage -check`).

## 4. Build command

The release runner's exact sequence (`.github/workflows/release-dag.yml`, job
*reproducible build + release matrix (same SHA)*, job-level `env: CGO_ENABLED: "0"`,
`runs-on: ubuntu-latest`):

```sh
# checkout of the exact gated SHA, WITHOUT tags (actions/checkout@v4 default)
git clone --no-tags https://github.com/samibel/graphi.git && cd graphi
git checkout --detach fb3bf037e9b7fe05eda50514189caeff4c06679d

# released binaries embed the web UI
(cd web && npm ci && npm run build)
rm -rf surfaces/http/webui/dist
cp -R web/dist surfaces/http/webui/dist

# the recorded release build command
CGO_ENABLED=0 go run ./cmd/release -dist dist -webui -version v0.6.7
```

`cmd/release` first proves the flavor reproducible (two builds, byte-compared), then
cross-compiles the matrix. Per target it runs exactly
(`internal/release.CanonicalBuildArgs`, contract `internal/release.CanonicalBuildArgs/v1`):

```sh
CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build \
  -trimpath -buildvcs=true \
  -tags "grammar_subset grammar_subset_typescript … webui_embed" \
  -ldflags "-X github.com/samibel/graphi/internal/version.Version=v0.6.7" \
  -o dist/graphi-<os>-<arch> ./cmd/graphi/
```

## 5. SBOM reference

- Release asset: <https://github.com/samibel/graphi/releases/download/v0.6.7/sbom.spdx.json>
  (`sha256:bf93922359d345e80c8e878c9489796e8d25738a4951586cde61b8607aedd759`)
- Produced by the `sbom` job of run
  [30278205371](https://github.com/samibel/graphi/actions/runs/30278205371) — SPDX-JSON
  via `anchore/sbom-action`, over the assembled release binaries plus the capability
  manifest; workflow artifact `sbom-fb3bf037e9b7fe05eda50514189caeff4c06679d`
- Scope caveat: the SBOM describes the *assembled release assets*, not the source
  tree. It is attested (§6) like every other asset.

## 6. Attestation reference

- Attestations index: <https://github.com/samibel/graphi/attestations>
- API entry for the reference binary:
  `GET /repos/samibel/graphi/attestations/sha256:6fd561c27a728a8c4085afe80e9b592c2f3019a27ccfb1861d6cb13d29b21b94`
  (returns 1 attestation)
- Produced by the `publish` job of run
  [30278205371](https://github.com/samibel/graphi/actions/runs/30278205371) via
  `actions/attest-build-provenance` over `release-assets/*`

SLSA v1 provenance predicate, read back from the signing certificate:

| Property | Value |
|---|---|
| Builder ID | `https://github.com/samibel/graphi/.github/workflows/release-dag.yml@refs/heads/main` |
| Run invocation | `https://github.com/samibel/graphi/actions/runs/30278205371/attempts/1` |
| Source repository | `https://github.com/samibel/graphi` |
| **Source repository digest** | `fb3bf037e9b7fe05eda50514189caeff4c06679d` |
| Runner environment | `github-hosted` |
| OIDC issuer | `https://token.actions.githubusercontent.com` |

All **eight** assets verify against that workflow identity and that source digest
(`gh attestation verify`, exit 0 each). The same command with the *old* candidate
`4e72637` as `--source-digest` exits 1 — the binding is checked, not assumed.

### Why AC-1 is satisfied without publishing anything

AC-1 asks that the release be published from a green `main` SHA through
`release-dag.yml` with every gate job on that same SHA. That is a property of run
30278205371, evidenced three ways rather than by performing a second publish:

1. **The DAG's shape makes it unconditional.** `publish` `needs: [gate, build, sbom]`,
   and every job checks out `ref: ${{ github.sha }}`. GitHub skips a job whose
   dependency failed, so a published release *is* proof that gate, build and sbom all
   succeeded on that one SHA. The invariant is asserted statically by
   `cmd/publish-lock/workflow_test.go`.
2. **The run is green on that SHA.** All four jobs of run 30278205371
   (`headSha = fb3bf03`, event `push`, branch `main`) concluded `success`.
3. **The attestation binds it cryptographically.** Builder identity *is*
   `release-dag.yml@refs/heads/main`, `sourceRepositoryDigest` *is* `fb3bf03`,
   `runnerEnvironment` *is* `github-hosted` — none of which a local build could forge.

## 7. Reproducibility (PRD FR-1: "Ein Release-Binary kann aus dem Candidate reproduzierbar neu gebaut werden")

**Result: all five published platform binaries were rebuilt bit-for-bit from
`fb3bf03` on a different machine and a different host OS.**

Re-derived 2026-07-28 on macOS 26.5.2 / arm64 (Darwin 25.5.0), Go 1.26.5, Node v26.0.0 —
against a release built on `ubuntu-latest` with Node 20. Every one of the five digests
in §3 matched. The rebuild was performed **twice, from two independent fresh clones**,
and both runs produced the same five digests — so the match is a property of the frozen
SHA, not of one lucky working directory.

Two independent facts follow, and both are worth writing down because either could
plausibly have broken: the Go cross-compile is host-independent under `-trimpath`
(a darwin/arm64 host produced bit-identical linux and windows binaries), and the Vite
web-UI bundle is byte-stable across Node majors — the embedded asset names
`assets/index-B-Wcokm3.js` and `assets/index-Cg5Q50yJ.css` are *content* hashes, and
they are identical inside the Node-20-built published binary and the Node-26-built
local one.

Two preconditions are load-bearing, and a reproduction attempt that skips either will
*fail* with a perfectly correct build. They are recorded because a silent mismatch
would otherwise look like non-reproducibility:

1. **The checkout must carry no tags.** `actions/checkout@v4` fetches with `--no-tags`,
   so when the `build` job ran, `v0.6.7` did not yet exist in the workspace (the
   `publish` job pushes it afterwards). Go therefore stamped the module version as the
   pseudo-version `v0.0.0-20260727150422-fb3bf037e9b7`. A clone *with* tags stamps
   `v0.6.7` instead and every digest differs. Use `git clone --no-tags`.
2. **A linked `git worktree` stamps no VCS metadata at all.** Building in one yields
   `mod … (devel)` with no `vcs.*` settings and, again, different digests. Use a real
   clone.

A third observation is recorded because it looks like a defect and is not one:
`vcs.modified` differs *between targets of the same release*. `graphi-linux-amd64`
(built first, while `dist/` is still empty and therefore invisible to git) carries
`vcs.modified=false`; the four later targets see the untracked `dist/` directory and
carry `vcs.modified=true` with a `+dirty` module version. **The local rebuild
reproduced that asymmetry exactly, target for target** — which is itself evidence that
the reproduction is faithful rather than coincidental. Making the matrix uniform would
mean writing `dist/` outside the work tree or ignoring it; that is a `release-dag.yml`
change and is deliberately **out of scope** here (no demonstrated failure forces it).
It is left as a follow-up.

Not reproduced by the build command, by construction:

- `sbom.spdx.json` — produced by `anchore/sbom-action` in a separate job; its digest
  is recorded and attested, but reproducing an SBOM generator's byte output is not a
  property this release claims.
- `SHA256SUMS` — the published file is the *complete* index written later by
  `cmd/release -write-release-sums` (binaries + SBOM + manifest), whereas the build
  job's `dist/SHA256SUMS` covers only the five binaries. The five binary lines are
  byte-identical between the two; the files differ only by the two extra lines.
- `capability-manifest.json` — copied from the tree, not built; verified byte-identical
  to `docs/capability-manifest.json` at `fb3bf03`.

## 8. Owner, date, sign-off

- **Owner:** `samibel` (Graphi Maintainer). The project is solo: ENG, EVAL, SEC and
  PROD in the PRD's role table are the same person. Per the spec's decision 3, a gate
  met by a solo substitute is labelled as such and never reported as the original.
- **Freeze date:** 2026-07-28. **Release date:** 2026-07-27.
- **Sign-off: PENDING.** SW-121 is a build; the human ship gate signs off. This record
  is written to be *checkable* before it is signed, not to assert an approval that has
  not happened. When the gate is passed, replace this line with the date and the name
  — do not back-date it.

## 9. Change-control and the STALE rule (PRD FR-1, execution plan §2.3)

The execution plan's rule stands verbatim and is inherited, not rewritten:

> *Der Candidate-SHA wird nach M0 nur über dokumentierte Blocker-Fixes bewegt. Jeder
> neue SHA invalidiert alle davon abhängigen Messungen und Artefakte.*

In force, for this and every future candidate:

1. **The candidate SHA moves only for a documented blocker fix**, and only onto a
   **published, tagged, attested** release. Convenience, drift and ordinary feature
   work are not grounds. "`main` has moved on" is never a reason.
2. **A move must be recorded before it takes effect** — in a successor to this file,
   naming the new SHA, the blocker that forced it, and the explicit list of
   measurements it invalidates. This file is that decision log.
3. **Every dependent evidence row is marked `STALE` in the same change.** Concretely,
   in `docs/rc/evidence-index.yaml`:
   - a row whose `current` or `evidence_uri` was stated or measured against the old
     candidate gets `status: STALE`;
   - its `current` must **name** the superseded candidate and the record that replaced
     it — `go run ./cmd/evidence -check` rejects a `STALE` row with an empty `current`,
     so the marking cannot be silent;
   - a row is **never** re-pointed at the new candidate by editing its SHA or URI in
     place. Re-pointing without re-measuring is the exact substitution P0 exists to
     remove;
   - `STALE`, like `UNKNOWN`, **counts as not passed** (plan §2.4). It exists to say
     *why* a row is not passed: measured, but against something else.
4. **Marking is manual and that is a known limitation.** The **automatic** candidate-
   staleness detector is P0-E and is deferred; until it exists, step 3 is a discipline
   enforced by review, and the only mechanical backstop is that a `STALE` row must
   explain itself.
5. **UNKNOWN is not a pass** (§2.4) — unchanged from the superseded record.

## 10. What this candidate move marks STALE, right now

At the time of the move **no** evidence row was `PASS`, and **no** row carried an
`evidence_uri` — so nothing measured was lost. What existed was prose in the `current`
column that spoke about "the candidate", and would have silently come to mean the new
one:

| Row | Why it is STALE |
|---|---|
| **WP2** — Candidate & reproducible technical baseline | its `current` ("no eval-full baseline has been run on the candidate SHA / release binary") was stated about `4e72637` |
| **M0** — Wahrheit und Scope, exit gate | its `current` cited "Candidate frozen (SW-116)", i.e. the superseded candidate |

Rows left `UNKNOWN` deliberately, so the choice is not silent: **WP4** and **M1**
refer to other *rows* ("blocked on WP2", "M0 not exited"), not to the candidate;
**WP0, WP1, WP3, WP5–WP10, M2–M5** never mentioned a candidate at all. They were
never measured against `4e72637`, so calling them STALE would overstate what was lost.

## 11. Verification

Every fact above was re-derived on 2026-07-28, not transcribed:

```sh
git rev-list -n 1 v0.6.7                       # → fb3bf037e9b7fe05eda50514189caeff4c06679d
git cat-file -p v0.6.7                         # → annotated tag object at that SHA
gh release view v0.6.7 --json isDraft,publishedAt,targetCommitish,assets
gh run view 30278205371                        # → 4/4 jobs success, push on main, headSha fb3bf03
gh release download v0.6.7 --dir /tmp/v067
shasum -a 256 /tmp/v067/*                      # → the digests in §3
go version -m /tmp/v067/graphi-linux-amd64     # → go1.26.5, -tags=…, -trimpath, CGO_ENABLED=0,
                                               #   vcs.revision=fb3bf03…, vcs.modified=false
for a in /tmp/v067/*; do
  gh attestation verify "$a" --repo samibel/graphi \
    --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
    --source-digest fb3bf037e9b7fe05eda50514189caeff4c06679d
done                                           # → exit 0 for all 8 assets
gh attestation verify /tmp/v067/graphi-linux-amd64 --repo samibel/graphi \
  --signer-workflow samibel/graphi/.github/workflows/release-dag.yml \
  --source-digest 4e72637d3c2c0dc7d32142a590d46c0c62c10733
                                               # → exit 1 (negative control)
# reproducibility, §7
git clone --no-tags https://github.com/samibel/graphi.git repro && cd repro
git checkout --detach fb3bf037e9b7fe05eda50514189caeff4c06679d
(cd web && npm ci && npm run build)
rm -rf surfaces/http/webui/dist && cp -R web/dist surfaces/http/webui/dist
CGO_ENABLED=0 go run ./cmd/release -dist dist -webui -version v0.6.7
shasum -a 256 dist/graphi-*                    # → identical to the five binary digests in §3
```

## 12. References

- Supersedes: [`docs/decisions/2026-07-m0-candidate-freeze.md`](2026-07-m0-candidate-freeze.md) (SW-116, candidate `4e72637`)
- Evidence index: `docs/rc/evidence-index.yaml` (source) → `docs/rc/evidence-index.md` (generated); `go run ./cmd/evidence -check`
- PRD: `docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md` — FR-1 (candidate freeze), FR-12 (evidence index, status values incl. `STALE`), §8.3, §17
- Execution plan: `docs/plan/2026-07-graphi-9of10-execution-plan.md` §2.3 / §2.4 / §6 WP0
- Release DAG: `.github/workflows/release-dag.yml`; ADR 0005; publish lock `.github/publish-lock.json` (`locked: false`, untouched by this story)
- RC dossier: `docs/rc/focused-core-rc.md`
