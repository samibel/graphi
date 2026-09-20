# Forecast of candidate `40133db5` on the reviewed split

Status: **the reviewed split is now the reference** (`REVIEW.md`; dataset
SHA-256 `33760861d5c7…`). Measured with `TestDraftDevForecast` on the
production compact MCP path, one index build, static embedder:

| Measure (64 reviewed questions) | Count | Rate |
|---|---:|---:|
| Span overlapped | 45/64 | 70.3 % |
| Span cited | 34/64 | 53.1 % |
| Span complete in one source | 28/64 | 43.8 % |
| Mean span share delivered | — | 56.6 % |
| Median / max tokens | 1,036 / 1,190 | — |

| Stratum | Overlapped | Complete |
|---|---:|---:|
| exact_identifier | 11/11 | 11/11 |
| ambiguous | 7/10 | 6/10 |
| exact_path | 8/11 | 2/11 |
| nl_behaviour | 7/11 | 3/11 |
| config_docs | 5/10 | 3/10 |
| architecture_flow | 7/11 | 3/11 |

Misses by first losing stage: 5 absent from the 50-row window (three are
the reviewer's non-Go `exact_path` files, which retrieval does not index),
4 below the 15-candidate cap, 27 retrieved and lost in selection (nine of
them `exact_path` pieces, which the rubric accepts — see `REVIEW.md`).

Under the rubric's reading (pieces count, paraphrase allowed) the
candidate's proxy on this split is the overlap rate, 70 %; the last sealed
holdout scored 66 %. `P(≥56 of 64)` at 70 % is 0.001. The reviewed split
forecasts the sealed result well and says the same thing the draft said:
the gap is selection, on questions whose answers fit.

The sections below are the record made on the *unreviewed* draft
(dataset SHA-256 `d7af540a0515…`) and are kept for the probes and sweeps
they document; their per-question ids refer to that draft.

---

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

## Selector probes on this shape (2026-09-14, all rejected)

Each probe was a one-variable change to the compact selector, measured on
this draft and on the committed development split, and reverted. The
committed selector is unchanged.

| Probe | Draft (overlapped / complete of 64) | Old split (reached / ≥1 complete / all complete of 40; cheaper; paired median saving) | Decision |
|---|---|---|---|
| baseline `40133db5` | 50 / 30 | 40 / 33 / 18; 24/40; +67 (5.9 %) | — |
| H1: when the top-ranked body is too long to complete, grow it to its weighted quota *before* completing its dependencies | 50 / 30 | 40 / 33 / 18; 24/40; +67 | no effect: the "root" the selector picks is not the retrieval's first row (below) |
| H2: the +1,000,000 whole-symbol tie-break only when the question spells the symbol as declared (`Flag`, not "flag") | 50 / 30 | 40 / 33 / **19**; 21/40; **+15 (1.2 %)** | no draft gain; buys one old-split span for most of the savings claim |
| H3: retrieval-rank bonus for primary candidates (`300000/seed`) | **51 / 32** (nl_behaviour 4→6) | **38** / 31 / 18; 22/40; +38 | reject: breaks the required-overlap gate (`cb-14`, `ci-678`, whose answers arrive only through the lexical fallback the bonus now outranks) |
| H3b: the same bonus bounded to seeds 1–3 at `90000/seed` | 50 / 30 | **38** / 29 / 18; 20/40; +10 | reject: the regression stays, the gain does not |

What the traces behind these probes established, for `cd-24` and `cd-31`:
retrieval placed the answer's declaration first in both (`execute`,
`InitDefaultHelpCmd`); the compact selector then re-scored every candidate
from scratch and put `ValidateFlagGroups` and
`IsAdditionalHelpTopicCommand` above them, because their *names* contain
more of the question's words ("flag", "help", "command"), and `type Command`
and `Command.Flag` above everything, because a whole-word name match earns
a million points. The selector's "root" — the region that receives depth —
is chosen from that re-scoring, not from retrieval.

The conclusion is not another probe. The compact selector re-ranks the
retrieval result with lexical name matching, twelve query-shape predicates
and fixed bonuses spanning five orders of magnitude, and every one-variable
change to that stack either does nothing on holdout-shaped questions or
trades a hard gate on the old split.

## The retrieval-ordered rewrite (compact/10), attempted and withdrawn

A replacement natural-language selector was built and measured in four cuts
on both splits (retrieval order for primary candidates, depth by rank,
completion by structural unit, directed growth around the question's words,
test files demoted without test intent, duplicate windows merged, two
lexical-discovery regions kept). It is parked outside the tree; the
committed selector is unchanged.

| Cut | Draft (overlapped / complete of 64) | Old split (reached / ≥1 complete; cheaper; saving) |
|---|---|---|
| committed v9 | 50 / 30 | 40 / 33; 24/40; +67 |
| 1: order + depth | 45 / 27 | 33 / 22; 27/40; +42 |
| 2: + tests demoted, 6 seeds, alternating growth | 47 / 28 | 33 / 20; 24/40; +30 |
| 3: + best-line ranking of supporting regions, 2 discovery regions | 47 / 30 | 34 / 20; 23/40; +34 |
| 4: + duplicate windows merged, supporting regions by priority | 47 / 30 | 35 / 20; 18/40; −13 |

It reaches parity with v9 on the draft and never exceeds it, and it loses
five to seven old-split questions whose answers v9 reaches only through
its query-shape predicates (`cb-14` via the shell-protocol literal,
`cb-21` via the parent-link literal, `ci-678` via the test-flag literal).
That confirms the earlier diagnosis of the old split — those questions are
answered by dev-specific rules, not by a general mechanism — but it does
not make the rewrite better on the shape that matters.

## What actually bounds the draft: two sweeps

With the committed selector, on the draft:

| Source frontier (ceiling 1,200) | Overlapped | Complete |
|---:|---:|---:|
| 325 | 50 | 30 |
| 425 | 50 | 30 |
| 550 | 50 | 30 |
| 700 | 50 | 29 |
| 900 | 49 | 30 |
| 1,200 | 50 | 29 |

And with the projector's wire ceiling raised temporarily as a diagnostic:

| Ceiling | Frontier | Overlapped | Complete | Median tokens |
|---:|---:|---:|---:|---:|
| 2,000 | 700 | 50 | 31 | 1,699 |
| 2,000 | 1,000 | 50 | 31 | 1,954 |
| 3,000 | 1,000 | 51 | 33 | 2,245 |

**Fourteen questions do not receive their span at two and a half times the
budget.** Budget is therefore not what bounds them. Checking what the
projector was *given* for each — the pre-compact bundle's evidence windows
on the span's path — splits the fourteen:

| The bundle's evidence on the span's path… | Questions |
|---|---:|
| contains the whole span, and the selector still never emits it, at any budget | **8** (`cd-26`, `cd-29`, `cd-31`, `cd-33`, `cd-37`, `cd-53`, `cd-61`, `cd-62`) |
| only touches the span — the hydrated window stops short | 1 (`cd-49`) |
| does not exist — the declaration never reached the bundle | 5 (`cd-28`, `cd-40`, `cd-41`, `cd-51`, `cd-52`) |

So eight are selection after all, of a specific kind: the covering
evidence is a low-ranked seed (window ranks 6–14) or, twice, the rank-1
declaration whose emitted window is placed at its doc comment rather than
at the lines the question is about (`cd-31`, `cd-37`: `InitDefaultHelpCmd`
at rank 1, emitted as its first four lines). Neither the ten-region cap
nor the budget lets those eight in; the score order does not reach them.
Five are the candidate pool (the 15-item cap against the 50-row window, or
a declaration outside the window altogether), and one is a hydration
window.

The consequence for the plan: two slices, not one. Upstream, the candidate
pool and hydration windows (six questions); in the selector, the placement
of the emitted window inside a long rank-1 declaration and the admission of
low-ranked seeds whose evidence covers the question's lines (eight). Both
are measurable on this split alone, both must hold the old split's gates,
and neither is a budget change.

## The candidate-pool cap, swept (committed selector, both splits)

`taskctx.candidatePoolLimit` was raised temporarily, pre-compact bundles
re-captured for both splits, and both measured; the cap is restored at 15.

| Cap | Draft (overlapped / complete of 64) | Old split (reached / ≥1 complete / all complete of 40; paired median saving) |
|---:|---|---|
| 15 (committed) | 50 / 30 | 40 / 33 / 18; +67 |
| 18 | 51 / 30 | 40 / 31 / 16; +75.5 |
| 20 | 51 / 31 | 39 / 31 / 17; +73.5 |
| 25 | 51 / 32 | 39 / 30 / 17; +83 |

Every widening buys one or two holdout-shaped questions and pays one to
three old-split ones: more candidates compete for the same ten regions, and
the committed selector's ordering does not prefer the ones that answer.
The cap is not a free lever either; it becomes one only together with a
selector that admits by retrieval order — which is the rewrite that was
just withdrawn for losing the old split. The two splits pull against each
other through the same selector, and the old split's `≥1 complete`
figure is, as the withdrawn rewrite showed, partly earned by dev-specific
literals.

That is the honest end of this slice. Every projector-side and pool-side
lever has now been measured on the holdout's shape: ceiling, frontier,
selector ordering, and pool width. None moves the complete count past
32 of 64 while the old split is held. The decision that unblocks the next
step is not an engineering one: whether the reviewed holdout-shaped split
replaces the old split as the gate for selection decisions. Until then the
committed candidate stands.

Reproduce:

```sh
export CGO_ENABLED=0
GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370 \
GRAPHI_DRAFT_DATASET=docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json \
go test ./internal/eval/retrieval -run '^TestDraftDevForecast$' -count=1 -v
```
