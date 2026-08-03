// Package freshness carries the shared freshness-fact vocabulary for a
// repository's auto-managed graph state — the Report every surface (`graphi
// status`, the trust surface) consumes — plus the sync-stamp keys and their
// read-back. There is exactly one drift semantics because there is exactly one
// Report shape; rendering (JSON tags, human text) and exit-code mapping belong
// to the consumers.
//
// The probe that FILLS a Report (Compute) lives in the probe subpackage: it
// needs engine/ingest's read-only observer, while this package must stay
// importable from engine rank — engine/trust derives snapshot state from a
// Report and engine/ingest persists trust snapshots, so a probe here would
// cycle ingest → trust → freshness → ingest.
package freshness

import (
	"time"

	"github.com/samibel/graphi/internal/gitinfo"
)

// Report is the semantic content of one freshness probe.
type Report struct {
	Repo           string
	GitPresent     bool
	Git            gitinfo.Info
	DBPath         string
	NodeCount      int
	Profile        string
	LastSync       SyncStamp
	Index          IndexState
	Drift          Drift
	Current        bool
	Recommendation string
}

// SyncStamp is the last successful sync stamp read back from the store.
// Recorded=false means the store was never stamped.
type SyncStamp struct {
	Recorded bool
	Time     time.Time
	Branch   string
	Commit   string
}

// IndexState describes the durable index. FullPassInProgress reports the
// sidecar's full-pass recovery marker; LockHeld reports whether another
// process currently holds the cross-process ingest lock. Together they split
// "index running right now" from "previous index crashed".
type IndexState struct {
	Exists             bool
	WarmStartable      bool
	FilesCached        int
	FullPassInProgress bool
	LockHeld           bool
}

// Drift counts source files that changed since the last sync.
type Drift struct {
	Added   int
	Changed int
	Removed int
}
