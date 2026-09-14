# Forecast of candidate `40133db5` on the unreviewed draft

Status: **indicative only.** The dataset is an unreviewed draft written by
the candidate's author (`REVIEW.md`); the numbers below can be moved by the
review in either direction and are evidence of nothing. They are recorded
because they are the first measurement on a holdout-shaped split and they
say where the loss is, which the reviewed split will refine rather than
overturn.

Instrument: `TestDraftDevForecast` (`internal/eval/retrieval/draft_forecast_dev_test.go`),
production compact MCP path, static embedder
`potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b`, one index build,
dataset SHA-256 `d7af540a0515c26a8720cac824c59c86d707f98cf256d2d60249d0bb5c789d4f`,
pinned checkout `a0a6ae020bb3899ff0276067863e50523f897370`.

## Aggregate

| Measure (64 draft questions, one grade-3 span each) | Count | Rate |
|---|---:|---:|
| Span overlapped by at least one emitted source | 50/64 | 78.1 % |
| Span cited (a source starts inside it) | 41/64 | 64.1 % |
| Span delivered complete in one source | 30/64 | 46.9 % |
| Mean share of span lines delivered | — | 63.9 % |
| Median / max response tokens | 1,019 / 1,200 | — |

The last sealed holdout scored 42/64 = 65.6 % under its rubric, between this
draft's overlap and complete rates. Read either as a forecast of the rubric:

| If the true per-question rate were | P(≥ 56 of 64) |
|---|---:|
| 46.9 % (complete) | ≈ 0 |
| 78.1 % (overlapped) | 0.042 |

## Per stratum

| Stratum | Overlapped | Cited | Complete |
|---|---:|---:|---:|
| exact_identifier | 11/11 | 10/11 | **11/11** |
| ambiguous | 8/10 | 8/10 | 8/10 |
| exact_path | 11/11 | 11/11 | 2/11 |
| nl_behaviour | 6/11 | 5/11 | 4/11 |
| config_docs | 6/10 | 0/10 | 3/10 |
| architecture_flow | 8/11 | 7/11 | 2/11 |

## Where the 34 misses are

Each miss is attributed to the first stage that loses it. A retrieval row
carries only its declaration line, so the instrument parses the pinned
checkout to find the declaration's full extent (Go: top-level declaration
with doc comment; Markdown: heading to next heading) and asks whether that
extent overlaps the target span; for a path query the file rows contain
every span in the file.

| First stage that loses the span | Misses | What it means |
|---|---:|---|
| Retrieved (its declaration is in the 50-row window, within the 15-candidate cap) but the emitted lines do not cover the span | **19** | compact selection: depth, anchor placement, region order |
| In the 50-row window at rank 16–20, so dropped by the task-context cap | 5 | candidate admission (`cd-23` 17, `cd-28` 19, `cd-34` 17, `cd-40` 17, `cd-42` 20) |
| `exact_path`: whole span present in adjacent pieces, 86–94 % delivered, blank line dropped | 9 | a rubric question for the reviewer, not an engineering loss until it is one |
| Not in the 50-row window at all | 1 | retrieval recall (`cd-41`, the completion-function choice inside `getCompletions`) |

(The instrument's own summary line folds the nine `exact_path` rows into
"lost in selection" — `absent=1 below_cap=5 lost_in_selection=28` — because
a file row does contain the span; the table separates them because they are
a different kind of loss.)

Of the 19 selection losses, eleven have their declaration at **rank 1** in
the window and still lose: the projector emits a one- to three-line anchor
of the top-ranked declaration and spends the remaining budget on ten to
thirteen other regions (`cd-24`: `execute` at rank 1, target 33 lines, 3 %
delivered; `cd-31`, `cd-37`: the help command at rank 1, 0 % delivered;
`cd-49`, `cd-52`, `cd-54`: the documentation section at rank 1, delivered as
a two-line fragment or not at all).

## What this changes in the plan

It reverses the priority order that the committed development split
suggested. On the old split the losses were retrieval recall (7), the
candidate cap (3) and selection (11); on holdout-shaped questions they are
selection (19), the cap (5) and recall (1). Retrieval is finding the answer;
the compact projection is not showing it. The lever is depth on the
top-ranked region — the very trade the earlier frontier sweep and the
"fewer regions" probe rejected *on the old split*, whose multi-span keys
reward breadth. That is the concrete reason the old split was the wrong
instrument, and it is why any selector change from here must be measured on
both splits: it must gain here without losing the old gates and cost.

`exact_identifier` is solved on this shape. `exact_path` waits on the
rubric answer.

Reproduce:

```sh
export CGO_ENABLED=0
GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v
```
