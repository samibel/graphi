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
