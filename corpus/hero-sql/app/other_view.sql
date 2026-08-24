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
-- the ambiguous QN returns the AMBIGUOUS outcome). The FROM clause
-- here ALSO references schema.core and schema.salute — both cross-file
-- PendingRefs, never EdgeReferences — which is the SQL honest-empty
-- doctrine at the cross-file boundary.

CREATE VIEW join_view AS
  SELECT core.id, salute.body
  FROM core
  JOIN salute ON salute.id = core.id;