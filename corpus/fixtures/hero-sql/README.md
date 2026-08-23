# hero-sql fixture — W5.k SW-197

Tier-1 pinned SQL fixture for the SW-197 hero-sql tasks. SQL has NO
import construct (per ISO/IEC 9075 and the engine/link README: SQL defines
no file-inclusion construct at the language level), so the cross-file
operations — `callers`, `callees`, `references`, `impact`, `related_files`
across files — return well-formed empty outcomes. That's the
parse-determinism honest-empty discipline AC-4 binds. Same-directory
intra-file operations remain askable and are exercised through
`search` / `definition` / `agent_brief` / `explain_symbol`.

## Layout

```
hero-sql/
├── schema/
│   └── tables.sql         CREATE TABLE core, salute, run
└── app/
    ├── join_view.sql      CREATE VIEW join_view FROM core JOIN salute JOIN run
    └── other_view.sql     CREATE VIEW join_view FROM core   (duplicate name)
```

## Symbol QN convention

QN keys on `<dir>.<bare>` (the parent directory's basename, via
`core/parse/parser_tswalk.go:240 langPackage`), so the three CREATE TABLE
statements in `schema/tables.sql` are indexed as `schema.core`,
`schema.salute`, `schema.run` (all `KindType`). The two CREATE VIEW
statements in `app/*.sql` both declare `join_view` — the bare-name
collision inside the `app/` directory makes `app.join_view` AMBIGUOUS
(two distinct NodeIds under the same byDir key), exactly like the bash
init / c/cpp init_c ambiguity the parallel heroes use to exercise the
"ambiguous" failure class.

## Cross-file honest-empty doctrine

SQL has no IMPORT system (the parser at `core/parse/parser_sql.go:77`
returns empty `Imports/References`). The only inter-file relation is
the FROM-clause reference INSIDE a view, which is intra-file when the
referenced table lives in the SAME file. Cross-file FROM references
(no SQL construct here) become `PendingRef`s, NOT cross-file calls
edges. The SQL parser therefore mints no EdgeImports graph edges, no
file→file imports edges, and no cross-file calls/references — `engine/link/`
has no `resolve_sql.go`. The hero-sql scenarios document this honestly
in their description fields per AC-4.

## Why this shape

* Three tables + two views (one duplicate-named) exercise the four
  failure classes (found, not_found, ambiguous, partial, empty) without
  needing a network, a SQL engine, or a fixture compiler.
* `core/parse/parser_sql.go:133 sqlCollectDefs` indexes every CREATE
  TABLE / CREATE VIEW as `KindType`, so search/definition/agent_brief
  are askable on every bare relation name.
* `sqlScanFrom` records every FROM-clause identifier as an intra-file
  `references` edge or, when unresolved, a `PendingRef` — providing the
  items list that powers `explain_symbol(view, max_items=1)` truncation
  for the partial scenario.
* The directory-basename package convention guarantees `app.join_view`
  is ambiguous without any explicit ambiguity machinery; the SQL parser
  inherits the dirAmbiguous behavior from the shared CST walk.