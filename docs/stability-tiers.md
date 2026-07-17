# graphi — stability tiers (GA / Preview / Labs / Source-only)

**This file is the single canonical definition of graphi's stability tiers and of
how they map onto the machine-checked [capability coverage matrix](coverage-matrix.md).**
Every other surface — `readme.md`, the website, [`FEATURES.md`](FEATURES.md),
`CHANGELOG.md`, and `graphi help` — points here instead of restating the mapping.
If a statement anywhere else disagrees with this file, this file is wrong or that
statement is: they are not allowed to drift apart quietly.

## The two vocabularies

graphi carries two tier vocabularies, and they answer **different questions**.

| | Vocabulary | Where it lives | Enforced? |
|---|---|---|---|
| **Prose** | GA · Preview · Labs · Source-only | this file, and the public docs that point at it | no — human discipline |
| **Machine** | `stable` · `labs` · `disabled` | [`coverage-matrix.yaml`](coverage-matrix.yaml) | **yes** — `go run ./cmd/coverage -check` |

The machine vocabulary is law. `cmd/coverage -check` fails the build if the matrix
drifts from the live registries, and `internal/coverage.CheckStableTier` freezes
`tier: stable` to **exactly 12 operation ids**. Where this file and the matrix
disagree about a capability, **the matrix wins and this file changes.**

## The mapping

| Prose tier | What it promises | Matrix correspondence |
|---|---|---|
| **GA** | The 12 frozen operations, on **Go**, over **CLI + MCP stdio**, in the CGo-free binary. This is the whole product promise. | `tier: stable` — but only on the *operation* axis (see below) |
| **Preview** | A **GA operation** run against a **non-Go shipped language**. Same code path, same 12 ops — but the language is outside the GA promise and unproven. | `category: parser`, `status: shipped`, `tier: labs` (every parser except `go`) |
| **Labs** | A capability **outside the frozen 12**. Shipped and in-tree, advertised with a `[labs] ` prefix, but not something we stand behind. | `tier: labs` on any non-parser row |
| **Source-only** | Present in the tree or upstream, **not advertised, not reachable from any surface, unsupported**. | `tier: disabled` — **or no matrix row at all** (the matrix only covers capabilities the live registries expose) |

## The part that will trip you up

**GA is not a matrix tier.** The matrix's `tier` field answers exactly one
question: *"is this row one of the 12 frozen operations?"* It does **not** answer
*"is this GA?"*.

That has a consequence that looks alarming until you know why:

> The `go` parser row, the `cli` surface row and the `mcp` surface row all carry
> **`tier: labs`** — even though Go, the CLI and MCP stdio are the entire GA scope.

This is **structural, not a judgement**. `CheckStableTier` requires every
`tier: stable` row's id to be one of the 12 canonical operation ids
(`index`, `search`, `definition`, `callers`, `callees`, `references`,
`neighborhood`, `impact`, `agent_brief`, `related_files`, `explain_symbol`,
`change_risk`). A row named `go`, `cli` or `mcp` is not an operation id, so it is
**ineligible for `stable`** and can only ever be `labs`. Its `labs` label
therefore carries **no information about GA**.

So the matrix pins **one of GA's three axes**. GA is a conjunction:

```
GA  =  operation ∈ {the 12 tier:stable rows}     ← pinned by cmd/coverage -check
   AND language  = Go                            ← NOT encoded in the matrix
   AND surface   ∈ {CLI, MCP stdio}              ← NOT encoded in the matrix
   AND build     = the CGo-free default binary   ← enforced by cgoconformance CI
```

The language and surface axes live in prose — in this file — because no matrix
row expresses them. That is precisely why this file has to exist.

## Why Preview and Labs are both `tier: labs` — and are still different

Both map to `labs`, but they are **orthogonal axes**, not competing labels, so
they are kept distinct rather than collapsed:

- **Preview is about the *language* axis.** A Python user calling `callers` is
  using a **GA operation** on a **non-GA language**. The operation is frozen and
  proven; the language is not.
- **Labs is about the *capability* axis.** `analyze_taint` is not one of the 12
  operations at all, in any language.

Mechanically: a `labs` **parser** row (other than `go`) is Preview; a `labs` row
in any other category is Labs. The distinction is drawn cleanly and is not
collapsed. (Had it not been drawable, the honest fallback would have been to
collapse Preview into Labs and say so here.)

## What is GA today

**Operations (12, frozen).** `index`, `search`, `definition`, `callers`,
`callees`, `references`, `neighborhood`, `impact`, `agent_brief`,
`related_files`, `explain_symbol`, `change_risk`. `index` is lifecycle-only, so
the default MCP profile advertises **11** tools.

**Language.** **Go only.** Every other language is Preview.

**Surfaces.** **CLI** and **MCP stdio** only.

## What is explicitly NOT GA

Everything below is Labs, Preview or Source-only. None of it is part of the GA
promise, and none of it receives feature work in the current program.

| Not GA | Tier | Why |
|---|---|---|
| Every non-Go language (Python, TypeScript, Java, Rust, C/C++, …) | Preview | shipped and usable; outside the GA promise |
| HTTP / SSE surface | Labs | not an operation; not a GA surface |
| Daemon | Labs | not an operation; not a GA surface |
| Web / browser UI | Labs | not an operation; not a GA surface |
| TUI | Labs | not an operation; not a GA surface |
| VS Code extension | Labs | not an operation; not a GA surface |
| GitHub Action / PR automation | Labs | not an operation; not a GA surface |
| Refactorings (`inline`, `safe-delete`, `refactor`, `undo`) | Labs | write path, outside the frozen 12 |
| Taint (`analyze_taint`, `taint-query`, `interproc`) | Labs | outside the frozen 12 |
| Agent memory / distill / skillgen | Labs | outside the frozen 12 |
| Semantic search (`search_semantic`) | Labs | optional, off by default, no embedder shipped |
| **Wiki** | **Source-only** | `engine/wiki` exists in the tree but has **no matrix row, no MCP tool and no CLI subcommand** — it is not reachable from any surface |
| SaaS / hosted service | Source-only | does not exist; nothing is hosted |
| HTML parser | Source-only | `status: planned`, `tier: disabled` — grammar exists upstream, not shipped |

Source-only has **two shapes**, and both are represented today:

1. **A `tier: disabled` row.** Exactly one exists: the `html` parser, which is
   `status: planned` — its grammar exists upstream but is not subset-buildable in
   isolation, so graphi does not ship it.
2. **No row at all.** The matrix is machine-checked against the *live registries*,
   so a package that no surface exposes never appears in it. `engine/wiki` is the
   worked example: real code, real tests, zero reachability from CLI or MCP. Absence
   from the matrix is therefore not an oversight — it is the honest statement that
   nothing advertises the capability.

This is why "Source-only = `tier: disabled`" alone would be wrong: it would imply
the wiki is Labs (or GA) simply because the matrix is silent about it.

## How this is advertised at runtime

- **MCP:** Labs tool *descriptions* carry a `[labs] ` prefix
  (`surfaces/mcp/tools.go`, applied by `markLabs`). **Tool names never carry a
  tier tag** — they are frozen wire identifiers.
- **CLI:** `graphi help` leaves stable operations unmarked and prefixes everything
  else with `[labs] `. The marking is derived from `surfaces/mcp.StableOperations`,
  the same single source, so the CLI cannot drift from MCP.
- **Default profile:** Labs MCP tools require an explicit `graphi mcp -labs`; Labs
  HTTP routes require `GRAPHI_HTTP_LABS=1`. Labs is opt-in, never on by default.

## Claims discipline

A public number must name the artifact that produced it — a checked-in gate,
report or budget a reader can re-run. A number that names no source is not
conditioned, it is unsupported, and it does not belong on a public surface.

Nothing here asserts accuracy, performance, savings, adoption, security posture or
superiority over a competitor beyond what a checked-in artifact demonstrates.
Those remain **UNKNOWN** until independently measured, and UNKNOWN counts as *not
passed*.
