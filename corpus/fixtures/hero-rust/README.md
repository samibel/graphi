# hero-rust fixture — W5.k SW-197

The multi-file Rust fixture for the SW-197 hero-rust tasks. The
fixture exercises cross-module resolution through the rustResolver's
**clause-keyed core** at `engine/link/resolve_rust.go`, where the
clause is the SECOND-to-last `::` segment of an import path
(`use crate::shop::run` → clause `shop`, bare `run` resolves via
`crossModule("shop", "run")` to `shop.run`).

## Layout

| file | role | QN |
|---|---|---|
| `shop/core.rs` | canonical shared service | `shop.core`, `shop.salute`, `shop.run` |
| `app/caller.rs` | cross-module caller (`use crate::shop::run;`) | `app.entry`, `app.twice`, `app.init` |
| `app/other.rs` | second `init` definition (ambiguity shape) | `app.init` (2nd NodeId) |

## Resolution shape

The functions in `shop/core.rs` are kept at the **top level** of the
file (not wrapped in `mod shop { ... }`), so the cstWalk's
`langPackage(filename)` keys them by the parent directory's basename
(`shop/core.rs` → package `shop`, QN `shop.<bare>`) — matching the
`<dir>.<bare>` convention every tree-sitter language parser shares
(`core/parse/parser_tswalk.go:240`).

`app/caller.rs` declares `use crate::shop::run;` so the bare `run(who)`
call inside its body reaches `shop.run` through the heuristic
clause-keyed path. The fixture therefore exercises the resolver's
**cross-module** synthesis (G2SUB for Rust, where no typed binder
exists at this tier).

## Ambiguity

`init` is defined in BOTH `app/caller.rs` AND `app/other.rs`. Both
files share the `app/` directory, so both mint `app.init` — two
distinct NodeIds in `byDir["app"]["init"]`. `callers(init)` therefore
returns **ambiguous** (the resolver drops and counts instead of
guessing among same-clause collisions), matching the
`callers-ambiguous` discipline every language's hero fixture asserts.

## Negative anchor

`app.other_salute` (defined in `app/caller.rs`) NEVER calls `core`,
so it MUST NOT appear in `callers(shop.core)` or `impact(shop.core)` —
the language's honest cross-module negative anchor.

## Honest-empty opportunities (per AC-4)

- `references(shop.core)` → empty: the Rust parser
  (`core/parse/parser_rust.go`) emits no EdgeReferences edges; only
  EdgeCalls from `call_expression` sites and EdgeDefines from
  `function_item` / type / const nodes are tracked.
- `related_files(shop.core, dependents)` → empty at the function level:
  file→file imports edges target committed file nodes (`shop/core.rs`),
  not the function node `shop.core`, so the dependents collection
  has no neighbours to score.

## Sources

- rustResolver: `engine/link/resolve_rust.go:15`
- clause-keyed core: `engine/link/resolve_common.go:213` (resolveRefs)
- parser: `core/parse/parser_rust.go`
- QN convention: `core/parse/parser_tswalk.go:240` (langPackage)

No network, no `rustc`.
