# Finding: the pinned admission limit and the RSS budget cannot both hold

Date: 2026-09-19
Candidate: CodeRankEmbed at `3c4b60807d71f79b43f3c4363786d9493691f8b1`,
`admission.max_tokens = 8192`, manifest sha256 `15f4b86e8df8291f364894658b1f820f45b5674bd86569007ea41ff16821ef63`

## Result

`measure` completed. Four of five operating gates pass with wide margins; one
fails, and it fails structurally rather than narrowly.

| gate | measured | ceiling | |
|---|---:|---:|---|
| `sidecar_rss_budget` | 5,043,961,856 | 2,147,483,648 | **FAIL (235 %)** |
| `artifact_budget` | 547,945,013 | 1,073,741,824 | PASS (51 %) |
| `query_embed_p95_budget` | 62.8 ms | 1000 ms | PASS (6 %) |
| `min_query_samples` | 128 | 100 | PASS |
| `reindex_budget` | 110.0 s | 600 s | PASS (18 %) |

Query latency: min 42.6 ms, median 52.6 ms, max 108.6 ms.

On the preregistered thresholds this candidate is `DEVELOPMENT PROMOTION: NO`,
decided by an operating gate before any quality evidence exists.

## Why: memory grows quadratically with document length

Measured directly against the pinned sidecar, one document at a time (GrapHi
embeds one document per call — `engine/embed/generate.go:457`, so the server's
`MAX_BATCH = 32` is never reached and is NOT the driver):

| tokens in one document | peak RSS |
|---:|---:|
| 830 | 1,471 MB |
| 1,612 | 1,547 MB |
| 3,176 | 2,629 MB |
| 6,327 | 6,610 MB |
| 8,192 | 7,601 MB |

Classic attention scaling. A single long document is enough to breach the
budget, because `peak_rss` is a high-water mark that never falls.

## The limit that holds, measured

Each point against a FRESH sidecar, for the same reason:

| admitted tokens | peak RSS | of 2 GiB | |
|---:|---:|---:|---|
| 1,492 | 1,464 MB | 71 % | holds |
| 1,989 | 1,658 MB | 81 % | holds |
| 2,483 | 1,914 MB | 93 % | holds |
| 2,980 | 2,107 MB | 103 % | breaches |

The budget holds up to roughly 2,500 tokens; **2,048 leaves a 19 % margin**.

## What a lower limit costs — very little

`admission_truncations: 0` in every capture: at 8,192 tokens **not one** of the
768 cobra documents is truncated. The limit is decoration for this corpus, and
the budget breaks anyway.

Token distribution over cobra's declarations, measured with the authoritative
tokenizer via `/v1/admit`:

```
median      87 tokens
p90        310
p99      2,142
max      3,309
```

| limit | truncated | share |
|---:|---:|---:|
| 1,536 | 4 / 298 | 1.3 % |
| 2,048 | 3 / 298 | 1.0 % |
| 2,560 | 2 / 298 | 0.7 % |
| 8,192 | 0 / 298 | 0 % |

**Approximation, stated plainly:** the probe measured 298 top-level
declarations; GrapHi embeds 768 documents. The difference is methods, fields and
nodes a regex does not catch — all SHORTER units, so the real distribution is
more skewed and the truncated share smaller, not larger. The order of magnitude
holds; the exact figure does not.

## Conclusion

`admission.max_tokens = 8192` and `max_sidecar_rss_bytes = 2 GiB` are
incompatible by a factor of 3.7. Both were set without measurement, and the
contradiction predates the first run.

Lowering admission to ~2,048 tokens holds the budget with margin and truncates
roughly 1 % of documents. That changes the manifest, hence the durable identity,
hence the candidate: it needs its own preregistration and its own capture.

**Still unanswered:** whether CodeRank retrieves better than Potion. An
operating gate says nothing about retrieval quality. That question needs the
blind-grading path, which does not yet exist — see
`BLIND-EVAL-RESEARCH.md` in this directory.

## What is reusable

The dataset (`cobra-v3-dev-reviewed`, 64/64, gate ACCEPTED), the grading rubric
(`ec24be9f…`), the derived bootstrap seed, the reference machine, and every
tooling fix made along the way. A new candidate is not a fresh start.
