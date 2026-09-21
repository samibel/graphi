# Miss diagnosis: cd-29 and cd-87, the two surviving reviewed-split retrieval misses

Status: development diagnosis; no production rule changed; nothing here is
release evidence.

The reviewed-split gradings at compact/14 and compact/17, under both the
codex panel and the subagent panel
(`../runs/2026-09-15-dev-grading-reviewed/`,
`../runs/2026-09-20-dev-grading-compact14-kimi-panel/`,
`../runs/2026-09-20-dev-grading-compact17/`), fail exactly two questions
for retrieval reasons in every capture and under every panel:

- **cd-29** (`nl_behaviour`): "how does completion resolve a one-letter
  flag shorthand to its flag" — reviewed span `completions.go:841-858`
  (`findFlag`).
- **cd-87** (`architecture_flow`): "how does legacy Bash completion
  serialize available subcommands and their aliases" — reviewed span
  `bash_completions.go:447-457` (`writeCommands`).

## Evidence

Diagnostic instrument: `TestDevMissDiagnostic` (env-gated, this
directory's sibling package), run against a checkout of the corpus at
`a0a6ae020bb3899ff0276067863e50523f897370` (the dataset's cited SHA; the
checkout path is passed via `GRAPHI_PRODUCT_COMPACT_DEV_COBRA`) with
the pinned static embedder, `ModeAuto`, limit 50 — the same index build
and engine the dev capture uses.

**cd-29.** The reviewed span IS retrieved: row 11 of 50,
`completions.go 841-841`, `evidence_ranked`, base 6054, semantic rank 8,
lexical rank 15, graph support 557. Semantic carries it well
(`cobra.findFlag`, score 0.6054, semantic rank 8). Both captures then
exhaust the 325-token source budget on the rows ranked above it (the
compact/17 bundle reads 325/325 across 9 windows; the compact/14 bundle
324/325 across 9). `findFlag`'s window never enters the transcript. It
dies at **evidence-rank 11**: rows above it carry graph bonuses of
1000-1601 (e.g. `command.go:1806`, graph 1601) against its 557.

**cd-87.** The reviewed span IS retrieved, but at row 46 of 50:
`bash_completions.go 447-447`, `evidence_ranked`, base 4431, semantic
rank 26, lexical absent (lex 0), graph support 0. The top of the list is
occupied by the completion documentation pages
(`site/content/completions/_index.md`, twelve rows in the top 50) and by
`bash_completionsV2.go`; the legacy serializer `writeCommands` sits below
all of them. The compact/17 capture stops after 7 sources with 74 budget
tokens unspent (251/325) — the projection never reaches rank 46 at any
budget. Its callee neighbourhood is visible (`writeCmdAliases`,
`bash_completions.go:632`, row 16; `writeArgAliases`, row 21), but the
carrier function itself is ranked out of reach. It dies at
**evidence-rank 46**, i.e. in ranking, full stop.

## Rejected hypotheses

- **Candidate pool too small / span never retrieved.** Rejected: both
  spans are inside the top-50 pool in a ready semantic generation (rows
  11 and 46).
- **Semantic embedder misses the carrier functions.** Rejected:
  `findFlag` is semantic rank 8, `writeCommands` semantic rank 26; both
  are carried by the semantic channel. cd-87's failure is the fusion
  above semantic rank 26, not the embedding.
- **Source budget too small.** For cd-87, rejected directly: 74 of 325
  tokens were left unspent; the projection stopped on rank, not budget.
  For cd-29 the budget is full, but `DefaultSourceBudget` is a pinned
  methodology constant of the 1,200-token ceiling work and is out of
  scope as a fix; the binding constraint is that the answer sits at
  evidence-rank 11, above the fold only if ranking changes.
- **A projection rule can rescue these.** Rejected for both: the
  projection selects in evidence-rank order; neither miss is a
  window-cutting artefact (both rows' windows are coherent single
  declarations). No projection rule that uses only query text, retrieval
  results, graph data and repository bytes promotes row 11 or row 46
  without changing the ranking itself.

## Consequence

Both misses are ranking-side, at different depths (11 and 46). They are
the retrieval slice's business — evidence ranking and its audited integer
signals — with the ranking gates as their own evidence. The compact
projection, the 325-token budget, the 1,200-token ceiling and the
methodology are unchanged and not implicated.

## Reproduce

```sh
CGO_ENABLED=0 GRAPHI_MISS_DIAG=1 \
GRAPHI_PRODUCT_COMPACT_DEV_COBRA=/abs/path/to/cobra-at-a0a6ae02 \
  go test ./internal/eval/retrieval -run '^TestDevMissDiagnostic$' -count=1 -v
```

## Addendum: a measured rejected fix (caller-side graph admission)

After the diagnosis above, one ranking change was built and measured, then
reverted. Mechanism: mirror `expandCallees` across the "calls" edge
direction — admit the direct callers of the top-16 rows (width 16, cap 4:
cd-87's supporting callee `writeCmdAliases` sits at evidence-rank 16 and has
exactly one direct caller, `writeCommands`, verified against the production
graph), gated so a caller enters only with its own query evidence (an exact
name-term score, or a leaf-suffix predicate — "command" inside "subcommand"
— evaluated only against the identifier's own leaf name, never its
receiver). Unit evidence passed: the name-evidenced caller is admitted, the
relation-only orchestrator (`ExecuteC`) is not, and no admitted caller can
outrank its seed.

Measurement against the frozen targets (scratch reports, candidate tree
`63952b44`+dirty, pinned corpus and embedder, `cobra-v2-dev`):

- cd-87's `writeCommands` moved from evidence-rank 46 to 17 — admitted and
  raised to its seed's cap, but still below the transcript fold (~7-8).
- cd-29's `findFlag` moved from 11 to **15** — caller admissions above it
  (`InitDefaultCompletionCmd`, `IsAvailableCommand` — lifecycle names that
  lexically overlap completion queries) pushed it further from the fold.
- architecture-flow nDCG@10 fell from 0.4625 to **0.3625**, below the
  frozen floor 0.4579 — a MISS on a 5-query stratum. nl_behaviour (0.7127),
  exact-identifier Top-1 (1.0) and bundle coverage (6/6) passed; the
  architecture-flow number was identical with and without the partial-score
  variant, so the caller admissions, not the suffix scoring, drove the
  damage.

The change was reverted in full; production ranking is unchanged. The
negative is recorded because it is load-bearing: caller-side graph admission
at any seed width that reaches rank 16 reshuffles flow rankings
destructively, because the callers it surfaces on completion-flow queries
are lifecycle initializers whose names overlap the query's terms. A future
attempt needs a discriminator between lifecycle-initializer callers and
flow-carrier callers; none was found here. cd-29 and cd-87 remain missed in
production.
