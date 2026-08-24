<?php
// lib/util.php — the canonical helper module exercised by app/.
//
// QN keys on `<dir>.<funcname>` (langPackage at
// core/parse/parser_tswalk.go:240), so `lib/util.php` -> package `lib`.
// Functions here are `lib.core`, `lib.salute`, `lib.run`.
//
// `app/main.php` opens with `require '../lib/util.php'`. The
// requireBinder (engine/link/resolve_common.go:332) joins the spec to
// the caller's directory (`app` + `../lib/util.php` -> `lib/util.php`,
// posixDir `lib`), and `lib` is recorded as an ambient dir. Bare
// `core(...)` / `run(...)` references in `app/main.php` therefore
// resolve at the heuristic tier to `lib.core` / `lib.run` (the
// cross-file edges the witness scenarios pivot on).
//
// The intra-file `run -> core` call is the source of the
// `callees(lib.run) -> lib.core` edge in hphp-08.

function core($name) {
  return $name;
}

function salute($name) {
  return $name;
}

function run($name) {
  return core($name);
}
