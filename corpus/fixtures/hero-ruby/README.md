# hero-ruby fixture — W5.k SW-197

The shared Ruby fixture for the SW-197 hero_ruby tasks. The fixture
exercises one shared library (`lib/util.rb`) with the `core`, `salute`,
`run` module-level functions, and two callers in `app/`:

- `app/main.rb` calls `require_relative '../lib/util'`, then `core(...)`,
  `run(...)` (cross-file bare-call resolution), and has its own
  `app.entry` QN.
- `app/other.rb` defines a duplicate `init` function (ambiguous by
  directory — both files live in `app/`, so `app.init` collides).

The rubyResolver (`engine/link/resolve_ruby.go`) drives the cross-file
wiring. Its `requireBinder` (`engine/link/resolve_common.go:332`) takes
the `require_relative '../lib/util'` from `app/main.rb`, joins it
against `app/main.rb`'s directory (`app/`) to produce `lib/util.rb`,
records that as a file→file `imports` edge target, and adds `lib` (the
posixDir of the joined path) to `ambientDirs`. Bare `core(...)` and
`run(...)` references inside `app/main.rb` then resolve through the
ambient-dir lookup to `lib.core` and `lib.run` (heuristic tier — the
require-binding's confirmed cross-file claim).

Symbol QNs follow `<dir>.<bare>` (`core/parse/parser_tswalk.go:127`):
- `lib.util.rb` → `lib.core`, `lib.salute`, `lib.run`
- `app/main.rb` → `app.entry`
- `app/other.rb` → `app.init` (collides with `app.init` in
  `app/main.rb` if defined there — kept here as the duplicate
  declaration to make `init` ambiguous by directory).

The `init` ambiguity mirrors the C/C++ twin's `init_c` /
`init_cpp` shape (`corpus/hero-c-cpp/hccpp-07-callers-ambiguous.yaml`):
both files in `app/` declare `init`, so `dirAmbiguous["app"]["init"]`
flips to true and `callers(init)` returns AMBIGUOUS.

No network, no Ruby runtime. Pure-Go gotreesitter Ruby parser
(`core/parse/parser_ruby.go`, grammar tag
`grammar_subset_ruby`).