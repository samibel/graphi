# `sw-279-phase-2a-superseded/` — retained evidence of a disclosed §1 violation

The Phase 2a harvest in `sw-279-phase-2a-superseded/` is **superseded and must not be used to build
the v2 dataset.** It is retained, unedited, because §8 of the frozen inclusion rule requires an
append-only record: deleting the evidence of a disclosed violation would be a worse act than the
violation.

## The finding

Phase 2a fetched issue data through a connector that returned broad normalized envelopes. The
selector's own access ledger records, in `sw-279-phase-2a-superseded/access-ledger.jsonl` line 2,
that **labels, comments, reactions, state, assignees and milestones were transported** at the step
whose output is the semantic-classification input.

§1 of `docs/eval/retrieval/dataset-v2-inclusion-rule.md` (lines 27–31) says those fields
"**must not be fetched or read for selection**". The implementer read that as prohibiting *use*,
not *transport*, recorded the reading in `reviewer-determination-transport-overfetch.md`, and
explicitly referred it upward rather than certifying itself compliant. That referral was correct
and the reading was overturned.

The full ruling — including why the transport limb is load-bearing, why §8 defeats the
"undetectable clauses are not prohibitions" argument, and why the run additionally fails on its own
reading for want of an isolated workflow — is
`projects/graphi/stories/SW-279/decision-transport-overfetch.md` (portfolio, outside this repo).

**The violation was disclosed by the selector, not discovered by audit.** That is worth recording
plainly: the control that caught this was the implementer writing down a thing that counted against
it.

## What survived and what did not

| artefact | status |
|---|---|
| `dataset-v2-inclusion-rule.md` | **untouched**; still hashes to `d9aea986…5a2c`, identical to the blob at `a0a13a7` |
| the Phase-1 boundary proof (rule commit precedes first fetch) | **holds** — a property of git, not of this harvest |
| the §2 mechanical derivation (1,116 rejects / 139 eligible) | **survives as a prediction to re-verify**; it is a pure function of the titles |
| the 139-row semantic C/E classification | **discarded** — this is the contaminated artefact and the whole cost |
| the 66 candidates | **only if reproduced** by the re-harvest |
| `sw-279-phase-2b-family-review/` | **conditional** — reviewers A and B never held the envelopes; only their input set was at risk |

## Two further defects recorded in the same ruling

- `first-fetch-record.json` and `population-manifest.json` carry key sets that
  `scripts/eval/fetch_cobra_issue_population.py` cannot produce — that script writes
  `requested_fields`/`explicitly_not_requested` unconditionally and they are absent. The committed
  fetch script is dead code; the population fetch has no code artefact.
- `created_at` is `null` in 1,255 of 1,255 rows of `issue-text.jsonl`, while the ledger and
  `issue-text-metadata.json` both claim it was projected. The population cutoff is defined on that
  field, so its absence makes the population boundary un-re-derivable from the archive alone.

The replacement harvest lives in `sw-279-phase-2a2/` and is produced by committed, field-selective
scripts whose GraphQL selection sets are auditable in source rather than asserted in a sentence.

---

## The outcome, recorded after the re-harvest ran

Added 2026-09-04, after `sw-279-phase-2a2/` was produced. The table above was written before the
re-harvest and stated what *would* survive; this section states what actually did.

| prediction | outcome |
|---|---|
| the frozen rule is untouched | **held.** Still `d9aea986…5a2c`, byte-identical to the blob at `a0a13a7`. |
| the Phase-1 boundary proof holds | **held.** A property of git, unaffected by a second fetch. |
| the mechanical pass reproduces (1,116 / 139) | **reproduced, on a population one row larger.** 1,117 mechanical rejects and **139** syntactically eligible over 1,256 issues, and the 139 issue numbers are *identical* to the superseded run's. All 1,255 shared title and body digests match byte for byte. |
| the 66 candidates reproduce "only if reproduced" | **they did not.** The re-run yields **94 candidates**, not 66. |
| `sw-279-phase-2b-family-review/` survives if the candidate set is unchanged | **it is not unchanged, so the review was redone.** See `sw-279-phase-2b-family-review/SUPERSEDED.md` and `sw-279-phase-2b2-family-review/`. |

Two things the prediction did not anticipate:

- **The population is 1,256, not 1,255.** The count check fired and the fetch script refused to
  continue. The extra row is issue #1036, created six years before the cutoff, and the new manifest
  is a strict superset of the old. The cause is named in
  `sw-279-phase-2a2/population-discrepancy-decision.md`: the superseded run used the GitHub Search
  API, the re-harvest uses the GraphQL `issues` connection, and the two agree exactly in all
  fourteen creation years except 2020 — where the connection sees 180 and Search sees 179. #1036 is
  absent from GitHub's Search index. It lands in the *mechanical* reject bucket, so the larger
  population cannot have moved the candidate set.
- **`created_at` is now non-null in all 1,256 rows.** It was null in 1,255 of 1,255 here.

### The measured cost of the violation

The two semantic classifications agree on 107 of 139 rows and differ on 32, and the difference is
almost entirely one-directional: **30 rows this harvest rejected are candidates under the re-run,
and 2 it accepted are now rejects.** The clause signature is what makes it worth publishing rather
than filing:

| clause | superseded (primary / mentions) | re-run (primary / mentions) |
|---|---|---|
| **E1** bug report | **29 / 30** | **8 / 12** |
| **E4** program-specific support | 12 / **42** | 9 / **13** |
| E2 feature or change request | 11 / 13 | 11 / 14 |
| C2 Cobra is the subject | 12 / 12 | 10 / 14 |

E1 is the clause a maintainer's `bug` label encodes; E4 is what a maintainer's "please share your
code" reply encodes. Both collapsed. E2 — `enhancement`, an equally common label — did not move at
all, which is a real counter-signal and is stated rather than left out.

**This is consistent with the harm the ruling described; it is not proof of it.** The two runs used
different models, and two honest classifiers reading the same clean text would also disagree — the
re-run's own classifier marked 52 of 139 rows as genuine semantic boundaries. What can be said is
narrower and still worth saying: the run that held maintainer labels, comments and state in context
rejected issues as bug reports and as program-specific support at three times the rate of the run
that never saw them.
