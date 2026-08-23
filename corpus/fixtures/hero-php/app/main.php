<?php
// app/main.php — the cross-file caller of the shared core service.
//
// QN keys on `<dir>.<bare>` (langPackage at
// core/parse/parser_tswalk.go:240), so functions in this file get QNs
// `app.entry`, `app.entry_twice`, `app.init` — distinct from the
// `lib.*` prefix used in `lib/util.php`.
//
// `require '../lib/util.php'` is the cross-file construct: the
// requireBinder (engine/link/resolve_common.go:332) turns the spec
// into a file->file `imports` edge (visible to related_files
// dependents at the FILE level) AND an ambient lookup dir (`lib`,
// the posixDir of the joined spec). Bare `core(...)` / `run(...)`
// references here therefore resolve at the heuristic tier to
// `lib.core` / `lib.run`.
//
// `app.init` duplicates the bare name in `app/other.php`; that
// duplication is what gives `callers(app.init)` its AMBIGUOUS outcome
// in hphp-07 (two distinct NodeIds under the same byDir key).

require '../lib/util.php';

function entry($name) {
  return run($name);
}

function entry_twice($name) {
  return run($name) . run($name);
}

function init() {
  return "init-main";
}
