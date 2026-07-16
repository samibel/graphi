# ADR 0005 — The Commit-Bound Release DAG (SW-120 / REL-01)

- Status: Accepted (implemented in `.github/workflows/release-dag.yml`)
- Date: 2026-07-14
- Story: SW-120 — REL-01: SHA-bound release DAG, pins, SBOM, attestation
- Spec / Gate: master WBS `REL-01`; exit evidence "Gate→Build→Provenance→
  Publish auf demselben SHA; Red-Gate-Test"
- Depends on: SW-109 (CT-01 publish lock), SW-117 (CAP-01 capability manifest —
  the "Manifest-Join vor RC")
- Feeds: `RC-01` (lifting the lock is the documented Go step)

## Problem

Publication was a decoupled chain: `auto-release.yml` listened for the
**`release`** workflow's completion (`workflow_run`), tagged the newest
CHANGELOG version, and dispatched `release.yml` against **`main`**. Three
structural faults:

1. **The wrong gate.** The chain triggered on `release` (build/test/repro) —
   `release-gate.yml`, the actual release gate, was a separate workflow whose
   failure could not prevent tagging.
2. **No SHA binding.** The gates that happened to be green ran in other
   events/runs; nothing bound "assets uploaded" to "gates green on that exact
   commit", and the dispatch built from a ref, not a pinned SHA.
3. **No supply-chain artifacts.** No SBOM, no provenance, tag-pinned actions.

## Decision

One workflow — `release-dag.yml` — carries `github.sha` through four jobs:

```text
gate (publish-lock → layerguard → one authoritative release-gate [testgate +
      coverage/manifest + privacy + benchmark + eval + UX] → scorecard →
      CHANGELOG version → remote-integrity probe)
  → build (grammar-subset gate → deterministic web build → reproducibility
           verification of the exact web-embedded release flavor/version →
           web-embedded matrix)
    → sbom (SPDX of release binaries + capability manifest → artifact)
      → publish (assemble + verify complete checksummed set → attest every
                 release asset + verify provenance → verify/push tag AT
                 github.sha → create/resume draft with --verify-tag →
                 upload + byte-verify → publish + re-verify → Homebrew/Scoop)
```

- **Red gate ⇒ no tag, no release**: `publish` `needs:` every prior stage;
  GitHub skips needs-failed dependents. Statically proven by
  `cmd/publish-lock/workflow_test.go` (the act-free red-gate test), together
  with: every checkout pins `ref: github.sha`, the tag is created explicitly
  at that SHA, and a repo-wide scan asserts the DAG is the ONLY workflow that
  pushes tags, creates releases, or dispatches workflows.
- **Protected-main pushes are the only publication trigger**: the privileged
  DAG has no `workflow_dispatch` entry point, and every job independently
  requires a push event on `refs/heads/main`. A maintainer therefore cannot
  publish a side-branch SHA by manually dispatching the workflow. The CT-01
  lock additionally conditions the publish job and fails closed. While
  engaged there is NO publish path at all; the former side-channel (manually
  dispatching `release.yml` with a tag input) is gone. An emergency pre-RC
  publish requires one reviewed commit flipping `.github/publish-lock.json`
  — auditable, and exactly the RC-01 Go step.
- **Manifest join**: the gate runs `coverage -check` (matrix = live set =
  stable tier = generated `capability-manifest.json`), and the manifest ships
  as a release asset.
- **One authoritative verdict**: `cmd/release-gate` owns testgate, coverage,
  privacy, benchmark-budget, eval, and UX. The DAG does not run those noisy
  measurements a second time and risk contradictory release decisions;
  `layerguard` remains the independent structural prerequisite.
- **Vulnerability gates run on the publishing SHA**: the privileged `gate` job
  itself runs pinned `govulncheck` over every first-party Go package and
  high/critical production npm audits for both lockfiles. The separate
  `dependency-security.yml` workflow provides earlier PR and scheduled signal,
  but can never authorize a release. A scanner, advisory-registry, or audit
  failure inside the release DAG fails closed before version resolution.
- **Existence is not completeness**: an existing tag is resolved through its
  peeled commit and must equal the gated `github.sha`. A published release is
  complete only when its asset names exactly match `cmd/release
  -list-release-assets` and the complete `SHA256SUMS` verifies every binary,
  the SPDX SBOM, and the capability manifest. Draft/tag-only states are
  resumable. A published release is immutable by policy: missing tags,
  unexpected bytes, missing assets, or invalid provenance fail closed and
  require an explicit manual incident repair. Automation never unpublishes or
  overwrites it. A fully valid release may re-enter only for read-only
  verification and downstream-packaging recovery; it is not re-attested.
- **A historical release is not a new candidate**: the most recent released
  CHANGELOG header remains visible after publication. On later `main` pushes,
  a published tag on its original SHA yields `already-published` and skips the
  build/publish chain until CHANGELOG names a new version. This skip is allowed
  only when the first parent already named that version and the exact asset
  contract, checksums, and attestations verify against the peeled tag SHA and
  `release-dag.yml` identity. A tag collision for a version introduced by the
  candidate fails closed. Drafts are discovered
  through the release-list endpoint (the by-tag endpoint omits drafts), so an
  interrupted first run is resumed instead of colliding with a second draft.
- **The v0.5.0 historical contract is frozen, not silently upgraded.** That
  already-published release has exactly eight assets, but its original
  `SHA256SUMS` covers only the five binaries and its original release-DAG
  attested those five plus `SHA256SUMS`. Historical verification accepts that
  exact tag-specific contract and nothing broader. Its unattested SBOM and
  capability manifest are presence-checked but never treated as trusted
  provenance or reused for a new release. Every later release is
  self-describing: its complete, attested `SHA256SUMS` names every other asset,
  and every named asset plus the checksum file must carry provenance bound to
  the peeled tag SHA and this workflow. Historical verification therefore does
  not apply today's platform matrix or asset count retroactively. The canonical
  two-space `SHA256SUMS` format is consequently a frozen compatibility surface;
  any future format change must add a versioned verifier before publishing it.
- **Provenance and exact tag before any release creation**: the complete local
  asset contract and checksums verify first; every asset is then attested and
  that provenance is identity- and `github.sha`-verified. Only after those
  checks pass may the workflow create or verify/push the tag, which must peel
  to the gated SHA. `gh release create` always uses `--verify-tag`, so it cannot
  silently create a tag before those controls. The workflow then creates or
  resumes a draft, replaces its assets, and downloads them for byte comparison
  before the public `draft=false` transition. The tag peel and draft state are
  revalidated immediately before that transition, preventing a tag move during
  upload from becoming public first and failing only afterward. An interruption
  after tagging leaves a resumable tag-only state; one after draft creation
  leaves a resumable draft; a failure after publication can resume packaging
  without recreating the release.
- **The reproducibility proof matches what ships**: the web UI is built first,
  and `cmd/release -webui -verify-only` uses the same version and build-tag
  config as the cross-compiled matrix. `cmd/release` shares one flavor config
  across double-build verification, host output, and matrix output.
- **`release.yml` is CI-only** (tags trigger, dispatch inputs, and the
  `release-assets` job removed); `auto-release.yml` is deleted;
  `release-gate.yml` stays as the fast PR check, and the DAG re-runs the same
  gate binary itself on the SHA it publishes.
- **Artifact SBOM + complete attestation fail closed**: the SPDX input is the
  assembled release payload (binaries plus gated capability manifest), not the
  source checkout. Provenance subjects are the exact public files: all
  binaries, complete checksum index, SBOM, and capability manifest. If the
  repository/plan cannot attest or verify them, the run creates neither a new
  tag nor a new draft; a pre-existing resumable tag-only/draft state is left
  unchanged.

## Explicit follow-ups (UNKNOWNs)

- **U1 — full-SHA action pins. RESOLVED (SW-124, 2026-07-15).** Every remote
  `uses:` in every repository workflow is pinned to a 40-hex commit SHA; the
  release DAG additionally requires a trailing `# <tag>` audit comment. Refs
  were resolved from live remotes via `git ls-remote`, using peeled commits
  for annotated tags. `TestEveryWorkflowActionIsSHAPinned` enforces the
  repository-wide invariant, while `TestReleaseDAG_EveryActionIsSHAPinned`
  enforces the stricter release-DAG form. The read-only
  `dependency-security.yml` workflow adds PR dependency-diff review, pinned
  `govulncheck`, and production npm audits. The release DAG repeats the
  vulnerability checks on its exact publishing SHA so independently scheduled
  workflow state is never trusted. Bumping any action requires resolving and
  reviewing its new immutable SHA in the same diff.
- **U2 — environment protection.** GitHub "environments" with required
  reviewers on the `publish` job are repo settings, not committed files;
  configure once in the repo UI and note it in the RC-01 checklist.

## Consequences

- A release can only exist for a commit whose full gate suite ran green in
  the same workflow run; the peeled tag, reproducible web-embedded binaries,
  complete checksum set, artifact SBOM, provenance and capability manifest
  all bind to that one SHA.
- Publishing stays impossible until RC-01 flips the lock — after which a
  main-merge with an unpublished CHANGELOG version publishes automatically
  through the full DAG.
