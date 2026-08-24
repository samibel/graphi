# hero-bash fixture — W5.k SW-197

bash has **no cross-file construct** in its language specification per
`docs/plan/2026-08-per-language-ga-template-v1.md` §5.5's language-spec
test. The `source` builtin is shell execution, not a script-level
language construct, so callers/callees/references/related_files
**across files** are not askable of bash. The hero fixture exercises
**parse-determinism honest-empty** — same-shell intra-file symbols are
askable, cross-file results return well-formed empty.

## Layout

| file | role | QN |
|---|---|---|
| `serve.sh` | entry-point script | `serve.main`, `serve.run`, `serve.init` |
| `report.sh` | second script (also defines `init` for ambiguity) | `report.init` |
| `lib/util.sh` | canonical helper | `lib.hello`, `lib.shout` |

`main()` in serve.sh invokes `summarize` four times — that populates the
items list for `explain_symbol(summarize, max_items=1)` to trigger
`Limits.Truncated`, marking the result `partial` (the bash parallel of
the JVM twin's hjvm-15).

`init` is defined in BOTH serve.sh AND report.sh — that duplication is
what gives `callers(init)` its `ambiguous` outcome (both QNs match).

The two top-level scripts (`serve.sh`, `report.sh`) share ZERO
cross-file relations because the fixture intentionally avoids `source`.
The cross-file operations (`callers`/`callees`/`references`/`impact`/
`related_files` across files) return `empty` because bash's language
spec defines no askable cross-file construct (§5.5): a `callers` of
`serve.main` returns `empty`, a `references` of `serve.run` returns
`empty`, the `related_files` of `serve` summary does NOT invent
cross-file edges.

## Honest-empty substitutions (per AC-4)

- `callers(serve.main)` -> empty, with `description:` naming bash's
  absent cross-file construct.
- `references(lib.greet)` -> empty, because bash has no askable
  cross-file reference relation.
- `related_files(serve, dependents)` -> empty, same.

## Sources

- §5.5 abstention path (template plan): `docs/plan/2026-08-per-language-ga-template-v1.md:1090`
- bash resolver (informational, not the substituted claim):
  `engine/link/resolve_bash.go:3`
