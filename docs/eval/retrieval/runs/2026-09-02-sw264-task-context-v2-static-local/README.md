# SW-264 AC-9 — task_context/2 production-static measurement

This run measures every dev query in the pinned cobra-v1 nl_behaviour stratum. It does not use or copy holdout queries. The measured population contains 6 queries and the observed grade-3 span coverage is 6/6 (1.000000).

The coverage resolution is 1/6 (0.166667): one query changes the aggregate by that amount. Coverage is a hit metric, not a cost metric. The bundle cost observed alongside it was:

| Query | Items | Evidence citations | Emitted snippet whitespace tokens | Engine-reported snippet tokens | Item cap | Items dropped | Truncated |
|---|---:|---:|---:|---:|---:|---:|:---:|
| cb-11 | 40 | 153 | 500 | 500 | 40 | 16 | true |
| cb-12 | 40 | 129 | 605 | 605 | 40 | 12 | true |
| cb-13 | 40 | 89 | 580 | 580 | 40 | 7 | true |
| cb-14 | 34 | 44 | 380 | 403 | 40 | 0 | false |
| cb-15 | 40 | 81 | 517 | 517 | 40 | 4 | true |
| cb-16 | 37 | 45 | 618 | 618 | 40 | 0 | false |

The two token columns are intentionally distinct. The emitted count independently recounts whitespace-separated fields in snippet evidence remaining in the final contract.Result. The engine-reported count is taskctx's context-bundle accounting parsed from its summary before snippet citations are deduplicated into the final evidence set. Both use whitespace tokenization, but they count different populations and can disagree. Neither count is substituted for the other.

The run used a non-nil instance returned by engine/retrieval.New, the production static embedder static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b:75cf7a6c2171:mean:true:107bbdcbad4b:148e5691a6fc:embedeach-f16-tree:d686c1edad9b, a persisted generation reloaded and independently validated as ready (768 vectors), a RootedReader over cobra at a0a6ae020bb3899ff0276067863e50523f897370, and task_context/2 with TokenBudget=1200. Each per-query record contains the exact contract.Result bundle and every evidence/judgement pair for which internal/eval/retrieval.SpanMatches returned true.

eligible_for_threshold is true because every eligibility check in measurement.json passed. This is measurement evidence for SW-266; it does not set or instruct a threshold.

## Recompute

Set SW264_AC9_MODEL_DIR to the pinned static model directory and SW264_AC9_COBRA_ROOT to the pinned cobra checkout. The command fails before running if either input is missing.

~~~bash
: "${SW264_AC9_MODEL_DIR:?set to your potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b model dir}"
: "${SW264_AC9_COBRA_ROOT:?set to the cobra checkout at a0a6ae020bb3899ff0276067863e50523f897370}"
GRAPHI_STATIC_MODEL_DIR="$SW264_AC9_MODEL_DIR" \
SW264_AC9_COBRA_ROOT="$SW264_AC9_COBRA_ROOT" \
SW264_AC9_MEASURE=1 CGO_ENABLED=0 \
go test ./internal/eval/retrieval -run '^TestSW264_AC9Measurement$' -count=1 -v
~~~

run.json content-addresses measurement.json, the dev-only dataset slice, this README, and each raw query record.
