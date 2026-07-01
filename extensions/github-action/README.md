# graphi PR Review — Local-First GitHub Action

Run the **graphi** code-intelligence PR-review vertical entirely inside your CI
runner and post a single sticky review comment on the pull request.

This Action **packages and wires together** graphi's existing PR-review
components — it adds no analysis or comment-formatting logic of its own:

| Step | graphi subcommand | Component |
|------|-------------------|-----------|
| 1 | `graphi analyze pr-risk` | Risk score |
| 2 | `graphi analyze pr-signals` | Hub / bridge / surprise signals |
| 3 | `graphi analyze pr-questions` | Reviewer questions |
| 4 | `graphi pr-comment --publish` | Sticky comment + optional merge gate |

## Local-first contract

- The PR diff is computed **locally** in the runner with `git diff base..head`
  against the checked-out repo — there is **no GitHub "get diff" API call**.
- The graphi engine runs **in-runner with a pinned runtime**, and engine
  analysis (steps 1–3) performs **zero outbound network** calls.
- The **only** outbound connection is the sticky-comment upsert in step 4,
  through the GitHub REST API. This is enforced structurally: the GitHub
  comment host (`engine/review/githubhost.go`) is the sole file in the
  PR-review engine that imports a network package, which is asserted by
  `TestGitHubHostIsSoleNetworkUser`.

## Security

- **Token via environment only.** `github-token` is passed to the engine as the
  `GITHUB_TOKEN` environment variable. It is **never** placed on the command
  line (argv is world-readable via `/proc`) and is never echoed. The
  `validate` package asserts that the entrypoint never passes the token as a flag.
- **Pinned runtime.** `runs.using: composite`, and every `uses:` step is pinned
  by a full 40-hex commit SHA (no floating `@v4` / `:latest`); the engine itself
  is pinned via the `graphi-version` input. The `validate` package **fails CI**
  on any unpinned ref.
- **Least privilege.** The Action only needs `permissions: pull-requests: write`
  to post the sticky comment. The merge gate is **advisory**: it surfaces a
  `gate-status` output (and fails the step on `BLOCK`), so your **branch
  protection** enforces the block — the Action itself needs no merge rights.
- **Redaction.** `provenance` defaults to `summary`, so source/sink paths never
  leak into a world-readable PR comment.

## Inputs

| Input | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `github-token` | string | **yes** | — (secret) | Token used only to post/update the sticky comment. Passed via env, never argv. |
| `graphi-version` | string | no | `v0.43.0` | Pinned graphi engine version (git ref / release tag) built in-runner. |
| `base-ref` | string | no | `""` | Base git ref of the PR (diff computed locally as `base..head`). |
| `head-ref` | string | no | `""` | Head git ref (defaults to checked-out HEAD). |
| `pr-ref` | string | no | `""` | PR reference for the comment header / issue resolution (e.g. `owner/repo#42`). |
| `merge-gate` | boolean | no | `false` | Enable the optional risk-threshold merge gate (fails the step on `BLOCK`). |
| `gate-threshold` | number | no | `700` | Risk threshold in fixed-point units (1/1000) the worst region must exceed to `BLOCK`. |
| `comment-marker` | string | no | `graphi-pr-review` | Informational sticky-comment label (the engine owns the canonical hidden marker). |
| `provenance` | string | no | `summary` | Evidence redaction level for the public comment: `summary` \| `full`. |
| `working-directory` | string | no | `.` | Directory containing the checked-out repo to analyze. |

## Outputs

| Output | Type | Description |
|--------|------|-------------|
| `risk-score` | string | Worst-region risk score (fixed-point decimal, e.g. `0.730`) projected from the pr-risk report. |
| `gate-status` | string | Merge-gate verdict projected from the `PublishResult`: `PASS` \| `BLOCK`. |
| `comment-url` | string | Identifier/URL of the upserted sticky comment projected from the `PublishResult` upsert result. |

> Outputs are **projected** from the byte-stable `engine/review.PublishResult`
> JSON contract; they are never recomputed by the Action.

## Usage (copy-paste, pinned to the action version)

Pin the Action to a release tag, or to a commit SHA for the strictest
supply-chain posture. Replace `OWNER/graphi-pr-review` and the version with
your published Action coordinates.

```yaml
name: graphi PR review
on:
  pull_request:

permissions:
  contents: read
  pull-requests: write   # least privilege: only to post the sticky comment

jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - name: graphi PR review
        id: graphi
        uses: OWNER/graphi-pr-review@v0.43.0   # pin to a release tag or commit SHA
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
          base-ref: ${{ github.event.pull_request.base.sha }}
          head-ref: ${{ github.event.pull_request.head.sha }}
          pr-ref: ${{ github.repository }}#${{ github.event.pull_request.number }}
          merge-gate: true
          gate-threshold: 700
          provenance: summary

      - name: Surface graphi outputs
        run: |
          echo "risk score:  ${{ steps.graphi.outputs.risk-score }}"
          echo "gate status: ${{ steps.graphi.outputs.gate-status }}"
          echo "comment:     ${{ steps.graphi.outputs.comment-url }}"
```

A ready-to-copy version of this workflow lives at
[`examples/pr-review.yml`](./examples/pr-review.yml).

## How it is kept honest

The Action contract is **machine-checked** by the Go package
[`./validate`](./validate), which runs in CI:

- `TestRealActionYMLSatisfiesContract` — every input documents its
  type/default/required status, every output is documented and projected,
  `runs.using` is composite, and every `uses:` is full-SHA pinned.
- `TestRealEntrypointSatisfiesContract` — the entrypoint drives the four real
  subcommands in order, requests a real `-publish`, and never puts the token
  on argv.
- `TestGitHubHostIsSoleNetworkUser` (in `engine/review`) — confirms the GitHub
  host is the only network user, proving zero-egress analysis.

```bash
go test ./extensions/github-action/validate/ ./engine/review/
```
