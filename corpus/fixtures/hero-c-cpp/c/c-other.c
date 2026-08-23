/* c/c-other.c — second C caller in the same `c/` directory;
 * ambiguous `init_c` with c-caller.c.
 *
 * Both c-caller.c and c-other.c live in `c/`, so the byDir index
 * records `c.init_c` TWICE (two distinct NodeIds, one per source
 * location). The c/cpp resolver drops a duplicate by directory
 * (dirAmbiguous["c"]["init_c"] = true), so any reference to `init_c`
 * from inside `c/` — including the search/resolve.Strict lookup —
 * returns AMBIGUOUS, mirroring the bash/python "two files, same def"
 * shape.
 *
 * `other_salute` is a NEGATIVE ANCHOR: it never calls `core`, so
 * callers(shop.core) MUST NOT surface `c.other_salute`.
 */

#include "../shop/core.h"

const char *init_c(void) {
  return "init-c-other";
}

const char *other_salute(const char *name) {
  /* never calls core() — negative anchor for callers(shop.core) */
  return "hola";
}