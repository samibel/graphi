# CodeRank qualification — candidate query review queue

Companion to `coderank-qualification-candidate-queries.json`.

**Status: 35 unreviewed candidates. Not a dataset. Nothing here is ground truth.**

Cobra revision read: `a0a6ae020bb3899ff0276067863e50523f897370` — the requested
commit, exactly. Obtained by cloning `https://github.com/spf13/cobra` into the
session scratchpad and checking that SHA out (`HEAD` subject: *Improve API to
get flag completion function (#2063)*). The Go module cache was tried first and
was empty: `go env GOMODCACHE` is the user's Go module cache, which has no
`github.com/spf13` directory at all.

| stratum | requested | produced |
|---|---:|---:|
| ambiguous | 7 | 7 |
| architecture_flow | 7 | 7 |
| config_docs | 0 | 0 |
| exact_identifier | 7 | 7 |
| exact_path | 8 | 8 |
| nl_behaviour | 6 | 6 |
| **total** | **35** | **35** |

No shortfall. Every candidate carries at least one grade-3 span, and every span
was read at the pinned revision before it was written down.

---

## What is already machine-verified (don't re-check these by hand)

These were checked by script against the pinned checkout, not by eye:

1. **Every span is in range.** For all 167 judgements across 30 files, `1 <=
   start_line <= end_line <= <lines in file>`.
2. **Every anchor really appears inside its span.** The anchor string is a
   substring of the cited line range in the checked-out file. A wrong line
   number would have to be wrong *and* still contain the anchor text to survive.
3. **The span convention matches cobra-v2.json.** Spans run from the first line
   of the doc comment to the closing brace in column 0. This rule was
   reverse-engineered from the existing dataset and reproduces cb-01, cb-02,
   cb-11, cb-15, cb-18, cb-22, cb-23, cb-24 and cb-33 exactly before any
   candidate was drafted.
4. **All 35 family ids are unique and collide with nothing.** Checked against
   all 224 `cobra-family-*` ids occurring anywhere in this repository — not just
   `cobra-v2.json`, but also `cobra-v1.json`, the harvest ledgers and the
   `2026-09-14-holdout-shaped-dev` draft.
5. **All 35 ids are new.** `cq-NN` collides with neither `cb-*`, `ci-*`, nor the
   `cd-*` ids used by the holdout-shaped draft.

So the reviewer's time belongs on **grades and family semantics**, not on line
numbers.

---

## Review order — most uncertain first

### Tier 1 — decide this first, it governs seven rows

**The policy question: may a new family reuse an existing family's grade-3 span?**

Eight candidate grade-3 spans are byte-identical to a grade-3 span already used
in `cobra-v2.json`. The machine-computed list is in the JSON under
`grade3_span_collisions_with_cobra_v2.exact_matches`. Summary:

| candidate | span | already the grade-3 of |
|---|---|---|
| cq-26 | `command.go:1139-1144` | ci-827 (`…2dac3eed574054fb`) |
| cq-26 | `args.go:24-39` | ci-1202, ci-2138 (`…1b1bb1e8402d0ab1`) |
| cq-20 | `command.go:257-266` | ci-1289 (`…bd4afc6a794098e9`), ci-1416 (`…e890b5e57973fefd`) |
| cq-16 | `command.go:470-476` | ci-470 (`…34f15dcf692eb04c`) |
| cq-18 | `command.go:41-45` | cb-28 (`…5139464c59f6d6a7`) |
| cq-23 | `completions.go:193-264` | cb-14 (`…6069545f930ad2e6`) |
| cq-33 | `command.go:1292-1300` | ci-1861 (`…1b1bb1e8402d0ab1`) |
| cq-34 | `command.go:879-881` | ci-1343 (`…37f0353f67a41908`) |

The precedent in `cobra-v2.json` permits this: `command.go:1146-1168` is the
grade-3 span of five distinct families there, and `command.go:257-266` is
already shared by two. But the qualification's own rule is what binds. **If the
rule is "one grade-3 span, one family", all seven rows above must be replaced
and the batch is seven short.** If it is "family ids must be distinct strings",
all seven stand as drafted.

Worst offender if only one row can be cut: **cq-26** (two collisions, one of
them against a two-row family).

### Tier 2 — spans that are a judgement call, not a lookup

- **cq-23** — grade-3 `bash_completionsV2.go:31-379` is **349 lines**: the whole
  embedded bash script. It is genuinely the answer to "how does the script call
  back", but it will blow past any per-span token ceiling. Consider narrowing to
  the `__%[1]s_get_completion_results` block, or demoting it and promoting
  `completions.go:193-264` to sole grade-3 (which then collides — see Tier 1).
  Note: my automatic span finder gets this function wrong (it stops at the first
  `}` in column 0, which is *inside* the shell script at line 44); 31-379 was set
  by hand after reading the file. Please confirm 379.
- **cq-22 (`Run`)** — grade-3 is a **struct field**, `command.go:132-133`, not a
  function. Does the qualification's conformance rule accept a two-line field
  span as an answer? If not, this row needs a different grade-3.
- **cq-33** — grade-2 `command.go:1285-1290` is **hand-widened**. The
  declaration alone is 1285-1286; I extended it to 1290 to include the three
  `sort.Interface` methods. Every other span in the file is machine-derived.
- **cq-32** — carries **two** grade-3 spans, one of which
  (`completions.go:352-364`) is a sub-span inside `getCompletions`. Sub-spans
  have precedent (ci-43, ci-1111, ci-1289) but the split between "the API you
  call" and "the place it takes effect" is mine, not the data's.
- **cq-25, cq-26, cq-29** — each has two grade-3 spans on the theory that the
  flow genuinely has two halves. cb-14 and cb-19 set that precedent; confirm it
  applies.

### Tier 3 — topic proximity without a span collision

These rows have unique family ids and unique grade-3 spans, but sit close enough
to an existing family that a human might call them the same thing:

- **cq-28** vs **cq-18** — both about command groups, within this batch. Grade-3
  spans differ (`checkCommandGroups` vs `type Group`). One family or two?
- **cq-21 (`Parent`)** — its grade-2/grade-1 set contains cb-20's grade-3
  (`command.go:1855-1874`) and cb-21's grade-3 (`command.go:1302-1329`). No
  grade-3 collision, but the judgement set intersects two existing families.
- **cq-22 (`Run`)** — grade-2 `command.go:134-135` is ci-1148's grade-3, and
  grade-1 `command.go:114-127` is the grade-3 of ci-2176 and ci-674.
- **cq-05 (`GenYamlCustom`)** — the holdout-shaped draft already has
  `cd-08 = GenYamlTreeCustom`. Different symbol, non-overlapping spans (92-147
  vs 59-85), same file. Tolerable?
- **cq-13 (`doc/util.go`)** — that draft's `cd-11` is `hasSeeAlso`, which lives
  in this file. Different stratum and different span; flagged for completeness.
- **cq-01** — grade-1 `flag_groups_test.go:22-195` is also cb-02's grade-1 span.
  Low stakes; drop the judgement if that bothers you.

### Tier 4 — spot-check only

The remaining rows have no collision, no hand-set span and no cross-batch
proximity. The useful check is *grade calibration*, not correctness:

- exact_identifier: cq-02, cq-03, cq-04, cq-06, cq-07
- exact_path: cq-08, cq-09, cq-10, cq-11, cq-12, cq-14, cq-15
- ambiguous: cq-17, cq-19
- architecture_flow: cq-24, cq-27
- nl_behaviour: cq-30, cq-31, cq-35

For `exact_path` the grade-3 span is the whole file (`1-<total lines>`), matching
cb-07 through cb-10. Confirm the qualification accepts whole-file spans at all —
if it does not, all eight `exact_path` rows need reshaping and the batch is eight
short. cq-13 (`doc/util.go`) is the deliberate hard case: a generic filename with
no topical signal.

---

## Open questions the reviewer must settle

1. May a new family reuse an existing family's grade-3 span? (governs 7 rows)
2. Are whole-file `exact_path` spans conformant? (governs 8 rows)
3. Is a struct-field span a valid grade-3 answer? (governs cq-22)
4. Is there a per-span token ceiling, and what does cq-23 do about it?
5. Is `cq-` the right id prefix, or should these be renumbered into `cb-41…` or
   `cd-65…` on merge?

## What I did not verify

Grades. Every grade in the JSON is a proposal by the drafter and nothing more.
The grade-3/grade-2 boundary in the `ambiguous` stratum is especially soft: I
followed cb-32/cb-33/cb-34, which put the exact-name match at 3 and every
plausible misread at 2, but cb-31 (`Gen`) has no grade-3 at all, so the existing
dataset is not self-consistent on this point.
