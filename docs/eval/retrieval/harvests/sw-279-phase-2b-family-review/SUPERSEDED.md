# `sw-279-phase-2b-family-review/` — superseded, and retained

This family review is **superseded and must not be used to build the v2 dataset.** It is retained,
unedited, for the same reason the Phase 2a harvest beside it is: §8 of the frozen inclusion rule
requires an append-only record.

## Why

Nothing is wrong with the review itself. Reviewers A (pi, minimax/MiniMax-M3) and B (Codex) never
held the over-fetched issue envelopes that got Phase 2a re-harvested; their judgements were clean
and their blindness held. What failed was their **input set**.

`projects/graphi/stories/SW-279/decision-transport-overfetch.md` made the review conditional in
terms:

> "If the re-classification yields the same 66 candidates, `blind-queries.txt` is byte-identical and
> the review stands unchanged. If it does not, the review is redone on the new set."

The re-classification yielded **94 candidates, not 66**. 30 rows the superseded run rejected are
candidates under the re-run, and 2 it accepted are now rejects; the two runs agree on 107 of 139
rows. So `blind-queries.txt` here (106 lines, sha256 `93f9f792…dc81`) is missing 30 questions that
belong in the all-pairs comparison, and §7 requires family assignment over **all** provisional
candidates together with all existing `cobra-v1` queries. A closure computed over a short list is
not the closure §7 defines.

`scripts/eval/build_sw279_family_ledger.py` refuses to run against this directory for exactly that
reason, and names the count: "2 blind ids have no provenance, 30 provenance keys are absent from
the blind list."

## What replaces it

`../sw-279-phase-2b2-family-review/`, over 134 queries (94 new candidates + 40 existing
`cobra-v1`), with the same two reviewers and the same brief — the brief's only edits are the query
list's path, its sha256, and the count.

## What this directory still evidences

- The two reviewers' access-ledger rows and attestations of record, added while closing the §8 gap
  that `decision-holdout-dev-overlap.md` Q3 item 6 identified. Those rows describe *this* review and
  stay pointed at these files and these digests.
- The cb-05 / cb-11 same-task merge that `decision-holdout-dev-overlap.md` rests on. Both reviewers
  recorded it here, blind, over a pair of pre-existing `cobra-v1` queries that appear in both blind
  lists. The 2b2 review is a fresh judgement on a larger list and may or may not reproduce it; if it
  does not, that is a finding for the Phase 2 report, not something to reconcile quietly.
- The reviewers' own difficult-pair lists, which §7 requires to be retained and §10 asks a reviewer
  to audit.
