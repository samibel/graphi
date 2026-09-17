# Compact wire v5 blind development result

Status: **FAIL — development diagnostic, not a release result**.

- Candidate: `252b9539e71261de5583efb713d93deda00a9a83`
- Registration: `b6a6a65bca7b5ad863245d02550e44dd29baf9bb16b56cc262c00d1452183ad2`
- Population: 40 answerable development questions
- Pre-registered threshold: k=36
- Result: **28/40 passed**
- Primary responses: 80
- Responses beginning `INSUFFICIENT`: 11
- Non-mechanical grades: 69 (56 pass, 13 fail)
- Primary grade disagreements: 0; no adjudicator was used
- Sealed outcome content digest: `185f39c28647c4d05727822a5fce8980d9ad8c9a46a1199236ef7be8c7b0ba5d`

Failed query IDs:

`cb-14`, `cb-15`, `cb-19`, `cb-24`, `cb-28`, `ci-467`, `ci-511`,
`ci-1133`, `ci-1222`, `ci-1236`, `ci-1991`, `ci-2314`.

Compared with the v4 blind run, v5 recovered `cb-12`, `cb-13`, `cb-21`,
`cb-22`, and `ci-771`, but regressed `cb-14`, `cb-15`, `cb-24`, `ci-1133`,
and `ci-1236`. Seven failures remained in both versions: `cb-19`, `cb-28`,
`ci-467`, `ci-511`, `ci-1222`, `ci-1991`, and `ci-2314`.

The paired frontier's 40/40 source overlap therefore did **not** predict blind
answerability. V5 increased complete grade-3 spans from 8 to 24, but its wider
declaration hydration displaced evidence needed by five previously answerable
queries. The next candidate must preserve a known-sufficient core before it
spends budget on hydrated depth, and must hydrate the declaration surrounding
a GrepRead hit rather than only declarations named by semantic items.

Recompute the sealed verdict without changing it:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-13-compact-wire-v5-blind-dev
```
