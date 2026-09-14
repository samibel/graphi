# Answer-span ceiling of the two sealed holdouts

Status: **aggregate-only measurement of sealed data; not a release result and
not a candidate measurement.** Protocol `answer-span-ceiling/1`
(`docs/eval/retrieval/answer-span-ceiling-protocol.md`).

## Custody disclosure

The protocol says the curator runs this. Here it was run by the candidate's
author, with the repository owner's explicit permission, because no curator
was available in the session. What that cost and what it did not:

- The tool was run without `-answer-span-detail`; the only bytes that reached
  the author are the two aggregate reports below, which name no query, path
  or line (`TestAnswerSpanCeiling_AggregateNamesNothingDetailIsOptIn`).
- The sealed dataset files were never opened, printed, grepped for content or
  copied; the only reads were `shasum` and the tool's own load, and the
  recorded SHA-256 values match the pre-registrations.
- The sealed run directories are unchanged; the reports live here so that
  the sealed directories' hash comparisons stay valid.
- The author nevertheless now knows two numbers about each sealed key that
  a curator-run protocol would have kept from them. Neither number can steer
  a candidate towards a question; a later curator may still prefer to re-run
  the tool and compare SHA-256s before relying on these files.

## Results

| Sealed holdout | Dataset SHA-256 | Answerable | Grade-3 spans | Over ceiling | ≥1 feasible (`F`) | All feasible |
|---|---|---:|---:|---:|---:|---:|
| First fresh (`…-v5-fresh-sealed-holdout`) | `9f2289c7…10d5aa` | 64 | 64 | 2 | **62/64** | 62/64 |
| Second fresh (`…-v5-second-fresh-sealed-holdout`) | `331bd256…85ae` | 64 | 64 | 0 | **64/64** | 64/64 |

Report files: `first-fresh-sealed-holdout.json`,
`second-fresh-sealed-holdout.json`; pinned checkout
`a0a6ae020bb3899ff0276067863e50523f897370`; tokenizer
`tiktoken:cl100k_base:ordinary`, vocabulary
`223921b76ee99bde995b7ff738513eef100fb51d18c93597a113bcffe865b2a7`.

## What this settles

1. **The frozen 1,200-token ceiling is not why the holdouts fail.** On the
   holdout that returned 42/64, every one of the 64 questions can carry its
   complete answer span; on the one that returned 32/64, 62 can. The
   pre-registered `k = 56` is attainable in principle on both.
2. **The prediction in `contract-v2-options.md` was wrong.** It extrapolated
   the development split's whole-file `exact_path` targets to the holdout
   and predicted a holdout ceiling of at most 53. The curator did not
   judge whole files; every holdout question has exactly one grade-3 span
   and all but two fit. Option A of that document (derive `k` from the
   ceiling) is therefore moot for these holdouts and is withdrawn as a
   recommendation.
3. **The development split is a poor proxy for the holdout.** Development
   has 63 spans over 40 questions, multi-span `config_docs` keys and three
   whole-file targets that cannot fit; the holdouts have one small span per
   question. Every development ceiling argument, and the `all_complete`
   measure in particular, describes a shape the holdout does not have. The
   development split can still catch regressions; it cannot forecast a pass
   count.
4. **The whole 42 → 56 gap is candidate quality against a rubric**, on
   questions whose answers fit. With `F = 64`, a candidate needs a true
   per-question rate near 94 % to pass `k = 56` with 95 % probability, and
   about 87.5 % to pass with even odds; the last observed rate is 65.6 %.

## What follows

- The next slice is ranking and recall (option D of
  `contract-v2-options.md`), not budget, not the ceiling, not `k`.
- Before that slice, a development set shaped like the holdout — one
  reviewed small span per question, authored fresh, never a holdout question
  — is worth more than any further tuning on the current 40, because the
  current 40 cannot measure what the holdout grades.
- The compact selector's twelve query-shape predicates remain the most
  likely reason development gains have not transferred; a ranking slice
  should be gated on removing them, not adding to them.

## Reproduce

```sh
export CGO_ENABLED=0
for run in 2026-09-13-product-compact-v5-fresh-sealed-holdout 2026-09-13-product-compact-v5-second-fresh-sealed-holdout; do
  go run ./cmd/retrieval-eval -answer-span-ceiling \
    -dataset docs/eval/retrieval/runs/$run/sealed-dataset.json \
    -checkout /absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
    -out /tmp/$run.json
done
```

The reports are byte-comparable to the committed files; only the curator, or
someone else who is not a candidate's author, should add
`-answer-span-detail`.
