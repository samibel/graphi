# Population discrepancy: 1,256 issues, not 1,255 — resolved without reading issue text

**Recorded before any issue title or body was fetched in this harvest, and before anything about
issue #1036's content was known to anyone.** The order matters: this decision could not have been
influenced by what #1036 turns out to say, because at the time it was written nobody had read it.

## What the check said and what happened

`scripts/eval/fetch_cobra_issue_population.py` refused to continue (exit code 3) with:

```
population count discrepancy: stop before issue-text fetch
{
  "population_size": 1256,
  "expected_population_size": 1255,
  "manifest_sha256": "2c35bf714abc32bc9074dfe75df7f5f36ba4d19958de9ca2eea596b353c74de4",
  ...
}
```

Section 1 of the frozen rule: *"The expected population is the 1,255 issues recorded by SW-279. A
different count is a discrepancy to report and resolve without reading issue text; it does not
authorize silently changing the population."*

## The difference is exactly one issue, and the new set is a strict superset

`population-discrepancy-record.json`, produced by
`scripts/eval/resolve_sw279_population_discrepancy.py` (selection set
`issue(number:) { number createdAt }`, nothing else):

| | |
|---|---|
| present in the new manifest, absent from the superseded one | **`1036`** |
| present in the superseded manifest, absent from the new one | *(none)* |
| `1036` created at | `2020-02-17T20:49:06Z` |
| cutoff | `2026-09-03T22:57:37Z` |
| created at or before the cutoff | **yes**, by six and a half years |

The other 1,255 numbers are identical. This is not a re-drawn population; it is the same population
plus one row that the first instrument did not see.

## The cause is an instrument defect, and it is named rather than assumed

The superseded harvest built its population with the **GitHub Search API** —
`first-fetch-record.json:15-16` in `../sw-279-phase-2a-superseded/`: `"tool_class": "installed
GitHub connector issue search"`, `"query_scope": "repo:spf13/cobra is:issue, partitioned by
creation time"`. The re-harvest uses the GraphQL **`issues` connection**, which enumerates the
repository's issues directly rather than querying an index.

`population-instrument-probe.json` compares the two instruments year by year
(`scripts/eval/probe_sw279_population_instruments.py`, selection sets
`issues(...) { nodes { number createdAt } }` and `search(...) { issueCount }`):

| creation year | GraphQL `issues` | GitHub Search | delta |
|---|---:|---:|---:|
| 2013–2019 | 4, 20, 49, 80, 123, 119, 108 | identical | 0 each |
| **2020** | **180** | **179** | **+1** |
| 2021–2026 (to cutoff) | 142, 154, 122, 66, 64, 25 | identical | 0 each |
| **total** | **1256** | **1255** | **+1** |

Every one of these Search counts equals the superseded run's own recorded `year_counts`
(`../sw-279-phase-2a-superseded/population-manifest.json:23-37`), including `"2020": 179`. So the
superseded run transcribed Search faithfully; **Search is the thing that is wrong**, and it is wrong
about exactly one issue, in exactly the year #1036 was created.

Two competing explanations are excluded on the evidence:

- **Not a pagination cap.** Search returns at most 1,000 results per query, which is why the first
  run partitioned by year and re-split capped years into quarters. #1036 was created in Q1 2020, and
  Search itself reports only 37 issues in `2020-01-01..2020-03-31`. No cap was reached.
- **Not a transcription slip.** A slip would be uncorrelated with the instrument. Here the
  superseded run's year buckets match Search exactly in all fourteen years, including the year that
  is short. The row was never delivered to be transcribed.

The remaining explanation is that **issue #1036 is absent from GitHub's Search index** while being
present in the repository's issue list. Search is an index with its own consistency guarantees; the
`issues` connection is the repository's own enumeration.

## The disposition

**The population for Phase 2 is the 1,256 issues in
`issue-numbers.txt` (`sha256 2c35bf714abc32bc9074dfe75df7f5f36ba4d19958de9ca2eea596b353c74de4`).**

This is applying the rule's definition, not amending it. Section 1 line 20-21 defines the population
as *"every `spf13/cobra` GitHub object that GitHub classifies as an issue rather than a pull request
and that existed at the Phase 1 rule commit's committer timestamp."* #1036 satisfies that definition
on both limbs: the GraphQL `issues` connection and the `issue(number:)` field never return pull
requests, and it was created in 2020. The sentence that follows — "The expected population is the
1,255 issues recorded by SW-279" — is a **check against a prior recording**, and that prior recording
is the artefact already found non-compliant in
`projects/graphi/stories/SW-279/decision-transport-overfetch.md`. Where a check disagrees with the
definition the check is calibrated against, the definition governs; the alternative is to let a
lossy index define a frozen population.

**The direction of the change is the safe one and it was not chosen.** The population grew, by a row
selected by nothing but its issue number and creation date. Nobody — not the fetcher, not the
classifier — knew what #1036 said when this was decided. It enters the mechanical Section 2
transform and the semantic C1–C5/E1–E5 pass on exactly the same terms as the other 1,255, and its
terminal state is recorded in the ledger like any other row.

**What this does not license.** It does not license reopening any reject, re-running Search with a
different partitioning, or treating a future count mismatch as routine. It resolves one discrepancy,
with a named cause, in one direction, before any content was visible.

## Downstream effect

`issue_numbers_sha256` changes from `b9f712af1bea40bbde437dee649a35346de023891839e8ae148138a94a8c4a17`
to `2c35bf714abc32bc9074dfe75df7f5f36ba4d19958de9ca2eea596b353c74de4`. Every artefact that binds to
the manifest digest binds to the new value. The superseded harvest keeps the old digest and is not
edited.

Because the population grew by one row, the Section 2 mechanical counts (1,116 rejects / 139
eligible) are a prediction over 1,255 rows, not 1,256. The re-run is expected to reproduce them for
the shared 1,255 and to add #1036 to exactly one of the two buckets; the report states which.

- resolved by: Claude Opus 5 (SW-279 Phase 2 re-harvest fetcher)
- resolved at: 2026-09-04T21:55:13Z (record), 2026-09-04T21:56:36Z (instrument probe)
- issue text read in reaching this decision: **none**
