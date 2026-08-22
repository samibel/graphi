# SW-150 sweep record — CPU attribution and one-sided gate-9 p95

**Date:** 2026-08-20 · **Ticket:** SW-150 · **Round:** 1

This is the record the SW-150 correction blocks cite. It enumerates **every** occurrence
in `docs/` of (a) the superseded "two CPU models / two CPU families / different silicon"
framing and (b) a gate-9 `agent_context_p95` p95 figure, and classifies each as
**corrected here**, **already covered** by a dated in-file correction, or **correct as
written**.

---

## Why this record was rewritten

Round 1 of this sweep used

```bash
grep -rn "8573C\|two CPU families\|two different CPU families\|two CPU models" docs/ | grep -v "/run-[ab]/"
```

and then asserted completeness from it. That pattern matches only *specific* strings —
a model number, or the exact phrase with "two" in it. It **cannot** match a class-level
phrasing such as "a different CPU family" (singular, no model number). The tell is in
the result: a sweep that returns exactly the instances the ticket already named and
nothing else is not evidence of exhaustiveness.

## Commands that establish completeness

Run from the repository root, against the corrected tree.

```bash
# (i) CPU attribution — the CLASS, not four literal strings.
grep -rniE 'cpu famil|different silicon|different (cpu|hardware|machine|runner|host)|two cpus|8573|8370|9v74|7763' docs/

# (ii) gate-9 p95 — every spelling of both figures, including spaced and µs forms.
grep -rnE '471[ .]?250|471250|601[ .]?732|601732' docs/

# (ii, prose pass) — surfaces that discuss the gate without quoting a figure.
grep -rniE 'agent.context.p95|gate 9|undersampled pool' docs/
```

**Scope.** All of `docs/`. The 40 per-job `run-{a,b}/**/environment.json` files and the
sibling raw arrays are the ground truth these corrections are *derived from*, not claims
about it, so they are counted but not classified individually; `p0-baseline.json` is
counted as one row for the same reason. Every other hit is classified below.

**Result.** Occurrences classified: **13 corrected here** (plus 2 regenerated mirrors),
**12 already covered**, **13 correct as written**. The widened pattern found
**8 occurrences the round-1 pattern could not reach**, one of which was a genuine
uncorrected instance.

---

## Class (i) — CPU misattribution

| # | File : line(s) | Claim | Classification |
|---|---|---|---|
| 1 | `docs/rc/evidence-index.yaml` WP2 `current:` | "different silicon (Intel Xeon Platinum 8573C / AMD EPYC 9V74)" | **Corrected here** — in-string dated correction appended to the `current:` scalar |
| 2 | `docs/rc/evidence-index.md` WP2 row | mirror of #1 | **Regenerated** from the `.yaml` by `go run ./cmd/evidence -generate`; never hand-edited |
| 3 | `docs/rc/evidence-index.yaml` WP4 `current:` | "2.7% apart on two different CPU families" | **Corrected here** — true only in the AMD-generation sense; qualified in place |
| 4 | `docs/rc/evidence-index.md` WP4 row | mirror of #3 | **Regenerated** |
| 5 | `docs/decisions/2026-07-p0-candidate-decision.md:128` (§2.1) | "on two CPU families" | **Corrected here** — additive block after the paragraph |
| 6 | `docs/decisions/2026-07-p0-candidate-decision.md:135` (§2.2) | "on an Intel Xeon Platinum 8573C and an AMD EPYC 9V74" | **Corrected here** — additive block, self-contained in both directions |
| 7 | `docs/decisions/2026-07-p0-candidate-decision.md:182` (§3.1) | "both runs, two CPU families" | **Corrected here** — additive block, self-contained in both directions |
| 8 | `docs/decisions/2026-07-p0-candidate-freeze-v071.md:110` | "8573C and an AMD EPYC 9V74" | **Corrected here** — additive block |
| 9 | `docs/eval/p0/partial-outcome-diagnosis.md:231` (§4.2) | "an Intel Xeon 8573C (run-a) and an AMD EPYC 9V74 (run-b)" | **Corrected here** — additive block |
| 10 | `docs/eval/runs/2026-07-28-ubuntu-latest/README.md:115` (FR-9 substitution §) | "run-b landed on a different CPU family" | **Corrected here** — additive block after the paragraph |
| 11 | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md` (multiple) | SW-130 four-model correction | **Already covered** — it *is* the correction every other block cites |
| 12 | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.json` (per-job CPU data) | machine-readable per-job CPU data | **Correct as written** — ground truth, not a claim |
| 13 | `docs/decisions/2026-07-p0-candidate-freeze.md`, `freeze-v070.md`, `freeze-v071.md` (FR-1 rebuild claims) | "on a different machine and a different host OS" | **Correct as written** — out of class; PRD FR-1 *reproducible-build* claim, not about the eval baseline |

**Class (i) totals:** 13 occurrences — 8 corrected here (6 additive blocks + 2 `.yaml`
in-string corrections), 2 regenerated mirrors, 1 already covered, 2 correct as written.

---

## Class (ii) — one-sided gate-9 p95

| # | File : line(s) | What it carried | Classification |
|---|---|---|---|
| 1 | `docs/decisions/2026-07-p0-candidate-freeze-v071.md:171` | run-a only (471.250 ms) | **Corrected here** — additive block with both figures and the two-sided framing |
| 2 | `docs/eval/p0/partial-outcome-diagnosis.md` §11 | run-a only | **Corrected here** — additive block; run-b's figure is made to serve §11's *own* argument |
| 3 | `docs/eval/p0/partial-outcome-diagnosis.md` §12 evidence row | run-a only (471 250 µs) | **Corrected here** — row now carries both pool figures |
| 4 | `docs/eval/runs/2026-07-28-ubuntu-latest/STALENESS-NOTICE.md:50` | run-a only | **Corrected here** — additive block, indented into the bullet it corrects |
| 5 | `docs/eval/runs/2026-07-28-ubuntu-latest/STALENESS-NOTICE.md:100` | run-a only | **Corrected here** — additive block |
| 6 | `docs/decisions/2026-07-p0-candidate-decision.md` §5 | run-a figure, one-sided in isolation | **Already covered** — the sentence carries an explicit in-document pointer to §6, which holds both figures |
| 7 | `docs/decisions/2026-07-p0-candidate-decision.md` §6 | both figures | **Already covered** — SW-130 correction round 1, inline |
| 8 | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md:94-128` | both figures | **Already covered** — SW-130 |
| 9 | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md:243` | both figures (deviations table) | **Already covered** — SW-130 |
| 10 | `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.json` | both figures | **Already covered** — SW-130 |
| 11 | `docs/eval/runs/2026-07-28-ubuntu-latest/README.md:70` | both figures | **Already covered** — SW-130 |
| 12 | `docs/rc/evidence-index.yaml` WP4 `current:` | both figures | **Already covered** — SW-130 |
| 13 | `docs/decisions/2026-07-p0-candidate-decision.md:519` (§10) | `p95_us 471250` inside a verbatim `jq` transcript | **Correct as written** — it is literal output of a command run against **run-a's** `report.json`, whose path is on the line above; a transcript is not a claim |
| 14 | `docs/decisions/2026-07-p0-candidate-decision.md:539` | "§11 the 471.250 ms" in a reference list | **Correct as written** — it is a section *title*, and that section carries both figures |

**Class (ii) totals:** 14 occurrences — 5 corrected here, 7 already covered, 2 correct as
written.

---

## Summary

| Defect class | Classified | Corrected here | Regenerated mirror | Already covered | Correct as written |
|---|---|---|---|---|---|
| (i) CPU misattribution | 13 | 8 | 2 | 1 | 2 |
| (ii) one-sided gate-9 p95 | 14 | 5 | — | 7 | 2 |
| **Total** | **27** | **13** | **2** | **8** | **4** |

## Dating convention

The SW-150 correction blocks are dated **2026-08-20**, the day they land. The in-repo
precedent (SW-130's blocks read `CORRECTION 2026-07-29`, the day *those* landed) is
followed. Dating a correction the day it actually lands, not the day the ticket was
authored, is the convention this programme exists to enforce.
