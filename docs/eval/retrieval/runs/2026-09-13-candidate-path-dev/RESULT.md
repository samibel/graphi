# Exact-path candidate retention development result

Status: **positive development result; not a release result**.

The capture is bound to candidate
`a940d438a885ec7578068bbaa0daddbf27cd4e06`, the pinned Cobra checkout
`a0a6ae020bb3899ff0276067863e50523f897370`, and development dataset
`2d05e3bb015a1447e0c31a9a855712e6aae6f4281adbf7acd72e86c923a43d6c`.
It contains only the 44 development records (40 answerable and four no-hit);
no holdout record was opened or evaluated.

## Diagnosis and change

For `cb-30`, retrieval already ranked the answer-bearing
`site/content/docgen/man.md:1` at position 16. `task_context/2` requested only
15 rows, so the exact filename clue was discarded before source selection.
The old compact projector then spent source budget on sibling documentation
(`md.md`, `yaml.md`, and `rest.md`) while the requested `man.md` section was
absent.

The accepted change keeps all public bounds fixed:

- retrieval still computes its existing 50-row union, while task context
  inspects that window and emits at most 15 internal candidates;
- when a meaningful query term exactly equals a candidate file stem, the first
  such row receives the first extra-candidate slot, below the unchanged five
  primary seeds;
- the definition selector carries the same exact-basename signal through its
  bounded snippet competition;
- compact selection hydrates and retains that Markdown section atomically when
  it costs at most 160 source tokens;
- contained fallback regions are removed, and affordable code/documentation
  and caller/callee pairs are completed atomically.

No qrel, target span, dataset identifier, repository-specific path, or answer
callback enters production selection. The frozen 1,200-token serialized ceiling,
325 source-field budget, 40-item cap, targets, and scoring remain unchanged.
The resulting compact wire identity is `task_context/2-compact/8`.

## Before / after

The before row is the immediately preceding compact development measurement on
the committed answer-recovery inputs. The after row uses the exact production
MCP bytes in `bundles.json` from the candidate above.

| Measure (40 answerable dev queries) | Before | After |
|---|---:|---:|
| Required grade-3 overlap reached | 40 | 40 |
| Every grade-3 span overlapped | 29 | 30 |
| At least one complete grade-3 span | 31 | 32 |
| Required complete spans reached | 31 | 32 |
| Every grade-3 span complete | 17 | 17 |
| `config_docs` with a complete span | 18/19 | 19/19 |
| Responses at or below 1,200 cl100k tokens | 40 | 40 |
| Bundles with a strictly contained duplicate source | 0 | 0 |
| Median response tokens | 1,027.0 | 1,021.0 |
| Maximum response tokens | 1,199 | 1,174 |
| Individually cheaper than equal-recall GrepRead/2 | 24 | 24 |
| Paired median token saving | 67.0 | 76.0 |
| Paired median percent saving | 5.9066% | 6.6892% |

`cb-30` changes from incomplete to containing the complete
`site/content/docgen/man.md:1-31` answer span. No development query loses its
required overlap or a complete answer span.

Two independent index builds used 768 input documents each and produced
identical MCP response bytes, SHA-256 digests, and cl100k counts for all 44
records (`44/44`) while recording distinct freshness generations. The capture
SHA-256 is
`e0be164b44fe800f13a3f950e407797f052b7746782acfc66b988263437e4bf1`.

The separate ranking gates remain green on the current unchanged retrieval
ranking: architecture-flow nDCG@10 is `0.46246715228468249` (required
`0.4578575262772977`), NL-behaviour nDCG@10 is `0.70290472235070689`, and
exact-identifier Top-1 is `1.0`.

## Reproduce

```sh
export CGO_ENABLED=0
export GRAPHI_STATIC_MODEL_DIR=/absolute/path/to/potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b
export GRAPHI_RECOVERY_COBRA=/absolute/path/to/cobra-at-a0a6ae020bb3899ff0276067863e50523f897370

GRAPHI_RECOVERY_EMBEDDER=static:potion-code-16M-v2@e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-candidate-path-dev/bundles.json" \
GRAPHI_RECOVERY_CANDIDATE_SHA=a940d438a885ec7578068bbaa0daddbf27cd4e06 \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
go test ./internal/eval/retrieval -run '^TestRecoveryDevCapture$' -count=1 -v

GRAPHI_PRODUCT_COMPACT_DEV_COBRA="$GRAPHI_RECOVERY_COBRA" \
GRAPHI_PRODUCT_COMPACT_DEV_BUNDLES=docs/eval/retrieval/runs/2026-09-13-candidate-path-dev/bundles.json \
go test ./internal/eval/retrieval -run '^TestProductCompactTaskContextDev$' -count=1 -v

go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-13-product-compact-v7-dev/cobra-v2-dev-report.json

go test ./...
```

The target checker reports all four development gates as PASS and exits 1 only
for the immutable historical blind-smoke `RELEASE: NO`. This development result
does not override that decision. A release `YES` requires a newly
pre-registered independent holdout evaluation of this frozen candidate.
