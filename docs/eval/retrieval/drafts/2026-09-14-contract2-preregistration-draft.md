# Pre-registration draft for a fresh holdout under the second-response contract

Status: **draft for the curator; not a pre-registration.** Nothing here is
recorded, sealed or committed as an evaluation input. It exists so that
the curator of the next independent holdout can see, before authoring a
single question, what will be frozen, what tool computes what, and which
pieces of the contract are still missing. It is written by the
candidate's author and therefore contains no holdout content and asks for
none.

## What must exist before this can be executed

The second-response contract (`../contract-v2-second-response.md`) is a
draft. Its enforcement table lists what is built. Before a holdout is
pre-registered under it, all of the following must land in their own
reviewed slice — none of them may be done by the person who tunes the
candidate:

1. Contract version constants `sw266-measurement-contract/2` and
   `sw280-qrel-blind-smoke-evaluation/2`, accepted by
   `ValidatePreconditionRecord` and `ValidatePreRegistration` **only**
   together with a non-zero `followup_max_lines`.
2. `PreRegisteredQuery` binding of slice 2: `followup_sha256`,
   `followup_byte_count`, `followup_token_counts`, required when the
   pre-registered bundle designates a follow-up and forbidden otherwise.
3. The release aggregate (`SavingsAggregateInput`) scoring candidate arms
   through `ScoreTaskContextTranscriptEqualRecallDev` instead of the
   contract-1 scorer, with `ValidateSavingsAggregateInput` accepting a
   two-payload candidate arm only under contract 2.
4. A versioned methodology text that says the same thing as the draft,
   reviewed and adopted.

**Status (2026-09-15): all four have landed** — version constants
`MeasurementContractVersion2` / `QrelBlindSmokeContractVersion2` coupled
to `followup_max_lines = 120`; `PreRegisteredQuery.followup_*` fields bound
to the captured read at capture, seal and decide; `validateSavingsArm`
accepting one- or two-payload candidate arms under version 2 only; and
`methodology-v2.md` (a separate file, because `methodology.md` is a frozen,
hash-pinned input of the sealed evaluations). Version 1 remains the
default; a pre-registration selects version 2 by naming it.

## What the curator freezes, and with which tool

| Item | Rule | Tool / record |
|---|---|---|
| Dataset | 64 answerable questions, one reviewed grade-3 span each, stratum counts as in the reviewed development split (11/11/11/11/10/10), family ids never shared with any development split, authored fresh from the pinned checkout `a0a6ae020bb3899ff0276067863e50523f897370` | curator's own; `CheckSpanCoverage` must pass |
| Answer-span ceiling `F` | computed on the sealed key before capture, aggregate only, recorded with its SHA-256 | `go run ./cmd/retrieval-eval -answer-span-ceiling …` (`answer-span-ceiling-protocol.md`) |
| Pass count `k` | smallest count whose exact Clopper-Pearson 95 % lower bound is ≥ 3/4 — for `N = 64`, `k = 56`; **unchanged** by this contract | `DerivePassCount` |
| Candidate | one commit SHA; compact version `task_context/2-compact/13` or later; `FollowupMaxLines = 120` | precondition record |
| Contract versions | `sw266`/2, `sw280`/2, `followup_max_lines: 120`; `methodology` input role → `methodology-v2.md` | precondition record |
| Capture | per query: slice 1 = the exact `task_context/2` MCP response bytes; slice 2 = `CaptureFollowupRead` of slice 1's designation, present iff designated | `retrieval-eval -blind-eval capture` under a version-2 precondition (writes `followup_read` and the `followup_*` pre-registration fields) |
| Transcript validity | `ValidateCapturedTranscript` on every captured bundle before sealing | seal step (`-checkout` at the pinned SHA) |
| Rater prompt | the exact packet bytes both raters see: both slices when designated | pre-registered `prompt_sha256` |
| Grader packet | both slices, labelled, with both content addresses | `buildGraderPacket` |
| Raters, grader, adjudicator | identities with independence basis; at least one primary rater who took no part in implementation or annotation | `Participant` |

## What is measured and what may be claimed

- Primary: the qrel-blind pass count against `k = 56 of 64`, graded on
  the two-slice transcript.
- Secondary: the paired median token saving against `GrepRead/2` with the
  candidate charged by the earliest-prefix rule (slice 1 alone when it
  reaches; both otherwise).
- Reported beside them, never folded in: number of follow-up reads
  issued, their median and maximum `cl100k` tokens, and the number of
  queries whose transcript total exceeds 1,200 tokens.
- The ceiling sentence stays a property of the `task_context/2`
  response. No claim may describe the transcript as within 1,200 tokens.

## What the candidate's author will and will not receive

Receives: the aggregate ceiling report (`F` and the six counts), the
pre-registration's SHA-256, the outcome, and — after the run is closed —
the per-stratum pass counts. Does not receive: questions, spans, paths,
lines, rater prompts, rater responses, grades, or which queries passed.

## Development evidence the curator may read

`../runs/2026-09-14-compact14-dev/RESULT.md` (reviewed holdout-shaped
split: 59/64 overlapped, 49/64 complete with the read, `P(≥ 56) ≈ 0.94`
at that overlap rate; one response alone 56/64, `P ≈ 0.59`). The
condition this draft first set — hold until two-call overlap reaches 56
of 64 on the reviewed split — is met as of compact/14. What blocks the
holdout now is the contract, not the candidate: the four adoption items
above. A holdout spent under contract 1 today would be graded on one
response and stands at a coin toss.
