/* c/c-caller.c — the C caller of the shared core service.
 *
 * The directory basename `c` is the language-prefix namespace key (the
 * c/cpp parsers share `<dir>.<bare>` QN convention at
 * core/parse/parser_tswalk.go:127 + langPackage:240), so functions in
 * this file get QNs `c.entry`, `c.twice`, `c.init_c` — distinct from
 * the C++ callers in `cpp/` (QN prefix `cpp.*`).
 *
 * `#include "../shop/core.h"` is the cross-file construct: the
 * cResolver's includeBinder at engine/link/resolve_c.go:41 turns it
 * into a file→file `imports` edge (visible to related_files
 * dependents) AND an ambient lookup dir (`shop`, the posixDir of
 * the joined include path), through which bare `run(who)`
 * references resolve to `shop.run` (heuristic class — the
 * include-directory binder's confirmed cross-file claim).
 */

#include "../shop/core.h"

const char *entry(const char *who) {
  return run(who);
}

const char *twice(const char *who) {
  return run(who) + run(who + 1);
}

const char *init_c(void) {
  return "init-c";
}