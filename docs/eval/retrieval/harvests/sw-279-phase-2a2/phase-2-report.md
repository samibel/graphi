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
code" reply encodes. Both collapsed. **E2 — `enhancement`, an equally common label — did not move at
all**, which is a real counter-signal and is stated here rather than left out.

**This is consistent with the harm the ruling described. It is not proof of it.** Two things cut
against reading it as proof:

1. The two runs used **different models**. Two honest classifiers reading the same clean text would
   also disagree, and 32 of 139 is not an implausible amount of disagreement on a genuinely
   contested clause boundary.
2. The re-run's own classifier marked **52 of 139 rows as genuine semantic boundaries** — rows a
   second reader could reasonably decide the other way. 21 of the 32 differing rows are among them.

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

One append-only ledger, `access-ledger.jsonl` in this directory. Two honest caveats are written into
the rows themselves rather than left for a reader to discover:

- **Ledger sequence is append order, not access order.** Two diagnostic accesses (the discrepancy
  probe and the instrument probe) ran before the ledger helper existed and were back-filled by
  `scripts/eval/backfill_sw279_diagnostic_ledger_rows.py`, which takes each row's timestamp from the
  record the access itself produced. Their rows therefore carry timestamps earlier than the row
  numbered before them, and each row says so.
- **The family reviewers have no first-person attestation and one cannot be manufactured.** A
  process that has exited cannot attest, and a fresh session cannot attest to what an earlier one
  did. What is written for each is an **attestation of record**: a statement, labelled as such, of
  what the repository evidences about that reviewer's conduct and — separately and explicitly — what
  it does not. **The §8 gap is narrowed, not closed.**

The 2b2 reviewers leave materially better evidence than the first pair: the exact command line, the
model identifier, the wall-clock window, Codex's own session id, and the digests of the prompt as
delivered and as published, all in
`../sw-279-phase-2b2-family-review/invocation-record.json`. pi was invoked with `--no-session`, so
no transcript was retained for reviewer A; that is recorded rather than glossed.

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

Each batch had one annotator and one **separate** independent reviewer in its own session, so no
actor reviewed its own judgements. Both verified the checkout's `HEAD` against
`a0a6ae020bb3899ff0276067863e50523f897370` before opening a file. Every reviewer re-read every
cited span itself rather than trusting the annotator's description.

### Outcome

| terminal state | count |
|---|---:|
| `accept` | **71** |
| `reject:not_answerable` | **23** |
| `unresolved` | **0** |
| total | 94 |

Rejections by disqualifier: **D4 = 14** (Cobra does not implement the capability), **D3 = 5** (the
answer lives outside the pinned tree, almost always in `pflag`), **D5 = 4** (no span satisfying A2).
Every rejection carries positive, cited evidence; every reviewer re-derived it independently and
none rests on "could not find it", elapsed time, or an expectation about retrieval.

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

### The one unresolved row, and the judgement made on it

Issue **#1780** was returned `unresolved` by its annotator: it could not fix the intent of the
question from `Q` alone, and the readings gave different disqualifiers. §4 says an unresolved
candidate blocks completion of Phase 2, and the first finalizer run duly exited 3 and refused to
write an outcome.

The independent reviewer settled it as `not_answerable` / **D3**, from the sealed `Q` and the pinned
bytes, on the ground that §6's universal pass/fail requires preserving every explicit constraint in
`Q` — and `Q` writes the token with a leading dash, so the reading that would have made it
answerable is only reachable by discarding a character the question states. Every remaining reading
converges on the same fact: Cobra declares no flag types and no flag-declaration API; `Flags()` and
`PersistentFlags()` hand out a `*pflag.FlagSet` and parsing is delegated, and `pflag` is not in the
pinned tree.

**§4 permits a candidate to finish on "a positive D1–D5 finding citing … pinned source that
establishes the disqualifier". It does not say which of the two named actors must produce it, and
the independent reviewer is named in the same clause.** The finalizer therefore accepts a reviewer's
resolution of an `unresolved` row — **in one direction only**. `unresolved → not_answerable` removes
a query and can never be a route to a target; `unresolved → answerable` would add one and is refused
by the script even if a reviewer proposed it. Both positions stay on the row, and the disagreement
is in `phase-2-outcome.json` under `annotator_unresolved_resolved_by_reviewer`.

**This is an orchestrator judgement call and a reviewer should scrutinise it.** The honest
alternative was to leave the row unresolved and report Phase 2 as blocked on it. Two things make
that alternative worse rather than safer: the row is excluded from the dataset either way, so
nothing about the released set changes; and the yield clears 30/30 by a wide margin with or without
it, so no number depends on the choice.

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
dataset that carries them for some queries and not others, and refuses any family that crosses
dev and holdout — the same property `measurement_contract.go` already enforced at measurement time,
now enforced at load time too.

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
