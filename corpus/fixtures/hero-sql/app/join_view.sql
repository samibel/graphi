-- app/join_view.sql — the canonical view that joins all three base tables,
-- PLUS an in-file helper table that powers the intra-file references edge.
--
-- QN keys on `<dir>.<bare>` via core/parse/parser_tswalk.go:240
-- langPackage; the parent dir basename is "app", so the CREATE statements
-- in this file are indexed as `app.helper` (KindType) and `app.join_view`
-- (KindType). The CREATE VIEW statement is parsed by sqlCollectDefs
-- (parser_sql.go:133). The FROM-clause identifiers are recorded by
-- sqlScanFrom (parser_sql.go:164).
--
-- TWO reference shapes live here:
--   1. INTRA-FILE: `app.join_view → app.helper` (the FROM helper reference
--      resolves against the in-file types table at parser_tswalk.go:181 and
--      becomes a real EdgeReferences edge — both ends live in this file).
--      This is the single candidate for references(app.helper) and the
--      single items-list item that powers explain_symbol truncation when
--      max_items=1 is applied.
--   2. CROSS-FILE PendingRef: `app.join_view → schema.core / schema.salute /
--      schema.run` — the three JOIN-target identifiers live in schema/tables.sql,
--      outside this file's types table, so they become PendingRefs (no
--      EdgeReferences edge). Per the §5.5 honest-empty doctrine, cross-file
--      references are not askable at this tier.

CREATE TABLE helper (
  id   INTEGER PRIMARY KEY,
  note TEXT NOT NULL
);

CREATE VIEW join_view AS
  SELECT helper.id, core.name, salute.body, run.label
  FROM helper, core, salute, run
  WHERE core.id = helper.id;