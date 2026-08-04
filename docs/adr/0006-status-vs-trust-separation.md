# ADR 0006 — Status vs. Trust: Separate Surfaces, One Freshness Source (P1)

- Status: Accepted (design decision of record for the P1 Trust Surface build;
  nothing is implemented — the source audit measures P1 at 0 %)
- Date: 2026-08-03
- Story: none yet — P1 stories are unsliced. The start itself is recorded in
  `docs/decisions/2026-08-p1-start-before-p0-go.md`; this ADR decides design,
  not permission.
- Spec / Gate: registered P1 PRD
  (`docs/plan/2026-07-graphi-p1-trust-surface-prd.md`) §7.1 (surface
  separation), §14.4 (atomicity), §18 (FR-4 freshness reuse + snapshot
  states); §43 No-Gos "Status und Trust widersprechen sich" and "Snapshot
  kann auf falscher Generation current erscheinen"
- Depends on: `docs/plan/2026-08-graphi-p1-code-baseline-audit.md` (the code
  ground truth every fact below cites), ADR 0004 (the marker+generation
  crash model the atomicity choice builds on)
- Feeds: every P1 story that reads freshness or publishes a snapshot;
  PRD §43 GO criterion "Snapshot atomar und generation-sicher"

## Context

P1 adds a second read surface over the same store `graphi status` reads:
`graphi trust-report` (CLI) and `graph_health` (MCP). Both must speak about
freshness — a trust verdict on a stale graph is worthless — and that creates
exactly the two failure modes the PRD names: a second, slowly diverging drift
implementation (FR-4 forbids "zweite Drift-Implementierung, andere
Branch-Semantik, andere Warm-Start-Definition, widersprüchliche
Recommendation"), and a trust answer that contradicts status (§43 No-Go).

The code facts, per the baseline audit:

- The entire status logic is unexported in `package main`
  (`cmd/graphi/status.go`): the versioned `--json` document
  (`schema_version` 1), the 0/1/2 exit contract, the drift/warm-start/
  recommendation derivation. There is no status library to reuse. The
  composable primitives underneath (`internal/state`, `internal/gitinfo`,
  `internal/ingestlock`, `cmd/internal/runtime/syncmeta.go`, read-only
  `engine/ingest`) do exist.
- There is **no staged-DB/pointer-flip build commit**: a full pass writes in
  place across three separate batch transactions
  (`engine/ingest/ingest.go:129-257`, audit gap 4), and WAL readers can
  observe intermediate graphs. Crash safety is marker+generation
  (`full_pass_in_progress` plus the `beginFullPass`/`finishFullPass`
  generation nonce — ADR 0004), not isolation.
- The raw trust signals are in-memory and reset per pass; for the one-shot
  CLI they are gone the moment the process exits, so a snapshot must be
  collected in the same pass that produced the signals.

## Decision

1. **`graphi status` and `graphi trust-report` stay separate surfaces
   (PRD §7.1).** `status` keeps answering: is a graph present, is it
   current, is an index running or crashed, what drift, what should the
   user do next. `trust-report` answers evidence quality: which confidence
   tiers, which degraded areas, which skips, where coverage ends, whether a
   policy fits. The trust surface MAY consume status facts internally; it
   MUST NOT copy status's responsibility — it mints no freshness prose and
   no rebuild recommendation of its own, so a contradictory recommendation
   (FR-4's fourth prohibition) has no place to come from.

2. **One freshness implementation, extracted into `internal/freshness`
   (FR-4).** The freshness logic moves out of `cmd/graphi/status.go` into a
   shared **read-only** package that `status` and the trust surface both
   consume. Extraction, not duplication: FR-4 forbids a second drift
   implementation, and two copies diverging is precisely the §43
   contradiction No-Go — with one package there is one drift semantics
   because there is only one implementation. Constraints on the extraction:
   behavior-preserving (the `schema_version` 1 JSON document and the 0/1/2
   exit codes do not change), and the pure-observer property is kept in the
   package itself (mode=ro, never creates state directories), so a trust
   caller cannot accidentally create or repair state (§44: "Keine
   automatische Reparatur eines degradierten Graphen durch Trust Report").

3. **Trust snapshot state is derived from the status facts and can never
   contradict them.** CURRENT / STALE / INCOMPLETE / UNAVAILABLE (§18) is
   computed as a pure function of (a) the shared freshness facts and (b)
   the snapshot's generation binding — never measured independently.
   Invariants, in the PRD's own terms: `status.current=false` can never
   yield CURRENT; a full-pass marker or running ingest reads INCOMPLETE;
   no graph, no snapshot, or unmigratable trust data reads UNAVAILABLE;
   source drift or a generation mismatch reads STALE. Missing evidence
   always maps away from CURRENT (§7.5), never toward it. Because the state
   is a derivation, a status/trust contradiction is unrepresentable rather
   than merely tested-for; parity tests pin the mapping anyway.

4. **Snapshot atomicity uses §14.4 variant 3: post-commit write,
   fail-closed "snapshot unavailable" until complete.** §14.4 permits three
   couplings. Variants 1 (same durable commit) and 2 (staged snapshot plus
   generation pointer flip) presuppose a staged-DB/pointer-flip build
   commit the ingest does not have — the audit shows the full pass writes
   in place across three batch transactions with no such commit step, and
   rebuilding the ingest around one is a rewrite P1 does not order. So the
   snapshot is written after the graph's own commit, stamped with the graph
   generation it observed, and a reader treats it as valid only when that
   stamp equals the live graph generation. Until the write completes — and
   after any crash, candidate change or schema change that breaks the
   binding — the snapshot reads UNAVAILABLE (or STALE where an older
   generation's snapshot exists), never CURRENT. §14.4's prohibitions hold
   by construction: an old snapshot on a new graph and a new snapshot on an
   old graph both fail the generation equality.

## Consequences

- **The named risk: a fail-closed window.** Between the graph's commit and
  the snapshot's publish, readers see snapshot UNAVAILABLE or STALE —
  never a wrong CURRENT. Variant 3 trades availability inside that window
  for correctness; the failure direction is "no answer", and the direction
  §43 forbids is "wrong answer". A crash inside the window leaves a
  committed graph with no snapshot; trust stays UNAVAILABLE until the next
  successful pass publishes one. Accepted, not accidental.
- One shared package means the two surfaces can never disagree about drift
  — and a freshness bug is now shared by both. Deliberate: one shared wrong
  answer is caught by the existing status tests; two subtly different
  answers are not caught by anything.
- The extraction edits `cmd/graphi/status.go` on `main`. That changes
  product bytes relative to the frozen P0 candidate v0.7.1 (`80d67ed`); it
  does **not** move the candidate — candidate moves remain P0
  decision-record actions, per the scope guard in
  `docs/decisions/2026-08-p1-start-before-p0-go.md`.
- `trust-report`/`graph_health` inherit status's observer discipline
  wholesale: read-only, no state-directory creation, no repair.
