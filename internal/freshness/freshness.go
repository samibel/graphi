// Package freshness reports read-only freshness facts for a repository's
// auto-managed graph state: does a graph exist, is it warm-startable, what
// drift has accumulated since the last sync, and what should the caller do
// next. It is strictly observational — nothing is created, nothing is
// ingested — so any surface (`graphi status`, the trust surface) can consume
// the same facts without side effects and without a second drift
// implementation. The package lives under internal/ outside the
// cmd→surfaces→engine→core rank graph and must not import engine/query,
// surfaces/, or cmd/, so engine-rank consumers can import it.
package freshness

import (
	"context"
	"fmt"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/gitinfo"
	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/state"
)

// Report is the semantic content of one freshness probe. Rendering (JSON
// tags, human text) and exit-code mapping belong to the consumer.
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

// Compute probes the graph state for root. When dbPath or metaDir is empty
// the auto-managed locations are resolved WITHOUT Ensure: a pure observer
// must not create state directories for repos that were never indexed. A
// non-nil error is an operational failure with nothing to render (`graphi
// status` maps it to exit 2); "not indexed yet" is a nil-error Report with a
// recommendation instead.
func Compute(ctx context.Context, root, dbPath, metaDir string) (Report, error) {
	if dbPath == "" || metaDir == "" {
		p, err := state.Resolve(root)
		if err != nil {
			return Report{}, err
		}
		if dbPath == "" {
			dbPath = p.DB
		}
		if metaDir == "" {
			metaDir = p.Meta
		}
	}

	info, gitOK := gitinfo.Head(root)
	r := Report{Repo: root, GitPresent: gitOK, Git: info, DBPath: dbPath}

	store, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		// No durable store yet — the one non-error "not indexed" outcome.
		r.Recommendation = "run 'graphi sync' to build the graph"
		return r, nil
	}
	defer store.Close()
	r.Index.Exists = true

	if n, cerr := store.CountNodes(ctx); cerr == nil {
		r.NodeCount = n
	}
	if prof, perr := store.Metadata(ctx, "index.profile"); perr == nil {
		r.Profile = prof
	}
	if ts, branch, commit, ok := LastSync(ctx, store); ok {
		r.LastSync = SyncStamp{Recorded: true, Time: ts, Branch: branch, Commit: commit}
	}

	ro, err := ingest.NewReadOnly(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		r.Recommendation = "run 'graphi sync' to build the graph"
		return r, nil
	}
	defer ro.Close()

	files, warmOK, werr := ro.CanWarmStart(ctx, root)
	r.Index.FilesCached = files
	if werr != nil || !warmOK {
		// Incomplete pass, older-binary semantics, or a generation mismatch: the
		// store needs a full pass, and drift over an untrusted cache is noise.
		// The marker + lock probe split the actionable sub-states; both degrade
		// silently so the probe keeps working on a partially readable state dir.
		if marker, merr := ro.FullPassInProgress(ctx); merr == nil {
			r.Index.FullPassInProgress = marker
		}
		if lock, lerr := ingestlock.Probe(ctx, metaDir); lerr == nil {
			r.Index.LockHeld = lock == ingestlock.StateHeld
		}
		switch {
		case r.Index.FullPassInProgress && r.Index.LockHeld:
			r.Recommendation = "wait for the running index to finish — another graphi process is building it"
		case r.Index.FullPassInProgress:
			r.Recommendation = "run 'graphi index' to rebuild now with visible progress (the previous index did not complete)"
		default:
			r.Recommendation = "run 'graphi rebuild' to re-certify the graph"
		}
		return r, nil
	}
	r.Index.WarmStartable = true

	drift, derr := ro.DriftDetail(ctx, root, nil)
	if derr != nil {
		return r, fmt.Errorf("drift check: %w", derr)
	}
	r.Drift = Drift{Added: len(drift.Added), Changed: len(drift.Modified), Removed: len(drift.Deleted)}
	if drift.Total() == 0 {
		r.Current = true
	} else {
		r.Recommendation = "run 'graphi sync' to update the graph"
	}
	return r, nil
}
