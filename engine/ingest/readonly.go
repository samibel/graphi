package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/link"
)

// ErrReadOnly is returned by every mutating Ingester entry point on an
// Ingester built with NewReadOnly.
var ErrReadOnly = errors.New("ingest: read-only ingester")

// NewReadOnly constructs an observer over an EXISTING meta sidecar without
// modifying its content: the sidecar is opened mode=ro plus query_only, no
// schema init or migration runs, and file modes are left untouched. It backs
// strictly-observational surfaces (`graphi status`) that need CanWarmStart and
// drift detection over a store someone else maintains. A missing sidecar is an
// error — a read-only observer must never create one. As with
// graphstore.OpenSQLiteReadOnly, reading a WAL-mode database may create the
// transient -wal/-shm coordination sidecars; the database content is never
// touched.
func NewReadOnly(store graphstore.Graphstore, parser Parser, metaDir string) (*Ingester, error) {
	if strings.TrimSpace(metaDir) == "" {
		return nil, errors.New("ingest: read-only requires an existing meta dir")
	}
	dbPath := filepath.Join(metaDir, "ingest-meta.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("ingest: open read-only sidecar: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("ingest: open read-only sidecar: %w", err)
	}
	i := &Ingester{store: store, parser: parser, meta: db, linker: link.New(), bounds: parse.DefaultResourceBounds(), clock: realClock{}, heartbeatMode: HeartbeatNonTTY, heartbeatInterval: heartbeatModeInterval(HeartbeatNonTTY), lastProgressTime: time.Now(), readOnly: true}
	// Probe with a harmless query so a corrupt/non-SQLite sidecar fails here,
	// not on the first caller read.
	var one int
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&one); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ingest: read-only probe: %w", err)
	}
	return i, nil
}

// guardReadOnly is the shared top-of-function check for mutating entry points.
func (i *Ingester) guardReadOnly() error {
	if i.readOnly {
		return ErrReadOnly
	}
	return nil
}
