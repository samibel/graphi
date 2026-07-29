# Staleness notice — this baseline measures a candidate that has since been superseded

**Status as of 2026-07-29: this directory is `STALE`.** The successor release
**v0.7.1 at `80d67ed586723ab22704cf7aada316138cb1360e`** is published, tagged and
attested, and its freeze record
([`2026-07-p0-candidate-freeze-v071.md`](../../../decisions/2026-07-p0-candidate-freeze-v071.md))
is complete and in effect. In the same change, evidence rows **WP2**, **WP4** and
**M1** in `docs/rc/evidence-index.yaml` were marked `STALE`.

Nothing here is deleted, re-labelled, re-run in place, or re-pointed at another
candidate. Delta PRD §6.1 requires the first honest baseline to be preserved, and a
baseline that reads FAIL and UNKNOWN is still useful evidence. **Every byte of the
raw data stays.** `STALE` is a statement about *which candidate these numbers
describe*, not a judgement on how they were produced: they were measured honestly and
published correctly, and they remain true about `5815db5`.

---

## What is true today

| | |
|---|---|
| **What this directory measures** | candidate **v0.7.0 at `5815db5b053c2bb1bf3119cdb9939c1dea03cc45`**, runner class `ubuntu-latest`, harness `p0-perf/1` |
| **Is it still the candidate of record?** | **No — superseded on 2026-07-29** by **v0.7.1 at `80d67ed`** ([`docs/decisions/2026-07-p0-candidate-freeze-v071.md`](../../../decisions/2026-07-p0-candidate-freeze-v071.md), which supersedes [`…-v070.md`](../../../decisions/2026-07-p0-candidate-freeze-v070.md)) |
| **Are these numbers still valid statements about v0.7.0?** | **Yes.** They were honestly measured and correctly published, and remain reproducible from the committed raw data by `go run ./cmd/eval -aggregate <run-directory>` |
| **Are they statements about the current candidate?** | **No.** Not one of the ten verdicts carries across, in either direction |
| **Is the instrument that produced them still the current one?** | **No.** SW-136 corrected it — see below |
| **Are these numbers evidence about what graphi will measure next?** | **No.** A corrected instrument is a different instrument |
| **Does a baseline exist for v0.7.1?** | **No.** None at all, until SW-143–145 run one. The successor candidate has *no* measurements |

## What changed, and why it matters

SW-136 corrected **D1**, the defect SW-135's decision record
([`2026-07-p0-candidate-decision.md`](../../../decisions/2026-07-p0-candidate-decision.md))
named as forcing the candidate to move:

> the agent-tools countability rule at `cmd/eval/querylatency.go:430-435` omitted
> `partial` from the `allowed` set of `explain_symbol`, `change_risk` and
> `related_files`, while `agent_brief` — the fourth member of the same FR-8 pool —
> declared it countable.

The consequence for **this** directory, precisely:

- **`agent_context_p95` (PRD §12.2 gate 9) reads `UNKNOWN` here, and that `UNKNOWN`
  is now final.** It is not a pending reading. The pool reached 975 of FR-8's
  required 1000 executions in **both** runs; the 25 missing executions are a
  deterministic property of the graph and the item cap, not of the machine, so
  re-running at `5815db5` would produce 975 again, forever. This candidate has no
  path to a verdict on gate 9.
- **The undersampled p95 of 471.250 ms (run-a) is not a verdict and never was.** It
  sits inside the 500 ms threshold, and it is deliberately not published as a PASS
  (`unknown_is_not_pass` in `p0-baseline.json`). The 25 excluded executions are not a
  random 2.5% — they are exactly the executions on the highest-item-count symbols,
  systematically the most expensive. The distribution here is missing its heaviest
  members **by construction**. That is a reason for caution in both directions, not
  a hidden PASS.
- **The other nine verdicts are unaffected as statements about v0.7.0.** Eight PASS
  and one FAIL (`freshness_p95`, ~3.2× budget) were measured on parts of the harness
  D1 does not touch. They stay true about `5815db5`. They are **not** carried across
  to any successor candidate — see below.

## How this directory became `STALE`

`STALE` (PRD §12) means: measured honestly, but against a candidate that has since
been **superseded**. Superseding requires a published, tagged, attested successor
release — not merely a corrected instrument. All three steps have now happened, in
order:

1. **The correction landed in-tree** (SW-136), and at that point the candidate had
   **not** moved. This notice's earlier revision recorded exactly that: *the
   instrument moved, the candidate has not.* It was deliberately **not** marked
   `STALE` then, because it would have been false.
2. **v0.7.1 was published** on 2026-07-29 via `.github/workflows/release-dag.yml`
   (run [`30473740673`](https://github.com/samibel/graphi/actions/runs/30473740673),
   `success`), and its freeze record
   ([`2026-07-p0-candidate-freeze-v071.md`](../../../decisions/2026-07-p0-candidate-freeze-v071.md))
   was completed with the release SHA `80d67ed586723ab22704cf7aada316138cb1360e`, all
   eight asset digests cross-checked three ways, attestation verified for all eight
   assets against a failing negative control, and a tagless rebuild from a real clone
   reproducing all five published binaries bit-for-bit. **This directory therefore
   became evidence about a superseded candidate.** In the same change, per the freeze
   record's §9 rule 3, evidence rows **WP2**, **WP4** and **M1** in
   `docs/rc/evidence-index.yaml` were marked `STALE` and this notice's header was
   updated to say so.
3. **A successor baseline is a separate piece of work** (SW-143–145), and it has
   **not** run. Until it does, the successor candidate has **no measurements at all**.

**What did not happen, deliberately:** these runs were not deleted, not re-labelled,
not re-run, and not re-pointed at `80d67ed`. The `sha` on the now-`STALE` index rows
still reads `5815db5` — it names what was actually measured, and a `STALE` row is
never re-pointed at a new candidate without being re-measured.

## The one thing not to conclude

**None of this predicts that gate 9 will pass.** Correcting an instrument is not a
promise about what it will read. The corrected pool will contain precisely the
executions this one excluded — the largest answers — and the harness still measures
at an item cap of **10** while every shipped surface defaults to **20** (F4, on
`backlog.md`, deliberately not corrected in the same change). **Both** of those push
the measured distribution **upward**. Anyone reading the 471.250 ms above as a
preview of the corrected number is reading it wrong, and this paragraph is the
reason.

## References

- [`docs/eval/p0/partial-outcome-diagnosis.md`](../../p0/partial-outcome-diagnosis.md) — SW-134, the proof (F1–F5)
- [`docs/decisions/2026-07-p0-candidate-decision.md`](../../../decisions/2026-07-p0-candidate-decision.md) — SW-135, Outcome B, D1
- [`docs/decisions/2026-07-p0-candidate-freeze-v071.md`](../../../decisions/2026-07-p0-candidate-freeze-v071.md) — SW-136, the successor freeze record: **complete and in effect**, candidate v0.7.1 at `80d67ed`
- [`docs/decisions/2026-07-p0-candidate-freeze-v070.md`](../../../decisions/2026-07-p0-candidate-freeze-v070.md) — SW-131, the **superseded** candidate these numbers are about
- `cmd/eval/partialoutcome_regression_test.go` — the correction, pinned against these published tallies
