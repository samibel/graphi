# hero-lua fixture — W5.k SW-197

The shared Lua fixture for the SW-197 hero_lua tasks. The fixture exercises
three `.lua` files pinned under `corpus/fixtures/hero-lua/`:

- `lib/util.lua` — top-level `core`, `salute`, `run`. langPackage derives
  `lib` from the parent-dir basename, so every def's QN is `lib.<bare>`
  (`lib.core`, `lib.salute`, `lib.run`).
- `app/main.lua` — requires `../lib/util`, then calls `run(...)` bare.
  Defines `entry` (one call site of `run`), `twice` (two call sites
  of `run`, the partial fixture), and `init` (duplicated with `other.lua`,
  the ambiguity fixture). `app.entry` and `app.twice` together provide
  the three call sites `callers(lib.run)` and
  `explain_symbol(lib.run, max_items=1)` pivot on.
- `app/other.lua` — second file in the same `app/` directory; defines a
  duplicate `init` (so `app.init` is ambiguous), and a `greet` that calls
  `salute` (positive caller of `lib.salute`).

## Why `require('../lib/util')` (with slash and `../`)

The luaResolver uses `requireBinder` (`engine/link/resolve_ruby.go:38` +
`engine/link/resolve_common.go:332`) which:

1. joins the import spec against the including file's directory
   (`joinRel(dir, spec)` → `path.Join` + `path.Clean`), and
2. records the join's `posixDir` as an `ambientDirs` entry. Bare calls in
   the including file resolve through `lookupInDirs(idx, ambientDirs, name)`
   against `byDir[<ambientDir>]`.

The lua parser stores the require path verbatim
(`core/parse/parser_lua.go:218`); it does NOT translate the Lua dot-style
`package.path` convention (`lib.util` → `lib/util.lua`). To land the
ambient dir on the shared module's directory, the require path must
`path.Join` to the actual file's directory. `app/main.lua` sits under
`app/`, so the path must climb out and into `lib/`:

```
require('lib/util')   → app/lib/util             (lib under app, wrong)
require('../lib/util') → ../lib/util → lib/util  (climbs out, then into lib)
```

With `../lib/util` the joinRel result is `<fixture>/lib/util`,
ambientDir = `<fixture>/lib` matches `byDir[<fixture>/lib]` from
`lib/util.lua`, and the bare `run()` calls in `app/main.lua`
resolve to `lib.run` via the luaResolver's ambient-dir
fallback (`engine/link/resolve_common.go:436`).

The task brief mentions `require "lib.util"` as the include spec; the
layout requires the slash+`..` form to reach `<fixture>/lib/util.lua`
through the resolver's relative path machinery. The README records this
so anyone tightening the fixture does not regress to the dot form.

## Failure-class coverage

- ambiguous: `init` is defined in both `app/main.lua` and `app/other.lua`,
  both under `app/`. The SymbolIndex records two distinct NodeIds for
  QN `app.init` and marks `dirAmbiguous["app"]["init"] = true`
  (`engine/link/index.go:266`). `callers(app.init)` short-circuits to
  AMBIGUOUS.
- partial: `run` is called from two sites in `app/main.lua` (inside
  `twice`, called twice); `explain_symbol(lib.run, max_items=1)`
  truncates and sets `Limits.Truncated`.
- empty: `references(lib.core)` is empty (the Lua parser
  `core/parse/parser_lua.go:121` only emits `function_declaration` defs
  and `variable_declaration` targets — no `EdgeReferences`).
- not_found: `definition(NoSuchSymbolXyz)` returns clean not_found.
- absent anchor: `impact(lib.core)` must NOT surface `app.init`
  (`init` does not call `core`).

No network, no Lua interpreter. Functions are at module top level so the
parser's `luaCollectDefs` (`core/parse/parser_lua.go:121`) picks them up.
