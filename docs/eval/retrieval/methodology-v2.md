# SW-266 measurement contract, version 2: second-response transcript

Status: **adopted alternative version, `sw266-measurement-contract/2` with
`sw280-qrel-blind-smoke-evaluation/2`.** It lives in its own file because
`methodology.md` is a frozen input of the sealed evaluations and is hashed at
rest; nothing in that file changes. Every rule of version 1 that this file
does not restate applies unchanged. A pre-registration under version 2
freezes this file under the `methodology` input role (it incorporates
version 1 by reference) and names both version-2 contract strings together
with `followup_max_lines: 120`.

Version 2 is an alternative estimand, not a replacement default. Version 1 remains the frozen
default described in `methodology.md`, and a pre-registration must explicitly name both
`sw266-measurement-contract/2` and `sw280-qrel-blind-smoke-evaluation/2`, together with
`followup_max_lines: 120`, before the alternative may be used. A partial mixture of version-1
and version-2 fields is invalid.

The candidate measured object becomes a transcript containing the one complete
`task_context/2` response plus at most one read that response itself designates. When present,
the read is exactly the designated repository span, capped at `compact/v9.FollowupMaxLines`, and
is preserved as one newline-terminated JSON `Source` line at sequence 2, boundary `candidate`,
under operation `task_context/2-followup-read/1`. The read is a second indivisible payload
boundary. Its SHA-256, byte count, whitespace-token count, real-tokenizer count and vocabulary
identity are frozen and checked independently of slice 1. No qrel, judgement, answer span or
query identifier chooses the read.

Equal recall is scored over the transcript by the same whole-grade-3-span rule as version 1. The
earliest prefix is charged: if slice 1 reaches the frozen target, only slice 1 is charged even
when slice 2 was preserved; otherwise a reached observation is charged the sum of slices 1 and
2. A miss is right-censored at that same sum, or at slice 1 alone when no follow-up was
designated, and still makes every magnitude aggregate unavailable. `GrepRead/1`, the paired
value, the population and the confidence method do not change.

The sentence “at a 1,200-token budget” continues to describe only the serialized
`task_context/2` response. The follow-up read is line-bounded rather than token-bounded, its cost
is charged and reported separately, and the transcript total may exceed 1,200 tokens. No claim
under version 2 may describe the complete transcript as being within the response ceiling.

| Under version 1 the candidate preserves exactly one complete `task_context/2` response; under version 2 it may additionally preserve the one designated follow-up payload. GrepRead preserves initial grep plus reads and terminates only at exhaustion/`MaxReads`. | Candidate/artifact shape: `TestValidateSavingsAggregateInput_RejectsReconstructedPayloadsAndCountDrift`, `TestValidateSavingsAggregateInput_SecondResponseCandidateShapes`; actual GrepRead producer: `TestGrepRead_HandComputedGolden`, `TestGrepRead_SearchAndReadCapsAreDeterministic`. |

## Enforcement matrix (version 2 additions)

"Enforced" means a named test executes the rule.

| Rule | Enforcement |
|---|---|
| Under version 1 the candidate preserves exactly one complete `task_context/2` response; under version 2 it may additionally preserve the one designated follow-up payload. GrepRead preserves initial grep plus reads and terminates only at exhaustion/`MaxReads`. | Candidate/artifact shape: `TestValidateSavingsAggregateInput_RejectsReconstructedPayloadsAndCountDrift`, `TestValidateSavingsAggregateInput_SecondResponseCandidateShapes`; actual GrepRead producer: `TestGrepRead_HandComputedGolden`, `TestGrepRead_SearchAndReadCapsAreDeterministic`. |
| The version-2 measurement literal is frozen with the designated operation and imported 120-line cap; neither version accepts fields from the other. | `TestSecondResponseMeasurementContract_IsFrozen`, `TestValidateMeasurementContract_RefusesPartialVersionCombinations` |
| The qrel-blind version, measurement version and follow-up line cap are coupled across the precondition and pre-registration; version 1 refuses every version-2-only field. | `TestQrelBlindSmoke_PreconditionVersionTwoCoupling`, `TestQrelBlindSmoke_PreRegistrationVersionTwoCoupling` |
| A captured `followup_read` is accepted only under version 2 and only when slice 1 designates it; its SHA-256, byte count and token counts are emitted into the pre-registration and must remain byte-identical before sealing and again before deciding. | `TestValidateCapturedTranscript_ContractVersionCoupling`, `TestPreRegisteredQueryFromBundleBindsFollowup`, `TestCheckPreRegisteredBundleBinding_EnforcesFollowupPresenceAndIdentity`, `TestCheckPreRegisteredCapturedBundlesRejectsFollowupBindingDrift` |
