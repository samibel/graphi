# Context Assembly (SW-016)

> Epic EP-003 · Token-Savings Ledger & Token-Efficient Context
> Package: `engine/context`

## Before

graphi's EP-001 query/search layer (`engine/query`, `engine/search`) returns
matched symbols with their source location, but an AI agent answering a question
about the repo still had to **read whole files** to get usable code context. The
`Match` / `ResultNode` carried a point location (file + line + column) and a
rank, but nothing shaped the raw file bytes into a minimal, citation-backed
bundle. Agents therefore consumed far more tokens than the answer required.

## After

`engine/context` adds a **context-shaping transform** that sits between the
EP-001 results and the agent. Given a query plus candidates, it produces a
compact, deterministic bundle of **winnowed, citation-backed evidence snippets**,
ranked and budget-bounded:

```mermaid
flowchart LR
  A["EP-001 results\n(search.Match / query.ResultNode)"] --> B["Intake adapters\nFromSearchMatches / FromQueryResult"]
  B --> C["Candidates\n(path, span, rank)"]
  C --> D["Rank\ndeterministic total order"]
  D --> E["Winnow\nspan + bounded padding\n(LocalReader, disk-only)"]
  E --> F["Budget\ngreedy rank-order inclusion\nTokens <= Budget"]
  F --> G["Bundle\n(snippets + exact citations)"]
```

### Key properties

- **Winnowed, not whole-file** — each snippet is the relevant span plus a bounded,
  configurable number of surrounding context lines; the rest of the file is
  excluded. A match on one line of a 1000-line file yields a snippet of a few
  lines, not 1000.
- **Exact citations** — every snippet carries `Citation{Path, StartLine, EndLine}`
  produced at extraction and carried verbatim; re-reading the cited span
  reproduces the snippet bytes exactly (the citation round-trips).
- **Token-budget ceiling** — snippets are included greedily in rank order until
  the configured budget is reached; the remainder is dropped. `Bundle.Tokens` is
  always `<= Budget`. Budget `<= 0` yields an empty bundle.
- **Deterministic across processes** — ranking is a stable total order
  (`Rank asc → Path asc → StartLine asc → EndLine asc`); there is no wall-clock,
  no randomness, and no map-iteration-order dependence. Identical inputs yield
  byte-identical bundles.
- **Local-first** — the only I/O path is `LocalReader`, which reads from disk
  only and **rejects remote sources** (`http(s)://`). Assembly never dials out.

## Why these decisions

- **Layer placement `engine/context`** — context shaping is an engine-level
  transform; placing it in `engine` keeps the `cmd → surfaces → engine → core`
  chain intact. It imports its engine siblings `engine/query` and `engine/search`
  only for the intake adapters. The SW-013 layer guard fires only on
  strictly-upward edges, so same-layer `engine→engine` is allowed and now appears
  as a verified allowed edge.
- **Local tokenizer (mirrors `eval.CountTokens`)** — the engine deliberately does
  not import `internal/eval`; runtime must not depend on tooling, and an
  `engine → internal` edge would fall outside the ranked chain. The tokenizer is
  a one-line stdlib call, duplicated intentionally for layer integrity.
- **Greedy rank-order inclusion** — the acceptance criterion says "include in
  rank order until the budget is reached and drop the remainder"; greedy
  prefix-filling is the faithful, deterministic reading (a knapsack fill would be
  neither rank-ordered nor trivially deterministic).
- **Citation = expanded span** — the citation labels the bytes actually carried,
  including context padding, so it always round-trips. The raw match line is
  preserved separately only if the caller supplies it.

## Scope boundary

This story emits the bundle only. **Metering** the bundle's tokens against a
baseline (SW-017), pricing the savings in USD (SW-018), persisting them (SW-019),
and the anti-gaming cap + readout (SW-020) are separate capabilities that compose
on this output.
