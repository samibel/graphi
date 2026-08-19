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
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/engine/typeresolve"
)

// trustEvidenceSchemaVersion is the sidecar schema version that introduced the
// evidence tables (schema.go ladder step 2 -> 3). A sidecar below it has no
// tables to read and MUST surface ErrTrustEvidenceUnavailable, never an empty
// (healthy-looking) result.
//
// packageEvidenceSchemaVersion is the version whose 3 -> 4 rebuild gave
// trust_package_evidence its language column. The PACKAGE read requires it (a
// v3 sidecar observed read-only has no such column and never migrates); the
// FILE read keeps the older floor — its table is unchanged, and refusing to
// serve intact rows would be a false unavailability.
// languageSkipsSchemaVersion is the version whose 4 -> 5 step added
// trust_language_skips. The PACKAGE read deliberately does NOT raise its floor
// to it: a v4 sidecar's package rows are intact and refusing them would be a
// false unavailability (the same reasoning that keeps the FILE read at 3).
// Instead the absence is made VISIBLE — PackageEvidence.SkipsAvailable reads
// false — so a consumer can never mistake "this sidecar cannot tell me" for
// "nothing was skipped".
const (
	trustEvidenceSchemaVersion   = 3
	packageEvidenceSchemaVersion = 4
	languageSkipsSchemaVersion   = 5
)

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
	Generation string
	// Language names the semantic registrant that produced the row (schema
	// 3 -> 4): with per-language resolvers one directory carries one row per
	// language, keyed (generation, language, package_key).
	Language       string
	PackageKey     string
	State          string
	DegradedReason string
	TypeErrors     int
	DroppedIntents int
	ConfirmedEdges int
	SkippedFiles   int

	// Languages names EVERY semantic registrant holding a row for this package
	// key under the generation, sorted. Language above collapses to "" once a
	// directory carries more than one language's row (the fold is an aggregate
	// and cannot honestly claim one), which loses the very fact a consumer of
	// repo-global per-language counters needs: WHICH registrants this package
	// is accounted by. Always populated — one entry for the single-language
	// case, so the identity fold gains a fact rather than changing one.
	Languages []string

	// NamedSkips is the LEGIBLE ABSTENTION record of the semantic registrant(s)
	// that produced this row: skip-reason name -> count (W0.g). It is joined
	// onto the row from trust_language_skips at read time, by language.
	//
	// READ THE SCOPE BEFORE USING IT. These counters are REPOSITORY-GLOBAL for
	// the language, not this package's: the binder tallies them per pass with
	// NO file, package, symbol or call-site attribution, and for two of the
	// JVM reasons (java_receiver_untyped, java_receiver_external) the callee is
	// undeterminable by definition, so no site exists to attribute them to.
	// A surface roll-up keyed on this row is a roll-up of a repo-global
	// number and must say so; it is NOT a per-symbol or per-package
	// accounting, and nothing here licenses the sentence "N sites in THIS
	// package were skipped".
	NamedSkips map[string]int
	// SkipsAvailable distinguishes "no named skip was recorded" (true, empty
	// NamedSkips) from "this sidecar cannot answer" (false) — a v4 sidecar
	// observed read-only has no trust_language_skips table and never migrates.
	// Fail closed: absence of the table is "no answer", never "no skips".
	SkipsAvailable bool
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
func packageEvidenceFromResult(lang string, res typeresolve.Result, dirOf map[model.NodeId]string) []PackageEvidence {
	byKey := map[string]*PackageEvidence{}
	for _, u := range res.Units {
		r := byKey[u.Dir]
		if r == nil {
			r = &PackageEvidence{Language: lang, PackageKey: u.Dir}
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
		if _, err := tx.ExecContext(ctx, "DELETE FROM trust_language_skips"); err != nil {
			return fmt.Errorf("ingest: clear language skips: %w", err)
		}
		if err := insertFileEvidenceTx(ctx, tx, generation, rows); err != nil {
			return err
		}
		if err := insertPackageEvidenceTx(ctx, tx, generation, i.combinedPackageEvidence()); err != nil {
			return err
		}
		return insertLanguageSkipsTx(ctx, tx, generation, i.combinedLanguageSkips())
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
		// The skip rows are replaced under exactly the same gate as the package
		// rows and in the same transaction: they describe the SAME whole-repo
		// recompute, so a state where one is this pass's and the other the
		// previous pass's must not be reachable.
		if _, err := tx.ExecContext(ctx, "DELETE FROM trust_language_skips"); err != nil {
			return fmt.Errorf("ingest: clear language skips: %w", err)
		}
		if err := insertPackageEvidenceTx(ctx, tx, generation, i.combinedPackageEvidence()); err != nil {
			return err
		}
		return insertLanguageSkipsTx(ctx, tx, generation, i.combinedLanguageSkips())
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
			(generation_id, language, package_key, state, degraded_reason,
			 type_errors, dropped_intents, confirmed_edges, skipped_files)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			generation, r.Language, r.PackageKey, r.State, r.DegradedReason,
			r.TypeErrors, r.DroppedIntents, r.ConfirmedEdges, r.SkippedFiles); err != nil {
			return fmt.Errorf("ingest: persist package evidence for %s: %w", r.PackageKey, err)
		}
	}
	return nil
}

// insertLanguageSkipsTx writes the pass's named abstention counters, one row
// per (generation, language, skip name), in deterministic language-then-name
// order. A zero count is never written: the row set is the list of reasons
// the pass ACTUALLY abstained under, so an absent name means "not observed",
// which is exactly what a reader should conclude.
func insertLanguageSkipsTx(ctx context.Context, tx *sql.Tx, generation string, skips map[string]map[string]int) error {
	langs := make([]string, 0, len(skips))
	for l := range skips {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		names := make([]string, 0, len(skips[lang]))
		for n := range skips[lang] {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			count := skips[lang][name]
			if count <= 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO trust_language_skips
				(generation_id, language, skip_name, count) VALUES (?, ?, ?, ?)`,
				generation, lang, name, count); err != nil {
				return fmt.Errorf("ingest: persist language skip %s/%s: %w", lang, name, err)
			}
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
	return i.trustEvidenceReadyAt(ctx, trustEvidenceSchemaVersion)
}

func (i *Ingester) trustEvidenceReadyAt(ctx context.Context, minVersion int) error {
	var v int
	if err := i.meta.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("ingest: trust evidence schema probe: %w", err)
	}
	if v < minVersion {
		return fmt.Errorf("%w: sidecar schema version %d predates the evidence tables (need %d)",
			ErrTrustEvidenceUnavailable, v, minVersion)
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

// PackageEvidence returns the persisted evidence for one package (keyed by
// its repo-relative unit directory, "." for the root) under the given
// generation, FOLDED across languages: since schema v4 a directory can carry
// one row per semantic registrant, and the caller's scope key is
// language-agnostic, so counters sum and a degradation in ANY language
// degrades the fold (reasons joined in language order, deterministic). A
// single-language directory folds to exactly its one row — the identity, so
// pre-JVM behavior is byte-unchanged. Same fail-closed contract as
// FileEvidence; missing schema (incl. a v3 sidecar observed read-only, which
// lacks the language column) is ErrTrustEvidenceUnavailable. Safe on
// read-only ingesters.
func (i *Ingester) PackageEvidence(ctx context.Context, generation, pkgKey string) (PackageEvidence, error) {
	if err := i.trustEvidenceReadyAt(ctx, packageEvidenceSchemaVersion); err != nil {
		return PackageEvidence{}, err
	}
	if generation == "" {
		return PackageEvidence{}, fmt.Errorf("%w: no generation to look up", ErrTrustEvidenceNotFound)
	}
	rows, err := i.meta.QueryContext(ctx, `SELECT language, state, degraded_reason,
			type_errors, dropped_intents, confirmed_edges, skipped_files
		FROM trust_package_evidence WHERE generation_id = ? AND package_key = ?
		ORDER BY language`, generation, pkgKey)
	if err != nil {
		return PackageEvidence{}, fmt.Errorf("ingest: read package evidence: %w", err)
	}
	defer rows.Close()

	pe := PackageEvidence{Generation: generation, PackageKey: pkgKey}
	n := 0
	var reasons []string
	var langs []string
	for rows.Next() {
		var r PackageEvidence
		if err := rows.Scan(&r.Language, &r.State, &r.DegradedReason,
			&r.TypeErrors, &r.DroppedIntents, &r.ConfirmedEdges, &r.SkippedFiles); err != nil {
			return PackageEvidence{}, fmt.Errorf("ingest: read package evidence: %w", err)
		}
		langs = append(langs, r.Language)
		n++
		if n == 1 {
			r.Generation, r.PackageKey = generation, pkgKey
			pe = r
			if r.DegradedReason != "" {
				reasons = append(reasons, r.DegradedReason)
			}
			continue
		}
		if n == 2 {
			// The fold leaves single-language reads untouched; from the
			// second row on it is a cross-language aggregate.
			pe.Language = ""
		}
		pe.TypeErrors += r.TypeErrors
		pe.DroppedIntents += r.DroppedIntents
		pe.ConfirmedEdges += r.ConfirmedEdges
		pe.SkippedFiles += r.SkippedFiles
		if r.DegradedReason != "" {
			reasons = append(reasons, r.DegradedReason)
		}
		if r.State == PackageStateDegraded ||
			(r.State == PackageStateCheckedWithErrors && pe.State == PackageStateChecked) {
			pe.State = r.State
		}
	}
	if err := rows.Err(); err != nil {
		return PackageEvidence{}, fmt.Errorf("ingest: read package evidence: %w", err)
	}
	if n == 0 {
		return PackageEvidence{}, fmt.Errorf("%w: package %q under generation %q", ErrTrustEvidenceNotFound, pkgKey, generation)
	}
	if n > 1 {
		pe.DegradedReason = strings.Join(reasons, "; ")
	}
	// Join the row's languages' REPO-GLOBAL named skip counters (W0.g). The
	// union across languages is sound because the vocabularies are
	// language-prefixed and therefore disjoint; the numbers stay repo-global
	// either way, which is what PackageEvidence.NamedSkips' doc forbids
	// readers from forgetting.
	sort.Strings(langs)
	pe.Languages = langs
	skips, err := i.languageSkipsFor(ctx, generation, langs)
	if err != nil && !errors.Is(err, ErrTrustEvidenceUnavailable) {
		return PackageEvidence{}, err
	}
	if err == nil {
		pe.SkipsAvailable = true
		pe.NamedSkips = skips
	}
	return pe, nil
}

// languageSkipsFor unions the named skip counters of the given languages under
// one generation. A sidecar predating trust_language_skips is
// ErrTrustEvidenceUnavailable — never an empty map, which would read as "no
// skips" (fail closed).
func (i *Ingester) languageSkipsFor(ctx context.Context, generation string, langs []string) (map[string]int, error) {
	if err := i.trustEvidenceReadyAt(ctx, languageSkipsSchemaVersion); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, lang := range langs {
		rows, err := i.meta.QueryContext(ctx,
			`SELECT skip_name, count FROM trust_language_skips
			 WHERE generation_id = ? AND language = ? ORDER BY skip_name`, generation, lang)
		if err != nil {
			return nil, fmt.Errorf("ingest: read language skips: %w", err)
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var name string
				var count int
				if err := rows.Scan(&name, &count); err != nil {
					return fmt.Errorf("ingest: scan language skips: %w", err)
				}
				out[name] += count
			}
			return rows.Err()
		}()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// LanguageSkips returns the whole generation's named abstention counters,
// keyed language -> skip name -> count: the repository-global record of every
// site a semantic registrant refused to bind under a NAMED reason rather than
// guessing (W0.g).
//
// The scope is the same one PackageEvidence.NamedSkips carries and the same
// one every surface must restate: repository-global per language, with NO
// file, package, symbol or call-site attribution.
//
// Fail-closed contract, and the distinction matters more here than anywhere
// else: a sidecar predating the skip table is ErrTrustEvidenceUnavailable
// ("cannot answer"), while an EMPTY map under a real generation means "no
// named skip was recorded by any registrant this pass" — which is a fact, not
// a claim that nothing was skipped by some other mechanism. An empty
// generation string is ErrTrustEvidenceNotFound. Safe on read-only ingesters.
func (i *Ingester) LanguageSkips(ctx context.Context, generation string) (map[string]map[string]int, error) {
	if err := i.trustEvidenceReadyAt(ctx, languageSkipsSchemaVersion); err != nil {
		return nil, err
	}
	if generation == "" {
		return nil, fmt.Errorf("%w: no generation to look up", ErrTrustEvidenceNotFound)
	}
	rows, err := i.meta.QueryContext(ctx,
		`SELECT language, skip_name, count FROM trust_language_skips
		 WHERE generation_id = ? ORDER BY language, skip_name`, generation)
	if err != nil {
		return nil, fmt.Errorf("ingest: list language skips: %w", err)
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var lang, name string
		var count int
		if err := rows.Scan(&lang, &name, &count); err != nil {
			return nil, fmt.Errorf("ingest: scan language skips: %w", err)
		}
		if out[lang] == nil {
			out[lang] = map[string]int{}
		}
		out[lang][name] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
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
