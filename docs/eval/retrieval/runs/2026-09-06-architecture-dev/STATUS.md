# STATUS — SW-282 architecture-flow gate (2026-09-06)

For whoever (human or Codex) picks this up next.

## What is done

- `architecture_flow` fusion-target gate: **PASS**
  (`0.4624671522846825 >= 0.4578575262772977`), was a **MISS**
  (`0.3127489860175855`) at session start.
- `nl_behaviour` gate, `exact_identifier` top1 no-regression, and
  `bundle_coverage`: all still **PASS**, none regressed.
- `config_docs` and `ambiguous` (ungated, reported only): flat or improved,
  no regression.
- Mechanism: `engine/retrieval/flow.go`'s `naturalLanguageRows` (already
  present, unfinished, before this session) now additionally does a bounded
  wrapper→callee expansion (`expandCallees`) before the Top-K cut — direct
  "calls" callees of the top 12 scored rows are admitted or re-scored using
  one bounded `OutgoingBounded` + `NodesByID` read per wrapper. See the full
  README in this directory for the exact scoring formula, the rejected
  intermediate tunings, and per-query before/after ranks.
- Exactly four files touched:
  `engine/retrieval/{retrieval.go,flow.go,flow_test.go,semantic_first_test.go}`.
  Everything else in the working tree (taskctx v2, embed pins, context
  definitions, recovery-dev artifacts, etc.) is untouched, pre-existing work.
- `go build ./...`, `go test ./engine/retrieval/...` /
  `./engine/agenttools/taskctx/...` / `./engine/context/...`, and
  `go run ./cmd/layerguard` all pass with `CGO_ENABLED=0`.

## What is NOT done / open

- `qrel_blind_smoke` still misses (`RELEASE: NO`, 31/64 vs k=56). This is
  the pre-existing, historical, held-out-informed smoke gate — untouched,
  not in scope, do not reinterpret as re-evaluated.
- The `task_context/2` **bundle** (not the ranking gate) only partially
  benefited: one query (`cb-20`) gained a graph-relation citation for both
  its grade-3 spans, but no new emitted source snippet; two queries
  (`cb-21`, `cb-22`) still miss the bundle entirely even though their
  grade-3 declaration now ranks at the border of the retrieval Top-10 —
  their new rank is outside both the 5-seed cut and the 1-hop graph-relation
  band task_context/2 builds from whichever nodes DO become seeds. Full
  40-query source-retention diagnostic is flat (32/63 → 31/63 complete
  spans; one fewer, not more).
- `cb-20`'s `updateParentsPflags` regressed slightly in raw retrieval rank
  (13 → 21) even though the query's overall ndcg improved (its sibling
  `mergePersistentFlags` moved up more). Widening the expansion
  (`wrapperExpansionWidth`/`calleeExpansionCap`) to chase it was tried and
  measured **worse** overall (0.4184 vs 0.4625) — left as a disclosed,
  known gap, not fixed.
- `internal/release.TestBuild_ProducesCGoFreeVersionStampedBinary` failed
  only when the full suite was run from a nested `.claude/worktrees/...`
  directory (a static-egress-scanner path-derivation artifact, malformed
  synthetic import paths in its output). Re-verified PASS (18.7s) from the
  canonical repository root — resolved, not a real finding, unrelated to
  `engine/retrieval`.
- Widening `task_context/2`'s own `retrievalSeedLimit`/`snippetNeighbors`
  to chase full bundle recovery for `cb-21`/`cb-22` was explicitly left for
  a follow-up story — it's a bundle-composition change, not a retrieval
  ranking change, and wasn't in this session's scope.

## Where to look

- Full narrative + tables: `README.md` in this directory.
- Ranking report: `after/cobra-v2-dev-report.json`, `after/aggregate.json`
  (761/761 reproduce clean).
- Bundle diagnostic: `bundles-after.json` (44/44 byte-identical across two
  independent index builds).
- Rejected/superseded starting point: `before-unfinished-attempt/` (the
  exact code state this session inherited, before callee expansion).
- Code: `engine/retrieval/flow.go` (`expandCallees`, `nameTermScore`,
  `scoreRow`), `engine/retrieval/retrieval.go` (`graphReader.calleeCandidates`,
  `degreeAdapter.calleeCandidates`, `retrievalVersion = "retrieval/4"`).

## Non-negotiables status

No commit/push/PR made. No holdout dataset opened. No repo/query-specific
rule added. All graph reads bounded/selective. All pre-existing dirty work
preserved. See README's "Non-negotiables checked" section for the full
list with evidence pointers.
