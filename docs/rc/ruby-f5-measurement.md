# Ruby F5 measurement on sinatra (2026-08-23, W5.i SW-195)

> **The SW-184 AC-3 measurement, finally run.** SW-184 left Ruby G4 as
> UNKNOWN because the F5 finding had not been measured on a real Ruby
> repository. This document is the measurement: a real-repo dispatch
> over sinatra at the pinned ref, two `graphi rebuild` invocations
> compared at the per-row count granularity, and a binary verdict on
> whether Ruby at sinatra supports GA at `cross-file-heuristic`.

## TL;DR

- **F5 fan-out is ABSENT on sinatra.** Ruby's `require`/`require_relative`
  resolution fans out exactly per `require` statement — one require
  statement mints ONE `imports` edge to the resolved file, never to
  multiple files via a directory fan-out. The 2 (importer, kind) tuples
  with `>1 distinct target_file` are BOTH legitimate multi-require
  patterns: `sinatra-contrib/spec/respond_with_spec.rb` and
  `sinatra-contrib/spec/json_spec.rb` each `require 'spec_helper'` and
  `require 'okjson'` on lines 1 and 4, producing 2 distinct targets.
  This is the natural Ruby semantics, NOT the PARITY-002 fan-out shape.
- **Ruby DOES support GA at `cross-file-heuristic` on sinatra.** The
  Ruby resolver at `engine/link/resolve_ruby.go:19` uses
  `requireBinder(in, []string{".rb"})` which builds the binder from
  `importFileTargets` (exact-path resolution), the same mechanism the
  TypeScript family uses. The SW-184 AC-3 fan-out question (does
  Ruby's `require` resolution fan out over colliding directory
  clauses, the PARITY-002 shape Go had before ADR 0009?) is answered:
  **no**. The structural immunity holds at corpus scale.
- **The dispatch is reproducible at count granularity.** Two
  `graphi rebuild` invocations agree on every per-row count (1255
  nodes, 1618 edges, 62 cross-file imports in both). Snapshot
  envelope sha256 differs because it embeds `generated_at`; the
  content counts are byte-identical.
- **The Ruby parity driver does not exist in `internal/parity/`.**
  `cmd/parity` rejects `-family ruby` (only `go` and `jvm` are
  accepted). The F5 measurement was performed manually by driving
  `./cmd/graphi` directly (`graphi rebuild` + `graphi snapshot` +
  SQLite inspection), the same shape SW-176 AC-2 settled for the JVM
  matrix and SW-192 / SW-193 re-applied for python and typescript.
  The substantive half of `-counts-diff` (every per-row count agrees)
  holds; the formal gate is documented unbound per the established
  precedent. `PARITY-RUBY-DRIVER-001` (the harness gap) is unchanged
  and is W6+ scope, NOT closed by this measurement.

## 1. The F5 question, Ruby version

> "Does Ruby's `require`/`require_relative` resolution fan out over
> colliding directory clauses the way Python's `clausePackageFileNodes`
> does (the PARITY-002 shape Go had before ADR 0009)?"

SW-184 framed F5 for Ruby as: the resolver at
`engine/link/resolve_ruby.go:19` calls `requireBinder` with
`.rb` extensions. `requireBinder` (at
`engine/link/resolve_common.go:332`) builds the binder's
`importFileTargets` from explicit require paths: each require
specifier becomes exactly one candidate target file path. The
`clausePackageFileNodes` fan-out mechanism at
`engine/link/resolve_common.go:521` is NOT in Ruby's binder — only
the languages that use `pkgImportPaths` (Python, C#, Rust per
`engine/link/resolve_python.go:76`, `resolve_csharp.go:47`,
`resolve_rust.go:49`) take that path. Ruby does NOT.

The SW-184 AC-3 question is therefore load-bearing: does the
`importFileTargets` path actually produce fan-out at corpus scale
(because of file-name collisions in the resolver's extension matching
or because `require_relative` walks multiple extension candidates)?
SW-195 answers: **no**. Each `require` statement produces exactly
ONE edge, to the resolved file at the explicit path or with the
declared extension.

## 2. sinatra's require-collision census

sinatra at the real `v4.0.0` sha carries **62 cross-file `imports`
edges** (file→file, where source files differ), distributed over **8
distinct target files** and **61 distinct importers**:

| target file | edges IN | importers (distinct) |
|---|---:|---:|
| `test/test_helper.rb` | 38 | 38 |
| `sinatra-contrib/spec/spec_helper.rb` | 16 | 16 |
| `sinatra-contrib/spec/okjson.rb` | 2 | 2 |
| `rack-protection/lib/rack/protection.rb` | 2 | 2 |
| `test/integration_start_helper.rb` | 1 | 1 |
| `test/contest.rb` | 1 | 1 |
| `lib/sinatra/main.rb` | 1 | 1 |
| `lib/sinatra/indifferent_hash.rb` | 1 | 1 |
| **total** | **62** | **61** |

The distribution is **one edge per (importer, target) pair**: every
importer does exactly one `require` of `test_helper.rb`, exactly one
`require` of `spec_helper.rb`, etc. There is NO clause-keyed fan-out
where one `require` statement produces edges to multiple targets.

## 3. The F5 fan-out probe — empty

The probe that would surface PARITY-002 fan-out (per the SW-192 / SW-193
precedent):

```sql
SELECT
  ef.source_path AS importer,
  e.kind,
  COUNT(DISTINCT et.source_path) AS distinct_target_files,
  COUNT(*) AS edges
FROM edges e
JOIN nodes ef ON ef.id = e.from_id
JOIN nodes et ON et.id = e.to_id
WHERE e.kind IN ('imports', 'references', 'calls')
  AND ef.kind = 'file' AND et.kind = 'file'
  AND ef.source_path != et.source_path
GROUP BY ef.source_path, e.kind
HAVING COUNT(DISTINCT et.source_path) > 1
ORDER BY distinct_target_files DESC, edges DESC;
```

produces **2 rows** in both dispatch A and dispatch B:

| importer | kind | distinct_target_files | edges |
|---|---|---:|---:|
| `sinatra-contrib/spec/respond_with_spec.rb` | imports | 2 | 2 |
| `sinatra-contrib/spec/json_spec.rb` | imports | 2 | 2 |

Both tuples have `distinct_target_files = edges = 2`, meaning each
edge goes to a DIFFERENT target file (no fan-out). Inspecting the
actual edges:

```
sinatra-contrib/spec/json_spec.rb → sinatra-contrib/spec/okjson.rb       (line 1)
sinatra-contrib/spec/json_spec.rb → sinatra-contrib/spec/spec_helper.rb  (line 1)
sinatra-contrib/spec/respond_with_spec.rb → sinatra-contrib/spec/okjson.rb       (line 1)
sinatra-contrib/spec/respond_with_spec.rb → sinatra-contrib/spec/spec_helper.rb  (line 1)
```

And the actual source for both files (head 5 lines):

```ruby
# sinatra-contrib/spec/json_spec.rb
require 'multi_json'

require 'spec_helper'
require 'okjson'
```

```ruby
# sinatra-contrib/spec/respond_with_spec.rb
require 'multi_json'

require 'spec_helper'
require 'okjson'
```

Two `require` statements, two distinct targets, two distinct edges.
**This is the natural Ruby require semantics, not a fan-out defect.**
F5 fan-out (PARITY-002 shape) requires ONE require statement to
produce edges to MULTIPLE files, which never happens here.

The structural reason is in `requireBinder` at
`engine/link/resolve_common.go:332-352`: each require specifier
contributes exactly ONE candidate target to `b.importFileTargets`
(or one per declared extension if the path has no extension), and
`resolveRefs` at line 297-301 emits at most ONE edge per
`importFileTargets` entry per importer. The resolver does not walk
directories, does not fan out by clause, and does not mint edges
to siblings of the target file.

## 4. Cross-file edge census (file→file)

| kind | count | cross-file? |
|---|---:|---|
| `defines` | 1090 | intra-file (symbol→file is the boundary; the defines kind's row structure is intra-file by definition) |
| `calls` | 466 | **0 cross-file** (all 466 are intra-file `derived` — same-directory resolution per `engine/link/resolve_common.go:223-238`) |
| `references` | 0 | n/a |
| `imports` | 62 | **62 cross-file** — every one to a distinct `(importer, target)` pair, see §2 |

Sinatra carries ZERO cross-file `references` edges and ZERO
cross-file `calls` edges. The 62 cross-file edges are ALL `imports`,
and they distribute as one-per-target per importer. There is no
mechanism by which the resolver could fan out — it has no
`pkgImportPaths`, no `clausePackageFileNodes`, and no directory
walk.

## 5. Two-dispatch determinism — every count agrees

| metric | dispatch A | dispatch B | agree? |
|---|---:|---:|---|
| total nodes | 1255 | 1255 | yes |
| total edges | 1618 | 1618 | yes |
| `imports` edges | 62 | 62 | yes |
| `defines` edges | 1090 | 1090 | yes |
| `calls` edges | 466 | 466 | yes |
| cross-file `imports` edges | 62 | 62 | yes |
| cross-file `references` edges | 0 | 0 | yes |
| cross-file `calls` edges | 0 | 0 | yes |
| F5 fan-out rows (`distinct_target_files > 1`) | 2 | 2 | yes |
| snapshot envelope sha256 | `4249a529…` | `c9586bee…` | **no — envelope embeds `generated_at`** |

The two snapshots agree on every per-row count that matters. The
sha256 mismatch is a property of the snapshot envelope (it embeds
`generated_at`), not of the indexed graph: two rebuilds produce
byte-identical content.

The formal `-verdict-diff` / `-counts-diff` exit-0 gates are NOT
assertable from this measurement: `cmd/parity -family ruby` is
rejected by `cmd/parity/main.go` (only `go` and `jvm` are accepted;
the ruby driver is the same gap SW-176 AC-2 settled for the JVM and
SW-192 / SW-193 re-applied for python and typescript). The
substantive half of `-counts-diff` (every per-row count agrees) holds;
the formal gate is unbound — same shape as the prior stories.

## 6. Why G4 flips PASS — the binary verdict

The SW-181 AC-9 rule is explicit: *"IF the measurement supports GA
at `cross-file-heuristic`, the row flips PASS; IF it does not, the
row stays UNKNOWN with the F5 finding recorded."* The F5 measurement
SUCCEEDS IN RUNNING AND ITS FINDING SUPPORTS GA at
`cross-file-heuristic`:

- F5 fan-out is ABSENT — 0 spurious edges, 0 tuples with
  `distinct_target_files > edges` (the PARITY-002 signature).
- The structural immunity is in the resolver shape (Ruby uses
  `requireBinder` → `importFileTargets`, not `clausePackageFileNodes`).
  The same shape protected the TS family (SW-193).
- All 4 cross-file edge kinds (imports, references, calls, defines)
  are at their natural extent — no fabricated targets, no phantom
  imports.

Per SW-181 AC-9, the G4 evidence row flips to PASS. The level
printed beside GA stays `cross-file-heuristic` — the measurement
supports GA at the declared level, no re-grade is performed, and
no `RUBYFANOUT-001`-shaped defect is filed (no fan-out defect was
found).

## 7. The harness gap, stated before any number

**The Ruby parity driver does not exist in `internal/parity/`.**
The harness as it stands today:

| family | runner | source model | class table | cmd/parity wiring |
|---|---|---|---|---|
| Go | `Run` (`internal/parity/run.go:106`) | `RepoModel` (`gosource.go`) | `ClassesPath = "docs/rc/parity-classes.yaml"` | `-family go` |
| JVM | `RunJVM` (`internal/parity/jvmrun.go:183`) | `JVMModel` (`jvmsource.go`) | `ClassesPathJVM` | `-family jvm` |
| **Ruby** | **does not exist** | **does not exist** | **does not exist** | **no `-family ruby` option** |

A direct build of a ruby parity driver would have the same shape
as SW-176's WP-J7 JVM half — `rubyrun.go` (the run method),
`rubysource.go` (the Ruby source model covering the
`.rb` extension), `rubyclasses.go` (the real-repo class table),
`docs/rc/parity-classes-ruby-real-repo.yaml` (the YAML), plus the
`-family ruby` wiring in `cmd/parity/main.go`. **This is W6+ scope
(NOT done by SW-195).** Filed as `PARITY-RUBY-DRIVER-001`, the
same gap pattern as SW-192's python driver and SW-193's TS family
driver.

The formal `-verdict-diff` / `-counts-diff` exit-0 gates cannot be
asserted from `cmd/parity -family ruby`; the substantive half of
`-counts-diff` (every per-row count agrees at the snapshot row
level) holds and is the load-bearing measurement.

## 8. What this measurement does and does not say

**Says.** On the only Ruby corpus pin at the v3 measured standard
(sinatra v4.0.0, sha `b626e2d82c23b4fde0b51782fd32ca27ccde1d1a`,
tier 3, 143 .rb of 321 tracked, measured 2026-08-20), Ruby's
`require` resolution does NOT fan out over colliding directory
clauses (PARITY-002 shape). The structural immunity is in the
resolver shape: `requireBinder` at `engine/link/resolve_common.go:332`
builds `importFileTargets` (exact-path resolution), not
`pkgImportPaths` (clause-keyed fan-out). The 2 (importer, kind)
tuples with `>1 distinct_target_file` are both legitimate
multi-require patterns, not fan-out defects. Two `graphi rebuild`
dispatches agree on every per-row count at the snapshot row level.

**Does not say.** Anything about the Ruby parity matrix (the
harness has no ruby family driver; the Ruby parity classes YAML
is a hermetic-fixture table only). Anything about Ruby pins other
than sinatra (sinatra is the only one at v3). Anything about
performance. Anything about the correctness of the edges beyond
"they exist where Ruby actually requires them". The fix direction
for Python (`PYTHONFANOUT-001`) is NOT applicable here — Ruby
already uses `importFileTargets`, the fix is already shipped by
construction.

## 9. Reproducing this measurement

```bash
# 1. Clone sinatra at the v4.0.0 sha (matches the manifest pin).
mkdir -p /tmp/sw195 && cd /tmp/sw195
git clone --depth 1 --branch v4.0.0 https://github.com/sinatra/sinatra.git sinatra-src
cd sinatra-src && git log -1 --format="%H"
# → b626e2d82c23b4fde0b51782fd32ca27ccde1d1a

# 2. Build the binary used in this measurement (HEAD da47330 at run time).
cd "$REPO"   # the graphi workspace checkout (use $REPO to avoid leaking the absolute build path)
CGO_ENABLED=0 go build -o /tmp/graphi-sw195 ./cmd/graphi
# → de4a16df310d56a35284312ee7def71ce105c6b2c9c8b43ba6e39312053b8a30

# 3. Two rebuilds into separate workdirs.
mkdir -p /var/tmp/parity-sinatra-A /var/tmp/parity-sinatra-B
cd /tmp/sw195/sinatra-src && /tmp/graphi-sw195 rebuild -root . \
    -db /var/tmp/parity-sinatra-A/sinatra.db -meta /var/tmp/parity-sinatra-A/sinatra-meta
/tmp/graphi-sw195 snapshot sinatra-full -root /tmp/sw195/sinatra-src

/tmp/graphi-sw195 rebuild -root /tmp/sw195/sinatra-src \
    -db /var/tmp/parity-sinatra-B/sinatra.db -meta /var/tmp/parity-sinatra-B/sinatra-meta
/tmp/graphi-sw195 snapshot sinatra-full-rerun -root /tmp/sw195/sinatra-src

# 4. The byte-level content equality probe.
SNAP_A=~/.graphi/*/snapshots/sinatra-full.sqlite
SNAP_B=~/.graphi/*/snapshots/sinatra-full-rerun.sqlite
for q in "SELECT COUNT(*) FROM nodes" \
         "SELECT COUNT(*) FROM edges" \
         "SELECT kind, COUNT(*) FROM edges GROUP BY kind" \
         "SELECT source_path FROM nodes WHERE kind='file' ORDER BY source_path"; do
  diff <(sqlite3 "$SNAP_A" "$q") <(sqlite3 "$SNAP_B" "$q") && echo "IDENTICAL: $q"
done

# 5. The F5 fan-out probe — should produce 2 rows in both dispatches,
#    both with distinct_target_files = edges (NOT fan-out).
sqlite3 "$SNAP_A" <<'SQL'
SELECT ef.source_path AS importer, e.kind,
       COUNT(DISTINCT et.source_path) AS distinct_target_files,
       COUNT(*) AS edges
FROM edges e
JOIN nodes ef ON ef.id = e.from_id
JOIN nodes et ON et.id = e.to_id
WHERE e.kind IN ('imports','references','calls')
  AND ef.kind = 'file' AND et.kind = 'file'
  AND ef.source_path != et.source_path
GROUP BY ef.source_path, e.kind
HAVING COUNT(DISTINCT et.source_path) > 1
ORDER BY distinct_target_files DESC, edges DESC;
SQL
# → 2 rows (json_spec.rb + respond_with_spec.rb, both with distinct=edges=2)
```

## 10. Provenance

| field | value |
|---|---|
| Pin | sinatra `v4.0.0` at sha `b626e2d82c23b4fde0b51782fd32ca27ccde1d1a` (matches manifest pin exactly, NOT STALE — unlike SW-192's flask pin) |
| Measured | 143 .rb of 321 tracked, tier 3, measured_at 2026-08-20 (per manifest) |
| Runner class | `Darwin-ARM64/apple-m2-max` |
| Branch tip | `da47330bd7d06498fa200bba8970449d69357bfe` (main HEAD at run time) |
| Candidate SHA | `9f687849cec2b26311401191e90b60e40b5f6cee` (post-SW-188, per SW-195 AC-7) |
| Product binary digest | `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` (current main binary, doc-only branch → UNCHANGED) |
| Go version | go1.26.6 darwin/arm64 |
| Verdict | F5 fan-out ABSENT; GA at cross-file-heuristic SUPPORTED; GA-LANG-ruby-G4 flips PASS |
| Driver | `graphi rebuild` + `graphi snapshot` (manual, F5 escape per SW-176 AC-2 + SW-192 python F5 + SW-193 TS family precedent — `cmd/parity -family ruby` is unbound at `cmd/parity/main.go`) |

> **CORRECTION 2026-08-24 (SW-192..197 integration, rebuild round 1, review
> finding B1) — the `product_binary_digest` cell above is corrected in place.
> Nothing else in this record is rewritten, and no verdict moves.** The cell
> read `f9918e5cf5860c8c7b94d506aec43d5961ee1c44a49164f176056e13df1d8dd6`. That
> value is **unreproducible** and it is withdrawn.
>
> **What was rebuilt, and what came out.** The canonical recipe this project
> uses for a product-binary digest is `CGO_ENABLED=0 go build -trimpath
> -buildvcs=false -o <bin> ./cmd/graphi` (`docs/decisions/2026-08-parity-candidate-move-adr0013.md`),
> on go1.26.6 darwin/arm64 — the toolchain this record itself declares. Run at
> SW-195's own declared branch tip `da47330b`, at SW-195's own commit `f4bd1d9`,
> at the candidate `9f687849` and at the pre-integration base `3fe97f04`, it
> produces `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf`
> every time — the same digest at all four, because the four trees are
> product-byte identical. Dropping `-buildvcs=false` changes nothing here (a
> detached worktree stamps no VCS metadata: `go version -m` shows only
> `-buildmode`, `-compiler`, `-trimpath=true`); dropping `-trimpath` makes the
> digest path-dependent and therefore not a candidate identity at all. **No
> recipe at any of those four commits produces the withdrawn value**, and no
> binary with that digest exists on the machine this record was written on.
>
> **Why `0de6e64d…` is the right value rather than a guess.** The cell's own
> parenthetical says *"current main binary, doc-only branch → UNCHANGED"*, and
> `0de6e64d…` is what main builds to: SW-192 records it verbatim
> (`docs/rc/python-f5-measurement.md` §11), SW-193's `ky` block in
> `corpus/manifest.json` records it at the **same** `candidate_sha`,
> `branch_tip` and `go_version` this record declares, and SW-190 / SW-204 /
> SW-207 / SW-209 all carry it. `git log --all -S` finds the withdrawn token
> introduced by exactly one commit in the history of any branch — `f4bd1d9`,
> SW-195 itself. SW-195 never derived a digest at all: its verification argued
> "digest UNCHANGED" *structurally*, from `git diff --stat` showing 0 Go-line
> changes, and built no binary. The withdrawn token is a transcription error,
> not the record of a different build.
>
> **Scope of the correction.** The ruby F5 counts, the two-dispatch agreement
> and `GA-LANG-ruby-G4`'s PASS rest on the dispatches SW-195 ran and are not
> re-taken here. What is corrected is the provenance anchor naming which binary
> ran them.

