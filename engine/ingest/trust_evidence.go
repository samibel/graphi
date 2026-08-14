package ingest

// This file is the P1 WP1.2 detail-evidence persistence (PRD §14.3): per-file
// and per-package trust evidence rows in the ingest meta sidecar
// (trust_file_evidence / trust_package_evidence, schema.go migration 2 -> 3),
// bound to the full-pass generation exactly like the snapshot triple
// (trust_persist.go). This is what makes target-scope assessments (PRD §27)
// decidable through selective reads instead of whole-graph scans.
//
// Write discipline mirrors the snapshot triple (ADR 0006 D4, PRD §14.4
// variant 3): the full pass writes every row under the pass generation AFTER
// the graph's own commits and BEFORE finishFullPass — so the open full-pass
// marker guards the publish window and a failure fails the pass loudly — and
// wipes every other generation's rows; an incremental pass refreshes the
// touched files' rows under the live generation and deletes rows of deleted
// files. Read ports are selective and generation-checked: a generation
// mismatch reads not-found (stale evidence is unusable evidence), and a
// sidecar predating the tables reads evidence-unavailable — never
// empty-healthy (PRD §20 "stale Skip-Evidence wird nicht weiterverwendet").

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/engine/typeresolve"
)

// trustEvidenceSchemaVersion is the sidecar schema version that introduced the
// evidence tables (schema.go ladder step 2 -> 3). A sidecar below it has no
// tables to read and MUST surface ErrTrustEvidenceUnavailable, never an empty
// (healthy-looking) result.
const trustEvidenceSchemaVersion = 3

// Parse-status vocabulary of FileEvidence.ParseStatus (closed set). The
// values are aliases of engine/trust's scope-facts vocabulary — trust cannot
// import ingest (layering), so the closed set lives there and the rows here
// alias it: one source, the persisted values and the policy evaluator's
// expectations cannot drift.
const (
	// ParseStatusParsed — the file parsed and its symbols were committed.
	ParseStatusParsed = trust.ScopeParseStatusParsed
	// ParseStatusSkipped — the file was skipped fail-closed; ParseReason
	// carries the SkipReason (oversize / timeout / max-depth / unreadable /
	// parse-error).
	ParseStatusSkipped = trust.ScopeParseStatusSkipped
)

// Package-state vocabulary of PackageEvidence.State (closed set, PRD §22),
// aliased from engine/trust for the same one-source reason as the parse
// statuses. The PRD-22 pin: type_errors > 0 alone is NOT degraded — a unit
// that checked with swallowed errors stays checked_with_errors, and
// DegradedReason is never invented from an error count.
const (
	// PackageStateChecked — the unit type-checked with zero swallowed errors.
	PackageStateChecked = trust.ScopePackageStateChecked
	// PackageStateCheckedWithErrors — the unit type-checked; some errors were
	// swallowed (expected with stub imports). Still authoritative, not degraded.
	PackageStateCheckedWithErrors = trust.ScopePackageStateCheckedWithErrors
	// PackageStateDegraded — the unit was not type-checked; DegradedReason
	// names why (multiple clauses, import cycle, full-parse failure, panic).
	PackageStateDegraded = trust.ScopePackageStateDegraded
	// PackageStateSkipped — reserved by the PRD §22 vocabulary for a unit the
	// resolver never attempted. The v1 Go collector never mints it (a skipped
	// resolver publishes no rows at all); readers must still accept it.
	PackageStateSkipped = trust.ScopePackageStateSkipped
)

// ErrTrustEvidenceUnavailable marks a sidecar that cannot serve evidence at
// all: its schema predates the evidence tables (e.g. an old store opened by a
// read-only observer, which never migrates). Fail-closed: absence of the
// tables is "no answer", never "no findings".
var ErrTrustEvidenceUnavailable = errors.New("ingest: trust evidence unavailable")

// ErrTrustEvidenceNotFound marks a selective lookup that found no row for the
// requested (generation, key) — including every generation mismatch: evidence
// of another generation is stale evidence and is never served silently.
var ErrTrustEvidenceNotFound = errors.New("ingest: trust evidence not found")

// FileEvidence is one file's persisted per-generation trust evidence row
// (PRD §14.3 / §21): parse coverage plus the linker's per-file resolution
// counters. Path is the normalized repo-relative POSIX path.
type FileEvidence struct {
	Generation        string
	Path              string
	Language          string
	ParseStatus       string
	ParseReason       string
	ResolvedDerived   int
	ResolvedHeuristic int
	ResolvedExternal  int
	Skipped           int
	Ambiguous         int
}

// PackageEvidence is one package's persisted per-generation trust evidence row
// (PRD §14.3 / §22). PackageKey is the unit's repo-relative directory ("." for
// the repository root) — the same key space typeresolve units and package-
// looking targets use. A directory holding multiple package clauses collapses
// to ONE degraded row (every unit in such a directory is degraded with the
// same reason by construction).
type PackageEvidence struct {
	Generation     string
	PackageKey     string
	State          string
	DegradedReason string
	TypeErrors     int
	DroppedIntents int
	ConfirmedEdges int
	SkippedFiles   int
}

// packageEvidenceFromResult folds one whole-repo typeresolve pass into the
// per-package evidence rows, keyed by unit directory. dirOf maps committed
// NodeIds to their source directory (path.Dir of the node's source path, "."
// for the root — the unit key space), so confirmed edges are attributed to the
// package that OWNS the from-node. Grouping-skipped files (test files,
// unparseable clauses) are attributed to their directory's unit when one
// exists; a directory with no checkable unit carries no row (no unit, no
// package claim). State derivation follows PRD §22: degraded only from a
// recorded degradation reason, NEVER from the type-error count alone.
func packageEvidenceFromResult(res typeresolve.Result, dirOf map[model.NodeId]string) []PackageEvidence {
	byKey := map[string]*PackageEvidence{}
	for _, u := range res.Units {
		r := byKey[u.Dir]
		if r == nil {
			r = &PackageEvidence{PackageKey: u.Dir}
			byKey[u.Dir] = r
		}
		r.TypeErrors += u.TypeErrors
		r.DroppedIntents += u.DroppedIntents
		if u.Degraded != "" && r.DegradedReason == "" {
			r.DegradedReason = u.Degraded
		}
	}
	for _, e := range res.Edges {
		if r := byKey[dirOf[e.From()]]; r != nil {
			r.ConfirmedEdges++
		}
	}
	for _, sf := range res.SkippedFiles {
		if r := byKey[path.Dir(sf.Path)]; r != nil {
			r.SkippedFiles++
		}
	}
	out := make([]PackageEvidence, 0, len(byKey))
	for _, r := range byKey {
		switch {
		case r.DegradedReason != "":
			r.State = PackageStateDegraded
		case r.TypeErrors > 0:
			r.State = PackageStateCheckedWithErrors
		default:
			r.State = PackageStateChecked
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].PackageKey < out[b].PackageKey })
	return out
}

// fileEvidenceRows builds this pass's file rows from the three pass-scoped
// signals: parsedLangs (path -> language of every file whose parse committed
// this pass), the fail-closed skip diagnostics, and the per-file linker
// counters. A file with neither a committed parse nor a skip diagnostic (an
// untracked asset with no registered parser) carries no row. Rows come back
// sorted by path.
func (i *Ingester) fileEvidenceRows(parsedLangs map[string]string) []FileEvidence {
	byPath := map[string]FileEvidence{}
	for rel, lang := range parsedLangs {
		byPath[rel] = FileEvidence{Path: rel, Language: lang, ParseStatus: ParseStatusParsed}
	}
	for _, s := range i.SkippedDiagnostics() {
		byPath[s.Path] = FileEvidence{Path: s.Path, ParseStatus: ParseStatusSkipped, ParseReason: string(s.Reason)}
	}
	out := make([]FileEvidence, 0, len(byPath))
	for p, fe := range byPath {
		st := i.lastFileLinkStats[model.NormalizePath(p)]
		fe.ResolvedDerived = st.ResolvedDerived
		fe.ResolvedHeuristic = st.ResolvedHeuristic
		fe.ResolvedExternal = st.ResolvedExternal
		fe.Skipped = st.Skipped
		fe.Ambiguous = st.Ambiguous
		out = append(out, fe)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	return out
}

// fullPassParsedLanguages derives the path -> language map of a full pass from
// its aligned units/parsed slices (only files whose parse actually committed:
// non-nil, non-skipped results — readFailed and no-parser units carry none).
func fullPassParsedLanguages(units []fileUnit, parsed []*ParsedFile) map[string]string {
	langs := make(map[string]string, len(units))
	for k, u := range units {
		if k >= len(parsed) {
			break
		}
		pf := parsed[k]
		if pf == nil || pf.skipped || pf.result == nil {
			continue
		}
		langs[u.relPath] = pf.result.Meta.Language
	}
	return langs
}

// persistTrustEvidenceFull publishes the full pass's evidence rows under the
// pass generation, wiping EVERY existing row first (the pass is authoritative
// for the whole tree, so the wipe is both the refresh and the other-generation
// cleanup). Called after the graph's own commits and before finishFullPass —
// the same publish window as the snapshot triple — so any failure fails the
// pass loudly while the open marker still guards readers.
func (i *Ingester) persistTrustEvidenceFull(ctx context.Context, generation string, units []fileUnit, parsed []*ParsedFile) error {
	rows := i.fileEvidenceRows(fullPassParsedLanguages(units, parsed))
	return i.metaTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM trust_file_evidence"); err != nil {
			return fmt.Errorf("ingest: clear file evidence: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM trust_package_evidence"); err != nil {
			return fmt.Errorf("ingest: clear package evidence: %w", err)
		}
		if err := insertFileEvidenceTx(ctx, tx, generation, rows); err != nil {
			return err
		}
		return insertPackageEvidenceTx(ctx, tx, generation, i.combinedPackageEvidence())
	})
}

// persistTrustEvidenceLive refreshes the evidence rows after a successful
// incremental mutation, under the CURRENT live generation (an incremental pass
// never mints one — mirrors persistTrustSnapshotLive, including the no-op on a
// store no full pass ever certified). touched is the pass's processed set (the
// changed files plus their cascade), parsedLangs the path -> language map of
// the parses that committed, removed the cached paths this pass purged.
//
// Refresh semantics: every touched or removed path's rows are deleted first
// (so a file that stopped producing evidence converges with a full pass), then
// the pass's rows are upserted — parse skips are upserted for every diagnostic
// of the pass (walk-time skips re-observe the whole tree, so this converges
// file rows even for paths outside the touched set). Package rows are replaced
// wholesale exactly when a semantic recompute completed and none read-failed
// (semanticEvidenceReady — each registrant's recompute is whole-repo); a pass
// that skipped the resolvers leaves the persisted package rows alone.
func (i *Ingester) persistTrustEvidenceLive(ctx context.Context, touched map[string]struct{}, parsedLangs map[string]string, removed []string) error {
	generation, err := i.store.Metadata(ctx, graphFullPassGenerationKey)
	if errors.Is(err, graphstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ingest: read live generation for trust evidence: %w", err)
	}
	if generation == "" {
		return nil
	}
	rows := i.fileEvidenceRows(parsedLangs)

	cleared := make([]string, 0, len(touched)+len(removed))
	for p := range touched {
		cleared = append(cleared, p)
	}
	cleared = append(cleared, removed...)
	sort.Strings(cleared)

	return i.metaTx(ctx, func(tx *sql.Tx) error {
		for _, p := range cleared {
			if _, err := tx.ExecContext(ctx, "DELETE FROM trust_file_evidence WHERE path = ?", p); err != nil {
				return fmt.Errorf("ingest: clear file evidence for %s: %w", p, err)
			}
		}
		if err := insertFileEvidenceTx(ctx, tx, generation, rows); err != nil {
			return err
		}
		if !i.semanticEvidenceReady() {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM trust_package_evidence"); err != nil {
			return fmt.Errorf("ingest: clear package evidence: %w", err)
		}
		return insertPackageEvidenceTx(ctx, tx, generation, i.combinedPackageEvidence())
	})
}

func insertFileEvidenceTx(ctx context.Context, tx *sql.Tx, generation string, rows []FileEvidence) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO trust_file_evidence
			(generation_id, path, language, parse_status, parse_reason,
			 resolved_derived, resolved_heuristic, resolved_external, skipped, ambiguous)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			generation, r.Path, r.Language, r.ParseStatus, r.ParseReason,
			r.ResolvedDerived, r.ResolvedHeuristic, r.ResolvedExternal, r.Skipped, r.Ambiguous); err != nil {
			return fmt.Errorf("ingest: persist file evidence for %s: %w", r.Path, err)
		}
	}
	return nil
}

func insertPackageEvidenceTx(ctx context.Context, tx *sql.Tx, generation string, rows []PackageEvidence) error {
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO trust_package_evidence
			(generation_id, package_key, state, degraded_reason,
			 type_errors, dropped_intents, confirmed_edges, skipped_files)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			generation, r.PackageKey, r.State, r.DegradedReason,
			r.TypeErrors, r.DroppedIntents, r.ConfirmedEdges, r.SkippedFiles); err != nil {
			return fmt.Errorf("ingest: persist package evidence for %s: %w", r.PackageKey, err)
		}
	}
	return nil
}

// trustEvidenceReady fails closed when the sidecar's schema predates the
// evidence tables: user_version is stamped by the migration ladder, so a value
// below the 2 -> 3 step means the tables were never created (a read-only
// observer never migrates). Missing schema is ErrTrustEvidenceUnavailable —
// evidence-unavailable, never empty-healthy.
func (i *Ingester) trustEvidenceReady(ctx context.Context) error {
	var v int
	if err := i.meta.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("ingest: trust evidence schema probe: %w", err)
	}
	if v < trustEvidenceSchemaVersion {
		return fmt.Errorf("%w: sidecar schema version %d predates the evidence tables (need %d)",
			ErrTrustEvidenceUnavailable, v, trustEvidenceSchemaVersion)
	}
	return nil
}

// FileEvidence returns the persisted evidence row for one file under the given
// generation — a selective, generation-checked point lookup (never a scan). A
// row of any OTHER generation is never served: a mismatch (or an unknown path,
// or an empty generation) is ErrTrustEvidenceNotFound, and a sidecar without
// the evidence schema is ErrTrustEvidenceUnavailable. path must be the
// normalized repo-relative POSIX path (the SymbolLookupPort convention:
// normalization stays in the caller). Safe on read-only ingesters.
func (i *Ingester) FileEvidence(ctx context.Context, generation, path string) (FileEvidence, error) {
	if err := i.trustEvidenceReady(ctx); err != nil {
		return FileEvidence{}, err
	}
	if generation == "" {
		return FileEvidence{}, fmt.Errorf("%w: no generation to look up", ErrTrustEvidenceNotFound)
	}
	fe := FileEvidence{Generation: generation, Path: path}
	err := i.meta.QueryRowContext(ctx, `SELECT language, parse_status, parse_reason,
			resolved_derived, resolved_heuristic, resolved_external, skipped, ambiguous
		FROM trust_file_evidence WHERE generation_id = ? AND path = ?`, generation, path).
		Scan(&fe.Language, &fe.ParseStatus, &fe.ParseReason,
			&fe.ResolvedDerived, &fe.ResolvedHeuristic, &fe.ResolvedExternal, &fe.Skipped, &fe.Ambiguous)
	if errors.Is(err, sql.ErrNoRows) {
		return FileEvidence{}, fmt.Errorf("%w: file %q under generation %q", ErrTrustEvidenceNotFound, path, generation)
	}
	if err != nil {
		return FileEvidence{}, fmt.Errorf("ingest: read file evidence: %w", err)
	}
	return fe, nil
}

// PackageEvidence returns the persisted evidence row for one package (keyed by
// its repo-relative unit directory, "." for the root) under the given
// generation. Same fail-closed contract as FileEvidence: generation mismatch
// or unknown key is ErrTrustEvidenceNotFound; missing schema is
// ErrTrustEvidenceUnavailable. Safe on read-only ingesters.
func (i *Ingester) PackageEvidence(ctx context.Context, generation, pkgKey string) (PackageEvidence, error) {
	if err := i.trustEvidenceReady(ctx); err != nil {
		return PackageEvidence{}, err
	}
	if generation == "" {
		return PackageEvidence{}, fmt.Errorf("%w: no generation to look up", ErrTrustEvidenceNotFound)
	}
	pe := PackageEvidence{Generation: generation, PackageKey: pkgKey}
	err := i.meta.QueryRowContext(ctx, `SELECT state, degraded_reason,
			type_errors, dropped_intents, confirmed_edges, skipped_files
		FROM trust_package_evidence WHERE generation_id = ? AND package_key = ?`, generation, pkgKey).
		Scan(&pe.State, &pe.DegradedReason, &pe.TypeErrors, &pe.DroppedIntents, &pe.ConfirmedEdges, &pe.SkippedFiles)
	if errors.Is(err, sql.ErrNoRows) {
		return PackageEvidence{}, fmt.Errorf("%w: package %q under generation %q", ErrTrustEvidenceNotFound, pkgKey, generation)
	}
	if err != nil {
		return PackageEvidence{}, fmt.Errorf("ingest: read package evidence: %w", err)
	}
	return pe, nil
}

// ListFileEvidence returns up to limit file rows of the given generation in
// canonical path order — a BOUNDED sample for detail output (PRD §14.3 /
// bounded-detail discipline), never an unbounded dump: limit must be positive.
// A generation with no rows at all — including every generation mismatch — is
// ErrTrustEvidenceNotFound rather than an empty slice, so absence of evidence
// can never read as an empty-but-healthy tree. Safe on read-only ingesters.
func (i *Ingester) ListFileEvidence(ctx context.Context, generation string, limit int) ([]FileEvidence, error) {
	if err := i.trustEvidenceReady(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, fmt.Errorf("ingest: trust evidence list limit must be positive, got %d", limit)
	}
	if generation == "" {
		return nil, fmt.Errorf("%w: no generation to look up", ErrTrustEvidenceNotFound)
	}
	rows, err := i.meta.QueryContext(ctx, `SELECT path, language, parse_status, parse_reason,
			resolved_derived, resolved_heuristic, resolved_external, skipped, ambiguous
		FROM trust_file_evidence WHERE generation_id = ? ORDER BY path LIMIT ?`, generation, limit)
	if err != nil {
		return nil, fmt.Errorf("ingest: list file evidence: %w", err)
	}
	defer rows.Close()
	var out []FileEvidence
	for rows.Next() {
		fe := FileEvidence{Generation: generation}
		if err := rows.Scan(&fe.Path, &fe.Language, &fe.ParseStatus, &fe.ParseReason,
			&fe.ResolvedDerived, &fe.ResolvedHeuristic, &fe.ResolvedExternal, &fe.Skipped, &fe.Ambiguous); err != nil {
			return nil, fmt.Errorf("ingest: scan file evidence: %w", err)
		}
		out = append(out, fe)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: no file evidence under generation %q", ErrTrustEvidenceNotFound, generation)
	}
	return out, nil
}
