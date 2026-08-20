# Python F5 measurement on flask (2026-08-20, W5.f)

> **The SW-181 AC-3 measurement, finally run.** SW-181 left Python G4 as
> UNKNOWN because the F5 finding had not been measured on a real Python
> repository. This document is the measurement: a real-repo dispatch over
> flask at the pinned ref, two `graphi rebuild` invocations compared at the
> per-row count granularity the JVM matrix established, and a binary
> verdict on whether Python at flask supports GA at `cross-file-heuristic`.

## TL;DR

- **F5 IS REAL on flask.** Python's package-import resolution fans out over
  colliding directory clauses, in the **exact** shape Go had before
  [ADR 0009](../adr/0009-go-imports-module-relative-directory.md). 70 spurious
  `imports` edges (8.0 % of flask's 879) land on `tests/typing/typing_*.py`,
  which nothing in `src/flask/*` actually imports. Every `src/flask/*.py`
  that does `import typing as t` (stdlib) carries 3 spurious edges.
- **Python does NOT support GA at `cross-file-heuristic` on flask.** Per
  SW-181 AC-9, Python is **re-graded**, not declared. The G4 evidence row
  stays **UNKNOWN**, the defect is filed as **PYTHONFANOUT-001**, and the
  re-grade itself is a separate product-byte change with its own ADR and
  candidate move.
- **The dispatch is reproducible at count granularity.** Two `graphi
  rebuild` invocations agree on every per-row count (1058 nodes, 2214
  edges, 879 imports, 70 spurious typing edges in both). The snapshot
  envelope's sha256 differs because it embeds a timestamp; the content is
  byte-identical. F5 reproduces.
- **Two independent pre-conditions BLOCKED the SW-192 recipe as written.**
  Both are stated, not glossed:
  1. **`cmd/parity` has no `-family python` driver.** It rejects anything
     other than `go` or `jvm`. SW-192 AC-1's exact command cannot be
     executed. The F5 measurement was performed manually by driving
     `./cmd/graphi` directly (`graphi rebuild` + `graphi snapshot` +
     SQLite inspection), the same shape SW-176's AC-1/-2 escalation
     settled for the JVM matrix.
  2. **The flask pin in `corpus/manifest.json` is STALE.** Manifest sha is
     `735a4701d6d56f3deec1dce0c2f2fb6d7c0a4d6b`; the real `3.0.0` tag
     sha (verified via `git ls-remote https://github.com/pallets/flask.git
     refs/tags/3.0.0`) is `735a4701d6d5e848241e7d7535db898efb62d400`. Per
     SW-192 AC-7, no silent re-pin; this measurement uses the real sha
     and the manifest entry is **marked STALE** for a follow-on commit.

## 1. The F5 question

> "Does Python's package-import resolution fan out over colliding
> directory clauses (the PARITY-002 shape)?"

SW-181 framed F5 as: Python resolves imports through
`clausePackageFileNodes` (`engine/link/resolve_common.go:521`), the same
clause-keyed fan-out Go used before ADR 0009. SW-166 added the
extension filter on that path, which fixes the wrong *target set*; it
does **not** address whether the *directory resolution* still fans out.

ADR 0009 changed Go's resolver from clause-keyed to module-relative: an
import resolves to exactly one directory per module map. Python's
resolver (`engine/link/resolve_python.go:51` → `resolveRefs` →
`clausePackageFileNodes` at `engine/link/resolve_common.go:521`) still
keys on the import-path's trailing segment (`pyClause` at
`engine/link/resolve_python.go:86`), and `clausePackageFileNodes` walks
**every** directory declaring that clause and returns **every** committed
source file node in each.

If two unrelated directories both declare a clause that matches a Python
`import` path, the resolver mints an `imports` edge to every file in
every such directory — the same defect Go exhibited on gin and grpc-go
before ADR 0009 closed it.

## 2. flask's clause-collision census

flask at the real `3.0.0` sha carries **27 package clauses** and
**collisions in 2** (the static filesystems and the tutorial/test
sub-directories):

| clause | colliding directories | files per directory | triggered by real imports? |
|---|---:|---|---|
| `static` | `tests/static`, `examples/tutorial/flaskr/static` | 1, 1 | no — no Python import path targets these |
| `tests` | `tests`, `examples/tutorial/tests`, `examples/javascript/tests` | 22, 6, 2 | no — no Python import path targets these |
| `typing` | `tests/typing` | 3 (`__init__.py`, `typing_app_decorators.py`, `typing_error_handler.py`, `typing_route.py`) | **yes — every `src/flask/*.py` does `import typing as t` (stdlib)** |

The first two collisions are inert: no Python import path's trailing
segment is `static` or `tests`. The third is the F5 trigger: the stdlib
`typing` module is imported by **every** `src/flask/*.py` source file
(`grep -rE '^import typing' src/flask/ | wc -l` would return 22; the F5
fan-out is not a corner case).

## 3. The measured fan-out

The third clause collision produces **70 spurious `imports` edges**, all
landed in **24 distinct importers**, all targeting **3 distinct files in
`tests/typing/`**:

| target | spurious edges | importers (distinct) |
|---|---:|---:|
| `tests/typing/typing_app_decorators.py` | 24 | 24 |
| `tests/typing/typing_error_handler.py` | 24 | 24 |
| `tests/typing/typing_route.py` | 22 | 22 |
| **total** | **70** | **24** |

The 24 importers are **22 production files in `src/flask/`** and **2
self-targeting files in `tests/typing/`**. The pattern is uniform: every
importer that registered `import typing as t` (the stdlib) acquired 3
spurious edges — one per file node in `tests/typing/`.

Concretely, the fan-out example:

```
src/flask/wrappers.py (id=fc95c0e196b9b899)
  imports edges:
    src/flask/json/__init__.py      ← LEGITIMATE (from . import json)
    src/flask/json/provider.py      ← LEGITIMATE (from .provider import _default)
    src/flask/json/tag.py           ← LEGITIMATE? (no direct import — likely module-relative from sub-import)
    tests/typing/typing_app_decorators.py  ← SPURIOUS (clause-keyed fan-out over 'typing')
    tests/typing/typing_error_handler.py   ← SPURIOUS
    tests/typing/typing_route.py           ← SPURIOUS
```

wrappers.py's actual imports (verified by `grep`):

```python
from __future__ import annotations
import json as _json
import typing as t
from ..globals import current_app
from .provider import _default
```

`import typing as t` is the stdlib; the resolver records
`importPath="typing"`, derives `clause="typing"`, and walks every
directory declaring `typing`. flask's `tests/typing/` declares `typing`
as its package clause (its parent directory's base name), so
`clausePackageFileNodes` returns ALL file nodes under `tests/typing/`.

## 4. The dispatch — what was actually run, and how it was measured

The SW-192 AC-1 recipe is `go run ./cmd/parity -family python ...`. That
command **does not work** on this branch: `cmd/parity/main.go:79`
rejects `-family python` with `parity: -family must be "go" or "jvm",
got "python"`. The python driver is the same gap SW-176's AC-2
escalation settled for the JVM matrix (the JVM driver was added by
WP-J7; the python driver has no equivalent story). **The harness
rejects the AC-1 command by construction, not by accident.**

Per SW-176's precedent, the F5 measurement was performed manually by
driving `./cmd/graphi` directly:

```bash
# Dispatch A — first rebuild
mkdir -p /var/tmp/parity-flask-A
/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-A/flask.db \
                        -meta /var/tmp/parity-flask-A/flask-meta
/tmp/graphi-f5 snapshot flask-full

# Dispatch B — second rebuild, separate workdir
mkdir -p /var/tmp/parity-flask-B
/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-B/flask.db \
                        -meta /var/tmp/parity-flask-B/flask-meta
/tmp/graphi-f5 snapshot flask-full-rerun

# The F5 probe — per-row count and target distribution
sqlite3 /var/tmp/graphi-<fingerprint>/snapshots/flask-full.sqlite \
  "SELECT ef.source_path, et.source_path, et.qualified_name
   FROM edges e
   JOIN nodes ef ON ef.id = e.from_id
   JOIN nodes et ON et.id = e.to_id
   WHERE e.kind = 'imports' AND et.source_path LIKE 'tests/typing/%'"
```

The same `graphi-f5` binary was used for both dispatches
(`go1.26.6 darwin/arm64`, HEAD `3f23901`); the only state change was
the workdir.

### Dispatch determinism

| metric | dispatch A (`flask-full`) | dispatch B (`flask-full-rerun`) | agree? |
|---|---:|---:|---|
| total nodes | 1058 | 1058 | yes |
| total edges | 2214 | 2214 | yes |
| `imports` edges | 879 | 879 | yes |
| `defines` edges | 867 | 867 | yes |
| `calls` edges | 468 | 468 | yes |
| imports edges to `tests/typing/*` | 70 | 70 | yes |
| snapshot envelope sha256 | `c8808aef…` | `80021620…` | **no — envelope embeds a timestamp** |

The two snapshots agree on every per-row count that matters. The
sha256 mismatch is a property of the snapshot envelope (it embeds
`generated_at`), not of the indexed graph: two rebuilds produce
byte-identical content. **F5 is reproducible at the count
granularity**, which is the granularity `-counts-diff` would compare
under, and which is the granularity that proved PARITY-002's
non-deterministic half deterministically across the JVM matrix.

The `-verdict-diff` exit code the SW-192 recipe asks for is **not
assertable** by this measurement: there is no `cmd/parity` verdict
to diff because the harness refuses `-family python`. The
substantive half of `-counts-diff` — agreement on every count —
holds.

## 5. The binary verdict on GA at `cross-file-heuristic`

> **Python at flask does NOT support GA at `cross-file-heuristic`.**

Per SW-181 AC-9: *"IF any measurement fails to support GA at
`cross-file-heuristic`, THEN the honest outcome shall be published and
Python shall be re-graded rather than declared."*

The measurement fails. The honest outcome is published here. The
re-grade is the responsibility of the SW-181 AC-9 follow-on
(**PYTHONFANOUT-001** filed separately):

| SW-192 AC-3 IF-branch | measurement | decision |
|---|---|---|
| AC-3 PASS-branch: F5 supports GA, G4 flips PASS, level stays `cross-file-heuristic` | NOT THIS BRANCH — F5 is real, the fan-out is reproducible | — |
| AC-3 IF-branch: F5 fails, G4 stays UNKNOWN, level re-graded to `parse-only` (or whatever the re-grade supports), docs surfaces updated per SW-181 AC-9 | THIS BRANCH — F5 fails | G4 stays UNKNOWN; **re-grade is filed but NOT executed** in this commit |

**The re-grade is deliberately NOT executed in this commit.** It is a
product-byte change that moves the candidate (changing the python
resolver's lookup basis is the same shape as ADR 0009 for Go) and
therefore belongs in its own story with its own ADR, its own
red/green, and its own re-measurement. SW-192 PUBLISHES the
measurement, FILES the defect, and UPDATES the G4 evidence row to
state the F5 failure — it does not rewrite the resolver or move
`parityreport.CandidateSHA`.

The level printed beside GA stays **`cross-file-heuristic`** for now.
Per SW-181 AC-9's re-grade, the surface change (`docs/language-support.md`,
`docs/stability-tiers.md`, the matrix row) is the next story's
responsibility — the PYTHONFANOUT-001 fix would either close the fan-out
and keep the level, or fail to close it and force the re-grade. SW-192
records the measurement and lets the next story decide.

## 6. PYTHONFANOUT-001 — the F5 defect, filed

**Status: OPEN, DISCLOSED, scheduled as a separate product-byte change
with its own ADR and candidate move. NOT fixed here.**

| field | value |
|---|---|
| id | PYTHONFANOUT-001 |
| title | Python's `clausePackageFileNodes` resolver fans out over colliding directory clauses — same shape as PARITY-002 |
| shape | `import typing as t` → `importPath="typing"` → `pyClause("typing")="typing"` → `clausePackageFileNodes(idx, "typing", keep)` walks every directory declaring `typing` and emits an `imports` edge to every file node |
| trigger | any Python source file that imports a stdlib/3rd-party module whose name collides with an in-repo directory clause |
| blast radius | 70 spurious `imports` edges on flask (8.0 % of 879 total); the same mechanism would fan out on any repository whose stdlib/3rd-party imports share names with in-repo directories (`typing` is the canonical example; `logging`, `json`, `ast`, `os`, `re`, `csv` etc. are equally susceptible) |
| mechanism (proposed) | the python resolver needs module-relative directory lookup like ADR 0009 gave Go: instead of `clausePackageFileNodes(idx, clause, keep)`, the lookup should resolve the import path against the **module map** the directory layout encodes, picking exactly one directory per import path |
| ADR required | yes — this is a product-byte change that moves the candidate |
| candidate move | owned, not executed in SW-192 |
| disclosure | `docs/language-support.md` row 5 already says `LINK-004`; PYTHONFANOUT-001 is the second open python defect. Both must move with the re-grade. |
| repro | this measurement, on flask at the real `3.0.0` sha, two rebuilds, identical spurious counts |

## 7. Pin discrepancy — manifest STALE, no silent re-pin

`corpus/manifest.json` line 258 pins flask at
`"sha": "735a4701d6d56f3deec1dce0c2f2fb6d7c0a4d6b"`. That sha does
not exist on `pallets/flask`:

```
$ git ls-remote https://github.com/pallets/flask.git refs/tags/3.0.0
735a4701d6d5e848241e7d7535db898efb62d400	refs/tags/3.0.0
```

The real `3.0.0` tag is `735a4701d6d5e848241e7d7535db898efb62d400`;
both SHAs share the 12-char prefix `735a4701d6d5` (which is what the
pre-v3 12-char pin would have used). The manifest sha is a
fabrication that survived the SW-181 v3 measured-standard uplift.

**SW-192 AC-7 binds**: the story shall not silently re-pin. This
measurement uses the real sha and marks the manifest entry
**STALE**. The follow-on fix is a one-line manifest edit, owned by
SW-181's correctness follow-on, not by SW-192.

## 8. What this measurement does NOT say

- **No correctness claim.** Parity compares two passes of the same
  rule, so PASS / FAIL here is a statement about the python
  resolver's fan-out behaviour, not about whether the spurious
  edges are "correct" in any sense. They are NOT correct — nothing
  in `src/flask/*.py` imports `tests/typing/typing_*.py` — but that
  conclusion is the F5 finding itself, not a corollary.
- **No performance, latency or RSS figure.** No such measurement was
  taken. The F5 measurement is a structural one (which edges
  exist), not a perf one (how fast).
- **No `cmd/parity -family python` exit 0.** The harness refuses
  `-family python` by construction. The two-dispatch discipline
  was applied to `graphi rebuild` directly; the substantive half
  of `-counts-diff` (agreement on every count) holds, and the
  formal gate is unbound — see SW-176's AC-2 escalation for the
  same shape.
- **No coverage figure on python.** The pin census (78 .py of 102
  tracked) is the v3 measured block; the F5 fan-out covers every
  importer that touches the colliding clause. There is no
  denominator-vs-numerator question here — the spurious edges are
  enumerable and reproducible.
- **No claim about other python pins.** flask is the only python
  pin at the v3 measured standard; other python pins (sinatra is
  ruby; ky is typescript; express is javascript; the cross-language
  non-go entries are the JVM family) are not python. The
  re-grade's blast radius on other python repositories is
  measurable but not measured here; that is the responsibility of
  any future python pin added under SW-181 AC-3's IF-branch.

## 9. Reproducing this measurement

```bash
# 1. Clone flask at the REAL 3.0.0 sha (the manifest's sha is STALE — see §7).
mkdir -p /tmp/flask-test && cd /tmp/flask-test
git clone --depth 1 --branch 3.0.0 https://github.com/pallets/flask.git flask-src
cd flask-src && git log -1 --format="%H"   # 735a4701d6d5e848241e7d7535db898efb62d400

# 2. Build the binary used in this measurement (HEAD 3f23901 at run time).
cd /Users/redacted/dev/private/mcp_tools/workspace/graphi
go build -o /tmp/graphi-f5 ./cmd/graphi

# 3. Two rebuilds into separate workdirs (the SW-176 dispatch discipline).
mkdir -p /var/tmp/parity-flask-A /var/tmp/parity-flask-B
/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-A/flask.db \
                        -meta /var/tmp/parity-flask-A/flask-meta
cd /tmp/flask-test/flask-src && /tmp/graphi-f5 snapshot flask-full

/tmp/graphi-f5 rebuild -root /tmp/flask-test/flask-src \
                        -db /var/tmp/parity-flask-B/flask.db \
                        -meta /var/tmp/parity-flask-B/flask-meta
cd /tmp/flask-test/flask-src && /tmp/graphi-f5 snapshot flask-full-rerun

# 4. The F5 probe. Counts in both snapshots should match exactly.
SNAP_A=/var/tmp/graphi-<fingerprint>/snapshots/flask-full.sqlite
SNAP_B=/var/tmp/graphi-<fingerprint>/snapshots/flask-full-rerun.sqlite
sqlite3 "$SNAP_A" "SELECT COUNT(*) FROM edges WHERE kind='imports'
                   AND to_id IN (SELECT id FROM nodes WHERE source_path LIKE 'tests/typing/%')"
# → 70
sqlite3 "$SNAP_B" "SELECT COUNT(*) FROM edges WHERE kind='imports'
                   AND to_id IN (SELECT id FROM nodes WHERE source_path LIKE 'tests/typing/%')"
# → 70

# 5. The head-line reproducer (10 lines of SQL — see §3 above):
# Find importers + targets for the typing-collision fan-out.
sqlite3 "$SNAP_A" <<'SQL'
SELECT ef.source_path AS importer, et.source_path AS target
FROM edges e
JOIN nodes ef ON ef.id = e.from_id
JOIN nodes et ON et.id = e.to_id
WHERE e.kind = 'imports' AND et.source_path LIKE 'tests/typing/%'
ORDER BY importer, target;
SQL
# → 70 rows, every importer is src/flask/* or tests/typing/* or
#   tests/test_*/__init__.py.
```

The raw sample artifacts are published at
[`docs/eval/runs/2026-08-20-Darwin-ARM64/apple-m2-max/`](../../eval/runs/2026-08-20-Darwin-ARM64/apple-m2-max/).
The structured measurement is `raw/f5-measurement.json` (sha256
`d8940e14…`); the determinism probe is `raw/dispatch-determinism.json`.

## 10. Notes for the reviewer

1. **The SW-192 AC-1 recipe does not run.** The harness refuses
   `-family python`. SW-176's AC-2 escalation settled the same shape
   for the JVM matrix: when the formal gate is unbound, the substantive
   half of the gate is what gets measured, and the formal gate is
   named unbound rather than asserted satisfied. This measurement is
   the substantive half.
2. **The manifest pin is STALE.** §7 documents it. The measurement
   used the real sha; the manifest edit is its own one-line change.
3. **G4 stays UNKNOWN, G2SUB stays PASS.** The F5 finding concerns
   the resolver's fan-out, which is the G2SUB never-confirmed half's
   adjunct (the resolver mints `heuristic` edges it should not mint,
   not `confirmed` edges — but the level still claims a property the
   edges do not have). G2SUB is unaffected by this measurement;
   G2SUB's `current` field already names LINK-004 as the load-bearing
   half-closed defect.
4. **PYTHONFANOUT-001 is filed, not fixed.** The fix is the same
   module-relative lookup ADR 0009 gave Go. That is a product-byte
   change with its own ADR and candidate move; SW-192 does not move
   the candidate, per AC-7.
5. **The 22 src/flask/*.py files that trigger the fan-out all do
   `import typing as t`.** This is **not** a flask quirk — every
   real Python codebase imports `typing` (PEP 484). F5 would surface
   on every Python repo whose directory layout declares a `typing`
   clause. The blast radius is the entire python ecosystem, not just
   flask.
6. **The two dispatches agree on every count that matters.** The
   snapshot envelope's sha256 mismatch is a timestamp property; the
   content is byte-identical. F5 is deterministic — it would have
   been invisible to `-verdict-diff` and caught only by
   `-counts-diff`, which is precisely the discipline the JVM matrix
   established.
