# Finding: CodeRank does not beat Potion, and the run was stopped before grading

Date: 2026-09-19
Candidate: CodeRankEmbed at `3c4b60807d71f79b43f3c4363786d9493691f8b1`,
`admission.max_tokens = 1536`, identity digest
`51402e2c6919e250f36af879e35deae3fd6c2474c2b5eff9e468773e539399d9`,
manifest `0c5e126d…`, candidate `9996961215522b92a30bfdc23710c88f95d6ba6f`

## What this document is, and is not

It is **not** a `DEVELOPMENT PROMOTION: NO`. That verdict requires blind
grading, which was never run.

It is the reasoned decision **not to finish the run**, because evidence already
in hand contradicts the hypothesis the run exists to test. Recorded so a later
reader can check the reasoning rather than repeat the work.

## The candidate holds every operating budget

`measure` completed against a fresh sidecar at the 1536-token manifest:

| gate | measured | ceiling | |
|---|---:|---:|---|
| `sidecar_rss_budget` | 2,020,507,648 | 2,147,483,648 | PASS (94 %) |
| `artifact_budget` | 547,945,013 | 1,073,741,824 | PASS (51 %) |
| `query_embed_p95_budget` | 58.4 ms | 1000 ms | PASS (6 %) |
| `min_query_samples` | 128 | 100 | PASS |
| `reindex_budget` | 214.6 s | 600 s | PASS (36 %) |

Latency min 44.4 ms, median 50.9 ms, max 79.1 ms.

The predecessor at 8192 tokens measured 5,043,961,856 bytes — 235 % — see
`FINDING-admission-and-rss-budget-are-incompatible.md`. Lowering admission
solved that: peak RSS under the full 768-document reindex fell from 5.04 GB to
2.02 GB.

Capture succeeded first try: 8 captures, 2 builds × 4 arms × 64 queries, all
eight digests byte-identical between builds in every arm.

## And it retrieves worse than what GrapHi already ships

`complete_grade_3_span` records mechanically whether the delivered bundle
contains the dataset's reviewed grade-3 answer span. It needs no model call: it
is a comparison against ground truth already in the frozen dataset.

Build 1, 64 queries:

| arm | span in bundle | |
|---|---:|---|
| M0_lexical | 42/64 | 66 % |
| **M1_potion_512** | **60/64** | **94 %** |
| M2_potion_8192 | 58/64 | 91 % |
| **M3_coderank** | **51/64** | **80 %** |

Paired against M1_potion_512: **0 won, 9 lost, net −9.**

The promotion threshold requires **at least +9 paired gains**
(`QualificationMinPairedGain`). The candidate sits 18 apart from it, in the
wrong direction.

### The losses fall exactly where the hypothesis predicted gains

| stratum | Potion-512 | CodeRank | delta |
|---|---:|---:|---:|
| ambiguous | 10/10 | 7/10 | **−3** |
| nl_behaviour | 10/11 | 7/11 | **−3** |
| config_docs | 10/10 | 8/10 | −2 |
| architecture_flow | 8/11 | 7/11 | −1 |
| exact_identifier | 11/11 | 11/11 | ±0 |
| exact_path | 11/11 | 11/11 | ±0 |

`ambiguous` and `nl_behaviour` are the strata a semantic embedder is supposed to
win — they are where it loses hardest. On the lexically solvable strata it
merely ties.

## Why this is enough to stop

What remained was an exporter from `captures.json` to the blind-evidence and
decision arrays — absent from the codebase, estimated at 1-3 engineering days
(see `BLIND-EVAL-RESEARCH.md`) — followed by roughly 2,700 model calls: 896
rater responses (two raters per query, `blindeval.go:590`), 896 grader calls,
plus two per disagreement.

That work would decide whether an answer written FROM a bundle passes. It cannot
rescue a bundle that does not contain the answer: a span absent from the bundle
cannot be read out of it. The measured deficit is not marginal.

### The honest limit of this reasoning

`complete_grade_3_span` is necessary, not sufficient. A bundle could in
principle miss the exact reviewed span and still support a passing answer. At 0
wins against 9 losses that is not a plausible reversal, but it is the reason
this document does not claim a verdict.

## Both admission limits end at NO, for opposite reasons

| | 8192 tokens | 1536 tokens |
|---|---|---|
| RSS budget | 5.04 GB — breaches | 2.02 GB — holds |
| span in bundle | not measured | 51/64 |
| vs Potion-512 | — | −9 paired |

One candidate does not fit the operating budget. The other fits and retrieves
worse than the profile GrapHi ships today — Potion-512 delivers the answer span
in 94 % of queries with 512 tokens of admission, no sidecar, no daemon and no
523 MB of weights.

## What survives for a future attempt

The dataset (`cobra-v3-dev-reviewed`, 64/64, gate ACCEPTED), the grading rubric
(`ec24be9f…`), the derived bootstrap seed, the reference machine, the whole
capture/measure toolchain, and five structural defects fixed along the way —
each of which would have blocked any future run:

1. `graph_generation` preregistered against a `crypto/rand` value
2. a model id compared against a canonical, at four sites
3. a consistency gate that could never fail rather than never pass
4. an observations digest covering a per-build minted value
5. `method_version` compared against a restated copy of itself

A sixth, a tautological `background_load` check, is reported in the ledger and
deliberately left in place.
