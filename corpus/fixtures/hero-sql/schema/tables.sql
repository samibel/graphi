-- schema/tables.sql — the three base relations the views reference.
--
-- QN keys on `<dir>.<bare>` via core/parse/parser_tswalk.go:240
-- langPackage; the parent dir basename is "schema", so these CREATE
-- TABLE statements are indexed as `schema.core`, `schema.salute`,
-- `schema.run` (KindType). The three tables are uniquely defined — no
-- other file in the fixture declares them — so they are NOT ambiguous
-- under dirAmbiguous. They are the single-candidate targets for
-- definition(schema.X), search("core"), and the explain_symbol
-- partial target (the FROM-clause references in the two views give
-- schema.core ≥ 2 reference edges, so explain_symbol(schema.core,
-- max_items=1) truncates and marks the result partial).

CREATE TABLE core (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);

CREATE TABLE salute (
  id   INTEGER PRIMARY KEY,
  body TEXT NOT NULL
);

CREATE TABLE run (
  id    INTEGER PRIMARY KEY,
  label TEXT NOT NULL
);