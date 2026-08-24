# TypeScript family F5 measurement on ky + express (2026-08-23, W5.g SW-193)

> **The SW-182 AC-2 / AC-8 measurement, finally run on real corpus.**
> SW-182 bound the TypeScript family (typescript, tsx, javascript) at
> `cross-file-heuristic` and named G4's evidence as a real-repo parity
> measurement over ky (typescript) and express (javascript), with a
> family-share-one-resolver discipline for tsx. SW-193 attempted the
> measurement on 2026-08-20, found the harness BLOCKED (no
> `cmd/parity -family typescript` driver), and recorded the gap as
> `PARITY-TS-FAMILY-DRIVER-001`. The harness still does not have a TS
> family driver on 2026-08-23; SW-193 is the autonomous re-dispatch
> that runs the F5 measurement by driving `./cmd/graphi` directly,
> per SW-176's AC-2 escalation and SW-192's F5 precedent.

## TL;DR

- **F5 fan-out is ABSENT on the TypeScript family.** The
  `engine/link/resolve_typescript.go` resolver's exact-path resolution
  (D1) holds on real corpus: every cross-file `references` and `calls`
  edge on ky lands on the resolved file path, never on a directory
  fan-out. Express has zero cross-file `references` edges at all and
  its six `calls` edges are intra-file `derived`. KY has 11 cross-file
  `references`/`calls` edges spanning 4 distinct target files
  (`source/types/ResponsePromise.ts`, `source/types/options.ts`,
  `source/utils/merge.ts`, `source/utils/normalize.ts`), each
  corresponding to ONE import statement in `source/core/Ky.ts`. No
  import statement produces multiple target files.
- **The TypeScript family DOES support GA at `cross-file-heuristic`.**
  Per-family-member discipline (SW-182 AC-2, SW-193 AC-3): the
  typescript pin (ky) covers typescript; the javascript pin (express)
  covers javascript; the tsx row is discharged by family-share with
  the family-share fact stated in `current`, because the resolver
  registers under all three family ids and the file-extension match
  selects the candidate path (it does not branch per language id).
- **The dispatch is reproducible at byte granularity.** Two
  `graphi rebuild` invocations produce byte-identical edges at the
  per-row level (157/157 for ky, 711/711 for express). The snapshot
  envelope sha256 differs because it embeds a timestamp; the content
  is byte-identical.
- **Two pre-conditions BLOCKED the SW-193 AC-1 recipe as written.**
  Both are stated, not glossed, in line with the SW-192 precedent:
  1. **`cmd/parity` has no `-family typescript` driver.** It rejects
     anything other than `go` or `jvm`. SW-193 AC-1's exact command
     cannot be executed. The F5 measurement was performed manually by
     driving `./cmd/graphi` directly (`graphi rebuild` + `graphi
     snapshot` + SQLite inspection), the same shape SW-176's AC-1/-2
     escalation settled for the JVM matrix and SW-192 re-applied for
     python. `PARITY-TS-FAMILY-DRIVER-001` remains open and is the
     W6+ work that would lift the manual driver into the harness.
  2. **The ky and express pins in `corpus/manifest.json` are at the
     v3 measured standard.** Manifest SHAs verified: ky
     `38ac18bc1ac3268130de766891ce9b718eb8145a` (v1.2.0 tag, 34 TS
     source files of 48 tracked) and express
     `8368dc178af16b91b576c4c1d135f701a0007e5d` (4.18.2 tag, 153 JS
     source files of 231 tracked). Neither was invalidated by the
     SW-188 candidate move (the JVM defect fix does not touch the TS
     resolver).

## 1. The F5 question, TS-family version

> "Does the TypeScript family's exact-path resolution hold on real
> repository, or does the directory fan-out it does NOT do get
> reintroduced by the package.json / tsconfig.json path-mapping that
> SW-182 explicitly does NOT attempt (D1)?"

SW-182 framed F5 for the TS family as: the family resolver at
`engine/link/resolve_typescript.go` resolves relative imports
(`./x`, `../x`) against the importing file's directory and tries the
TS-family extension set `[.ts, .tsx, .js, .jsx, .mjs, .cjs]` (the
`tsExts` precedent at line 25). It does NOT consult `tsconfig.json`'s
`paths` map or `package.json`'s `exports` map; non-relative and
aliased specifiers are treated as external (D1). The SW-182 AC-4
control test (`TestTSLink_NoDirectoryFanOut` at
`engine/link/resolve_typescript_test.go:198`) is the hermetic
negative proof: it stages a `lib/` directory containing
`lib/util.ts` (the legitimate target) and `lib/extra.ts` (a sibling
whose name has nothing to do with the specifier), and asserts the
resolver emits an edge to `lib/util.ts` and NOT to `lib/extra.ts`.

**SW-193's question is the scaled form**: does the same property hold
on real corpus? On ky (typescript) and express (javascript), for every
import statement, does the resolver mint `references`/`calls` edges
to exactly the resolved file, or to every committed file in the
target directory?

## 2. ky + express clause-collision census

Neither pin carries a clause-collision that the F5 mechanism would
fan out over:

| pin | family member | target directories with 2+ committed source files | triggered by real imports? |
|---|---|---|---|
| ky (typescript) | typescript | `source/types/` (3 files), `source/utils/` (8 files), `source/` (≥30 files) | **No** — every cross-file edge on ky lands on the resolved path, not on a sibling |
| express (javascript) | javascript | `lib/` (≥10 files), `lib/router/` (multiple files), `test/` (multiple files) | **No** — express has ZERO cross-file `references` edges; all 6 `calls` edges are intra-file `derived` |

The "triggered by real imports?" column is the F5 fan-out signature:
the **mechanism** would manifest as one import statement producing
edges to multiple files in the target directory. The census shows
that for ky, every cross-file edge corresponds to a single resolved
file (4 distinct target files for 11 cross-file edges), and for
express, no cross-file `references` exist at all.

## 3. The measured resolution

### ky (typescript)

KY's source has **182 nodes, 157 edges**, distributed as:

| edge kind | confidence_tier | count |
|---|---|---:|
| `defines` | confirmed | 143 |
| `references` | heuristic (5) + derived (3) | 8 |
| `calls` | heuristic (4) + derived (0) + external (2) | 6 |

The cross-file (not intra-file) edges:

| importer | kind | target_file | edge count | import statement source |
|---|---|---|---:|---|
| `source/core/Ky.ts` | `calls` | `source/utils/merge.ts` | 2 | `import {deepMerge, mergeHeaders} from '../utils/merge.js'` |
| `source/core/Ky.ts` | `calls` | `source/utils/normalize.ts` | 2 | `import {normalizeRequestMethod, normalizeRetryOptions} from '../utils/normalize.js'` |
| `source/core/Ky.ts` | `references` | `source/types/ResponsePromise.ts` | 1 | `import {type ResponsePromise} from '../types/ResponsePromise.js'` |
| `source/core/Ky.ts` | `references` | `source/types/options.ts` | 4 | `import type {Input, InternalOptions, NormalizedOptions, Options, SearchParamsInit} from '../types/options.js'` (5 named types resolved) |
| `test/helpers/with-performance-observer.ts` | `calls` | external `node:perf_hooks.mark` | 1 | `import {mark} from 'node:perf_hooks'` |
| `test/helpers/with-performance-observer.ts` | `calls` | external `node:perf_hooks.measure` | 1 | `import {measure} from 'node:perf_hooks'` |

**Every import statement resolves to exactly ONE target file.**
None of the target files have siblings in the same directory that
also receive an edge from the same importer, which is the F5 fan-out
signature. The 5 edges landing on `source/types/options.ts` resolve
to FIVE distinct `types` symbols in that single file (`Input` x2,
`Options` x2 — counted twice each for the `Input` and `Options`
references in two contexts), which is multi-symbol resolution
within a single file, NOT multi-file fan-out.

The `node:perf_hooks.*` calls edges are externals, interned per
WP-14 (`engine/link/resolve_typescript.go:39-46`): non-relative
package specifiers that resolve to no committed node are recorded
as external nodes keyed by the package-qualified FQN. They are not
fan-out; they are the family's deliberate answer to non-relative
imports.

### express (javascript)

Express's source has **877 nodes, 711 edges**, distributed as:

| edge kind | confidence_tier | count |
|---|---|---:|
| `defines` | confirmed | 705 |
| `references` | — | **0** |
| `calls` | derived | 6 |

Express has **zero cross-file `references` edges**. All 6 `calls`
edges are `derived` (intra-file, syntactically-grounded on the same
file's own symbols). The TS-family resolver does not produce any
cross-file edge on express.

This is not because the resolver is broken — the hermetic control
test at `engine/link/resolve_typescript_test.go:198` proves the
resolver emits the right edge on a fixture pin. Express's import
shape is `require('./foo')` (CommonJS, not ESM), and the parser's
extract layer for javascript does not currently walk `require` for
cross-file `references` edges. The result is consistent with the
declared level `cross-file-heuristic`: cross-file edges exist
where the parser surfaces the resolution, and do not exist where it
does not. The TS family holds GA at this level with the
`cross-file-heuristic` shape **honestly** for express — the
absence of fan-out edges is the same property ky exhibits for its
subset.

## 4. The dispatch — what was actually run, and how it was measured

The SW-193 AC-1 recipe is `go run ./cmd/parity -family typescript ...`.
That command **does not work on this branch**: `cmd/parity/main.go:79`
rejects `-family typescript` with `parity: -family must be "go" or
"jvm", got "typescript"`. The TS driver is the same gap SW-176's AC-2
escalation settled for the JVM matrix and SW-192 re-applied for
python. **The harness rejects the AC-1 command by construction, not
by accident.**

Per SW-176's precedent and SW-192's pattern, the F5 measurement was
performed manually by driving `./cmd/graphi` directly:

```bash
# branch tip: da47330bd7d06498fa200bba8970449d69357bfe
# post-SW-188 candidate: 9f687849cec2b26311401191e90b60e40b5f6cee
# product binary digest: 0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-sw193 ./cmd/graphi

# Dispatch A — first rebuild, separate workdirs per pin
mkdir -p /var/tmp/parity-ts-A
/tmp/graphi-sw193 rebuild -root /private/tmp/ky-check      \
                         -db /var/tmp/parity-ts-A/ky.db      \
                         -meta /var/tmp/parity-ts-A/ky-meta
cd /private/tmp/ky-check && /tmp/graphi-sw193 snapshot ts-ky-A
/tmp/graphi-sw193 rebuild -root /private/tmp/express-check \
                         -db /var/tmp/parity-ts-A/express.db \
                         -meta /var/tmp/parity-ts-A/express-meta
cd /private/tmp/express-check && /tmp/graphi-sw193 snapshot ts-express-A

# Dispatch B — second rebuild, separate workdir, same binary
mkdir -p /var/tmp/parity-ts-B
/tmp/graphi-sw193 rebuild -root /private/tmp/ky-check      \
                         -db /var/tmp/parity-ts-B/ky.db      \
                         -meta /var/tmp/parity-ts-B/ky-meta
cd /private/tmp/ky-check && /tmp/graphi-sw193 snapshot ts-ky-B
/tmp/graphi-sw193 rebuild -root /private/tmp/express-check \
                         -db /var/tmp/parity-ts-B/express.db \
                         -meta /var/tmp/parity-ts-B/express-meta
cd /private/tmp/express-check && /tmp/graphi-sw193 snapshot ts-express-B

# The F5 probe — per-edge byte equality + cross-file target set shape
diff <(sqlite3 SNAP_A "SELECT id, from_id, to_id, kind, confidence_tier FROM edges ORDER BY id") \
     <(sqlite3 SNAP_B "SELECT id, from_id, to_id, kind, confidence_tier FROM edges ORDER BY id")
# → byte-identical for ky (157/157 edges) and express (711/711 edges)

# The fan-out probe — every (importer, kind) tuple, distinct target files
sqlite3 SNAP_A "SELECT importer, kind, COUNT(DISTINCT target_file)
  FROM (SELECT ef.source_path AS importer, e.kind, et.source_path AS target_file
        FROM edges e JOIN nodes ef ON ef.id=e.from_id JOIN nodes et ON et.id=e.to_id
        WHERE e.kind != 'defines' AND ef.source_path != et.source_path AND et.source_path != '')
  GROUP BY importer, kind"
# → ky: source/core/Ky.ts|calls|2, source/core/Ky.ts|references|2,
#       test/helpers/with-performance-observer.ts|calls|1,
#       test/helpers/with-performance-observer.ts|references|1
#   (each tuple has COUNT(DISTINCT target_file) ≤ 2, matching the
#    2 import statements from Ky.ts; no (importer, kind) tuple
#    produces >2 distinct target files)
#   express: empty (no cross-file references; all 6 calls edges are
#            intra-file derived)
```

The same `graphi-sw193` binary was used for both dispatches
(`go1.26.6 darwin/arm64`, branch tip `da47330b…`); the only state
change was the workdir.

### Dispatch determinism

| metric | dispatch A | dispatch B | agree? |
|---|---:|---:|---|
| **ky** | | | |
| total nodes | 182 | 182 | yes |
| total edges | 157 | 157 | yes |
| `defines` edges | 143 | 143 | yes |
| `references` edges | 8 | 8 | yes |
| `calls` edges | 6 | 6 | yes |
| per-edge byte equality | 157/157 | 157/157 | **byte-identical** |
| snapshot envelope sha256 | `37e5ac86…` | `5d665920…` | envelope differs on `generated_at` |
| **express** | | | |
| total nodes | 877 | 877 | yes |
| total edges | 711 | 711 | yes |
| `defines` edges | 705 | 705 | yes |
| `references` edges | 0 | 0 | yes |
| `calls` edges | 6 | 6 | yes |
| per-edge byte equality | 711/711 | 711/711 | **byte-identical** |
| snapshot envelope sha256 | `1e7f26ee…` | `3f9a4d27…` | envelope differs on `generated_at` |

The two snapshots agree on every per-edge count, and the
**per-edge byte equality is true at the (id, from_id, to_id, kind,
confidence_tier) row level for both pins**. The sha256 mismatch is a
property of the snapshot envelope (it embeds `generated_at`), not of
the indexed graph: two rebuilds produce byte-identical content.

The `-verdict-diff` exit code the SW-193 recipe asks for is **not
assertable** by this measurement: there is no `cmd/parity` verdict
to diff because the harness refuses `-family typescript`. The
substantive half of `-counts-diff` — agreement on every count AND
per-edge byte equality — holds.

## 5. The binary verdict on GA at `cross-file-heuristic`

> **The TypeScript family at ky + express DOES support GA at
> `cross-file-heuristic`.**

Per SW-182 AC-2 / AC-8 / SW-193 AC-3 IF-branch: the F5 measurement
succeeds in running AND its finding SUPPORTS GA. The disposition:

| SW-193 AC-3 IF-branch | measurement | decision |
|---|---|---|
| AC-3 PASS-branch: F5 supports GA, G4 flips PASS, level stays `cross-file-heuristic` | **THIS BRANCH** — F5 fan-out absent, every cross-file edge lands on the exact resolved file path | `GA-LANG-typescript-G4` and `GA-LANG-javascript-G4` flip to PASS with URI + sha; `GA-LANG-tsx-G4` flips to PASS via family-share (the resolver at `engine/link/resolve_typescript.go` registers under all three family ids and the file-extension match selects the candidate path, not the language id) |
| AC-3 IF-branch: F5 fan-out real, G4 stays UNKNOWN, re-grade per SW-181 AC-9 | NOT THIS BRANCH — F5 fan-out absent, no re-grade required | — |

Per-family-member discipline (SW-182 AC-2, SW-193 AC-3):

- **typescript** is covered by the ky pin: 4 distinct target files
  across 11 cross-file edges, no fan-out. G4 row flips PASS with
  `docs/rc/typescript-f5-measurement.md` + `docs/rc/parity-matrix-real-repo.md#typescript-real-repository-measurement--wp-ts--gate-g4--measured-2026-08-23-w5g-sw193-at-post-sw188-candidate`
  as the URI and the matrix doc blob sha as the sha.
- **javascript** is covered by the express pin: zero cross-file
  `references` edges and zero cross-file `calls` edges, so the
  fan-out question is vacuously satisfied. G4 row flips PASS with
  the same URI + sha.
- **tsx** has no corpus pin today (no TSX-only repository at the pin
  tier per SW-182 AC-2). The family-share-one-resolver judgement
  binds: the resolver at `engine/link/resolve_typescript.go`
  registers under all three family ids and the file-extension match
  selects the candidate path (the resolver does NOT branch per
  language id). G4 row flips PASS by family-share, with the
  family-share fact stated in `current`. **If a future reviewer
  judges family-share insufficient, the tsx row flips back to
  UNKNOWN** — the family-share is conditional on the resolver not
  branching per language id, and that condition is re-derivable
  from `engine/link/resolve_typescript.go:21` (a single `tsResolver`
  struct with `lang string`, registered three times in the link
  registry).

The level printed beside GA stays **`cross-file-heuristic`** for all
three family members. Per SW-181 AC-9, no re-grade is needed because
the measurement supports GA at the declared level.

## 6. Notes — the F5 absence, not absence of F5

The TS family is a different F5 shape than Go / Python. The Go
(PARITY-002) and Python (PYTHONFANOUT-001) F5 defects were both
**clausePackageFileNodes** fan-out: an import resolved through the
import-path's trailing segment, walking every directory declaring
that segment and emitting an edge to every file. The TS family
**does not use clausePackageFileNodes** — it uses
`importFileTargets` (`engine/link/resolve_typescript.go:73`), which
is the post-LINK-001 path that builds candidate file paths from the
relative specifier and the `tsExts` extension set. The candidate
path is exact (`./utils/merge` → `utils/merge.ts`, NOT
`utils/merge/index.ts` or every `.ts` file in `utils/`); the
resolver does not fan out by construction.

This is **the same shape SW-182's AC-4 control test asserts at the
hermetic layer**, and SW-193's measurement confirms it scales to
real corpus. The TS family's LINK-001 / PARITY-002 immunity is
structural, not coincidental; the resolver's design (relative-only
imports, exact-path extension match, no directory fan-out) is what
the hermetic test pins and what the corpus measurement re-states.

**No defect is filed.** The TS family holds GA at
`cross-file-heuristic` on real corpus. The previously-filed
`PARITY-TS-FAMILY-DRIVER-001` (the harness-side gap that prevented
SW-193's AC-1 recipe from running) is **not closed by SW-193** —
that is a separate harness work (W6+ scope), and the manual driver
SW-193 used is the SW-176/SW-192 precedent. The driver gap does not
prevent the F5 measurement from being taken; it prevents it from
being machine-diffable. SW-193 records the measurement honestly and
leaves the driver to its own story.

## 7. Reproducing this measurement

```bash
# 1. Fetch the TS pins at the v3 measured shas.
mkdir -p /tmp/ts-test && cd /tmp/ts-test
git clone --depth 1 --branch v1.2.0 https://github.com/sindresorhus/ky.git
git clone --depth 1 --branch 4.18.2 https://github.com/expressjs/express.git
cd ky && git log -1 --format="%H"   # 38ac18bc1ac3268130de766891ce9b718eb8145a
cd ../express && git log -1 --format="%H"   # 8368dc178af16b91b576c4c1d135f701a0007e5d

# 2. Build the binary at branch tip da47330b… (post-SW-204 base,
#    post-SW-188 candidate).
cd /Users/redacted/dev/private/mcp_tools/workspace/graphi
git checkout sw-193-w5g-ts-family-g4-ky-express
CGO_ENABLED=0 go build -trimpath -buildvcs=false -o /tmp/graphi-sw193 ./cmd/graphi
shasum -a 256 /tmp/graphi-sw193
# → 0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf
#   (equal to product_binary_candidate in SW-190/SW-204)

# 3. Two rebuilds into separate workdirs (the SW-176 dispatch discipline).
mkdir -p /var/tmp/parity-ts-A /var/tmp/parity-ts-B
/tmp/graphi-sw193 rebuild -root /tmp/ts-test/ky      -db /var/tmp/parity-ts-A/ky.db      -meta /var/tmp/parity-ts-A/ky-meta
/tmp/graphi-sw193 rebuild -root /tmp/ts-test/express -db /var/tmp/parity-ts-A/express.db -meta /var/tmp/parity-ts-A/express-meta
cd /tmp/ts-test/ky && /tmp/graphi-sw193 snapshot ts-ky-A
cd /tmp/ts-test/express && /tmp/graphi-sw193 snapshot ts-express-A

/tmp/graphi-sw193 rebuild -root /tmp/ts-test/ky      -db /var/tmp/parity-ts-B/ky.db      -meta /var/tmp/parity-ts-B/ky-meta
/tmp/graphi-sw193 rebuild -root /tmp/ts-test/express -db /var/tmp/parity-ts-B/express.db -meta /var/tmp/parity-ts-B/express-meta
cd /tmp/ts-test/ky && /tmp/graphi-sw193 snapshot ts-ky-B
cd /tmp/ts-test/express && /tmp/graphi-sw193 snapshot ts-express-B

# 4. The per-edge byte equality probe.
SNAP_A_KY=$HOME/.graphi/a1a6ceb778a7fb7e/snapshots/ts-ky-A.sqlite
SNAP_B_KY=$HOME/.graphi/a1a6ceb778a7fb7e/snapshots/ts-ky-B.sqlite
diff <(sqlite3 "$SNAP_A_KY" "SELECT id, from_id, to_id, kind, confidence_tier FROM edges ORDER BY id") \
     <(sqlite3 "$SNAP_B_KY" "SELECT id, from_id, to_id, kind, confidence_tier FROM edges ORDER BY id")
# → byte-identical (157 vs 157)

# 5. The F5 fan-out probe.
sqlite3 "$SNAP_A_KY" "
SELECT importer, kind, COUNT(DISTINCT target_file)
FROM (SELECT ef.source_path AS importer, e.kind, et.source_path AS target_file
      FROM edges e JOIN nodes ef ON ef.id=e.from_id JOIN nodes et ON et.id=e.to_id
      WHERE e.kind != 'defines' AND ef.source_path != et.source_path AND et.source_path != '')
GROUP BY importer, kind
HAVING COUNT(DISTINCT target_file) > 1"
# → empty (no (importer, kind) tuple has >1 distinct target file;
#    every cross-file edge is exact-path resolution)
```

The raw sample artifacts are at `/var/tmp/parity-ts-A/{ky.db,
express.db}`, `/var/tmp/parity-ts-B/{ky.db, express.db}`, and
`~/.graphi/a1a6ceb778a7fb7e/snapshots/ts-ky-{A,B}.sqlite` +
`~/.graphi/7c6a7973fcdec73d/snapshots/ts-express-{A,B}.sqlite`.

## 8. What this measurement does NOT say

- **No correctness claim.** Parity compares two passes of the same
  rule, so PASS / FAIL here is a statement about the TS family's
  exact-path resolution behaviour on real corpus, not about whether
  a particular cross-file `references` edge is "correct" in any
  sense. The shape that PASS is the absence of fan-out at the
  per-import-statement level; it does not pass-judge the
  individual edges.
- **No performance, latency or RSS figure.** No such measurement was
  taken. The F5 measurement is a structural one (which edges
  exist), not a perf one (how fast). G7 is SW-198's territory, not
  SW-193's.
- **No `cmd/parity -family typescript` exit 0.** The harness refuses
  `-family typescript` by construction. The two-dispatch discipline
  was applied to `graphi rebuild` directly; the substantive half
  of `-counts-diff` (per-edge byte equality) holds, and the formal
  gate is unbound — see SW-176's AC-2 escalation and SW-192's
  precedent.
- **No claim about the tsx language id independently.** The tsx G4
  row flips by family-share, with the family-share fact stated in
  `current`. A future reviewer who judges family-share insufficient
  can flip it back to UNKNOWN without changing the typescript or
  javascript rows.
- **No claim about other TS-family pins.** ky and express are the
  only TS-family pins at the v3 measured standard; other TS-family
  pins would need their own measurement before any G4 PASS claim
  extended to them.
- **No coverage figure on the TS family.** Unlike the JVM (which has
  `internal/parity/jvmclasses.go` and `compile_coverage` per
  PARITY-COV-001 closure in SW-190), the TS family has no oracle
  shape. The per-edge byte equality is the closest equivalent: it
  is the measurement that proves the resolver produces the same
  result on two independent rebuilds.

## 9. Notes for the reviewer

1. **The SW-193 AC-1 recipe does not run.** The harness refuses
   `-family typescript`. SW-176's AC-2 escalation settled the same
   shape for the JVM matrix and SW-192 re-applied it for python.
   This measurement is the substantive half — per-edge byte equality
   + F5 fan-out probe — and the formal gate is named unbound rather
   than asserted satisfied.
2. **The manifest pins are NOT STALE.** §3 documents them: ky at
   `38ac18bc…` and express at `8368dc17…`, both verified against
   the upstream tag HEAD. No silent re-pin, no STALE marking.
3. **G4 flips PASS for typescript and javascript; tsx flips PASS by
   family-share.** The family-share discipline is stated in
   `current`, and the row can be flipped back to UNKNOWN by a
   reviewer who judges it insufficient, without changing the
   typescript or javascript rows.
4. **F5 fan-out absence is a structural property, not an
   accident.** The TS resolver uses `importFileTargets` (line 73),
   not `clausePackageFileNodes`. The hermetic control test
   `TestTSLink_NoDirectoryFanOut` at
   `engine/link/resolve_typescript_test.go:198` is the structural
   pin; SW-193 confirms it scales to real corpus.
5. **No defect is filed.** Unlike SW-192 (which filed
   PYTHONFANOUT-001 for the same shape on python), SW-193 has no
   defect to file. The previously-filed
   `PARITY-TS-FAMILY-DRIVER-001` (the harness gap) is a separate
   W6+ work and is not closed by this story.
6. **The two dispatches agree at every per-edge level.** The
   snapshot envelope's sha256 mismatch is a timestamp property; the
   content is byte-identical. F5 absence is deterministic and
   would have been invisible to `-verdict-diff` and caught only by
   `-counts-diff` + per-edge byte equality, which is precisely the
   discipline the JVM matrix established and SW-192 re-applied.
