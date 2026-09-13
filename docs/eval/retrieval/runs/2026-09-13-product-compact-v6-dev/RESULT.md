# Presealed-rubric compact task_context/2 — development result

Status: **candidate-bound development evidence, not a release result**.

Frozen candidate: `d8d6a2c1d8da2de0bd90d94350a98e32a138e26c`.

This is the successor to V5 after independent audit found that the blind seal
phase did not verify run-local rubric bytes before producing grader packets.
The successor refuses missing or drifted rubric bytes before any packet is
written and embeds the verified rubric bytes and SHA in every packet.

The run uses only the committed development slice and exercises the exact MCP
stdio product path twice from independent indexes. No holdout was opened.

## Result

- 44/44 final MCP responses, payload digests and real-tokenizer counts were
  identical across two independent index builds.
- All 88 actor-visible payloads identify themselves as
  `task_context/2-compact/4`.
- Every one of the 44 development responses was within the frozen 1,200-token
  ceiling; the full-population range was 577–1,079 cl100k tokens.
- Equal-recall grade-3 overlap was 40/40. At least one complete grade-3 span was
  present in 26/40; all grade-3 spans overlapped in 30/40 and were fully
  contained in 14/40.
- On the 40 answerable queries, mean / median / maximum cost was
  910.075 / 914.5 / 1,079 cl100k tokens.
- Against the preserved GrepRead/2 development baseline, 32/40 were cheaper
  and 8/40 more expensive. Paired median saving was 106.5 tokens, or 9.5930%.
- The frozen retrieval report remains above the architecture-flow target
  (0.4624671523 >= 0.4578575263), holds exact-identifier Top-1 at 1.0, and
  passes natural-language behavior and bundle coverage.

## Reproduce

```sh
GRAPHI_EVAL_TOKENIZER_DIR="$PWD/internal/eval/tokenizer/testdata/artifact" \
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-v6-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_CANDIDATE_SHA=d8d6a2c1d8da2de0bd90d94350a98e32a138e26c \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The exact `captures.json` SHA-256 is
`5abdcabdca489cd3431f8b9db4b126ea6c937671de87f3a17e3b40c63f803acb`.

This evidence authorizes completion of the independent fresh-holdout
preconditions. It does not authorize `RELEASE: YES`; that still requires the
newly curated sealed dataset, resolved independent participants and a completed
write-once blind decision.
