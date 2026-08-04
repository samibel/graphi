# Decision: P1 implementation starts before the documented P0 GO its PRD requires

> ## ⚠️ IN EFFECT — a recorded deviation, not a gate result
>
> On 2026-08-03 the owner (`samibel`) explicitly directed that P1
> implementation begin — **"Implementiert"**, following the registered P1
> PRD and its source audit — although the PRD's own precondition, a
> documented P0 GO, is **not met**. This file records that deviation so it
> is visible, bounded and citable instead of implicit. **It is not a P0 GO,
> it approves nothing, and it ticks no P0 checklist box.**

**Status:** in effect · **Date:** 2026-08-03 · **Story:** none — an owner
directive, recorded here · **Risk:** high (a deliberate deviation from a
registered precondition) · **Decision authority:** `samibel` — product owner
and P0 completion owner (solo, per the parent PRD's solo-substitution rule)

---

## 1. The precondition, and the fact that it is not met

The registered P1 PRD's own header requires, verbatim: *"Voraussetzung: P0
„Proof and Truth" hat ein dokumentiertes GO erhalten."* That precondition is
not met, checkably:

- Per [`docs/plan/2026-07-graphi-p0-completion-checklist.md`](../plan/2026-07-graphi-p0-completion-checklist.md),
  **17 of the 18 P0 stories (SW-132 … SW-149) are open.** The single ticked
  row is SW-144 + SW-158 (the complete parity and recovery matrix).
- SW-149 — *"Freeze the Evidence Pack and Hold P0 Go/No-Go"* — has no
  ticket and has not been held. There is no GO document because there has
  been no Go/No-Go.
- The PRD's own registration record states the consequence in terms:
  *"Registering this text does not start P1; starting P1 before P0 GO would
  be a deliberate, owner-decided deviation and is not decided here."*

This record is where that deviation **is** decided.

## 2. The decision

On 2026-08-03 the owner directed that P1 implementation begin regardless.
The directive — **"Implementiert"** — named its subject by reference: the
registered PRD and the source audit. What each input is, and is not:

| Input | What it is | What it is not |
|---|---|---|
| [`docs/plan/2026-07-graphi-p1-trust-surface-prd.md`](../plan/2026-07-graphi-p1-trust-surface-prd.md) | the registered P1 requirements text, hash-pinned since 2026-08-03 | **not approved, not contract-frozen** — Draft by its own header, WP1.0 open, "Owner: noch festzulegen" |
| [`docs/plan/2026-08-graphi-p1-code-baseline-audit.md`](../plan/2026-08-graphi-p1-code-baseline-audit.md) | the code ground truth: P1 is 0 % implemented; every §3 raw signal exists but none is persisted | not an authorization — its own standing-constraint section says so |
| this record | the owner's start decision, dated and bounded by §3 | not a P0 GO, not evidence, not a completion claim |

Registration is not approval. The audit records; it does not authorize. The
only thing that starts P1 is the owner's explicit directive, and that
directive — not an inference from it — is the fact this file records.

## 3. Scope guard — binding on every piece of P1 work

1. **No P0 evidence artifact is modified.** `docs/rc/`, `docs/eval/`,
   `internal/parity/`, `cmd/parity/` — the P0 evidence chain — are out of
   bounds for P1 changes. P1 produces its own artifacts in its own places.
2. **The frozen candidate does not move.** v0.7.1 at `80d67ed` remains the
   P0 candidate of record
   ([`2026-07-p0-candidate-freeze-v071.md`](2026-07-p0-candidate-freeze-v071.md)).
   P1 changes product bytes on `main`; that is expected and is **not** a
   candidate move. Moving the candidate remains a P0 action under the
   freeze record's §9 change control (blocker-only, record before effect)
   and cannot happen as a side effect of P1 work.
3. **P1 completion claims stay governed by the P1 PRD's own gates.** §43
   GO criteria and §44 stop rules apply unreduced. §43's first GO criterion
   is itself *"P0 GO dokumentiert"* — so this deviation lets P1 be **built**
   early, not **completed** early: P1 cannot reach its own GO before P0
   reaches one.
4. **No P0 checklist box is ticked by this record.** The checklist's rule
   stands: a box is ticked only from
   [`docs/rc/evidence-index.yaml`](../rc/evidence-index.yaml), never from a
   decision, a ticket, or a merge.

## 4. What this record does not do

- It is **not a P0 GO** and not a Go/No-Go outcome of any kind. SW-149
  remains open and unscheduled.
- It does **not approve the P1 PRD** and does not freeze its contract —
  those are WP1.0 outcomes with their own evidence.
- It does **not fill the PRD's Owner field**, which remains
  "noch festzulegen" in the registered body.
- It does **not re-plan P0**: the Delta PRD's 18-story mandatory sequence
  stands exactly as the checklist states it.
- It adds **no evidence anywhere**. Starting work produces no green.

## 5. References

- The precondition holder: [`docs/plan/2026-07-graphi-p1-trust-surface-prd.md`](../plan/2026-07-graphi-p1-trust-surface-prd.md)
  (registered Draft; see its registration header — registration is not approval)
- The code ground truth: [`docs/plan/2026-08-graphi-p1-code-baseline-audit.md`](../plan/2026-08-graphi-p1-code-baseline-audit.md)
- The open P0 work: [`docs/plan/2026-07-graphi-p0-completion-checklist.md`](../plan/2026-07-graphi-p0-completion-checklist.md)
  (17 of 18 open; a box is ticked only from the evidence index)
- The frozen candidate this decision must not move:
  [`2026-07-p0-candidate-freeze-v071.md`](2026-07-p0-candidate-freeze-v071.md)
  (v0.7.1 at `80d67ed`)
- The first P1 design decision recorded under this start:
  [`docs/adr/0006-status-vs-trust-separation.md`](../adr/0006-status-vs-trust-separation.md)
