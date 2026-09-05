# SW-279 Phase 2 report — the re-harvest, the cost of the violation, and the yield

This report covers the whole of Phase 2 as re-executed after
`projects/graphi/stories/SW-279/decision-transport-overfetch.md` ruled the first run
NON-COMPLIANT. It is written to be read by someone who wants to disbelieve it, so every number
below names the artefact it comes from and the command that reproduces it.

## The disclosure that has to come first

**Phase 2a was re-harvested because the first run fetched labels, comments, reactions, state,
assignees and milestones in violation of §1 of the frozen inclusion rule.** §1 says those fields
"must not be fetched or read for selection"; the ruling held that the prohibition binds the fetch,
not only the use.

**The violation was disclosed by the selector, not discovered by audit.** The first run's own access
ledger records, in its second row, that the connector returned broad envelopes and that the extra
fields were discarded. Nothing would have surfaced it otherwise. That is worth saying plainly
because the control that caught this was an implementer writing down a thing that counted against
it.

**The superseded harvest is retained in the repository for inspection**, unedited, at
`docs/eval/retrieval/harvests/sw-279-phase-2a-superseded/`, together with the first family review at
`sw-279-phase-2b-family-review/`. §8 requires an append-only record; deleting the evidence of a
disclosed violation would be a worse act than the violation.

## What is different about this harvest

| the first run | this run |
|---|---|
| issue text arrived through a connector returning whole envelopes | a committed script whose GraphQL selection set is exactly `nodes { number createdAt title body author { login } }` (`scripts/eval/fetch_cobra_issue_text.py`) |
| the ledger's compliance claim was a sentence written by the actor | the ledger row is emitted by the program that made the access (`scripts/eval/_access_ledger.py`) and quotes the selection set literally |
| the population fetch had no code artefact at all | `scripts/eval/fetch_cobra_issue_population.py` ran, refused on the count check, and wrote its own record |
| `created_at` was null in 1,255 of 1,255 rows | non-null in 1,256 of 1,256; the script refuses to write otherwise |
| `prepare_…py` read `issue-text.jsonl` as an input it had not written, then overwrote it in place | it reads `issue-text-raw.jsonl`, which only the fetch script writes, and refuses to overwrite its outputs |
| the 139-row semantic verdict was a dict literal in the finalizer's source, edited by the actor that had held the envelopes | the verdict is a file written by an isolated classifier that never touched the network; the finalizer validates and materialises it |
| one actor spanned fetch and classification, 19 minutes apart, with no isolation recorded | the fetching actor and the classifying actor are different, and the classifier's brief, digests and attestation are committed |

A REST issue response cannot express the narrow selection: it always carries `labels`, `state`,
`assignees`, `milestone` and `reactions`. GraphQL selects fields, so the compliance argument lives
in a selection set a reviewer can read rather than in a sentence.

## The three checks, and how each came out

### 1. Population — the check fired

**Expected 1,255 with `issue_numbers_sha256 == b9f712af…4a17`. Observed 1,256 with
`2c35bf71…4de4`.** `fetch_cobra_issue_population.py` exited 3 and refused to continue to the text
fetch, which is what the check is for.

Resolved without reading any issue text, and recorded before anything about the extra row's content
was known to anyone: `population-discrepancy-decision.md`, with the machine evidence in
`population-discrepancy-record.json` and `population-instrument-probe.json`.

- The difference is exactly one issue, **#1036**, created `2020-02-17T20:49:06Z` — six and a half
  years before the cutoff. The new manifest is a **strict superset**: nothing was dropped.
- The cause is named, not assumed. The superseded run used the **GitHub Search API**; this one uses
  the GraphQL **`issues` connection**. A per-year comparison of the two instruments agrees exactly
  in all fourteen creation years **except 2020**, where the connection sees 180 and Search sees 179.
  Every Search count equals the superseded run's own recorded `year_counts`, so that run
  transcribed Search faithfully — Search is what is wrong.
- Two competing explanations are excluded. **Not a pagination cap**: Search reports only 37 issues
  in `2020-01-01..2020-03-31`, nowhere near the 1,000-result limit. **Not a transcription slip**: a
  slip would be uncorrelated with the instrument, but the shortfall is exactly one row in exactly
  the year the instrument is short.
- The disposition applies §1's *definition* ("every GitHub object that GitHub classifies as an issue
  … that existed at the rule commit's committer timestamp") over its *expectation* ("the 1,255
  issues recorded by SW-279"), because the expectation was calibrated against the run already found
  non-compliant.
- **The change cannot have moved the candidate set.** #1036 fails the §2 syntactic gate on
  `S2_FIRST_TOKEN` and never reaches the semantic classifier.

### 2. Mechanical pass — reproduced

**Expected 1,116 rejects and 139 eligible over 1,255 rows. Observed 1,117 rejects and 139 eligible
over 1,256** — the one extra reject is #1036, and **the 139 eligible issue numbers are identical to
the superseded run's**. All 1,255 shared title and body digests match the superseded archive byte
for byte, so the first run's *text* was faithful; only `created_at` was lost.

### 3. Candidate set — did not reproduce, and this is the measured cost

**Expected the superseded 66. Observed 94.**

The two classifications agree on **107 of 139 rows** and differ on **32**, and the difference is
almost entirely one-directional:

- **30 rows the superseded run rejected are candidates under the re-run**;
- **2 rows it accepted are rejects under the re-run** (#891, E2; #957, E3);
- 64 candidates are common to both.

Every differing row, with the clause that decided it in the run that rejected it:

| the superseded run rejected → the re-run accepts (30) | | |
|---|---|---|
| issue | superseded clauses | flagged by the re-run as a boundary case? |
| 298 | E1 | yes |
| 678 | E1, E4 | yes |
| 689 | C2, E4 | no |
| 710 | E4 | no |
| 724 | E1, E4 | yes |
| 725 | E1, E4 | no |
| 829 | E1 | yes |
| 835 | C3 | yes |
| 852 | E4 | no |
| 943 | E1, E4 | yes |
| 1025 | E1, E4 | no |
| 1098 | E1, E4 | no |
| 1120 | E1 | no |
| 1151 | C3, E4 | no |
| 1168 | E4 | no |
| 1221 | E1, E4 | yes |
| 1236 | E4 | no |
| 1289 | E4 | no |
| 1416 | E4 | no |
| 1466 | C2, E4 | yes |
| 1521 | E2 | yes |
| 1631 | C3, E1 | yes |
| 1861 | E1, E4 | yes |
| 1894 | C3 | no |
| 1923 | E4 | yes |
| 2007 | E1, E4 | yes |
| 2014 | E1, E4 | no |
| 2138 | E1, E4 | no |
| 2160 | E4 | no |
| 2249 | E1, E4 | yes |

| the superseded run accepted → the re-run rejects (2) | | |
|---|---|---|
| issue | re-run clauses | flagged as a boundary case? |
| 891 | E2 | yes |
| 957 | E3 | yes |

### The clause signature, and what may and may not be concluded from it

| clause | superseded (primary / mentions) | re-run (primary / mentions) |
|---|---|---|
| **E1** bug report | **29 / 30** | **8 / 12** |
| **E4** program-specific support | 12 / **42** | 9 / **13** |
| E2 feature or change request | 11 / 13 | 11 / 14 |
| C2 Cobra is the subject | 12 / 12 | 10 / 14 |
| C3 standalone meaning | 5 / 11 | 3 / 4 |

E1 is the clause a maintainer's `bug` label encodes. E4 is what a maintainer's "please share your
code" reply encodes. Both collapsed. **E2 — `enhancement`, an equally common label — barely moved:
its primary count is flat at 11, and its mention count went 13 → 14.** That is a real counter-signal
and is stated here rather than left out. (An earlier draft said E2 "did not move at all"; the
primary count is what was flat, and the sentence now says which count it means.)

**This is consistent with the harm the ruling described. It is not proof of it.** Two things cut
against reading it as proof:

1. The two runs used **different models**. Two honest classifiers reading the same clean text would
   also disagree, and 32 of 139 is not an implausible amount of disagreement on a genuinely
   contested clause boundary.
2. The re-run's own classifier marked **52 of 139 rows as genuine semantic boundaries** — rows a
   second reader could reasonably decide the other way. **16 of the 32 differing rows are among
   them**: `{298, 678, 724, 829, 835, 891, 943, 957, 1221, 1466, 1521, 1631, 1861, 1923, 2007,
   2249}`, which is the count the two per-row tables above give (14 + 2).

The second point is weaker than it looks, and the honest reading has to say so. **Half the
disagreement — 16 of 32 rows — is on rows the re-run's own classifier did not consider borderline
at all.** Ordinary boundary noise explains the flagged half far better than the unflagged half; on
those 16, one run rejected and the other accepted with neither reader treating the call as close.
So the "two honest readers would differ anyway" defence covers about half of the divergence and
leaves the rest unexplained by it.

(An earlier draft of this paragraph said 21 of 32. That was wrong, and wrong in the direction that
flattered the superseded run, inside the very sentence arguing against reading the clause collapse
as contamination. It is corrected here rather than quietly fixed: the number is 16, and it makes
this a weaker counter-argument than the report previously claimed.)

What can be said is narrower, and still worth saying: **the run that held maintainer labels,
comments and state in context rejected issues as bug reports and as program-specific support at
roughly three times the rate of the run that never saw them, while its rate on the one clause a
label would not help with barely moved.** The diff is published in full above so a reader can form
their own view rather than take this one.

## Phase 2b had to be re-run, and why

`decision-transport-overfetch.md` made the family review conditional: byte-identical candidates,
review stands; different candidates, review redone. The candidates differ, so the review was redone.

`scripts/eval/build_sw279_family_ledger.py` refuses to run against a stale blind list and names the
gap — "2 blind ids have no provenance, 30 provenance keys are absent from the blind list" — so the
re-run was forced by a check rather than remembered by an implementer.

The new review is `sw-279-phase-2b2-family-review/`: 134 queries (94 new candidates + 40 existing
`cobra-v1`), the **same two reviewers** (pi/minimax-M3 and Codex), and the **same brief** — its only
edits are the list's path, its sha256, and the count.

| | first review | second review |
|---|---|---|
| queries | 106 | 134 |
| reviewer A pairs | 16 | 31 |
| reviewer B pairs | 13 | 41 |
| agreed / only-A / only-B | 8 / 8 / 5 | 22 / 9 / 19 |
| union | 21 | 50 |
| families | 89 | 94 |
| cross-split conflicts | 1 | 1 |

**Both new reviewers, independently and blind, again merged `cb-05` with `cb-11`.** The conflict
family is byte-identical to the first review's — `cobra-family-b63d365b20f4ca64`, still exactly
{`cb-05`, `cb-11`} — because no new candidate joined it. So
`decision-holdout-dev-overlap.md`'s withdrawal applies unchanged, and §7's prohibition on
recomputing the family id after a withdrawal is satisfied trivially.

Four independent blind judgements across two reviewers, two models and two different query lists
now stand behind that merge. That is as much corroboration as this design can produce.

## The cb-05 withdrawal, executed

Exactly as `decision-holdout-dev-overlap.md` §3 specifies:

- `cb-05` is **withdrawn from the v2 release dataset**, not reassigned. It is absent from
  `cobra-v2.json`.
- **`cobra-v1.json` is byte-unchanged.** `cb-05` continues to live there in full, still
  `split: holdout`, and SW-258 / SW-263 / SW-264 continue to describe exactly the set they described.
- **`cobra-family-b63d365b20f4ca64` is not recomputed.** `cb-11` carries a family id whose
  provenance-key list still names `cb-05`. That is the visible trace of the withdrawal, kept
  deliberately rather than tidied away.
- The withdrawal row is in `../sw-279-phase-2b2-family-review/family-ledger.jsonl`, templated
  exactly as the decision specifies.

### The three-row shared-grade-3-span census, republished

`decision-holdout-dev-overlap.md` requires this to be published so the disposition is visibly
principled rather than selectively honest. Enumerating every grade-3 span shared by two `cobra-v1`
queries:

| span | queries |
|---|---|
| `command.go:1051-1137` `func (c *Command) ExecuteC(` | cb-01 (dev), cb-19 (dev) |
| `command.go:1146-1168` `func (c *Command) ValidateRequiredFlags(` | **cb-05 (holdout), cb-11 (dev)** |
| `completions.go:678-839` `func (c *Command) InitDefaultCompletionCmd(` | **cb-23 (holdout), cb-35 (dev)** |

`cb-23`/`cb-35` has the identical structural property — a holdout query whose grade-3 answer span is
also a dev query's — and no reviewer, in either review, merged them. **`cb-23` is knowingly
retained.** The withdrawal criterion is **family membership under §7, not span overlap.** Any future
release sentence that leans on "the holdout answers were never visible in dev" would be false, and
must not be written.

## The §8 access ledger

One append-only ledger, `access-ledger.jsonl` in this directory, 33 rows. Three honest caveats are
written into the rows themselves rather than left for a reader to discover:

- **Ledger sequence is append order, not access order.** Two diagnostic accesses (the discrepancy
  probe and the instrument probe) ran before the ledger helper existed and were back-filled by
  `scripts/eval/backfill_sw279_diagnostic_ledger_rows.py`, which takes each row's timestamp from the
  record the access itself produced. Twelve answerability rows (below) were back-filled the same
  way. Every back-filled row carries `"backfilled": true` and says so in its `detail`.
- **Ten answerability actors had no ledger row at all until review round 1.** This was found by the
  round-1 review, not by us, and it is the same defect
  `../../../../projects/graphi/stories/SW-279/decision-holdout-dev-overlap.md` §3 item 6 treated as
  a hard stop for two family reviewers. Sequences 19–28 are the five annotators and five reviewers
  of the first answerability pass; 29–30 are the re-annotation pass's A6 and R6. Each carries the
  actor's own attested timestamp, the digests of the file it read and the file it wrote, and its
  attestation's path and digest. `scripts/eval/backfill_sw279_answerability_ledger_rows.py`
  re-verifies every attested digest against the committed bytes and refuses on a mismatch; all
  twelve resolved.
- **The family reviewers have no first-person attestation and one cannot be manufactured.** A
  process that has exited cannot attest, and a fresh session cannot attest to what an earlier one
  did. What is written for each is an **attestation of record**: a statement, labelled as such, of
  what the repository evidences about that reviewer's conduct and — separately and explicitly — what
  it does not. **The §8 gap is narrowed, not closed.** The actors still lacking a first-person
  attestation are exactly two: **family reviewer A (pi/minimax-M3)** and **family reviewer B
  (Codex)**, for the 2b2 review. Both processes had exited before the gap was noticed, `pi` was
  invoked with `--no-session` so no transcript was retained for A, and nothing in a later session
  can honestly stand in for either. All twelve answerability actors do have first-person
  attestations.

### What the annotators' attestations do and do not say about their inputs

The first-pass annotator attestation schema recorded `output_sha256` and **no input digest at all**,
so the bytes each annotator actually read were recorded nowhere. The five reviewers were better off:
each attested the `annotator_file_sha256` it read, and every one of those resolves to the committed
annotation file.

That is now recorded, and recorded honestly rather than repaired. Each of
`annotator-A{1..5}-attestation.json` gained one field,
`input_artifact_orchestrator_recorded`, which names the batch input the plan assigned to that
annotator, its digest, and — in the field's own `note` — that the orchestrator wrote it during
review round 1 and that **it is not a first-person statement by the annotator**, which has exited
and cannot make one. No existing field in any attestation was changed. The corresponding ledger rows
carry `input_digest_provenance` saying the same thing, so a reader who sees only the ledger is not
misled either.

A6's attestation is different: it was asked for an input digest and attested one first-person, and
the back-fill script checked that value against the committed bytes rather than overwriting it.

### The seal now refuses out-of-order, rather than asserting it did not happen

`scripts/eval/seal_sw279_phase2.py` used to check nothing about ordering and wrote
`"source_access_before_this_seal": "none for any provisional query"` unconditionally — a sentence,
not a check. The round-1 reviewer ran it in a clone with all five `annotations-*.jsonl` present and
it returned 0. It now refuses if any step-4 artefact exists under the harvest, and refuses if any of
the four attestations it consumes (the semantic classifier, the stratum assigner, and both family
reviewers) is missing. `scripts/eval/tests/test_sw279_gates.py` proves each refusal by deleting the
thing and asserting the exit code, and proves the gate is not simply always-refusing by re-sealing a
correctly-ordered tree and getting `sealed-questions.jsonl` back byte for byte.

**This does not retroactively prove the committed seal was taken in order.** That evidence is still
what it always was — timestamps, commit order, and the fact that the annotator attestations
(22:38–22:52Z) all postdate the seal (22:25:53Z at `004b806`). What changed is that the *next* seal
cannot be taken out of order without the script saying so.

The 2b2 reviewers leave materially better evidence than the first pair: the exact command line, the
model identifier, the wall-clock window, Codex's own session id, and the digests of the prompt as
delivered and as published, all in
`../sw-279-phase-2b2-family-review/invocation-record.json`. pi was invoked with `--no-session`, so
no transcript was retained for reviewer A; that is recorded rather than glossed.

### The gate suite, counted correctly

`scripts/eval/tests/test_sw279_gates.py` has **52 cases: 44 refusals and 8 positive controls.** The
round-1 commit message (`56307de`) said "Seventeen cases" and then listed all of them under "The
refusals", which was wrong in both directions — the suite then had 17 cases, of which **13 were
refusals and 4 were positive controls**, and describing a positive control as a refusal overstates
the evidence by counting a "the script still works" assertion as a "the script stops" one. The two
kinds are now named apart in the suite's own `POSITIVE_CONTROLS` inventory, the counts are derived
from the module rather than written down twice, and a run's last line states them:

```
SW279-GATES declared=52 refusals=44 positive_controls=8 ran=52 ok=52 skipped=0 failed=0
```

A refusal case breaks exactly one thing and asserts the script stops with a message naming what is
wrong. A positive control changes nothing and asserts the script still reproduces the committed
artefact byte for byte — or, for the GraphQL argument checks, asserts that a query reformatted
without changing what it asks for is still accepted. A gate that always fails looks identical to one
that works. Both kinds are necessary and neither substitutes for the other.

**The Go wrapper no longer reports PASS over a population that did not run.**
`internal/eval/retrieval/sw279_gates_test.go` asserted only that the output contained `... ok`
somewhere. With the pinned spf13/cobra clone unavailable, the 15 cases that drive the answerability
finalizer skipped, every other case printed `ok`, and `go test` returned 0 — a green gate over a skipped
population, which is the SW-273 defect class these gates exist to prevent. The wrapper now carries
its own declaration of the expected case count (so a deleted or renamed case fails the build rather
than quietly shrinking the evidence), requires every declared case to have run, fails on any skip it
does not sanction, and — where the skip *is* sanctioned, meaning one of the 15 named cobra-dependent
cases with the pinned clone genuinely absent — reports **SKIP rather than PASS**, naming what did not
run. A partial gate run is a non-result.

**Limit worth stating.** The pinned clone lives at `$GRAPHI_CORPUS_COBRA` or
`$HOME/.cache/graphi/corpus/cobra` and is **not provisioned in CI**, so those 15 cases skip there
today, exactly as `internal/eval/retrieval/datasets_test.go` already does. What changed is that the
skip is now visible as a skip instead of being absorbed into a pass; provisioning the clone in CI is
not done here.

## Recorded rule defects — for a future rule version, NOT repaired here

Amending frozen bytes after seeing the candidates is what Phase 1 exists to prevent, so none of
these is fixed. They are recorded because a future version should not have to rediscover them.

1. **§1:30 "must not be fetched or read for selection"** admits two parses. The ruling resolves it
   on the surrounding text; a future version should say "must not be requested from any API,
   transported, stored, or used for selection".
2. **§1 names no transport mechanism and imposes no isolation between the fetching and selecting
   actors.** §8 assumes an "isolated workflow" but never defines or requires evidence of one.
3. **§1:33-35's retention list omits creation time**, though §1:29 permits it and the population
   cutoff is defined on it.
4. **§1:23's population expectation is stated as a count, calibrated against a prior run**, with no
   instrument named. This harvest's discrepancy was caused entirely by the instrument. A future
   version should name the authoritative instrument, not a number.
5. **§5's precedence makes `architecture_flow` nearly unreachable.** Rule 1's trigger list is broad
   and wins outright over rule 2, so a question that names two lifecycle stages *and* asks how to do
   something with them lands in `config_docs` every time. 90 of the 94 candidates are `config_docs`
   as a direct result. That is the rule working as written.
6. **§4 gives no precedence between D-clauses.** Several rows satisfy D3 and D4, or D4 and D5,
   equally well; the schema admits one disqualifier. Annotators had to tie-break and said so.
7. **§3's E2 has no test for "can I do X?" when X does not exist.** A user asking about the present
   state and a user asking for a feature use the same words. 11 of 45 semantic rejects turn on it.

## Answerability at the pin — §8 step 4

Run only after the seal. The 94 sealed candidates were split into five contiguous batches by
ascending issue number — a batching that is a function of the sealed order, not of any question's
content, because a content-driven batching would be a soft form of the preference §8 forbids.

Each batch had one annotator and one **separate** independent reviewer, each in its own session, so
no actor reviewed its own judgements. Both verified the checkout's `HEAD` against
`a0a6ae020bb3899ff0276067863e50523f897370` before opening a file. Every reviewer re-read every
cited span itself rather than trusting the annotator's description. Separate *sessions* is not the
same as separate *machines*: they shared a scratchpad filesystem, and one annotator disclosed a
collision on it — see limit 7 below, which states what that could and could not have done.

The annotator's identity on every row now comes from the batch plan and that actor's own
attestation, not from the judgements. Rejections carry no judgements, so the earlier version
recorded a *filename* as the annotator on all 23 rejected rows, and the "no actor reviewed its own
judgements" guard was comparing a filename against an actor name and could never fire on them. That
is fixed, the 23 rows now name their actor, and
`scripts/eval/tests/test_sw279_gates.py` proves the guard fires on a rejection row by wiring one
actor into both jobs and asserting the refusal.

### Outcome

| terminal state | count |
|---|---:|
| `accept` | **71** |
| `reject:not_answerable` | **23** |
| `unresolved` | **0** |
| total | 94 |

Rejections by disqualifier: **D4 = 14** (Cobra does not implement the capability), **D5 = 5** (no
span satisfying A2), **D3 = 4** (the answer lives outside the pinned tree, almost always in
`pflag`). Every reviewer re-derived the evidence independently and none of it rests on "could not
find it", elapsed time, or an expectation about retrieval.

**How far "positive, cited evidence" is machine-checked, exactly.** Rejection evidence lives in
prose, not in graded spans, and until review round 1 nothing checked it at all: a reject needed only
to name a D-clause. Now `finalize_sw279_answerability.py` extracts every `file:line` reference from
both the annotator's and the reviewer's note and resolves it against the pinned tree — the file must
be tracked and the cited lines must be inside it — and refuses the whole run if any reference fails
or if a rejection carries no source citation at all. **181 citations across the 23 rejections all
resolve.** What this does *not* check, and no mechanical check can, is whether those bytes say what
the note claims about them. That remains a human — here, an agent — judgement, unlike the 278 graded
spans, which are checked three ways. The distinction is recorded in `phase-2-outcome.json` under
`rejection_evidence_check_limit`.

**All 278 cited spans resolve at the pin.** Every path is tracked and regular, every range is inside
its file, and every anchor occurs verbatim inside its own range — checked by the annotator, again by
the reviewer, and a third time mechanically by `scripts/eval/finalize_sw279_answerability.py`, which
refuses to write an outcome otherwise. Go's own `CheckSpanCoverage` re-verifies them in
`TestDatasets_CobraV2SpansResolveAtPinnedSHA`.

### What the review actually caught

A review that agrees with everything did not happen. This one disagreed:

- **19 spans across 17 rows were regraded**, 18 of them downward from 3 to 2 and one upward from 2
  to 3. **The reviewer's grade governs**: §6 makes grade inflation a violation, and A2 is checked
  against the reviewed grades, so a row that only reached A2 on a grade the reviewer took away would
  have failed. None did — every accepted row keeps a grade-3 `.go` span that survived review.
- The recurring defect the reviewers found is the same one four times over: a **bare setter or
  accessor** (`SetUsageTemplate`, `SetHelpCommand`, `Flags()`, `SetArgs`) graded 3 when its own bytes
  name the mechanism without containing it. That is exactly A2's "a nearby name … is insufficient by
  itself", and it is the failure mode a self-review would miss.
- Reviewers also caught **bad evidence behind correct verdicts** — most sharply on #1466, where the
  annotator's note claimed a `glob` search found only unrelated identifiers. It does not: the pin
  carries real glob-shaped bytes in `zsh_completions.go` and the repo's own zsh documentation. The
  reviewer read them, and D4 still holds — on better ground, because the repository says in its own
  words that this is "file extension filtering (not full glob filtering)". Right answer, wrong
  reason, corrected.

### The one unresolved row, the rule violation it produced, and how it was actually settled

**The first attempt at this row was a violation of the frozen rule, and it was corrected in review
round 1 rather than defended.** The sequence is worth writing out in full, because the reasoning
that produced it was plausible and still wrong.

Issue **#1780** was returned `unresolved` by its annotator (A4): it could not fix the intent of the
question from `Q` alone, and the readings gave different disqualifiers. Its independent reviewer
(R4) disagreed and produced a positive **D3** finding from the sealed `Q` and the pinned bytes.

The finalizer was then written to apply that: `unresolved → not_answerable` whenever the reviewer
supplied a D-clause. The argument was that §4 lets a candidate finish on "a positive D1–D5 finding
citing … pinned source", does not say which named actor must produce it, and that the conversion was
exclusionary-only and so could never be a route to a target. **That argument is wrong on the rule's
own text.** §4: "An unresolved candidate is not silently treated as a reject: it blocks completion
of Phase 2 and is reported." §8, among the acts the selector, family reviewers, source annotators
and their tools are forbidden to perform: "reinterpret an unresolved row as a reject". Neither
clause distinguishes a direction, and neither offers an exception for a well-evidenced
reinterpretation. Being exclusionary made the conversion harmless to the counts; it did not make it
permitted.

**What was done instead.** The conversion is deleted from
`scripts/eval/finalize_sw279_answerability.py`. An unresolved row now blocks: the run writes its
ledger and outcome under `-blocked` names, prints the blocking issue numbers, and exits 3.
`scripts/eval/tests/test_sw279_gates.py` reconstructs the exact historical situation — #1780 back to
`unresolved`, R4's D3 review in place, no re-annotation — and asserts the run blocks with the row
still reading `unresolved` and `disqualifier: null`. **That test fails if the conversion is ever
reinstated.**

**#1780 then needed a genuine terminal state, and got one.** §4 gives a candidate exactly three
ways to finish: (a) reviewed grade-3 evidence satisfying A1–A4, (b) a positive D1–D5 finding, or
(c) `unresolved`. The only legitimate route out of (c) is a fresh determination by an actor that has
not seen the row. So the sealed question was reissued as `answerability/batch-6-input.jsonl` under
`answerability/reannotation-plan.json`, and given to a **fresh annotator (A6)** and then to a
**fresh independent reviewer (R6)**, each in its own session under the same briefs and the same
prohibitions as A1–A5 and R1–R5.

Whether that route is permitted at all was disputed in review round 2 and adjudicated separately:
`projects/graphi/stories/SW-279/decision-unresolved-reannotation.md` rules it **PERMITTED**, on the
ground that §4's list constrains what a candidate may *finish with* rather than its history, and
that §8's ban is on *reinterpreting* a row — which A6 could not do, having never seen the earlier
pass. The same adjudication found that the route as implemented had **no bound at all**, and made
three machine refusals a condition of the ruling; they are described under "The re-roll channel is
bounded" below.

**What the re-annotation input withheld, stated precisely.** An earlier draft of this report said the
reissued question carried "no hint that the row was contested". That is too strong, and the accurate
version is worth more than the tidy one:

- **What is true.** `batch-6-input.jsonl` parses to exactly four keys — `issue_number`, `Q`,
  `stratum`, `rubric` — and is content-identical, field by field, to the `batch-4-input.jsonl`
  record for 1780. No verdict, disqualifier, note or contest marker appears anywhere in it. A6
  could not learn the earlier verdict, its direction, or which clause anyone had reached.
- **What is not.** A6 knew it was performing a re-annotation pass: its own attestation self-describes
  as "annotator A6, re-annotation pass" and states that it does not know what "any earlier pass"
  decided. And batch 6 is a **single-row batch** in a plan whose other five batches hold 18–19 rows
  each, which is legible on its face. Knowing that a row is being redone carries a weak prior that
  the earlier pass ended in a blocking state, since a re-do is likelier to follow `unresolved` than
  an accept. It carries no signal at all about which outcome anyone wanted, and the outcome A6
  produced — a reject — shrinks the dataset rather than growing it.

**What "fresh" and "independent" mean here, and what they do not.** All twelve answerability actors —
A1–A6 and R1–R6 — are **one model, Claude Opus 5, in twelve separate sessions**. A6 and R6 are
exactly as independent of A4/R4 as R1–R5 were of A1–A5: no more, and no less. §4 requires "an
independent reviewer" and does not define independence as model diversity, so this meets the rule as
written — but *fresh actor* here means **fresh session, not fresh model or fresh vendor**, and no
claim in this report should be read as asserting otherwise.

**A record gap that bounds all of the above.** The task prompts given to A1–A6 and R1–R6 were **not
archived**. The `sw-279-phase-2b2-family-review` pass did archive its invocation prompts
(`reviewer-A-invocation-prompt.txt`, `reviewer-B-invocation-prompt.txt`); the answerability pass
archived none. So "the actor saw only the sealed row and the briefs" rests on each actor's
attestation rather than on bytes anyone can re-read. This is a pre-existing gap across all twelve
actors, not something the re-annotation introduced — but it is the reason the claims above about
what A6 did and did not see cannot be checked against the repository.

- **A6 returned `not_answerable` / D5**, first-person, citing pinned bytes: Cobra performs no
  flag-token parsing of its own (`command.go:30` aliases `spf13/pflag`; `ParseFlags` hands the raw
  slice to `c.Flags().Parse(args)` at `command.go:1833`; `README.md:77-79` says flag functionality
  comes from pflag), `pflag` v1.0.5 (`go.mod:8`) is not among the 66 tracked files at the pin, and
  the Cobra-side code that inspects single-dash tokens treats one as a flag only when `len(s) == 2`.
  It examined `DisableFlagParsing`, `FParseErrWhitelist` and `SetGlobalNormalizationFunc` and
  explains why none of them contains the mechanism `Q` asks for.
- **R6 agreed**, having re-derived every cited fact itself, searched independently for a grade-3
  span rather than accepting its absence, and added two observations that do not change the verdict
  (a third copy of the `len(s) == 2` test at `command.go:803`, and that D3 applies independently on
  the same bytes).

**Nothing was deleted.** A4's `unresolved` verdict and note, and R4's D3 finding, are both retained
in `annotations-4.jsonl` and `reviews-4.jsonl` and travel onto the ledger row under
`superseded_annotation` and `superseded_review`. The row's terminal state is A6's and R6's, produced
first-person, not a reinterpretation of A4's.

**What did and did not change as a result.** The terminal-state counts are identical — 71 accept, 23
reject, 0 unresolved — because the row is a reject either way. The disqualifier moved from D3 to D5,
which is why the rejection mix above reads D4 = 14, D5 = 5, D3 = 4. `cobra-v2.json` rebuilds byte
for byte. **This decision was made on the rule, not on the arithmetic**: the yield clears 30/30 by
11 and 34, so no number depended on it, and had A6 also returned `unresolved` the honest outcome
would have been to report Phase 2 as blocked.

**An ordering irregularity, disclosed rather than left to be found.**
`internal/eval/retrieval/testdata/datasets/cobra-v2.json` was written at commit `60347a8`, **before**
the re-annotation. §8's permitted order puts the dataset freeze at step 5 and answerability at step
4, so a step-5 artefact predates a repeat of step 4. It is inert on this record, and each part of
that is checkable: the file was not modified by the re-annotation, it rebuilds byte for byte from the
current ledger, and no retrieval run has ever consumed it — `docs/eval/retrieval/runs/` contains no
run referencing `cobra-v2`, so §8 step 6 has not happened for this dataset. It should not recur, and
refusal 4 below is written so that it cannot: `build_sw279_reannotation_batch.py` refuses to build a
re-annotation batch at all once any file under `docs/eval/retrieval/runs/` names `cobra-v2`.

Round 3 of the review found this paragraph asserting that control while **no code implemented it** —
a harvest carrying a run whose dataset id was `cobra-v2` produced a batch with rc=0. The check exists
now, with a gate case
(`test_re_annotating_after_a_retrieval_run_consumed_the_dataset_is_refused`). Recording it: an
unbacked claim in a document whose purpose is to be checked by someone who wants to disbelieve it is
worse than the gap it papers over, because it spends the credibility of every other sentence around
it.

### The re-roll channel is bounded

A route out of `unresolved` that an operator may invoke at will empties §4's blocking effect, and
the implementation had **no bound on it whatever**. `build_sw279_reannotation_batch.py` took
`--issues` as free operator input and validated only that the numbers were in the sealed set; it
never read the existing annotations, so a re-annotation batch could be built for an `accept` row or
a `reject` row. `finalize_sw279_answerability.py` honoured a second annotation whatever the
first-pass verdict was. The only obstacle to a second round was accidental — the builder refuses to
overwrite an existing `reannotation-plan.json`, so a second round needed that file deleted, which is
a git-visible act and not a control anyone designed. In that state an operator could re-roll exactly
the rows whose answers they disliked.

Four refusals close it, and none of them leaves the operator any discretion:

1. **Only an `unresolved` row is eligible.** The builder reads the current verdict of every
   requested issue out of the committed `annotations-*.jsonl` and refuses anything else; the
   finalizer refuses a supersession whose superseded verdict is not `unresolved`. An accepted or
   rejected row becomes permanently non-re-rollable, and an unresolved row is by construction one
   with **no outcome to dislike**.
2. **Exactly one re-roll per row, ever.** Two batches declaring a supersession for the same issue is
   refused at the plan; a third annotation of any issue is refused in the finalizer; the builder
   refuses when any issue already carries a second annotation. If the second pass also returns
   `unresolved`, Phase 2 blocks and is reported — there is no pass three, and the `-blocked`
   machinery that makes that real is unchanged.
3. **The whole eligible set, or none.** The builder computes the unresolved set itself and refuses
   an `--issues` list that is not exactly it. Seeing which rows are unresolved therefore confers no
   choice: the operator's only decision is run-or-stop, and stopping is always available.
4. **No re-annotation at all once the dataset has been used.** The builder scans
   `docs/eval/retrieval/runs/` and refuses if any file there names `cobra-v2`, because §8 step 6 has
   then run and re-labelling a row is choosing the label that moves a number which already exists.
   No per-row bound reaches that, so the channel closes rather than narrows. On this record the scan
   finds nothing, which is why the builder still reproduces `batch-6-input.jsonl` byte for byte.

   The scan reads each file twice, and the second pass exists because the first was not enough.
   A literal byte search finds the id in whatever shape a run records it — `dataset.json`'s `id`,
   a `run.json` reference, a README — without assuming any run format stays as it is today. But a
   JSON file may spell the id with escapes: `"cobra\u002dv2"` decodes to exactly
   `cobra-v2` and contains none of its bytes. **The review round that followed the commit adding this refusal
   found precisely that, and it is recorded here rather than quietly repaired**, because it is the
   second time in this story that a check was asserted to cover more than it did. A run record does
   not stop being a run record for being escaped, so the scan now also decodes JSON and JSONL and
   searches the decoded strings; a file that is neither parses to nothing and is covered by the
   literal pass alone.

Each refusal has a gate case in `scripts/eval/tests/test_sw279_gates.py` proven to bite by breaking
it. **The refusals change nothing on this record, and that is the point**: with them in place the
finalizer reproduces `answerability-ledger.jsonl` and `cobra-v2.json` byte for byte and the counts
are unmoved, which is what makes them a bound rather than a rewrite. Trivially satisfied here — 1780
was the only unresolved row, so the eligible set and the re-annotated set were the same single row.

The frozen rule is **not amended**: it still hashes to
`d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c`. The adjudication records five
defects in the rule itself (RULEDEF-001…005, chiefly that `unresolved` has a blocking effect and no
defined exit) against a future, separately reviewed dataset-v3 rule. None of them is acted on inside
SW-279.

## The yield, and AC-2

| | |
|---|---:|
| existing `cobra-v1` answerable dev carried into v2 | 27 |
| existing `cobra-v1` answerable holdout carried into v2 (after the cb-05 withdrawal) | 7 |
| new answerable dev | **14** |
| new answerable holdout | **57** |
| **final answerable dev** | **41** |
| **final answerable holdout** | **64** |
| AC-2 minimum per split | 30 |

**AC-2's 30 answerable dev + 30 answerable holdout is met, with 11 and 34 to spare.** AC-10 did not
fire; no rule was loosened, no reject reopened, no interrogative list broadened, no title edited.

Against the pre-harvest arithmetic: `decision-holdout-dev-overlap.md` computed that 3 more dev and
23 more holdout were needed, on 11 provisional dev and 55 provisional holdout candidates — 27.3% and
41.8% required survival, with a 3.2-point margin the decision itself called "thin". The 94-candidate
set changed the denominators to 20 dev and 74 holdout, and observed survival was **70% dev (14/20)
and 77% holdout (57/74)**. The margin was never tested.

**Where the extra headroom came from is worth stating rather than enjoying.** It is a direct
consequence of the re-harvest: the 30 extra candidates are the rows the superseded, contaminated run
rejected. Had that run stood, the holdout side would have been decided by a 3.2-point margin.

## The dataset

`internal/eval/retrieval/testdata/datasets/cobra-v2.json` — 110 queries.

| | |
|---|---:|
| carried from `cobra-v1` | 39 (all 40 except the withdrawn `cb-05`) |
| new, issue-derived | 71 |
| dev / holdout | 44 / 66 |
| answerable dev / holdout | 41 / 64 |
| `no_hit` (excluded from relevance aggregates) | 5 |

Every query carries `family_id` and `provenance`. `internal/eval/retrieval/dataset.go` now refuses a
dataset that carries them for some queries and not others — **in both directions**, including the
one it used to wave through, where every row carries a provenance and none carries a `family_id` —
and refuses any family that crosses dev and holdout, the same property `measurement_contract.go`
already enforced at measurement time, now enforced at load time too.

**`cobra-v1.json` is byte-unchanged**, re-read and compared after the v2 write by the builder itself.

## Limits of this dataset, stated because they are real

1. **The independence claim is about when the questions were written, not about how carefully we
   behaved.** These are real users' words from years before graphi existed. The selection, the
   stratum, the family, and the answerability judgements are all ours.
2. **The semantic classifier is a labelled solo substitute for an independent selector, not an
   independent selector.** It was commissioned by the same orchestrator that runs the rest of the
   story. Its attestation corroborates procedure, not independence — and it says so itself.
3. **Annotators and reviewers share a model family.** They ran in separate sessions with separate
   contexts and the reviewers demonstrably disagreed with the annotators 19 times, but "independent"
   here means independent session, not independent vendor.
4. **90 of 94 candidates are `config_docs`.** That is §5's precedence working as written, and it
   means the new rows do not exercise the strata evenly. Any per-stratum claim over v2 has to say so.
5. **`cb-23` and `cb-35` still share a grade-3 span across the split**, knowingly. The permitted
   claim about the holdout is family independence, not span independence.
6. **Undisclosed off-ledger viewing cannot be proven absent from repository bytes.** §8 says so
   itself. What this harvest adds over the last one is that the transport is now a committed
   selection set rather than an assertion, and that the actor who fetched is not the actor who
   selected.
7. **The annotator sessions shared a scratchpad filesystem, and one of them hit a collision on it.**
   `answerability/annotator-A2-attestation.json` discloses, unprompted, that a temporary
   verification script A2 had written in the shared scratchpad was overwritten by another process's
   script while A2 was working, and that A2 noticed because the replacement's first lines named
   `annotations-3.jsonl`. A2 states it did not open, read or use that file and rewrote its script
   under a unique name. **That statement cannot be verified from repository bytes** — it is exactly
   the class of claim §8 says the ledger and the attestations make falsifiable rather than
   checkable. What *can* be said from the bytes is that the batches are disjoint: A2 held issues
   678–1111 and A3 held 1120–1466, so nothing in A3's file corresponds to any row A2 decided, and
   the channel could not have moved an A2 verdict even if it had been used. Separate sessions with
   separate contexts is what "independent" means here; it does not mean separate machines, and this
   report said "in its own session" without saying that until review round 1 pointed it out.
8. **Six attestations were path-rewritten before they were committed.** This repository is public
   and a pre-commit guard refuses a staged file containing the local username, so absolute paths in
   those actors' output were replaced with repository-relative ones. The six are
   `annotator-A{1..5}-attestation.json` and `reviewer-R4-attestation.json`; each records its
   pre-rewrite digest in a `publication_note`, and **the pre-rewrite bytes are not retained**, so
   those "as produced" digests cannot be independently checked by anyone. Nothing else in those
   files was changed. The other six of the twelve answerability attestations — `reviewer-R1`,
   `R2`, `R3`, `R5`, and the re-annotation pass's `annotator-A6` and `reviewer-R6` — wrote
   repository-relative paths in the first place and are committed exactly as produced.
9. **Rejection evidence is cited, and the citations resolve, but the claims about them are not
   machine-checked.** See "How far 'positive, cited evidence' is machine-checked, exactly" above.

## Four ledger irregularities, disclosed

An append-only ledger cannot be edited, so every one of these is corrected by a later row rather
than tidied away. **Ledger sequence is append order, not access order** — that is the single fact
behind three of the four.

1. **Sequences 3 and 4 carry timestamps earlier than sequence 2.** The discrepancy probe and the
   instrument probe ran before `scripts/eval/_access_ledger.py` existed and so did not emit their
   own rows at the time; they were back-filled by
   `scripts/eval/backfill_sw279_diagnostic_ledger_rows.py`, which takes each timestamp from the
   record the access itself produced. Each of those two rows says so in its own `detail`.
2. **Sequence 11 also precedes sequence 10** (22:19:42Z against 22:24:07Z), and so does sequence 12
   against 13. The 2b2 family reviewers' rows were recorded after the reviews were in, while the
   cb-05 withdrawal row (10) was written by the ledger build that followed them. The underlying
   order is correct — the reviews at 22:19 and 22:23 did precede the family-ledger build at 22:24
   and the seal at 22:25 — and only the numbering is out of order. **Rows 11 and 12 do not say so in
   their own `detail`**, unlike rows 3 and 4; an earlier version of this report said "each
   back-filled row says so", which was true of two rows and not of these two. It is corrected here
   rather than in the ledger, because the ledger is append-only.
3. **Sequences 19–30 all carry timestamps earlier than the rows numbered before them.** These are
   the twelve answerability actors, back-filled in review round 1 by
   `scripts/eval/backfill_sw279_answerability_ledger_rows.py` with each actor's own attested
   timestamp. Every one carries `"backfilled": true` and says in its `detail` that ledger sequence
   here is append order, not access order. They also carry `input_digest_provenance`, which
   distinguishes the digest an actor attested itself from one the orchestrator recorded on its
   behalf.
4. **Sequence 14's output digest no longer resolves, and neither do 15, 16 or 17's.** The
   answerability finalizer has run three times. The first run appended its row and then exited 3 on
   the unresolved row; the second (sequence 15) wrote the ledger that stood until review round 1,
   with sequence 16 consuming it to build the dataset and sequence 17 correcting 14. Review round 1
   changed what the finalizer writes — the conversion removed, annotator identity taken from the
   plan, rejection citations resolved — so it was re-run a third time (sequence 31), the dataset
   rebuilt (32, byte-identical), and **sequence 33 is the correction row naming the superseded
   digest and the authoritative one**. Sequences 14–17 stay, because removing them would be the
   precise defect this harvest exists to avoid. Both correction rows (17 and 33) name the access
   ledger itself as their `input_artifact`, so their `input_sha256` is the ledger's digest *before*
   the row was appended and cannot resolve against the file afterwards. That is arithmetic, not an
   irregularity, but it is the kind of thing a checker flags, so it is stated here.

A run blocked on an unresolved row no longer claims the authoritative artefact names at all: it
writes `answerability-ledger-blocked.jsonl` and `phase-2-outcome-blocked.json`, which is what
prevents irregularity 4 from recurring. An earlier version of this report said the first blocked run
"refused to write an outcome". It did not — it wrote both the ledger and the outcome and *then*
returned 3, which is how sequence 14 came to point at a file that was later replaced.

## What these scripts are not hardened against

Every gate in this harvest validates its **inputs**. None of them is hardened against an **operator
with commit rights**, and no acceptance criterion asked for that.

Three review rounds each added a validator and each found the next input class the previous one did
not cover. Four findings from the last round are recorded here as known follow-ups rather than
fixed:

1. **Freshness bypass via input-row coverage.** The re-annotation freshness check derives an actor's
   coverage from the batch's input rows, so an actor's prior involvement is invisible where the
   input does not carry the row.
2. **Freshness bypass via batch-number reuse.** Batch identity is the operator-supplied number, so a
   reused number can make a second pass look like the first.
3. **Backfill `./`-path spelling.** `backfill_sw279_answerability_ledger_rows.py` compares paths as
   written, so a `./`-prefixed spelling of the same file edits before the staleness check refuses.
4. **Citation extension shapes.** Rejection-citation resolution recognises a bounded set of path
   shapes; digit-leading and unusually long extensions are not seen as citations.

A fifth, adjacent one: the Go gate wrapper transmits skipped case **ids** but not their **reasons**,
so it sanctions a skip by name rather than by cause.

The reasoning is not that these are unimportant. It is that the operator they would defend against
owns the repository and can commit anything, including a change to the validator itself, so
hand-rolled input validation has no reachable fixed point against that adversary — three rounds of
evidence say each layer closed reveals the next. The controls that actually bind here are the
**externally checkable** ones, and they do not depend on any script behaving:

- the committed digests in `phase-2-seal.json`, the access ledger and the attestations;
- git history, in which every artefact and every change to one is a visible commit;
- the frozen rule's hash, `d9aea9863501d3d2827aa191275f689fc8afeda30ecb8dcbbb379d7339d85a2c`,
  recorded before the first fetch;
- byte-identical reproduction — the finalizer, the family-ledger builder and the dataset builder all
  rebuild their committed outputs exactly, which is what makes every refusal added in review a bound
  on the machinery rather than a rewrite of the record.

Anyone auditing this dataset should check those four, not the scripts' resistance to their own
operator.
