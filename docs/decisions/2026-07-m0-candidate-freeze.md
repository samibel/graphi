# Decision: the frozen M0 candidate — merge SHA + release digest (SW-116)

This is the record of the artifact every later measurement in the 9/10 program is bound
to. It exists so that "the candidate" is a versioned, retrievable fact rather than a
number quoted from memory: one SHA, one digest statement, and the rule that governs
moving it. If you are about to measure, benchmark, audit, or make a claim about graphi,
this file names what you are measuring.

**Status:** accepted · **Date:** 2026-07-16 · **Story:** SW-116 · **Risk:** high

## Context

M0 ("Wahrheit und Scope", `docs/plan/2026-07-graphi-9of10-execution-plan.md` §5, W0–2)
freezes the artifact before anything measures it. WP0's gate is explicit: *"Jede
9/10-Behauptung lässt sich auf ein versioniertes Rohartefakt zurückführen. Kein manuell
gepflegtes „grün" ohne Beleg."* This record is that versioned root.

The candidate is the **merge commit** of PR #55 on `main` — deliberately not the branch
head `e285822` the plan opens with (§ "Ausgangs-SHA"). `release-dag.yml` carries a single
`github.sha` through gate → build → SBOM → publish, so a candidate on a feature branch
cannot cleanly carry attestation, and merging would in any case invalidate a measurement
taken on the branch. Freezing the branch SHA was considered and rejected in shaping.

## Decision

The frozen M0 candidate is:

| Field | Value |
|---|---|
| **Candidate SHA** | `4e72637d3c2c0dc7d32142a590d46c0c62c10733` |
| **Short** | `4e72637` |
| **Kind** | true merge commit (two parents), not a squash |
| **Parents** | `65713de` (previous `main`) · `e285822` (PR #55 head) |
| **Subject** | `Merge pull request #55 from samibel/codex/fix-v050-review-blockers` |
| **Branch** | `main` (`origin/main` is at this commit) |
| **Merged** | 2026-07-16T12:23:40Z by `samibel` ([PR #55](https://github.com/samibel/graphi/pull/55)) |

## Release digest

The honest answer has two halves, and they must not be collapsed into one.

### 1. Published release digest: **UNKNOWN — none exists for the candidate**

There is **no published release, tag, attestation, or SBOM bound to `4e72637`.**

**Reason (observed, not inferred).** The `release-dag` run for the candidate
([run 29497965648](https://github.com/samibel/graphi/actions/runs/29497965648)) completed
with the gate **green** and every downstream job **skipped**:

| Job | Result |
|---|---|
| release gates on the exact SHA | success |
| reproducible build + release matrix (same SHA) | **skipped** |
| SBOM of the assembled release artifacts | **skipped** |
| attest + draft/verify/publish the exact gated SHA | **skipped** |

The gate emitted:

> `v0.5.0 is already published at 65713de33b33fe624c2f04f00a12c178bc267993; bump CHANGELOG before the next release`

`release-dag.yml`'s `build` job is conditioned on
`needs.gate.outputs.release_state != 'already-published'`. The publish version derives from
the first released `## [X.Y.Z]` header in `CHANGELOG.md`, which is still **v0.5.0** — and
v0.5.0 was already published on 2026-07-15 at `65713de`. So the DAG correctly declined to
rebuild or mutate a historical release on an ordinary `main` push. Nothing failed; there
was simply nothing to publish.

**This was not caused by the publish lock.** `.github/publish-lock.json` reads
`"locked": false` (RC-01 Go, 2026-07-15 — `docs/rc/focused-core-rc.md` §5), and the gate
logged `publish-lock: UNLOCKED`. Publication is open; the candidate merely carries no
unreleased version.

> **Do not borrow v0.5.0's digest.** v0.5.0 is published at `65713de`, which is the
> **first parent** of the candidate — an adjacent, *different* commit. Its asset checksums
> describe that commit's binaries, not the candidate's. Reading them as the candidate's
> digest is precisely the substitution this record exists to prevent.

**Next action.** A published, attested digest bound to the candidate can exist only once
`CHANGELOG.md` gains a new released version header and that lands on `main` — which cuts a
new release, i.e. a *new* SHA, and therefore a new candidate. Under §2.3 that is a
candidate move, not a free upgrade. Deciding whether the program needs a published digest
at all (versus the reproducible digest below) is an M1/WP2 question — see WP2's *"Candidate-SHA
und Release-Digest festlegen"* — and out of M0's scope.

Per plan §2.4 — *"UNKNOWN zählt als nicht bestanden"* — this UNKNOWN counts as **not
passed**, not as a pass pending paperwork. SW-119's dashboard must render it that way.

### 2. Reproducible build digest: **exists, and is the candidate's**

A deterministic build digest genuinely bound to `4e72637` does exist:

```
sha256=03f22af4682cba4323bef098ddb089594dc173046948f42c6aa15b819c3c92ab
```

**Provenance — re-derivable, not pasted:**

| | |
|---|---|
| Workflow | `release` (`.github/workflows/release.yml`) |
| Job | `reproducible static release binary` |
| Step | `reproducible release build (verify-only)` |
| Run | [29497965652](https://github.com/samibel/graphi/actions/runs/29497965652) (push, `4e72637`) |
| Job log | [job 87619397432](https://github.com/samibel/graphi/actions/runs/29497965652/job/87619397432) |
| Command | `go run ./cmd/release -version "main-4e72637d3c2c0dc7d32142a590d46c0c62c10733" -verify-only` |
| Emitted by | `cmd/release/main.go` — `release: reproducible default build verified (sha256=…)` |
| Conclusion | success, 2026-07-16 |

**Scope caveats — what this digest is not.** It is the **default flavor**, `linux/amd64`,
`CGO_ENABLED=0`, built **verify-only** (the binary is checked for byte-identical
reproducibility, then discarded — no artifact is uploaded). The version string
`main-4e72637d3c2c0dc7d32142a590d46c0c62c10733` is compiled in, so the digest is bound to
that exact string *and* SHA: a release build of the same tree under a `vX.Y.Z` version
string yields a **different** digest. It carries no attestation and no SBOM. It is
therefore a reproducibility proof for the candidate — not a release-asset checksum, and
not comparable to one.

To re-derive locally:

```sh
git checkout 4e72637d3c2c0dc7d32142a590d46c0c62c10733
CGO_ENABLED=0 go run ./cmd/release \
  -version "main-4e72637d3c2c0dc7d32142a590d46c0c62c10733" -verify-only
```

## Change-control rule (plan §2.3 / WP0)

Verbatim, §2.3:

> *Der Candidate-SHA wird nach M0 nur über dokumentierte Blocker-Fixes bewegt. Jeder neue
> SHA invalidiert alle davon abhängigen Messungen und Artefakte.*

and WP0's deliverable:

> *Change-Control-Regel: Candidate-Wechsel nur bei PRD-Blocker, mit expliziter Liste aller
> zu wiederholenden Messungen.*

In force, that means:

1. **The candidate SHA moves only for a documented blocker fix.** Convenience, drift,
   "while we're here" merges, and ordinary feature work are not grounds. No blocker, no
   move.
2. **Every move must list the measurements it invalidates** — explicitly, in the move's
   own record, before the move. Every measurement bound to the old SHA is invalid until
   re-run on the new one; a new SHA invalidates *all* dependent measurements and
   artifacts, not only the ones that look related.
3. **Every move is recorded here** (or in a successor record that supersedes this one),
   with the new SHA, the blocker that forced it, and that invalidation list. This file is
   the decision log for candidate changes that WP0 requires.
4. **UNKNOWN is not a pass** (§2.4). A digest, gate, or measurement that does not exist is
   reported as UNKNOWN and counts as not passed — never as blank, green, pending, or
   approximated from an adjacent commit.

## Verification

Every fact above was verified against `origin/main` and the GitHub API on 2026-07-16, not
transcribed:

```sh
git cat-file -e 4e72637d3c2c0dc7d32142a590d46c0c62c10733            # → exists
git merge-base --is-ancestor 4e72637d3c2c0dc7d32142a590d46c0c62c10733 origin/main
                                                                     # → on the trunk
git rev-list --parents -n 1 4e72637d3c2c0dc7d32142a590d46c0c62c10733 # → two parents (merge)
gh pr view 55 --json state,mergeCommit                               # → MERGED, oid 4e72637…
git rev-list -n 1 v0.5.0                                             # → 65713de… (NOT the candidate)
git tag --points-at 4e72637d3c2c0dc7d32142a590d46c0c62c10733         # → (empty: no tag)
gh run view 29497965648 --json jobs                                  # → build/sbom/publish skipped
```

## References

- RC dossier: `docs/rc/focused-core-rc.md` (§5 records the RC-01 Go and the lock handle)
- Release DAG: `.github/workflows/release-dag.yml`; CI-only build: `.github/workflows/release.yml`
- Publish lock: `.github/publish-lock.json`
- Spec: M0 — Freeze Truth and Scope (SW-115…SW-119); the gate dashboard (SW-119) cites this record
