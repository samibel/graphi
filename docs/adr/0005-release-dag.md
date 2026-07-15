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
gate (publish-lock → testgate full suite → layerguard → coverage/manifest →
      release-gate + scorecard → CHANGELOG version → idempotency probe)
  → build (repro verify → grammar-subset gate → web-embedded matrix +
           SHA256SUMS → artifact)
    → sbom (SPDX of the gated tree → artifact)
      → publish (attest-build-provenance over dist/* → tag AT github.sha →
                 gh release + assets [matrix, SBOM, capability manifest] →
                 Homebrew/Scoop)
```

- **Red gate ⇒ no tag, no release**: `publish` `needs:` every prior stage;
  GitHub skips needs-failed dependents. Statically proven by
  `cmd/publish-lock/workflow_test.go` (the act-free red-gate test), together
  with: every checkout pins `ref: github.sha`, the tag is created explicitly
  at that SHA, and a repo-wide scan asserts the DAG is the ONLY workflow that
  pushes tags, creates releases, or dispatches workflows.
- **The CT-01 lock got stricter**: it now conditions the publish job on every
  trigger, including `workflow_dispatch`. While engaged there is NO publish
  path at all; the former side-channel (manually dispatching `release.yml`
  with a tag input) is gone. An emergency pre-RC publish requires one
  reviewed commit flipping `.github/publish-lock.json` — auditable, and
  exactly the RC-01 Go step.
- **Manifest join**: the gate runs `coverage -check` (matrix = live set =
  stable tier = generated `capability-manifest.json`), and the manifest ships
  as a release asset.
- **`release.yml` is CI-only** (tags trigger, dispatch inputs, and the
  `release-assets` job removed); `auto-release.yml` is deleted;
  `release-gate.yml` stays as the fast PR check, and the DAG re-runs the same
  gate binary itself on the SHA it publishes.
- **Attestation fails closed**: if the repository/plan cannot produce
  attestations, the publish job fails loudly rather than shipping unattested
  artifacts. If that bites, switch to cosign or amend this ADR — do not
  `continue-on-error` it.

## Explicit follow-ups (UNKNOWNs)

- **U1 — full-SHA action pins. RESOLVED (SW-124, 2026-07-15).** Every
  `uses:` in `release-dag.yml` is pinned to its 40-hex commit SHA with a
  trailing `# <tag>` comment. The refs were resolved from the live remotes
  via `git ls-remote <repo> refs/tags/<tag> 'refs/tags/<tag>^{}'` — taking
  the peeled `^{}` commit for annotated tags (attest-build-provenance) and
  the tag object itself for lightweight ones; the REST-API route in the
  original note was unavailable, raw git was not. The pin assertion is
  armed: `TestReleaseDAG_EveryActionIsSHAPinned` in
  `cmd/publish-lock/workflow_test.go` fails on any unpinned `uses:` in the
  DAG. Non-publish workflows deliberately keep tag pins (they ship
  nothing); bumping an action = re-run the ls-remote, update SHA + comment
  in one diff.
- **U2 — environment protection.** GitHub "environments" with required
  reviewers on the `publish` job are repo settings, not committed files;
  configure once in the repo UI and note it in the RC-01 checklist.

## Consequences

- A release can only exist for a commit whose full gate suite ran green in
  the same workflow run; the tag, the assets, the SBOM, the provenance and
  the capability manifest all reference that one SHA.
- Publishing stays impossible until RC-01 flips the lock — after which a
  main-merge with an unpublished CHANGELOG version publishes automatically
  through the full DAG.
