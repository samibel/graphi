# Production compact task_context/2 — development result

Status: **development diagnostic, not a release result**.

Candidate: `5fe55210647d369cd7440c287e27d2277452cf26`.

This run exercises the real production chain twice from independent indexes:

`MCP stdio tools/call -> task_context/2 -> semantic retrieval -> bounded local source discovery -> compact structuredContent -> final JSON-RPC bytes`.

It uses only the committed 40-query answerable development population (plus the
four non-release diagnostic rows in the dev-only dataset). It does not open or
score the spent holdout.

## Result

- 44/44 exact MCP payloads, SHA-256 digests and tokenizer counts were identical across the two builds. The index freshness generations were distinct.
- Equal-recall grade-3 overlap was 40/40; 26/40 responses contained at least one complete grade-3 span.
- All grade-3 spans overlapped for 30/40 queries and were fully contained for 14/40.
- All 40 answerable payloads stayed within the frozen 1,200-token ceiling.
- cl100k_base mean / median / maximum was 911.55 / 916 / 1,081 tokens.
- Against the preserved GrepRead/2 development transcript, 32/40 candidates were cheaper and 8/40 were more expensive.
- Paired median saving was 107.5 tokens, or 9.5930%.

The earlier preregistered blind development sufficiency run over the same V9
selection behavior passed 37/40 questions. That result remains separate from
this source-overlap and payload-cost capture.

## Reproduce

Use a clean Cobra checkout at
`a0a6ae020bb3899ff0276067863e50523f897370`:

```sh
GRAPHI_RECOVERY_OUT="$PWD/docs/eval/retrieval/runs/2026-09-13-product-compact-dev/captures.json" \
GRAPHI_RECOVERY_COBRA=/path/to/clean/pinned/cobra \
GRAPHI_RECOVERY_REQUIRE_IDENTICAL=1 \
CGO_ENABLED=0 go test ./internal/eval/retrieval \
  -run '^TestRecoveryDevCapture$' -count=1 -v
```

The capture file SHA-256 is
`1a7da7cb946c1ee1fc66f208b4e447bda6b0d54f41aaeae6580ccd0c940a92ec`.

The unchanged ranking gates are reproduced with:

```sh
CGO_ENABLED=0 go run ./cmd/retrieval-eval -check-targets \
  docs/eval/retrieval/runs/2026-09-07-answer-recovery-dev/after/cobra-v2-dev-report.json
```

That command reports PASS for architecture flow (0.4624671523), exact
identifier (1.0), natural-language behavior and bundle coverage. It still exits
1 solely because the old, spent holdout correctly records RELEASE: NO; it
cannot certify this changed candidate.
