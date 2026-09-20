# How `grading-rubric.md` was derived

This note exists so that a reviewer can check the rubric against its precedent
without re-reading nine predecessor rubrics. It is not an input to any run: the
preregistration binds `grading-rubric.md` alone, by SHA-256, and only that file
is embedded in grader packets.

## Predecessors read

Complete, in this order:

- `docs/eval/retrieval/runs/2026-09-15-dev-grading-reviewed/grading-rubric.md`
  (transcript family) and its `RESULT.md`, the calibration run that graded the
  reviewed development split with the holdout pipeline.
- `docs/eval/retrieval/runs/2026-09-15-compact17-fresh-sealed-holdout/grading-rubric.md`
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-fresh-sealed-holdout/grading-rubric.md`
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-second-fresh-sealed-holdout/grading-rubric.md`
- `docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/grading-rubric.md`
  (the first rubric, `sw280-grading-rubric/1`)
- `docs/eval/retrieval/runs/2026-09-15-product-compact-v17-fresh-unseen-v4/grading-rubric.md`
  (the condensed variant)
- `docs/eval/retrieval/runs/2026-09-13-product-compact-v5-dev/FINDING-rubric-was-not-presealed.md`

Diffed rather than re-read, because they are byte-identical to one of the
above: `2026-09-15-product-compact-v14-third-fresh-sealed-holdout` (transcript
family, identical to `2026-09-15-dev-grading-reviewed`),
`2026-09-15-product-compact-v17-fresh-unseen-v2` and `-v3`, and
`2026-09-13-product-compact-v5-second-fresh-sealed-holdout` (all identical to
the compact/v5 bundle rubric).

Also read for the mechanics this rubric has to fit:
`cmd/retrieval-eval/blindeval_seal.go` (`buildGraderPacket`,
`loadFrozenGradingRubric`), `internal/eval/retrieval/blindeval.go`
(`DecideQuery`), `internal/eval/retrieval/blindeval_prompt.go`
(`RaterInstructions`), `internal/eval/retrieval/model_qualification*.go`, and
`docs/eval/retrieval/preregistration.md`.

## Which family the rubric belongs to

The predecessors form exactly two families, differing only in one noun:

- **bundle**-sufficiency: the rater saw one preserved `task_context/2` response.
- **transcript**-sufficiency: the rater saw that response plus one designated
  follow-up read (contract v2, two slices).

The embedded-model qualification captures one MCP `tools/call task_context`
response per query and has no follow-up path (`CaptureQualification` carries no
`FollowupRead`). It is therefore the **bundle** family, and the bundle family's
text is reproduced verbatim.

## Adopted verbatim

The title, the `Status:` paragraph, the "Grade one response …" paragraph, all
five PASS conditions, the paraphrase allowance, every FAIL bullet, the harness
paragraph about missing/empty/refused responses, the whole "Required raw grade
format" section, and the closing "When uncertain … grade `FAIL`" paragraph are
byte-for-byte the frozen bundle rubric
(`f0420f756d3a5742ca684916965921bbb6794010af781aaf129e61ba7f07457c`). No
threshold, wording or emphasis in them was "improved".

## Deviations, and why

**This file is the only record of the four deviations.** An earlier draft
declared them in a closing section of the rubric itself. That section was
removed, deliberately: `buildGraderPacket`
(`cmd/retrieval-eval/blindeval_seal.go:501-513`) embeds the rubric file's ENTIRE
bytes in every grader packet, so anything in it is read by the grader. The
removed section named four predecessor runs, pointed at a finding about an
invalidated run, and stated that this qualification grades several
independently produced bundles per question — none of which a grader needs, and
the last of which is a fact about the experiment's structure that the blinding
exists to withhold. The rubric's own "How a packet was produced is not a
grading input" section forbids acting on such a theory without supplying one.

The rubric therefore contains only what a grader needs to grade. Its
provenance lives here.

1. **Version and freeze paragraph** — restored from `sw280-grading-rubric/1`.
   The compact family dropped it, and
   `FINDING-rubric-was-not-presealed.md` records what that cost: the V5 run's
   seal phase named the run-local rubric path but did not re-read and
   hash-check those bytes before generating irreversible grader packets, so a
   drifted rubric would only have been caught much later, by `decide`. The
   whole V5 holdout was retained as negative evidence. For this qualification
   the same binding exists twice — `grader_prompt_sha256` in
   `preregistration.json`, and role `grading_rubric` in the blind-evidence
   precondition record, which `validateBlindEvidenceSets`
   (`internal/eval/retrieval/model_qualification_stats.go`) requires to equal
   the preregistered digest for each of the seven subjects — so the file states
   that contract itself.
   Note the authoring order this implies: the rubric is committed *before* the
   preregistration is derived (step 3 of "Order of operations" in
   `docs/eval/retrieval/preregistration.md`). And note what does *not* catch a
   later edit: `candidate_diff_sha256` is computed over the candidate worktree
   *excluding* this run directory, so a rubric edit — even an uncommitted one —
   does not move it and does not dirty the candidate binding. The only things
   that bite are `grader_prompt_sha256` and the precondition record's frozen
   `grading_rubric` digest, which is exactly the pair the V5 finding says must
   be checked before packets are generated.
2. **`INSUFFICIENT` paragraph** — restored from `sw280-grading-rubric/1`
   ("the response says it cannot answer (`INSUFFICIENT`), which is an honest
   answer and a failed one"). The frozen `RaterInstructions` explicitly invite
   that answer, the calibration run
   (`2026-09-15-dev-grading-reviewed/RESULT.md`) saw 11 of 128 primary items
   answer that way, and `DecideQuery` returns a hard error for an *answered*
   response that carries no grade. A grader who reads `INSUFFICIENT` as "not
   answered" and skips it would break finalization. The verdict is unchanged
   from the compact family, which already fails it as "materially incomplete,
   evasive or does not answer the question".
3. **"The same conditions for every question"** — new. The population is 64 dev
   queries over `spf13/cobra` in six strata (`ambiguous` 10,
   `architecture_flow` 11, `config_docs` 10, `exact_identifier` 11,
   `exact_path` 11, `nl_behaviour` 11) and the gate is one count over all of
   them. A criterion that only bites in one stratum would reweight the others
   without saying so, so the rubric states that there is no per-category
   criterion and that no particular *shape* of answer (path, symbol, line
   range, prose) is required beyond conditions 1-5.
4. **"One packet at a time" and "How a packet was produced is not a grading
   input"** — new, and the deviation that matters most. Every predecessor
   graded one system against one dataset; this run grades several
   independently produced bundles per question, and the grader is blinded to
   which produced which. Two consequences the predecessors never had to
   address:
   - the same question text reaches the grader repeatedly, inviting
     cross-packet comparison, ranking or count-balancing;
   - the preserved bytes themselves are not neutral. The `provenance` block of
     a `task_context/2` response carries `weights`, `model`, `retrieval`,
     `source_selection` and budget fields, which differ systematically between
     producers. The rubric therefore declares provenance and identity metadata
     to be neither evidence nor a grading input, and forbids using the bundle's
     shape, length or ordering to theorize about origin.

   Both sections are written so that they name no arm, no model, no producer
   count and no quality expectation — a rubric that explained *what* to avoid
   inferring would itself be the leak.

## Deliberately not added

- **No mention of the 1,200 cl100k_base token budget, of `compact/17`, or of
  the `56/64` threshold.** The budget is already handled by the frozen closing
  paragraph ("this gate measures bundle sufficiency"); naming the threshold
  invites strategic grading, and naming the compact version is an origin hint.
- **No "a partial window is graded as absent" clause.** The calibration run's
  reading 1 found the graders already apply PASS condition 3 that way under the
  frozen rubric (49 complete spans → 48 both-pass; overlapped-but-incomplete
  spans failed with "omits `X` identified as essential by the reviewed
  spans"). Writing the observed behaviour into the rubric would restate a
  criterion the predecessors' graders applied without it, and would risk making
  this rubric strictly harder than the one the `56/64` figure comes from.
  Left out as precedent-faithful; recorded here because it was a real choice.
- **No stratum-specific guidance, no examples, no per-category thresholds.**
- **Nothing about arms, models, sidecars or oracle controls.**

## For the human reviewer

Three points where a second opinion is worth having:

1. **Comparability.** The added sections are procedural, not criteria, so the
   pass bar should be the same as the holdouts'. If a reviewer judges that any
   of them tightens or loosens the bar, the `56/64` threshold's inherited
   meaning is affected and the rubric should be cut back to the verbatim
   predecessor plus deviation 1.
2. **The declared-deviations section travels into every grader packet**, since
   `buildGraderPacket` embeds the whole file. It was written to be inert
   (procedural reasons only, no arm-revealing content), but it is prompt text
   the grader reads. Dropping it into this note instead is a defensible
   alternative; it was kept in the rubric because the brief asked for
   deviations to be marked in the document itself.
3. **`INSUFFICIENT` wording.** It is reinstated precedent, not new policy, but
   it is the one place where this rubric says out loud what the compact family
   left implicit.
