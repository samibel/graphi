# P0 Completion Checklist — SW-132 … SW-149

**Derived from:** [`docs/plan/2026-07-graphi-p0-completion-delta-prd.md`](2026-07-graphi-p0-completion-delta-prd.md)
— §8 "Work Packages and Stories" and §9 "Recommended Story Order" of the **registered
Delta PRD**, and from nothing else. Not derived from the shaping document, the parent PRD,
the execution plan or any backlog: those cite the Delta PRD, and a checklist derived from
documents downstream of the authority is not the authority.
**Created:** 2026-07-29 (SW-132) · **Ticket state observed:** 2026-07-29

---

## What this list is

The Delta PRD names exactly **18 stories, SW-132 through SW-149, with no gaps**. This is
the one visible list of remaining P0 work. Story IDs, titles and order below are the Delta
PRD's; the two right-hand columns are repository observations and are labelled as such.

**A box is ticked only when the story's evidence is in
[`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml).** Not when a ticket closes, not
when a PR merges, not when the work "feels" done. That rule is the whole point of P0: no
hand-maintained green without a versioned artifact behind it. Every box below is therefore
open today, including the stories whose tickets already exist.

## The 18 stories

| # | Story | Title (as the Delta PRD states it) | WP | Ticket state (observed, not derived) | Done |
|---:|---|---|---|---|:--:|
| 1 | SW-132 | Register the P0 Completion Delta | WP-R0 | ticket exists — this story | [ ] |
| 2 | SW-133 | Run and Preserve the Authoritative v0.7.0 Baseline | WP-R1 | **UNMAPPED — no ticket exists.** See below. | [ ] |
| 3 | SW-134 | Diagnose `explain_symbol` and `change_risk` Partial Outcomes | WP-R1 | ticket exists | [ ] |
| 4 | SW-135 | Decide Whether the Candidate Must Move | WP-R1 | ticket exists | [ ] |
| 5 | SW-136 | Optional Minimal Correction Candidate | WP-R1 | ticket exists (conditional on SW-135) | [ ] |
| 6 | SW-137 | Freeze Gold Ontology and Annotation Guide | WP-R2 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 7 | SW-138 | Build the Gold Pilot | WP-R2 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 8 | SW-139 | Scale to the Full Gold Corpus | WP-R2 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 9 | SW-140 | Implement Versioned Gold Scorers | WP-R3 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 10 | SW-141 | Execute the First Sealed Accuracy Run | WP-R3 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 11 | SW-142 | Prioritize and Correct Measured Failures | WP-R4 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 12 | SW-143 | Run the Final Reference Performance Baseline | WP-R5 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 13 | SW-144 | Complete Full/Incremental Parity and Recovery Matrix | WP-R5 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 14 | SW-145 | Run the Manual Kubernetes Stress Scenario | WP-R5 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 15 | SW-146 | Independent Claims Review | WP-R6 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 16 | SW-147 | Two Consecutive Complete Green Runs | WP-R6 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 17 | SW-148 | Independent Clean Reproduction | WP-R6 | no ticket — not yet sliced; carried on the project backlog | [ ] |
| 18 | SW-149 | Freeze the Evidence Pack and Hold P0 Go/No-Go | WP-R6 | no ticket — not yet sliced; carried on the project backlog | [ ] |

Order 1–18 above is the Delta PRD's **mandatory sequence** (§9). Its safe parallel lane
(§9) permits SW-137 to begin while SW-133 and SW-134 run, permits reviewer recruitment for
SW-138, SW-146 and SW-148 to begin, and permits scorer schemas to be prototyped on
synthetic data — but **SW-141 must not start before SW-139 and SW-140 are complete**.

## SW-133 is UNMAPPED — and this checklist does not resolve it

SW-133 — "Run and Preserve the Authoritative v0.7.0 Baseline" — is the one story in the
Delta PRD's mandatory sequence with **no ticket anywhere**. It is recorded here exactly as
the Delta PRD states it, and flagged unmapped rather than quietly folded into another
story.

What is *not* asserted here, in either direction:

- The repository does contain a published two-run baseline of the frozen candidate v0.7.0
  at `5815db5` — `docs/eval/runs/2026-07-28-ubuntu-latest/` (SW-130), referenced by the WP2
  and WP4 rows of the evidence index. **Whether it discharges SW-133 is unverified, and
  this checklist does not assume that it does.**
- One of SW-133's acceptance criteria is *"Query sample floors are met"*. The published
  baseline reports **975 of the required 1 000** agent-tool executions pooled, in both runs
  — which is why gate 9 (`agent_context_p95`) reads UNKNOWN. So the discharge question is
  not a formality; at least one SW-133 criterion is visibly open against the artifact that
  would be claimed to satisfy it.
- SW-133's stated output path, `docs/eval/p0/baseline-v070.md`, does not exist. A summary
  table does exist, at `docs/eval/runs/2026-07-28-ubuntu-latest/p0-baseline.md`, beside the
  data it summarizes.

Resolving this — creating an SW-133 ticket, or recording a decision that SW-130 discharges
it and on what evidence — is deliberately **out of scope for SW-132**, whose job is to
register the document faithfully, not to adjudicate its contents.

## Inherited — complete, and not to be rebuilt

The Delta PRD §2.1 lists the work that is **complete at the start of the plan** and is an
*input* to it: the parent P0 PRD in-repo, the frozen published candidate `v0.7.0` at
`5815db5` with digests/SBOM/attestations and CGo-free build contract, the five pinned Go
repositories (`google/uuid`, `samber/lo`, `spf13/cobra`, `gin-gonic/gin`, `grpc/grpc-go`),
`kubernetes/kubernetes` as the manual stress target, the reference runner and scenario, the
measurement capabilities of the evaluation harness, `eval-full` measuring the frozen
candidate rather than the dispatch branch, the four evidence statuses
(`PASS`/`FAIL`/`UNKNOWN`/`STALE`), and the rejection of a PASS without evidence and
candidate provenance.

**None of it is reopened as new implementation work, and none of it appears as a story
above.** The Delta PRD's own caveat stands: *"These assets are inputs to this plan. They
are not proof that P0 has passed."*

## Completion owner

**The single P0 completion owner is the Graphi Maintainer (`samibel`)** — the solo
maintainer, who is also the implementation owner. This is the owner the Delta PRD's SW-132
requires ("Define a single P0 completion owner") and it matches the parent PRD's own
**Owner** field ("Graphi Maintainer (Solo-Projekt — eine Person trägt alle Rollen)").

## Independent reviewer requirement

The Delta PRD requires a reviewer who is **not** the implementation owner for two of the
eighteen stories:

- **SW-146 — Independent Claims Review:** an external reviewer inspects the public
  surfaces and confirms no claim exceeds the candidate artifact.
- **SW-148 — Independent Clean Reproduction:** *"A person other than the implementation
  owner must"* obtain the final candidate, verify the digest, obtain the pinned corpus, run
  the documented entry point, reproduce the reports, verify the evidence index, and sign
  the reproduction record.

**No such person is identified today.** Because the completion owner is also the sole
implementation owner, these two requirements cannot be met by the owner alone. Where the
parent PRD's solo-substitute rule (§8.8) permits a substitute — for example a clean,
freshly provisioned runner in place of a second person — the substitution must be named in
the evidence row and reported as the weaker evidence it is, exactly as WP2 already does.
A substitute is never recorded as if the original requirement had been met.

## Definition of Done

The Delta PRD §15 carries the item-level Definition of Done (five groups, every box
required for P0 completion). It is **not restated here** — a second copy would drift from
the authority. The group → story mapping, which the document's own structure makes
unambiguous, is:

| §15 group | Closed by |
|---|---|
| Candidate and baseline | SW-133, SW-134, SW-135, SW-136 (candidate sign-off lands with SW-149) |
| Gold Corpus | SW-137, SW-138, SW-139 |
| Accuracy | SW-140, SW-141 (regression tests / No-Go dispositions with SW-142) |
| Performance and reliability | SW-143, SW-144, SW-145 |
| Truth and reproducibility | SW-146, SW-147, SW-148, SW-149 |

Item-level mapping is deliberately not written here: the Delta PRD does not state it, and
inferring it would put an inference where an authority belongs.

## Related documents

- [`docs/plan/2026-07-graphi-p0-completion-delta-prd.md`](2026-07-graphi-p0-completion-delta-prd.md)
  — the registered Delta PRD (supplied verbatim; see its registration record).
- [`docs/plan/2026-07-graphi-p0-proof-and-truth-prd.md`](2026-07-graphi-p0-proof-and-truth-prd.md)
  — the parent P0 PRD: the gates and thresholds this plan completes.
- [`docs/plan/2026-07-graphi-9of10-execution-plan.md`](2026-07-graphi-9of10-execution-plan.md)
  — the execution plan: programme structure outside the P0 scope.
- [`docs/rc/evidence-index.md`](../rc/evidence-index.md) /
  [`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml) — the evidence source of
  truth. A box above is ticked from there, never the other way round.
