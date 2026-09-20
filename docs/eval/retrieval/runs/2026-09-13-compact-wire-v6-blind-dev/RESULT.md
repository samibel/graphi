# Compact wire v6 blind development result

Status: **FAIL — development diagnostic, not a release result**.

- Candidate: `841e1920b81014d6384246958ec3a0299276c24d`
- Registration: `0297f0438f6aa2cc646a2985501f606f28ab620dac7596c8fb9f6864567f704c`
- Population: 40 answerable development questions
- Pre-registered threshold: k=36
- Result: **25/40 passed**
- Primary responses: 80
- Responses beginning `INSUFFICIENT`: 6
- Graded answers: 75 (51 pass, 24 fail), including one adjudicator answer
- Primary grade disagreements: 1 (`cb-25`); the fresh blind adjudicator passed
- Sealed outcome content digest: `5e4f04263e71aaecbb450f1ee04e2ea410d2f36772a266d57ec172efb239fa24`

Failed query IDs:

`cb-01`, `cb-02`, `cb-03`, `cb-11`, `cb-12`, `cb-13`, `cb-15`, `cb-19`,
`cb-24`, `ci-467`, `ci-511`, `ci-771`, `ci-1222`, `ci-1991`, `ci-2314`.

The result rejects the automatic proxy as a sufficiency measure. V6 had source
overlap on 40/40 queries and complete grade-3 qrel-span containment on 28/40,
yet only 25/40 questions were answerable by the blind protocol. Compared with
v5's 28/40, v6 recovered `cb-14`, `cb-28`, `ci-1133`, and `ci-1236`, but
regressed `cb-01`, `cb-02`, `cb-03`, `cb-11`, `cb-12`, `cb-13`, and `ci-771`.

The failures are predominantly incomplete semantic units, not lack of a model:
the bundle often contains the named declaration but omits a required caller,
callee, constant definition, enforcement site, or later execution block. The
two raters frequently agree on the same plausible but incomplete answer. V7
therefore needs relationship-aware source selection that preserves a complete
answer chain, instead of spending the budget on independently high-ranked
fragments or treating qrel-span overlap as the objective.

Recompute the sealed verdict without changing it:

```sh
CGO_ENABLED=0 go run ./cmd/compact-sufficiency-dev decide \
  -run-dir docs/eval/retrieval/runs/2026-09-13-compact-wire-v6-blind-dev
```
