# hero-php fixture — W5.k SW-197

The shared PHP fixture for the SW-197 hero_php tasks. PHP is one of the
nine cross-file-heuristic residual languages (SW-184 / SW-197); its
resolver (engine/link/resolve_ruby.go:19 phpResolver) models
`require 'x.php'` / `include 'x.php'` as a `require` imports edge via
the shared `requireBinder` (engine/link/resolve_common.go:332), and
`use Foo\Bar` brings `Foo` in as an ambient namespace. The same fixture
serves as the witness for all 16 hero-php scenarios at the heuristic
tier; no `GRAPHI_*_TYPERESOLVE` switch is required — the default
binary IS the gate (the same posture as hero-bash / hero-python).

## Layout

| file | role | QN |
|---|---|---|
| `lib/util.php` | canonical helper | `lib.core`, `lib.salute`, `lib.run` |
| `app/main.php` | cross-file caller | `app.entry`, `app.entry_twice`, `app.init` |
| `app/other.php` | duplicate `init` for ambiguity | `app.init`, `app.other_salute` |

`app/main.php` opens with `require '../lib/util.php'`. The
requireBinder (engine/link/resolve_common.go:332) joins the spec to
the file's directory (`app` + `../lib/util.php` → `lib/util.php`) and
records `lib` as an ambient dir. The c/cpp equivalent at
`corpus/fixtures/hero-c-cpp/shop/core.h` uses the same `../`-prefixed
include trick to make the ambient dir match `byDir`'s key.

Bare `core(...)` / `run(...)` references in `app/main.php` therefore
resolve at the heuristic tier to `lib.core` / `lib.run` — the cross-file
edges the witness scenarios pivot on. `other_salute` (defined in
`app/other.php`) NEVER calls `core`; it is the negative anchor for
`callers(lib.core)`.

The same-named `init` is defined in BOTH `app/main.php` and
`app/other.php`. Both files live in `app/`, so the byDir index records
`app.init` TWICE (two distinct NodeIds). The resolver drops the
duplicate as `dirAmbiguous["app"]["init"] = true`, so any reference to
`init` from `app/` — including `callers(app.init)` — returns
AMBIGUOUS. This mirrors the ccpp `init_c` / `init_cpp` shape at
`corpus/fixtures/hero-c-cpp/c/`.

PHP functions are kept at the top level (NOT inside `class X { ... }`)
because `phpCollectMethods` is the only class-body collector
(core/parse/parser_php.go:151) — top-level `function_definition` is the
shape `phpCollectDefs` indexes at the call-site of
`core/parse/parser_php.go:128`. Class methods are wired through a
separate code path that the hero-php gate does not exercise (mirrors
the ccpp gate's `function_definition`-only posture at
`corpus/fixtures/hero-c-cpp/shop/core.h`).

## Sources

- phpResolver: `engine/link/resolve_ruby.go:19`
- requireBinder: `engine/link/resolve_common.go:332`
- PHP parser: `core/parse/parser_php.go`
- ambient-dir join: the `../lib/util.php` trick mirrors the ccpp
  `#include "../shop/core.h"` pattern documented at
  `corpus/fixtures/hero-c-cpp/README.md:20`
