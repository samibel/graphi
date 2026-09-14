# Second-response contract (draft): `sw266-measurement-contract/2`, `sw280-qrel-blind-smoke-evaluation/2`

Status: **draft, not adopted.** `sw266-measurement-contract/1` and
`sw280-qrel-blind-smoke-evaluation/1` remain in force. This draft changes no
frozen input, reopens no sealed evaluation and authorises no claim. Adoption
is its own reviewed slice; until then every number produced under it is a
development observation, labelled as such.

It is option C of `contract-v2-options.md`, made concrete after the measured
ceiling there (54/64 overlapped, 45/64 complete on the reviewed
holdout-shaped split with one deterministic follow-up read).

## What changes, in one sentence

The measured candidate object becomes a **transcript of at most two
responses**: the one `task_context/2` response as today, plus at most one
exact read of the span that response itself designates; both responses are
charged in full; recall is scored over both.

Everything else — the 1,200-token ceiling on the `task_context/2` response,
`GrepRead/2` with its eight 40-line reads, the paired-median estimand, the
Clopper-Pearson floor and the resulting `k = 56 of 64`, the rubric, the
tokenizer — is unchanged.

## The designation is the product's, not the reader's

`task_context/2-compact/13` carries an optional `followup` citation in its
structured content:

```json
"followup": "command.go:820-939"
```

It is present only when the **lead** (first emitted) source is a cut window
into a larger unit — a Go declaration from its doc comment to its closing
brace, or a Markdown section from its heading to the next heading of the
same or higher level — and it names that unit from its start, capped at
`FollowupMaxLines = 120` lines. It is computed from repository bytes and the
emitted lead only; no query identifier, judgement or target span can reach
it (`followup_test.go` pins each of these properties).

The rule is deliberately one read of the lead's unit. On the reviewed split
the alternative — read the first cut declaration in emitted order — issued
five times as many reads for the same span counts.

Under this contract the reader is **not** free to choose: the follow-up, if
taken, is exactly the designated span. That is what keeps the transcript
judgement-blind and byte-reproducible. A reader that ignores the designation
has a one-response transcript, which is contract v1.

## Payload boundary for the follow-up

The follow-up is not a graphi MCP response; whoever reads the file produces
it. For measurement it is preserved as exactly one UTF-8 slice:

```
{"path":"<path>","start_line":<n>,"end_line":<m>,"text":"<exact lines n..m>"}\n
```

— Go `encoding/json` of the compact `Source` shape, newline-terminated,
labelled `sequence 2`, boundary `candidate`, operation
`task_context/2-followup-read/1`. `text` must equal the repository bytes at
`path:n-m` in the pinned tree, and `path`, `n`, `m` must equal the
designation in slice 1. A transcript whose slice 2 differs from its slice
1's designation, cites a span slice 1 did not designate, or exceeds
`FollowupMaxLines` is invalid.

Both slices carry SHA-256, byte count and both tokenizer counts exactly as
`sw266`/1 requires for every preserved response.

## Charging and the earliest-prefix rule

Slice 1 is the first indivisible prefix, slice 2 the second. The equal-recall
scorer credits reviewed grade-3 spans over verified emitted source bytes as
in `sw266`/1, now over both slices:

- if slice 1 alone reaches the query's frozen recall target, `tokens_to_target`
  is slice 1's real-tokenizer count and slice 2 is not charged, even if it
  was read;
- otherwise, if slices 1 and 2 together reach it, `tokens_to_target` is their
  sum;
- otherwise the query is a miss, right-censored at the sum of both slices
  (or at slice 1 alone when no follow-up was designated).

`GrepRead/2` is charged exactly as before. The paired value stays
`100 * (G - C) / G` with `C` now the candidate transcript's charged prefix.

## What the ceiling claim means afterwards

"≤ 1,200 `cl100k` tokens per serialized MCP response" remains a property of
the `task_context/2` response and is still enforced by the projector's
backoff loop against the real tokenizer. The follow-up is bounded in
**lines**, not tokens; its cost is reported (count of reads, median and
maximum tokens) beside the estimand, and the transcript total may exceed
1,200. No sentence under this contract may describe the *transcript* as
within the ceiling.

## Rubric packet (`sw280`/2)

The blind grading packet for a query contains both slices, in order,
labelled `response 1 of 2` and `response 2 of 2` (or `response 1 of 1` when
none was designated). The grader's instructions, grades and the pass rule
are unchanged; the follow-up is graded as emitted source bytes like any
other. The precondition record gains the contract version and
`FollowupMaxLines`.

## Pre-registration under this contract

A holdout pre-registered under contract 2 records: contract versions
`sw266`/2 and `sw280`/2, the candidate SHA, compact version
`task_context/2-compact/13` or later, `FollowupMaxLines`, the same `k`
rule (smallest count whose exact Clopper-Pearson 95 % lower bound is
≥ 3/4, i.e. 56 of 64) and the answer-span ceiling `F` computed by the
curator under `answer-span-ceiling/1`. Nothing about `k` is derived from
this contract; a lower bar is not what it buys.

## What must not happen

- No sealed or running evaluation is rescored under this contract.
- No holdout captured under contract 1 is extended with follow-up reads.
- No adoption by editing this file: adoption is a versioned change to
  `methodology.md` with its own tests, review and slice.

## Enforcement (this slice)

| Rule | Status |
|---|---|
| `followup` designation: lead only, whole unit, 120-line cap, absent when the lead is whole, absent without repository or on unreadable/unparsable input | enforced — `engine/agenttools/taskctx/compact/v9/followup_test.go` |
| Wire form is one `path:start-end` citation, round-trips exactly, rejects malformed citations, and is omitted when absent | enforced — same |
| Response ceiling unchanged with the field present | enforced — projector backoff loop; observed by `TestDraftDevForecast` / `TestProductCompactTaskContextDev` |
| Slice 2 is built from the designation alone (`CaptureFollowupRead`): sequence 2, `task_context/2-followup-read/1`, one newline-terminated source line, both tokenizer counts | enforced — `internal/eval/retrieval/followup_transcript_test.go` |
| Two-slice transcript validation: designation equality, verbatim pinned bytes, one line, no unknown fields, operation and sequence labels, 120-line cap, at most two slices, no slice 2 without a designation | enforced — same (`ScoreTaskContextTranscriptEqualRecallDev`, development scorer) |
| Earliest-prefix charging: slice 1 alone when it reaches; both slices when the read reaches; censored at both on a miss | enforced — same |
| Two-call development measurement uses that one implementation | instrument — `TestDraftDevForecast`, `TestProductCompactTaskContextDev`, `TestOneSpanCompactDev` (`GRAPHI_ONE_SPAN_FOLLOWUP=1`) |
| The same scorer in the release aggregate (`SavingsAggregateInput` with two-slice candidate arms) | **UNENFORCED** — the release path still calls the contract-1 scorer; wiring it is part of adoption |
| Rubric packet with both slices | **UNENFORCED** — must be built before adoption |
