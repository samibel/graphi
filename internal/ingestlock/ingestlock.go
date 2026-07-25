// Package ingestlock names and probes the cross-process ingest lock that
// cmd/internal/runtime holds around every warm/full ingest pass. The lock is a
// dedicated SQLite database inside a repo's meta sidecar dir, held via BEGIN
// IMMEDIATE for the duration of a pass; it stores NO data — only SQLite's
// file-locking state — and is safe to delete while no graphi process runs.
//
// The package exists so diagnostics (internal/doctor, `graphi status`) can
// inspect the lock without importing the runtime composition root: Go's
// internal-visibility rules hide cmd/internal/runtime from internal/doctor,
// and duplicating the lock's filename or busy classification would let the
// two drift apart.
package ingestlock

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go, CGo-free SQLite driver for the lock probe
)

// FileName is the lock database's basename inside a repo's meta sidecar dir
// (single-sourced here; the runtime acquires it, diagnostics probe it).
const FileName = "ingest.lock.db"

// Path returns the lock database path for a meta sidecar dir.
func Path(metaDir string) string { return filepath.Join(metaDir, FileName) }

// IsBusy reports whether err is SQLite's held-by-another-connection signal
// (SQLITE_BUSY/SQLITE_LOCKED families) rather than a real failure.
func IsBusy(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "busy") || strings.Contains(msg, "locked")
}

// State classifies a Probe outcome.
type State string

const (
	// StateAbsent: the lock DB does not exist — no contention ever occurred on
	// this state dir (the runtime creates it on first acquisition).
	StateAbsent State = "absent"
	// StateFree: the lock DB exists and no process currently holds it.
	StateFree State = "free"
	// StateHeld: another process holds the lock right now — an ingest pass over
	// this repo's state is in flight.
	StateHeld State = "held"
)

// Probe non-destructively reports whether another process currently holds the
// ingest lock. It never creates the lock DB (mode=rw, no create), never waits
// (busy_timeout(0) fails fast instead of queueing), and on success rolls its
// RESERVED lock back immediately — a real acquirer's busy_timeout retry loop
// absorbs that microsecond window invisibly, so probing cannot disturb a
// holder or starve a waiter.
func Probe(ctx context.Context, metaDir string) (State, error) {
	path := Path(metaDir)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return StateAbsent, nil
		}
		return "", fmt.Errorf("stat ingest lock: %w", err)
	}
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?mode=rw&_pragma=busy_timeout(0)", filepath.ToSlash(path)))
	if err != nil {
		return "", fmt.Errorf("open ingest lock: %w", err)
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		// The pool surfaces a held lock at connection time on some paths
		// (rollback-journal hot states); classify rather than fail.
		if IsBusy(err) {
			return StateHeld, nil
		}
		return "", fmt.Errorf("open ingest lock connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if IsBusy(err) {
			return StateHeld, nil
		}
		return "", fmt.Errorf("probe ingest lock: %w", err)
	}
	_, _ = conn.ExecContext(ctx, "ROLLBACK")
	return StateFree, nil
}
