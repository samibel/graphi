package doctor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/samibel/graphi/internal/ingestlock"
)

// fullPassInProgressKey mirrors the recovery-marker key the ingester writes
// into its meta sidecar (engine/ingest/warmstart.go). Doctor stays decoupled
// from the ingest engine and reads the sidecar directly, so the literal is
// duplicated here on purpose.
const fullPassInProgressKey = "full_pass_in_progress"

// ingestMetaFileName is the ingester's durable sidecar database inside the
// meta dir (engine/ingest/ingester.go).
const ingestMetaFileName = "ingest-meta.db"

// IndexCheck inspects a repo's per-repo index state: the full-pass recovery
// marker in the meta sidecar (read-only) and the cross-process ingest lock
// (non-destructive probe). Together they distinguish "another process is
// indexing right now — wait" from "a previous index crashed mid-pass —
// rebuild", the two states every other check (and the old advice to just run
// `graphi index`) conflated. repoRoot/metaDir come from the caller's repo
// detection; empty values mean "not inside a repository".
func IndexCheck(repoRoot, metaDir string) Check {
	return checkFunc{
		id:       "index",
		category: "index",
		fn: func(ctx context.Context, _ Env) CheckResult {
			if metaDir == "" {
				return StringResult("index", "index", "not inside a repository; no index state to inspect", StatusInfo)
			}
			marker, found, err := readFullPassMarker(ctx, metaDir)
			if err != nil {
				return ResultWithAction("index", "index", fmt.Sprintf("cannot inspect index state: %v", err), StatusUnverified, "re-run `graphi doctor`")
			}
			lock, err := ingestlock.Probe(ctx, metaDir)
			if err != nil {
				return ResultWithAction("index", "index", fmt.Sprintf("cannot inspect index state: %v", err), StatusUnverified, "re-run `graphi doctor`")
			}
			held := lock == ingestlock.StateHeld
			if !found && !held {
				return ResultWithAction("index", "index", fmt.Sprintf("no index state yet for %s", repoRoot), StatusInfo, "run `graphi index` to build it")
			}
			switch {
			case marker && held:
				return ResultWithAction("index", "index",
					fmt.Sprintf("a full index of %s is running in another graphi process (in-flight marker present, ingest lock held)", repoRoot),
					StatusInfo,
					"wait for it to finish; do not run `graphi index` concurrently — it would queue behind the same lock")
			case marker:
				return ResultWithAction("index", "index",
					fmt.Sprintf("a previous full index of %s did not complete (stale in-flight marker); the next session will re-run the full pass from scratch", repoRoot),
					StatusWarn,
					"run `graphi index` in a terminal to rebuild now with visible progress")
			case held:
				return ResultWithAction("index", "index",
					fmt.Sprintf("another graphi process holds the ingest lock for %s (sync or index in progress)", repoRoot),
					StatusInfo,
					"wait for it to finish")
			default:
				return StringResult("index", "index", "index state is consistent; no full pass in flight", StatusPass)
			}
		},
	}
}

// readFullPassMarker reads the recovery marker from the meta sidecar without
// creating or writing anything. found=false covers both "no sidecar yet" and
// "sidecar predates the marker table"; marker reports whether an unfinished
// full pass left its in-progress key behind.
func readFullPassMarker(ctx context.Context, metaDir string) (marker, found bool, err error) {
	path := filepath.Join(metaDir, ingestMetaFileName)
	if _, statErr := os.Stat(path); statErr != nil {
		if os.IsNotExist(statErr) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("stat meta sidecar: %w", statErr)
	}
	// Same read-only discipline as DBCheck: mode=ro never creates a missing
	// file, query_only(1) refuses writes on every pooled connection.
	dsn := fmt.Sprintf("file:%s?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(2000)", filepath.ToSlash(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return false, false, fmt.Errorf("open meta sidecar: %w", err)
	}
	defer db.Close()
	var value string
	err = db.QueryRowContext(ctx, "SELECT value FROM ingest_semantics WHERE key = ?", fullPassInProgressKey).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, true, nil
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return false, true, nil // pre-marker sidecar: state exists, no pass open
	case err != nil:
		return false, false, fmt.Errorf("read full-pass marker: %w", err)
	}
	return true, true, nil
}
