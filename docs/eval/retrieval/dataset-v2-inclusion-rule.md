# Cobra issue inclusion rule for dataset v2

Status: frozen Phase 1 rule for SW-279. This document defines selection before any
`spf13/cobra` issue is fetched or read. It contains no issue verdict, retrieval result, or dataset
change.

The repository pin is `a0a6ae020bb3899ff0276067863e50523f897370`. In this document, "the
pinned repository" means the tracked files at exactly that commit, not another Cobra release, the
current default branch, a dependency checkout, a web page, or an installed binary.

The purpose of the rule is not to obtain a quota. It is to turn issue selection into an auditable
classification made without candidate or baseline retrieval output. Phase 2 must apply this rule
to the complete population even if the required counts have already been reached. If the result
does not bring the release dataset to 30 answerable development and 30 answerable holdout queries,
SW-279 stops and reports the shortfall; this rule must not be relaxed, reinterpreted, or rerun with
a different split salt to fill the gap.

## 1. Frozen population and permitted evidence

The population is every `spf13/cobra` GitHub object that GitHub classifies as an issue rather than
a pull request and that existed at the Phase 1 rule commit's committer timestamp. Phase 2 must first
write an issue-number manifest, sorted by ascending integer issue number, before exposing title or
body text to a selector. The expected population is the 1,255 issues recorded by SW-279. A different
count is a discrepancy to report and resolve without reading issue text; it does not authorize
silently changing the population.

Every manifested issue is examined, in ascending issue-number order. The only issue content allowed
for selection is the issue author's title and opening body plus the immutable issue number,
author, and creation time. Labels, reactions, comments, maintainer replies, linked pull requests,
closing events, and external links must not be fetched or read for selection. An external link may
be observed as literal text in the opening body, but its target must not be opened.

The Phase 2 ledger must retain the raw title and opening body, their SHA-256 digests, the population
manifest digest, the selector, and the time of the verdict for every issue, including rejects. A
missing issue, an out-of-order early stop, a population-count mismatch passed without a recorded
decision, selection evidence from a comment or linked resource, or a raw-text digest that does not
recompute is a detectable violation of this clause.

The first-fetch record must name the Phase 1 rule commit and hash these exact rule bytes. The fetch
must fail unless that commit exists and its committer timestamp precedes the recorded fetch start.
A missing record, a rule-byte mismatch, or a fetch timestamp at or before the rule commit is a
detectable violation of the phase boundary.

## 2. Mechanical question text

For an issue title `T`, derive `Q` with this exact transformation:

1. Normalize `T` to Unicode NFC.
2. Replace each maximal run of Unicode White_Space characters with one ASCII space and remove
   leading and trailing space.
3. Remove at most one leading question marker, matched case-insensitively. The complete marker list
   is `question:`, `[question]`, `[question]:`, `(question)`, and `(question):`. After removal,
   remove leading space again.
4. Make no other change. In particular, preserve spelling, word order, capitalization, identifiers,
   and terminal punctuation. Do not add "Cobra", join text from the body, correct grammar, summarize,
   translate, or paraphrase.

`Q` is eligible for candidate classification only when it contains 3 through 200 Unicode code
points, contains at least three whitespace-separated tokens, contains at most one `?` and then only
as its final non-space character, and its first whitespace-separated token consists only of ASCII
letters and, after ASCII lowercasing, is one of:
`how`, `what`, `where`, `when`, `why`, `which`, `who`, `can`, `could`, `does`, `do`, `is`, `are`,
`should`, or `would`. A title beginning `how to` is included in `how`. A question mark is not
otherwise required.

Phase 2 must store both `T` and `Q`. A reviewer detects a violation by rerunning the four steps and
the syntactic eligibility test and byte-comparing the result with the recorded query. Any editorial
change, body-derived phrase, or eligible query that cannot be reproduced from its title is a
violation.

## 3. Candidate question predicate

An issue is a candidate only if all of C1 through C5 hold on the opening title/body, and none of E1
through E5 applies. The selector applies this clause before any candidate-directed inspection or
search of the pinned repository. Prior familiarity with Cobra cannot be erased, but it is not an
allowed verdict reason.

- **C1 — information request.** `Q` asks for an explanation of an existing fact: where Cobra code
  performs an operation; how or why existing Cobra behavior occurs; what a Cobra API, field,
  option, hook, template, flag, or command means; when a Cobra path is taken; or how a caller uses
  or configures an existing Cobra capability.
- **C2 — Cobra is the subject.** Read in the declared Cobra-repository context, the question's
  subject is Cobra's implementation, public API, configuration, or repository documentation. An
  example from a user's program does not disqualify an otherwise general Cobra question, provided
  the answer does not depend on that program.
- **C3 — standalone meaning.** `Q` expresses one answerable intent without requiring a comment,
  attachment, linked page, omitted quotation, screenshot, or maintainer reply to determine what is
  being asked. Explicit constraints and alternatives within that one intent remain part of it.
- **C4 — general answer.** A correct answer would be the same for two users with different command
  trees, callback bodies, arguments, environment, filesystem, terminal, and process state, except
  for conditions that Cobra itself defines and that can be stated from the pinned source.
- **C5 — English code question.** The prose is English. Go identifiers, CLI tokens, paths, and
  conventional technical terms do not count against this requirement.

The exclusions are:

- **E1 — bug report.** The primary request asserts an actual-versus-expected discrepancy, crash,
  regression, hang, incorrect output, or failed reproduction and asks maintainers to diagnose or
  correct it. An interrogative title does not turn such a report into a code question. A general
  question about documented behavior, with an example but no claimed defect, is not excluded by E1.
- **E2 — feature or change request.** The requested outcome is to add, remove, or change behavior,
  API, output, platform support, or documentation, including a request framed as "can Cobra
  support ...". A question about how an already-existing capability works is not E2.
- **E3 — dependency or release maintenance.** The requested outcome is a dependency bump, module or
  toolchain compatibility update, release action, backport, or version-policy change.
- **E4 — program-specific support.** Answering requires inspecting, executing, or guessing about
  the reporter's command tree, callbacks, application code, build, arguments, logs, environment,
  filesystem, shell, terminal, or other runtime state. A minimal example is allowed only when it
  illustrates a general question whose answer remains independent of the example.
- **E5 — non-question administration.** The issue is primarily an announcement, tracking item,
  task list, proposal, design draft, duplicate pointer, or other project administration rather than
  an information request about existing code.

The ledger records `candidate` or `reject`, all deciding clause IDs, and a one-sentence explanation
using only the allowed issue text. A reviewer detects a violation by applying C1–C5 and E1–E5 to the
retained title/body without viewing source or retrieval output. A verdict with no deciding clause,
a rationale relying on forbidden issue material, or a candidate that fails any C clause or meets an
E clause is a violation. Disagreement on a semantic boundary is not hidden: it is recorded for
review rather than resolved from source-search difficulty or yield.

## 4. Answerable at the pin

Only candidates proceed to this clause. Before source inspection, their query, stratum, rubric,
family, and provisional split must already be sealed as described below. Source annotators may then
read and search a local checkout only after verifying that its `HEAD` is the exact pin.

A candidate is answerable only if A1 through A4 hold:

- **A1 — affirmative source answer.** The pinned repository contains an affirmative answer to `Q`,
  not merely evidence that the requested behavior is absent or unsupported.
- **A2 — answer-bearing Go span.** At least one finite, contiguous span in a tracked, regular `.go`
  file at the pin directly supports the rubric's required answer. The span must contain the actual
  value, condition, mechanism, operation, or procedure asked for; a nearby name, call edge, test
  assertion, or relation-only citation is insufficient by itself. Additional answer-bearing spans
  may be in other tracked text files, but documentation alone cannot establish answerability.
- **A3 — repository sufficiency.** A correct answer can be written from `Q` and the pinned
  repository alone. It needs no issue discussion, external documentation, dependency source,
  historical commit, release note outside the pin, experimentation, execution, or private state.
- **A4 — pinned semantics.** The span answers the question as written at the pin, without silently
  translating a question about another version into a question about this one.

The following are mandatory disqualifiers:

- **D1 — another-version behavior.** The requested behavior is explicitly tied to a release,
  branch, or commit other than the pin, or to a before/after regression, and removing that version
  context would change the question's meaning. Coincidental similarity at the pin does not rescue
  it.
- **D2 — runtime-state answer.** The requested conclusion depends on values available only while a
  particular program runs, including its constructed command graph, callback behavior, arguments,
  environment, filesystem, OS/shell behavior, or observed logs/output. A source-defined conditional
  rule is answerable; deciding which branch a particular unseen run took is not.
- **D3 — outside-repository answer.** The necessary answer exists only in a website, generated API
  portal, external guide, issue comment, pull request, dependency repository, or file absent from
  the pinned tree.
- **D4 — unsupported is the answer.** The honest answer is only that Cobra does not support or
  implement the requested capability. Absence is not converted into a grade-3 span.
- **D5 — no answer-bearing span.** An annotator can find related code or tests but no span satisfying
  A2, or the proposed answer requires speculation or synthesis not supported by the cited bytes.

Difficulty is not D5. Failure to find an answer quickly, a large search area, an answer deep in a
call chain, poor identifier overlap, or an expectation that retrieval will miss is never a reject
reason. Every candidate must finish with either (a) reviewed grade-3 evidence satisfying A1–A4,
(b) a positive D1–D5 finding citing the issue text and/or pinned source that establishes the
disqualifier, or (c) `unresolved`. An unresolved candidate is not silently treated as a reject: it
blocks completion of Phase 2 and is reported. There is no per-candidate search-time or file-count
budget and no replacement sampling.

Every proposed grade-3 span must record repository path, inclusive line range, an anchor contained
in that range, a reason, annotator, and an independent reviewer, following the existing
`cobra-v1.json` discipline. A reviewer detects a violation by verifying the checkout SHA, resolving
every span and anchor, checking the span against the frozen rubric, and examining every rejection
for positive D-clause evidence. A `not found`, `too hard`, time-limit, rank, score, or search-effort
rationale; a documentation-only answer; an unreviewed span; or completion with an unresolved row is
a violation.

## 5. Stratum assignment

Issue-derived candidates can enter only the three answerable natural-language strata below. Apply
the rules in this precedence order to `Q` and the issue author's opening body, before source access:

1. **`config_docs`** when the requested answer is a caller-facing procedure or setting: how to use,
   set, enable, disable, register, customize, or generate something through Cobra's public API,
   fields, options, flags, templates, or documented commands.
2. **`architecture_flow`** otherwise, when the question explicitly asks about a lifecycle,
   traversal, propagation, registration path, dispatch path, ordering, or interaction among two or
   more named stages or components.
3. **`nl_behaviour`** for every other candidate: the location, mechanism, condition, meaning, or
   reason for one existing Cobra behavior.

Issue-derived questions are never assigned `exact_identifier`, `exact_path`, `ambiguous`, or
`no_hit`: the candidate syntax is a natural-language question, C3 requires a determinate intent,
and A1–A2 require a hit.

The ledger records the chosen stratum and the first applicable numbered rule. A reviewer detects a
violation by replaying the precedence rules from the sealed question and opening body. Assignment
based on where an answer was found, the number of answer spans, observed retrieval performance, or
a desired per-stratum count is a violation.

## 6. Answer rubric and judgement grades

Every candidate receives rubric `cobra-issue-direct-answer/v1` before source access. The rubric is
the query text plus the answer mode selected solely from `Q`'s first case-folded token:

| First token | Required answer mode |
|---|---|
| `where`, `which`, `who` | Name the requested location, value, or code entity and state its role. |
| `how` | State the mechanism or caller procedure and the essential conditions or ordered steps expressly requested by the query. |
| `what` | Define the requested entity or behavior and state its code-defined effect. |
| `why` | State the source-defined cause, condition, or invariant that explains the behavior. |
| `when` | State the source-defined trigger or lifecycle point. |
| `can`, `could`, `does`, `do`, `is`, `are`, `should`, `would` | Give a direct yes/no or conditional answer, then identify the source-defined mechanism or condition that makes it so. |

For every mode, a passing answer must answer the entire query at the pin, preserve every explicit
constraint or alternative in `Q`, and be supported by answer-bearing source bytes. It fails if it
only restates the query, names a related symbol without answering, relies on the reporter's program
or external material, substitutes another version's behavior, or says only that the capability is
unsupported.

Source judgements use the existing four grades without candidate-specific redefinition:

- **3 — exact answer:** the span contains answer-bearing bytes needed to satisfy the frozen answer
  mode, rather than merely pointing toward them.
- **2 — directly relevant:** the span materially explains or connects the answer but cannot satisfy
  the answer mode without another span or non-local inference.
- **1 — marginal:** an example, test, sibling, caller, or nearby declaration that corroborates the
  topic but does not directly explain the requested answer.
- **0 — irrelevant/negative:** a plausible false positive that does not answer the query.

Use the smallest contiguous range that preserves the answer-bearing declaration or documentation
unit; do not enlarge a range to catch a retrieval hit. Multiple grades are allowed, but at least one
reviewed grade-3 `.go` span is mandatory. Annotation happens without candidate or baseline results.

The sealed rubric record consists exactly of the rubric version, `Q`, the table-selected answer
mode, the universal pass/fail text above, and its SHA-256. A reviewer detects a violation by
regenerating that record from `Q`, checking its seal predates source access, and reviewing grades
against the fixed definitions. Bespoke expected-answer wording added after source inspection,
changing an answer mode, grade inflation for a retrieved location, padded spans, or a missing
independent reviewer is a violation.

## 7. Family assignment and split

Family assignment happens over all provisional candidates together with all existing `cobra-v1`
queries, before answerability annotation and before a split is assigned. Two questions are in the
same family when they are exact duplicates, duplicates after Unicode NFC/case-folding/whitespace
collapse/terminal-punctuation removal, paraphrases, or same-task variants. The operational
same-task test is conservative: they must express the same user intent and require the same answer
mode, and one correct answer at the pin could satisfy both without adding a distinct mechanism,
action, or code-defined fact. Merely sharing a topic, identifier, file, or answer span does not make
two different information needs the same task.

Two family reviewers independently compare every pair while seeing only query text, provenance,
and the frozen rubric or query class where applicable—not source, proposed spans, retrieval output,
or split. A pair is joined if either reviewer marks it same-task. Families are the transitive
closure of those joins, so a paraphrase chain is one family. Every pair decision and disagreement
is retained.

For each component, sort these provenance keys by raw UTF-8 bytes:

- new issue: `github:spf13/cobra#<decimal issue number>`;
- existing query: `dataset:cobra-v1:<query id>`.

Join the sorted keys with a single LF and no trailing LF. The `family_id` is `cobra-family-`
followed by the first 16 lowercase hexadecimal characters of the SHA-256 of those bytes. This ID is
mechanical once the conservative pair decisions are recorded.

A family containing an existing query inherits that query's existing split. If it contains existing
queries from both splits, Phase 2 stops and reports the conflict because existing SW-258 assignments
may not move. For remaining new-only families, compute SHA-256 of
`sw-279-family-split-v1` + LF + `family_id`, sort by the full digest and then `family_id`, and assign
positions 1, 9, 17, ... to `dev`; assign every other position to `holdout`. Positions are one-based.
This deliberate 1:7 allocation reflects the frozen shortfall (3 development and 22 holdout) without
using question content or results. Rejected and unresolved provisional candidates remain in the
family and split calculation; their removal must not cause a resplit. The salt, order, and starting
position are immutable even if either split misses its minimum.

A reviewer detects a violation by replaying all-pairs union and transitive closure, recomputing each
ID and split, and checking that no `family_id` crosses splits. Missing pair records, a family split
across dev/holdout, reassignment after an answerability failure, rerunning with a different salt or
offset, or separating two questions for which either reviewer recorded same-task is a violation.

## 8. Anti-gaming and order of operations

The permitted Phase 2 order is:

1. freeze and hash the complete issue-number population manifest;
2. archive allowed issue text and record candidate/reject decisions for the complete population;
3. derive and seal `Q`, stratum, `cobra-issue-direct-answer/v1`, every family, and every provisional
   split;
4. only then allow source annotators to inspect the verified pinned checkout and decide
   answerability and judgements;
5. freeze the final accepted-query and judgement artifact; and
6. only then permit any candidate or baseline retrieval run.

The selector, family reviewers, source annotators, and their tools are forbidden to:

- call `task_context/2`, semantic search, lexical search, `GrepRead`, the retrieval evaluator, or
  any proxy that produces a ranked or bundled retrieval result for a provisional query before the
  final seal;
- inspect saved hit lists, bundles, scores, ranks, metrics, traces, prior tuning output, or another
  person's retrieval preview for a provisional query;
- prefer, prioritize, accept, reject, rewrite, re-stratify, split, or grade a question because its
  answer seems easy or hard for a retriever to locate;
- reject a candidate merely because source annotation is slow or difficult, or replace it with an
  easier issue;
- stop examining issues or source candidates when a numeric target is reached;
- change a query, rubric, family, stratum, split, or verdict after seeing retrieval output;
- use holdout membership or holdout results to choose any ranking behavior, baseline behavior,
  tokenizer, budget, threshold, prompt, or claim wording; or
- loosen this rule, change the family split salt/offset, reinterpret an unresolved row as a reject,
  or sample a more favorable subset to reach 30.

Manual text search and source navigation inside the verified pinned checkout are allowed only in
step 4 to establish answerability and annotate spans. Their order, rank, elapsed time, and query
overlap are not selection evidence and must not be recorded as a reason for a verdict.

Phase 2 must preserve an append-only access ledger containing actor, timestamp, command/tool class,
input artifact digest, and output artifact digest; seal records with timestamps and commits; the
complete changed-file set; and attestations from each actor that no off-ledger candidate retrieval
output was viewed. A reviewer can detect a violation from any retrieval access predating the final
seal, a candidate-directed source access predating the question/family/rubric/split seal, a
post-seal mutation, an incomplete population ledger, a difficulty- or performance-based reason, an
unprocessed or silently dropped row, or inconsistent artifact digests/timestamps. Undisclosed
off-ledger viewing cannot be proven absent from repository bytes alone; the mandatory isolated
workflow, append-only ledger, and named attestations make such viewing an explicit falsification
rather than an uncheckable permission.

## 9. Phase 2 outcomes and yield rule

The ledger has four exhaustive terminal states for every manifested issue:

- `reject:not_candidate` with C/E clause evidence;
- `reject:not_answerable` with positive D-clause evidence;
- `accept` with sealed provenance, family, split, rubric, and independently reviewed grade-3 span;
- `unresolved`, which prevents completion.

Report counts at every gate, accepted questions and unique families by stratum and provisional
split, all rejection reasons, all unresolved rows, and the resulting full-dataset answerable counts.
No accepted query may be omitted from the dataset because it is inconvenient, duplicative in count,
or difficult for either arm; family clustering, not deletion, controls dependence.

Before seeing any issue, the yield estimate is that roughly 3–5% of 1,255 issues will survive both
the direct-question predicate and pinned-source answerability, or about 38–63 accepted issues before
family dependence is counted. A point estimate is about 4% (approximately 50 issues). After family
clustering and the frozen 1:7 new-family split, the uncertainty is wide; the estimate is not a quota
and was not derived from issue inspection.

If the accepted additions leave either the development split below 30 answerable queries or the
holdout below 30, Phase 2 stops and publishes the complete ledger and the exact shortfall. It must
not broaden the interrogative list, edit titles into better questions, admit unsupported answers,
resplit families, change the salt/offset, reopen rejects, or fetch a substitute population. Any such
change after observing yield is a new, separately reviewed rule and dataset version, not completion
of SW-279.

A reviewer detects a violation by reconciling the population manifest one-for-one with the terminal
states and the reported counts, checking that every accept is present and every minimum excludes
`no_hit`/unresolved rows, and comparing the final rule bytes with the Phase 1 seal. A success report
with a short split, missing ledger rows, a post-yield rule change, or a subset selected to hit a
target is a violation.

## 10. Known weak point

The weakest clause is semantic family equivalence. Exact and normalized duplicates are mechanical,
but whether two differently worded questions are the same underlying task cannot be reduced to a
string transform without missing paraphrases—the leakage AC-2 is meant to prevent. The all-pairs,
two-reviewer, either-reviewer-merges rule deliberately errs toward larger families, and its pair
ledger makes disagreement visible, but it remains a human semantic judgement. A reviewer should
therefore audit family disagreements and unusually small neighboring families before trusting the
split; retrieval output remains forbidden during that audit.

## Phase 1 declaration

This rule was written from the SW-279 story, the SW-266 sourcing decision, amended SW-266 AC-2, and
the structure and annotation conventions of the existing local `cobra-v1.json`. No Cobra issue was
fetched or read, no GitHub API was called, and no retrieval output was inspected while writing it.
