# Blind-evaluation path research for embedded-model qualification

Date: 2026-09-19

## Executive conclusion

`cmd/retrieval-eval -blind-eval` is **not an end-to-end producer for the
embedded-model qualification's seven `BlindEvidenceSet` and 448
`BlindDecision` records**. The qualification deliberately reuses the older
blind-evaluation record types and decision rules, but no shipping command
adapts the qualification capture into seven blind-evaluation runs or exports
the two arrays accepted by `embedded-model-qualification finalize`.

Worse, the existing blind-eval CLI cannot be used unchanged as that missing
adapter. It has several hard incompatibilities that can be proved before any
rater is called:

1. it selects only answerable `holdout` rows, while the qualification accepts
   exactly 64 `dev` rows;
2. it writes the SHA-256 of each **complete per-query prompt** into
   `PreRegisteredQuery.prompt_sha256`, while the qualification requires that
   field to equal one shared SHA-256 of only `RaterInstructions`;
3. it records the commit containing the precondition record, while the
   qualification requires that commit to equal the earlier frozen candidate
   commit;
4. it captures one ordinary candidate selector per run and has no mode for the
   four qualification arms plus the three already-captured oracle controls;
5. it emits a run directory and `outcome.json`, not `BlindEvidenceSet[]` or
   `BlindDecision[]`.

There is also a separate late-failure defect in the already captured
qualification evidence: capture records `method_version` as
`"task_context/2-compact/17"`, but the finalizer requires it to equal
`QualificationCompactVersion == "compact/17"`.

The practical recommendation is to stop before dispatching rating. First add
or specify a qualification-specific preflight/export path and prove it on
captured bytes with no model calls. The present path is incomplete and the
artifacts produced by the generic blind-eval CLI would not pass the current
qualification validator.

## 1. Is `retrieval-eval -blind-eval` the right path?

### What is shared

The qualification embeds the generic blind-evaluation types rather than
inventing new response and grade formats:

- `BlindEvidenceSet` contains a generic `PreconditionRecord`,
  `PreRegistration`, `[]RaterResponse`, `[]Grade`, and `[]Adjudication`
  (`internal/eval/retrieval/model_qualification_stats.go:39-50`).
- The final decision embeds the generic `QueryOutcome`
  (`internal/eval/retrieval/model_qualification_stats.go:52-65` and
  `internal/eval/retrieval/blindeval.go:1289-1312`).
- Qualification validation calls the generic `CheckPrecedence`,
  `CheckGradeBinding`, `CheckAdjudicationOrder`, and `DecideQuery`
  (`internal/eval/retrieval/model_qualification_stats.go:815-856`; their
  implementations begin at `internal/eval/retrieval/blindeval.go:1033`,
  `:1100`, `:1169`, and `:1327`).

Thus the generic sealing machinery is relevant, but this is type-level reuse,
not proof that the generic CLI produces qualification inputs.

### The two required top-level JSON schemas

`finalize` strict-decodes two top-level JSON arrays, rejecting unknown fields
and trailing JSON (`internal/eval/retrieval/model_qualification_finalize.go:52-65,119-132`).
Their exact element shapes are:

```json
{
  "arm": "M0_lexical | M1_potion_512 | M2_potion_8192 | M3_coderank",
  "control_kind": "current_candidates_oracle_packer | oracle_candidate_current_selector | oracle_candidate_oracle_packer",
  "precondition": { "...": "PreconditionRecord" },
  "pre_registration": { "...": "PreRegistration" },
  "responses": [{ "...": "RaterResponse" }],
  "grades": [{ "...": "Grade" }],
  "adjudications": [{ "...": "Adjudication" }],
  "sha256": "content address of this wrapper"
}
```

Exactly one of `arm` and `control_kind` must be non-empty. The nested schemas
are declared at `internal/eval/retrieval/blindeval.go:262-290` (precondition),
`:468-529` (pre-registration and participants), `:692-715` (response),
`:789-803` (grade), and `:848-896` (adjudication).

```json
{
  "arm": "...",
  "control_kind": "...",
  "query_id": "cd-08",
  "stratum": "...",
  "payload_sha256": "...",
  "reader_prompt_sha256": "...",
  "grader_prompt_sha256": "...",
  "outcome": {
    "query_id": "cd-08",
    "stratum": "...",
    "primary": [],
    "primary_disagreement": false,
    "adjudicated": false,
    "outcome": "pass | fail",
    "reason": "..."
  },
  "evidence_sha256": "the enclosing BlindEvidenceSet.sha256",
  "sha256": "content address of this decision"
}
```

The wrapper sealers clear their own `sha256`, hash the compact Go JSON
encoding, and restore the hash; a decision additionally receives the enclosing
evidence-set hash (`internal/eval/retrieval/model_qualification_stats.go:67-86`).

By contrast, generic `retrieval-eval decide` writes `outcome.json`, whose
top-level schema is `EvaluationOutcome`; it contains `queries: []QueryOutcome`
but has no `arm`, `control_kind`, payload/prompt bindings, evidence-set address,
or per-decision address (`internal/eval/retrieval/blindeval.go:1454-1481` and
`cmd/retrieval-eval/blindeval.go:509-536`). A historical example is
`docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/outcome.json`.

### Why there are 7 and 448

The four arm identifiers are defined at
`internal/eval/retrieval/model_qualification.go:87-96`; the three oracle
control identifiers are at
`internal/eval/retrieval/model_qualification_oracle.go:27-31`. The validator
computes `(4 + 3) * 64 == 448` and requires 64 unique decisions per subject
(`internal/eval/retrieval/model_qualification_stats.go:699-768`). It also
requires exactly one evidence set per subject and exactly 64 pre-registered
queries in each set (`:771-860`). The finalizer repeats the coarse 7/448 count
before evaluation (`internal/eval/retrieval/model_qualification_finalize.go:52-65`).

The stale comment at
`internal/eval/retrieval/model_qualification_preregistration.go:203-207` says
“all eight subjects”; executable code and report tests require seven
(`internal/eval/retrieval/model_qualification_report_test.go:34-63`).

### Hard incompatibilities with the current CLI

#### Population: holdout versus development

Generic capture calls `AnswerableHoldout` and derives `N` from only answerable
holdout rows (`cmd/retrieval-eval/blindeval.go:230-249`;
`internal/eval/retrieval/blindeval.go:92-158`). Qualification instead rejects
anything except exactly 64 `split == "dev"` rows
(`internal/eval/retrieval/model_qualification.go:1213-1275`).

This is not hypothetical. The dataset whose digest is pinned in the current
qualification preregistration is
`docs/eval/retrieval/drafts/2026-09-14-holdout-shaped-dev/dataset.json`: it has
64 queries, all `dev`, and SHA-256
`33760861d5c78f551d4203342f2e3b7350e030882bd74ab6ce166d00ce8168ba`,
matching
`docs/eval/retrieval/runs/embedded-model-qualification/preregistration.json:33`.
Generic capture therefore finds no answerable holdout population. Relabelling
the rows is not a workaround because it changes the dataset bytes and digest.

#### Prompt identity: full prompt versus instruction template

Generic capture constructs a different complete prompt per query and stores
`prompt.SHA256` in each `PreRegisteredQuery`
(`cmd/retrieval-eval/blindeval.go:343-372,408-425`). `BuildRaterPrompt` hashes
instructions + question + marker + exact payload bytes
(`internal/eval/retrieval/blindeval_prompt.go:80-123`). `decide` reconstructs
those bytes and rejects a different pre-registered prompt hash
(`internal/eval/retrieval/blindeval_rundir.go:357-430`).

Qualification preregistration instead sets `reader_prompt_sha256` to
`SHA256(RaterInstructions)` alone
(`internal/eval/retrieval/model_qualification_preregistration.go:203-207`),
then requires every source pre-registration query's `prompt_sha256` to equal
that one constant (`internal/eval/retrieval/model_qualification_stats.go:837-841`).
The current pin is
`9e6f38c03e1e21e0c71d1d646ac13cd1073116b567cfebfa05b0e8522b1faccb`
(`preregistration.json:35`). One already generated complete oracle prompt,
`captures/build-1/M3_coderank/oracle/cd-08/current_candidates_oracle_packer/grader-prompt.txt`,
hashes instead to
`4462b42a84fc53bd8648673798018174fc43b94a299e8ce3f422c5192f1f92b5`.

Therefore the same `PreRegisteredQuery` cannot satisfy both generic
`CheckPromptBinding` and qualification `validateBlindEvidenceSets`. The unit
fixture hides this contradiction by directly assigning the instruction-block
digest as `PromptSHA256`
(`internal/eval/retrieval/model_qualification_stats_test.go:730-735`) rather
than using `BuildRaterPrompt`.

#### Precondition commit identity

Generic capture intentionally records the commit that contains
`precondition-record.json` as `precondition_record_commit`
(`cmd/retrieval-eval/blindeval.go:319-338`). Qualification requires this field
to equal `QualificationPreregistration.candidate_sha`
(`internal/eval/retrieval/model_qualification_stats.go:808-810`), while also
requiring the precondition's `candidate_sha` and `freeze_commit` to equal that
candidate (`:796-804`). In the normal generic workflow the precondition is
written after the frozen candidate exists and committed later, so its
containing commit is different. The historical run documents exactly that
ordering correction in
`docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/METHOD.md:186-193`.
Again, the qualification test fixture simply assigns candidate SHA to both
fields (`internal/eval/retrieval/model_qualification_stats_test.go:686-708`).

#### Subject and artifact layout

Qualification capture already invokes the shared lower-level
`CaptureCandidateBundles` with injected per-arm embedders and writes two builds
for each of four arms (`internal/eval/retrieval/model_qualification.go:824-926`).
Build 1 contributes 256 ordinary subject/query payloads. M3/build 1 additionally
contains 192 oracle payloads, materialized as 64 queries times three controls
(`internal/eval/retrieval/model_qualification_oracle.go:454-541`). Together
those are the 448 payloads that must be blind-rated.

Generic `retrieval-eval capture` instead accepts one `-embedder`, recaptures one
population, and writes one `bundles/<query>.json` namespace
(`cmd/retrieval-eval/blindeval.go:202-405`). It cannot select the lexical M0
because its capture phase requires a non-empty `-embedder` (`:202-205`); it has
no flags for the qualification's injected M1/M2 profiles or development-only
CodeRank sidecar; and it has no oracle-control subject mode.

The physical schemas differ too:

- normal qualification bundles live nested in each `capture.json`/
  `captures.json` as `CapturedCandidateBundle`;
- oracle `artifact.json` has keys `control_kind`, `query_id`,
  `candidate_provenance`, `candidate_sha256`, `injected`, `injected_rows`,
  `payload_sha256`, `real_token_count`, and `payload`
  (`internal/eval/retrieval/model_qualification_oracle.go:462-472`);
- generic seal strictly loads `bundles/<query>.json` as
  `CapturedCandidateBundle` (`cmd/retrieval-eval/blindeval_seal.go:442-451`).

So even the existing oracle files are not drop-in generic bundle files. An
adapter must extract the `OracleBundle` data from M3/build 1, construct the
generic bundle/prompt package, and add the missing subject identity externally.

#### Existing capture has an independent method-version mismatch

Qualification pins `QualificationCompactVersion = "compact/17"`
(`internal/eval/retrieval/model_qualification.go:28-35`) and final validation
requires capture provenance `method_version` to equal that string
(`internal/eval/retrieval/model_qualification_stats.go:586-591`). But shared
capture writes `taskcompact.Version` (`internal/eval/retrieval/blindeval_capture.go:618-636`),
which is `"task_context/2-compact/17"`
(`engine/agenttools/taskctx/compact/v9/compact.go:29`). The current artifact
confirms that value at
`docs/eval/retrieval/runs/embedded-model-qualification/captures/build-1/M0_lexical/capture.json:20,68`.

This mismatch is unrelated to grading and would make finalization fail even if
valid 7/448 blind arrays appeared.

## 2. Which work is mechanical and which is manual/model work?

The current qualification capture has already mechanically produced the 448
payloads to be judged: 4 arms x 64 plus 3 oracle controls x 64. The missing and
manual stages are often understated because “448 decisions” is not “448 model
answers.” The generic contract requires exactly two primary raters per query
(`internal/eval/retrieval/blindeval.go:590-608`). Therefore a full run requires:

| Stage | Count in the qualification | Producer |
|---|---:|---|
| Subject/query bundles | 448 | Already produced mechanically by qualification capture |
| Primary answer texts | 896 | Human or model raters, two per bundle |
| Primary grades | up to 896 | Human or model grader, one per answered response |
| Adjudicator answer + grade | 0..448 | Human or model adjudicator/grader, only on primary pass/fail disagreement |
| Final `QueryOutcome` | 448 | Mechanical `DecideQuery` |
| `BlindEvidenceSet` wrappers | 7 | Missing production adapter/exporter |
| `BlindDecision` wrappers | 448 | Missing production adapter/exporter |

The generic tool itself never invokes a rater, grader, or adjudicator. Those
actors write plain text under `responses-raw/`, `grades-raw/`, and
`adjudications-raw/`. `-blind-eval seal` then mechanically:

1. derives response status and seals `RaterResponse` records;
2. creates grader packets containing the rubric, answer key, exact bundle, and
   response;
3. parses raw grades beginning with `PASS` or `FAIL` plus rationale and seals
   them;
4. seals adjudications after the necessary response/grade hashes exist.

The code is explicit at `cmd/retrieval-eval/blindeval_seal.go:1-26,120-283`,
with response sealing at `:302-351`, grade sealing at `:354-387`, and grader
packet construction at `:461-542`. The historical method record describes the
human/model dispatch boundary at
`docs/eval/retrieval/runs/2026-09-05-sw280-qrel-blind-smoke/METHOD.md:42-93,149-159`.
That 64-query historical run contains 128 primary raw/sealed responses, eight
adjudicator responses, 136 grades, and eight adjudications under the respective
directories. It is direct evidence that the command seals externally produced
text; it does not generate the judgments.

After sealing, `DecideQuery` mechanically combines the two primaries and, only
for an answered pass/fail split, the adjudicator
(`internal/eval/retrieval/blindeval.go:1314-1365` and following). Qualification
then independently recomputes every decision from the nested source evidence
and compares it byte-for-byte to the supplied `BlindDecision.outcome`
(`internal/eval/retrieval/model_qualification_stats.go:719-756,870-885`).

## 3. Is relevant logic hidden in tests or not connected to a CLI?

Yes, in two different senses.

First, `seal` is a real CLI phase but the public flag help omits it:

- executable phase set: `freeze`, `capture`, `seal`, `decide`
  (`cmd/retrieval-eval/blindeval.go:43-55,70-89`);
- flag help says only `freeze | capture | decide`
  (`cmd/retrieval-eval/main.go:135-138`);
- the nearby comment also incorrectly says “exactly these three values.”

Second, the qualification-specific assembly genuinely has no production path.
The only code that constructs a `BlindEvidenceSet`, derives its 64 outcomes,
constructs `BlindDecision` records, and seals both is the test fixture
`qualificationBlindEvidenceFixture`
(`internal/eval/retrieval/model_qualification_stats_test.go:684-782`, helper at
`:795-806`). Both qualification wrapper sealers are unexported
(`internal/eval/retrieval/model_qualification_stats.go:67-86`). A search of
non-test Go code finds no `BlindEvidenceSet{...}` or `BlindDecision{...}`
constructor outside their definitions.

The production finalizer is only a consumer: its CLI requires paths to the
already assembled arrays (`cmd/embedded-model-qualification/main.go:210-224`),
and `FinalizeQualification` strict-loads them
(`internal/eval/retrieval/model_qualification_finalize.go:23-71`). The operator
guide merely states that they exist and passes their paths to finalize; it does
not give a production command to create them
(`docs/eval/retrieval/embedded-model-qualification.md:167-183`). The plan says
to use the existing workflow and “import decisions” but names no implemented
importer (`docs/superpowers/plans/2026-09-16-embedded-model-dev-qualification.md:1145-1147`).

At the time of this research,
`docs/eval/retrieval/runs/embedded-model-qualification/` contains the
preregistration and eight-capture tree, but no `blind-evidence.json`,
`blind-decisions.json`, or final qualification report.

## 4. Earliest mismatch proof, sequence, and realistic effort

### Prove compatibility before rating

The cheapest safe sequence is:

1. **Do not dispatch any rater yet.** The dev/holdout split contradiction,
   prompt-hash contradiction, precondition-commit contradiction, and
   method-version mismatch are deterministic and need no model output.
2. **Resolve the contract choices explicitly.** Decide whether the qualification
   should bind the complete per-query prompt hash or a separate instruction
   template hash; decide what `precondition_record_commit` is meant to prove;
   and make the compact-version spelling consistent. These are schema semantics,
   not adapter plumbing.
3. **Build/specify a qualification preflight/exporter that consumes the existing
   capture, not a second retrieval capture.** It should enumerate exactly seven
   subjects, extract exactly 64 payloads for each, verify payload SHA against
   build-1 observations/oracle evidence, generate the actual prompts, and emit
   the seven precondition/pre-registration packages plus an import manifest.
4. **Run it in a throwaway directory with zero real answers.** Mechanically seal
   missing responses or use a tiny synthetic fixture, then prove that the
   exporter can create arrays with the exact schemas and that qualification
   validation reaches only the expected “missing/failed result,” not an identity
   or schema refusal. Do not seal missing responses into the real append-only run.
5. **Perform one complete disposable subject rehearsal** through response,
   grader packet, raw grade, optional adjudication, evidence-set sealing, decision
   export, and strict reload.
6. **Only then dispatch the real 896 primary ratings**, grades, and necessary
   adjudications, followed by the actual seven-set/448-decision export and
   `finalize`.

### Effort estimate

- **Early mismatch proof:** already available from source and current artifact
  bytes; turning it into a repeatable preflight test is roughly hours, not days,
  and requires no rating/model calls.
- **Production-quality missing adapter plus tests/docs:** realistically about
  1–3 focused engineering days, because it must settle three contradictory
  identity semantics, handle seven subjects and two physical bundle layouts,
  preserve append-only sealing, and be tested against the actual capture tree.
  This is not a file-concatenation script.
- **Actual evaluation:** at least 896 primary model/human calls and up to 896
  primary grades, plus disagreement adjudications. Wall time depends on the
  available concurrency and model latency, but the work volume is several times
  the historical 64-query run; it should not start until the zero-rating
  preflight succeeds.

The strongest direct answer is therefore: **a necessary qualification-specific
production path is missing, and the generic blind-eval CLI does not currently
fit the qualification contract.** Discovering that after 448 decisions would
be avoidable; all current blockers are visible before the first response.
