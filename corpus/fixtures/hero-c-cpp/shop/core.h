/* common/core.h — the shared service *definitions* exercised by both C and C++.
 *
 * Mirrors the cScene test fixture (engine/link/resolve_clang_test.go:16):
 *   - app/main.c includes "shared/util.h" → ambient dir "app/shared"
 *   - main.c calls helper() → resolves to shared.helper via lookupInDirs
 *   - main.c calls sibling() → resolves to app.sibling via sameDir
 *
 * In our fixture: c/caller.c includes "common/core.h" → ambient dir
 * "c/common" — but byDir keys on the basename ("common" not "c/common"),
 * so the ambient lookup DOES NOT reach common.core from c/. The
 * c/cpp includeBinder (resolve_c.go:44) builds ambientDirs as the
 * path-joined-include's posix dir, which is sibling-relative, not
 * repo-relative. To make the cross-file calls edge resolve under
 * the cResolver's documented behavior, the test fixture's callers
 * must live in directories whose bare basename EQUALS the include
 * path's first segment. We therefore put the header in `shop/` and
 * the callers in `c/` and `cpp/` with `#include "shop/core.h"`,
 * producing ambientDirs = "c/shop" — still mismatched.
 *
 * The cResolver's only honest cross-file path under includeBinder
 * (resolve_common.go:436 ambientDirs fallback) requires the include
 * spec to resolve to a same-basename directory. We use `#include
 * "core.h"` from each caller — the header sits beside the caller
 * file (c/core.h and cpp/core.h). That makes ambientDir = the
 * caller's own dir ("c" / "cpp"), where byDir keys for "run"
 * resolve via sameDir-derived (not ambient). The cross-file edge
 * then comes from importFileTargets wiring the file→file imports
 * edge; the bare-call resolution is same-directory (classSamePackage)
 * because each caller sits in its own dir.
 *
 * BUT — to exercise the include binder's AMBIENT fallback, we need
 * a layout where the include path's first segment IS a sibling
 * directory reachable from the caller's dir. The cScene shape
 * (caller in `app/`, header in `app/shared/util.h`) works because
 * the include path "shared/util.h" resolves to `app/shared/util.h`,
 * whose posixDir is `app/shared` — and byDir keys on `app/shared`
 * (the FULL path's dir, not its basename).
 *
 * We mirror that exactly: the callers sit at the top level
 * (`caller.c`, `caller.cpp`) with `#include "shop/core.h"`, where
 * `shop/` is a sibling subdir. From caller.c, ambientDir =
 * posixDir("shop/core.h") = "shop". The functions live in
 * `shop/core.h`, source path `shop/core.h`, posixDir = "shop",
 * byDir["shop"]["core"] = id. lookupInDirs matches.
 *
 * That gives us: callers(common.core) = c.entry, c.twice,
 * cpp.entry, cpp.twice_cpp — cross-file via the ambient include
 * directory (engine/link/resolve_c.go:41).
 */

static inline const char *core(const char *name) {
  return name ? name : "";
}

static inline const char *salute(const char *name) {
  return name ? name : "";
}

static inline const char *run(const char *name) {
  return core(name);
}