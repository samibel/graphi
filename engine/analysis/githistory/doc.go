// Package githistory implements graphi's git-history signal analyzer (SW-032,
// EP-005). It derives per-file and per-function signals from local git history:
// churn (number of changes in a bounded window), bus-factor (unique authors
// touching an entity), and co-change neighbors (files that frequently change
// together in the same commit).
//
// Scope (v1): reads an abstracted GitProvider interface so the analyzer can be
// tested without a real .git directory. The production provider shells out to
// local `git log`/`git blame`; tests use InMemoryProvider.
//
// Layering: githistory is a sub-package of engine/analysis. It imports ONLY
// stdlib packages and MUST NOT import engine/analysis (avoids import cycle).
// The parent analysis package wraps the exported Analyzer with a thin adapter
// (gitHistoryAdapter) for registry dispatch, mirroring the taint pattern.
//
// Bounded window: the analyzer accepts a dual constraint — max commits AND max
// age (whichever is tighter). This bounds computation on large repos without
// requiring configuration changes for repos of varying sizes.
//
// Determinism: commits are processed in the order returned by GitProvider
// (expected topological/chronological). All output maps are materialized into
// sorted slices before return, so identical inputs produce identical results
// across runs.
package githistory
