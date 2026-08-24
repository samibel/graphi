# Cross-file-heuristic residual corpus-pin abstention (2026-08-23, W5.i SW-195)

> **Eight of nine cross-file-heuristic residual languages have no
> corpus pin at v3 measured standard at SW-195 close.** This document
> records the abstention: the SW-195 F5 measurement cannot be run
> for bash, c, cpp, c_sharp, lua, php, rust, and sql because no
> real-repo pin exists at v3 (release-tag ref + full 40-char sha +
> measured block + tier + language-specific stratification) for any
> of them in `corpus/manifest.json` at the post-SW-188 candidate.
> Per the spec rule "STALE rows are re-marked, never re-pointed;
> abstention is honest" and per SW-195 AC-3 ("the row stays UNKNOWN
> with the SW-195-specific reason named"), each language's G4 row
> stays UNKNOWN with the abstention reason documented here. The
> dependency on SW-196 (corpus pins v3 for cross-file-heuristic
> residual) is the load-bearing work that lifts this abstention;
> SW-195 records the gap, does not paper over it.

## TL;DR

| language | corpus pin at v3? | F5 fan-out measured? | G4 disposition | reason |
|---|---|---|---|---|
| ruby | **YES** (sinatra v4.0.0) | **YES** (this story) | **PASS** | structural immunity (requireBinder → importFileTargets); see ruby-f5-measurement.md |
| bash | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| c | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| cpp | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency (c and cpp share scope per SW-184) |
| c_sharp | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| lua | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| php | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| rust | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency |
| sql | NO | NO | UNKNOWN (abstention) | no v3 pin in corpus; SW-196 dependency; ADR-W1 over-claim (JOIN/VIEW shape, not import shape) noted |

> **AMENDMENT 2026-08-24 (SW-192..197 integration, rebuild round 1, review
> finding B2) — six of the eight "no v3 pin in corpus; SW-196 dependency" cells
> above are now HISTORY, not the present state. Added, not rewritten: the table
> stands exactly as SW-195 published it, because it was true when published.**
>
> **SW-196 landed in this same integration** (merge `bf53c0f`) and lifted six of
> the eight to a v3 pin in `corpus/manifest.json` — c to cjson `v1.7.19`, cpp to
> nlohmann_json `v3.11.3`, c_sharp to Newtonsoft.Json `13.0.3`, lua to
> lua-resty-core `v0.1.26`, php to composer `2.9.8`, rust to serde `v1.0.219`.
> SW-196 added **eight** entries in all — those six v3 pins plus the two honest
> `no_pin` abstentions below — and the merged manifest at `bf53c0f` carries 28
> (20 already on main, 8 from SW-196). Each of those six languages'
> `GA-LANG-<lang>-G5` rows now reads PASS citing exactly those pins, field by
> field, machine-checked by `internal/corpus/pin_test.go`. For those six, read
> every "SW-196 dependency" cell above as *"was blocked on SW-196; the pin has
> since landed, and what remains is the measurement, not the pin"*.
>
> **bash and sql are NOT swept in, and their two cells stand unamended.** SW-196
> did not pin them and was not supposed to: it filed an honest `no_pin`
> abstention for each — the `bash-abstention` and `sql-abstention` entries in
> `corpus/manifest.json` — each carrying a named `no_pin_reason` whose substance
> is pinned by `internal/corpus/pin_test.go`. "No v3 pin in corpus" is still
> literally true for bash and sql, their `GA-LANG-{bash,sql}-G5` rows stay
> UNKNOWN, and no lift is owed for either.
>
> **Nothing above reads PASS wrongly, and no verdict moves here.** All eight
> `GA-LANG-<lang>-G4` rows in the table above are still UNKNOWN, because a pin is
> not a measurement: none of the six landed pins has had the SW-195 F5 dispatch
> run against it. This amendment corrects the tense of a blocker, not a verdict.


Nine languages, one corpus pin (sinatra), one measurement (ruby),
eight abstentions. Ruby flips PASS; the other eight stay UNKNOWN
with the abstention reason named in each row's `current` field.
The abstention is the honest outcome per SW-181 AC-9 and the spec
rule.

## 1. Why abstention is the honest outcome for eight languages

The SW-195 AC-3 condition is explicit:

> "GA-LANG-<lang>-G4 shall flip to PASS with URI and sha per
> language **only if** SW-196's pin is at v3 measured standard AND
> the two-dispatch parity agrees; otherwise the row stays UNKNOWN
> with the SW-195-specific reason named."

SW-195's measurement discipline is the SW-192 / SW-193 pattern:
run `graphi rebuild` + `graphi snapshot` + SQLite F5 probe over a
real-repo pin at the v3 measured standard, two dispatches agreeing
at per-row count granularity, raw samples checked in. The
discipline requires a pin to run against. Eight languages have no
such pin at the SW-195 close:

```
$ jq '.entries[] | select(.language == "bash" or .language == "c"
   or .language == "cpp" or .language == "c_sharp" or .language == "lua"
   or .language == "php" or .language == "rust" or .language == "sql")
   | .name' corpus/manifest.json
(empty result)
```

No pin exists for bash, c, cpp, c_sharp, lua, php, rust, or sql.
The corpus has pins for cobra, gin, grpc-go, kubernetes, lo, uuid
(Go); guava, kotlinx.serialization, okio (JVM); flask, sinatra
(Python / Ruby); ky, express (TypeScript family); plus the four
`tier1-fixture-*` hermetic fixtures. None of these cover the eight
abstained languages.

Per the spec rule (parity-matrix-real-repo §0): *"STALE rows are
re-marked, never re-pointed; abstention is honest."* Re-grading
the G4 row to PASS in the absence of a measurement would be the
exact failure mode SW-181 AC-9 names: declared without evidence.
The honest outcome is UNKNOWN with the abstention reason named.

## 2. What would unblock the abstention per language

The path to G4 PASS for each abstained language is concrete and
named:

| language | unblock work | owner story |
|---|---|---|
| bash | lift a bash repo (e.g. [nushell](https://github.com/nushell/nushell) subdir of pure .sh, or a small standalone bash utility) to the v3 measured standard as DATA in `corpus/manifest.json`; then run the SW-195 measurement recipe over it | **SW-196** |
| c | lift a C repo (e.g. libpng, cjson, or redis-stable's `deps/` C libs) to v3; pin at full 40-char sha | **SW-196** |
| cpp | lift a C++ repo (e.g. nlohmann/json, fmt, or catch2) to v3; pin at full 40-char sha. C and CPP share the G4 measurement scope (one resolver, two pins) per SW-184 | **SW-196** |
| c_sharp | lift a C# repo (e.g. newtonsoft.json, humanizer) to v3 | **SW-196** |
| lua | lift a Lua repo (e.g. luarocks, luajit) to v3; the PendingRef parser gap (SW-194b.5) is tracked separately | **SW-196** |
| php | lift a PHP repo (e.g. composer, phpunit) to v3; the PendingRef parser gap (SW-194b.5) is tracked separately | **SW-196** |
| rust | lift a Rust repo (e.g. serde, tokio) to v3 | **SW-196** |
| sql | lift a SQL repo (e.g. sqlite, postgres) to v3; the SQL G4 measurement scope is the JOIN/VIEW shape (ADR-W1), not the import shape, per SW-183 | **SW-196** |

> **AMENDMENT 2026-08-24 (SW-192..197 integration, rebuild round 1, review
> finding B2) — the "owner story" column above is discharged, and the "unblock
> work" column is done, for six of the eight rows. Added, not rewritten.**
>
> The lift named in the c, cpp, c_sharp, lua, php and rust rows **has happened**:
> SW-196 landed those six pins at merge `bf53c0f` in this integration (the
> repositories and refs are named in the amendment under the TL;DR above). For
> those six the remaining work is no longer "lift a repo" but the second half of
> each row — running the SW-195 measurement recipe over the landed pin — which
> SW-196 does not own and which each `GA-LANG-<lang>-G4` row's `next_action`
> tracks as `RESIDUALPINS-001`.
>
> The bash and sql rows are discharged **differently and deliberately**: SW-196
> settled both by honest `no_pin` abstention rather than by a pin, so the "lift a
> bash repo" / "lift a SQL repo" work named above is **not owed** and is not
> merely outstanding. Their G5 rows stay UNKNOWN with the abstention reason
> named, which is the same outcome this document argues for in section 1.


SW-196 (corpus pins v3 for cross-file-heuristic residual) is the
follow-on story that closes the eight abstentions. SW-195 names
them, does not lift them.

## 3. What the structural reasoning tells us BEFORE the measurement

For honesty's sake: the F5 fan-out question does not depend
strictly on a corpus pin — the resolver shape names the answer
in advance. The 9 languages' resolvers sit in one of three buckets:

| bucket | resolver shape | F5 fan-out expectation |
|---|---|---|
| `importFileTargets`-only | bash (`requireBinder`), c/cpp (`includeBinder`), lua (`requireBinder`), php (`requireBinder`), ruby (`requireBinder`) | **ABSENT** — exact-path resolution, no directory fan-out |
| `pkgImportPaths` (clause-keyed) | c_sharp (`resolve_csharp.go:47`), rust (`resolve_rust.go:49`), python (`resolve_python.go:76`) | **PRESENT** (PARITY-002 shape) — same shape Python exhibited on flask |
| empty binder | sql (`resolve_bash.go:46`) | **N/A** — no imports to fan out (cross-file `references` edges still possible via same-directory `derived` resolution; JOIN/VIEW shape, per ADR 0012 / SW-183) |

This structural reasoning is NOT a measurement and does NOT flip
G4 rows on its own. It is recorded here as a prediction the
post-SW-196 measurements will test:

- **bash, c, cpp, lua, php**: structural immunity — same outcome as
  TS family (SW-193 PASS) and ruby (this story PASS).
- **c_sharp, rust**: structural susceptibility — same outcome as
  python (SW-192 UNKNOWN with fan-out finding); the post-SW-196
  measurement will likely file `<LANG>FANOUT-001` defects
  (CSHARPFANOUT-001, RUSTFANOUT-001).
- **sql**: empty binder — the G4 measurement scope is the
  JOIN/VIEW same-directory `derived` shape (per ADR 0012 / SW-183),
  not the import shape. The structural outcome is vacuous PASS for
  the import-shape question, with the over-claim documented
  per AC-4.

These are PREDICTIONS, not measurements. The measurements run
AFTER SW-196 lifts the pins; this story records the structural
reasoning honestly and abstains from declaring G4 PASS in the
absence of evidence.

## 4. The SQL over-claim, stated before any number

The SQL row deserves special note. Per ADR 0012 (SW-183): SQL has
no file-inclusion construct (`\i` is psql client, `SOURCE` is
mysql client; ISO/IEC 9075 defines no `import`). The SQL resolver
at `engine/link/resolve_bash.go:46` uses an EMPTY binder — no
`imports` edges are emitted, ever.

The SW-195 AC-4 specification says SQL's G4 measurement shall
exercise the JOIN/VIEW resolution shape (same-directory `derived`
cross-file `references` edges for tables/views), not the import
shape. The empty-binder design means:

- SQL's `imports` edge count is 0 by construction.
- SQL's `references` edges can exist for same-directory JOIN/VIEW
  shapes (e.g. `query.sql` declares a VIEW on a TABLE in
  `schema.sql` — a cross-file `references` edge at `derived` tier).
- The ADR-W1 over-claim was that SQL was bound at
  `cross-file-heuristic` and would emit real cross-file edges; the
  resolution is that it emits them via same-directory `derived`,
  not via imports. Per SW-183's correction, SQL's
  `cross-file-heuristic` level is EARNED, not over-claimed.

A post-SW-196 SQL measurement will run the same recipe: clone a
SQL-bearing repo (sqlite's `src/` for example, which is C but
includes test fixtures with `.sql` files), probe for
same-directory `references` edges, verify they exist where the
schema actually references another schema's table/view. SW-195
records the scope honestly and abstains; the SQL over-claim
discipline (per ADR 0012) is the post-SW-196 measurement's
precondition.

## 5. What this abstention does and does not say

**Says.** At the SW-195 close, eight of nine cross-file-heuristic
residual languages have no corpus pin at the v3 measured standard.
The F5 measurement cannot be run for them. Per SW-195 AC-3, each
row stays UNKNOWN with this abstention reason named. Per the spec
rule, the abstention is honest — re-grading in the absence of a
measurement would be a declared PASS without evidence, the exact
failure mode SW-181 AC-9 forbids. The structural reasoning in §3
predicts the post-SW-196 outcomes (bash/c/cpp/lua/php/ruby → PASS,
c_sharp/rust → UNKNOWN with fan-out, sql → JOIN/VIEW shape) but
those are predictions, not measurements.

**Does not say.** That the languages cannot reach GA at
`cross-file-heuristic`. The structural reasoning suggests most can
— but the structural reasoning is not the measurement, and SW-195
does not declare the outcome in advance.

## 6. Provenance

| field | value |
|---|---|
| Story | SW-195 (W5.i, 2026-08-23) |
| Spec | `specs/ga-for-all-shipped-languages.md` line 154 |
| Dependency | SW-196 (corpus pins v3 for cross-file-heuristic residual) — NOT YET SHIPPED at SW-195 close |
| Files | `docs/rc/cross-file-residual-abstention.md` (this document), `docs/rc/ruby-f5-measurement.md` (the ruby measurement), `docs/rc/parity-matrix-real-repo.md` (W5.i section), `docs/rc/evidence-index.{md,yaml}` (9 G4 rows), `corpus/manifest.json` (f5_measurement block on sinatra) |
| Branch | `sw-195-w5i-real-repo-parity-cross-file-residual` |
| Branch tip | TBD (filled in at branch push) |
| Candidate SHA | `9f687849cec2b26311401191e90b60e40b5f6cee` (post-SW-188, per SW-195 AC-7) |
| Product binary digest | `0de6e64d6174f1793efbe8d3d0b2beb6561c3095a965f7ecdac3e86bfef46ebf` (current main binary, doc-only branch → UNCHANGED) |
| Runner class | `Darwin-ARM64/apple-m2-max` |
| Ruby disposition | PASS — F5 fan-out ABSENT, structural immunity confirmed |
| Eight-language disposition | UNKNOWN (abstention) — no v3 corpus pin, SW-196 dependency |

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

