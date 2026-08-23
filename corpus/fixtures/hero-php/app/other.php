<?php
// app/other.php — second caller in the same `app/` directory;
// duplicate `init` with app/main.php (the AMBIGUOUS seed for
// hphp-07-callers-ambiguous).
//
// Both app/main.php and app/other.php live in `app/`, so the byDir
// index records `app.init` TWICE (two distinct NodeIds). The
// resolver drops the duplicate by directory, so any reference to
// `init` from inside `app/` — including the resolve.Strict lookup —
// returns AMBIGUOUS.
//
// `other_salute` is the NEGATIVE ANCHOR for hphp-06 and hphp-11: it
// never calls `core` (or `run`), so callers(lib.core) MUST NOT
// surface `app.other_salute`. The ccpp twin at
// corpus/fixtures/hero-c-cpp/c/c-other.c uses the same shape.

function init() {
  return "init-other";
}

function other_salute($name) {
  // never calls core() -- negative anchor for callers(lib.core)
  return "other-$name";
}
