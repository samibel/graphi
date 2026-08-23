-- app/other_view.sql — a SECOND view declaration with the SAME bare
-- name `join_view`, in the SAME directory `app/`. This is the SQL
-- analog of the c/cpp init_c / bash init ambiguity.
--
-- QN: `app.join_view` (parent dir basename = "app"). Both this file
-- AND app/join_view.sql declare a relation named join_view, so the
-- shared CST walk records TWO distinct NodeIds under the byDir key
-- "app.join_view". dirAmbiguous["app"]["join_view"] = true, and
-- resolve.Strict("app.join_view") returns AMBIGUOUS at the shared
-- resolver seam (engine/agenttools/resolve/resolve.go:177).
--
-- This drives the hero-sql-07-callers-ambiguous scenario (callers on
-- the ambiguous QN returns the AMBIGUOUS outcome) and the
-- hero-sql-15-explain-partial scenario (any operation that has to
-- resolve app.join_view first lands in AMBIGUOUS, not partial — that's
-- why the partial scenario uses schema.core instead: schema.core has
-- ≥ 2 FROM-clause reference edges from both views, so
-- explain_symbol(schema.core, max_items=1) truncates and marks partial
-- while staying strictly resolved).

CREATE VIEW join_view AS
  SELECT core.id, salute.body
  FROM core
  JOIN salute ON salute.id = core.id;