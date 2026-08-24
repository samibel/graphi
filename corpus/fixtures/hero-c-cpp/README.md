# hero-c-cpp fixture — W5.k SW-197

The shared C/C++ fixture for the SW-197 hero_ccpp tasks. The fixture
exercises both `.c` (c/c-caller.c, c/c-other.c) and `.cpp`
(cpp/cpp-caller.cpp, cpp/cpp-other.cpp) source in one shared corpus
tree, against one shared header at `shop/core.h`. The header is
included by all four callers via `#include "../shop/core.h"` — the
cResolver's includeBinder at `engine/link/resolve_c.go:41` turns that
into a file→file `imports` edge and an ambient lookup dir (`shop`,
the posixDir of the joined include path), through which bare
`run(who)` references resolve to `shop.run` (heuristic class).

The ccpp gate (`cmd/eval/hero_ccpp_test.go`) runs the 16 scenarios
against both languages under the heuristic tier. Per AC-5 a gate
discharged on c does NOT discharge cpp unless the artefact genuinely
covers both — the shared `hero_ccpp_test.go` therefore loads the same
16 scenarios under both languages and asserts PASS for each.

The `../` parent-dir traversal is REQUIRED by the cResolver's
includeBinder: the binder resolves `#include "shop/core.h"` from a
caller in `c/` to the joined path `c/shop/core.h` (whose posixDir
`c/shop` does NOT match `byDir["shop"]`), so the bare call lookup
would miss. `#include "../shop/core.h"` instead joins to
`shop/core.h`, whose posixDir `shop` matches `byDir["shop"]`. The
cScene test (engine/link/resolve_clang_test.go:16) avoids the same
trap because its caller lives in `app/` and its header in
`app/shared/util.h` — the joined path is `app/shared/util.h` whose
posixDir `app/shared` matches `byDir["app/shared"]`. The two
layouts share the same invariant (joined path's posixDir ==
byDir key).

No network, no C compiler. The fixture ships `static inline`
function definitions in the header so the c/cpp parsers
(`core/parse/parser_c.go:147`, `parser_cpp.go:132`) — which only
collect `function_definition` nodes, not function prototypes —
index `shop.core`, `shop.salute`, `shop.run` as the cross-file
callees the scenarios pivot on.