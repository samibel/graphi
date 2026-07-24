// Package runtime is the RUN-01 composition root (ADR 0002 D0): store, meta
// sidecar, ingester, session identity and the surface client are owned exactly
// ONCE, by a Runtime, with a single idempotent Close. Surfaces stay thin
// transports; cmd/graphi decodes arguments and asks this package for a bound
// session instead of wiring stores by hand.
//
// Two entry points, matching the two contracts ADR 0002 distinguishes:
//
//   - Attach: the pre-RUN-01 behavior, preserved bit-for-bit (SW-110 pins it):
//     an explicit -db opens exactly that store, a live daemon socket dials it,
//     an empty path yields an in-memory store. NO discovery, NO ingest.
//   - OpenSession: the zero-config session (ADR 0002 D1–D5): resolve the repo
//     root (transport roots → cwd fallback), derive the per-repo state paths,
//     open → RECOVER → warm/full ingest → ready, then hand out the client. One
//     Runtime binds one repository; an MCP server may replace that Runtime when
//     the client announces a roots-list change.
package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
)

// Options configures OpenSession.
type Options struct {
	// Cwd is the fallback directory for repository detection when Roots is nil
	// (ADR 0002 D4: explicit DB/socket → client roots → cwd walk).
	Cwd string
	// Roots is the authoritative ordered set of repository candidates supplied
	// by a session-aware transport (for example MCP rootUri or roots/list).
	// A nil slice means "no transport roots were supplied" and permits the Cwd
	// fallback. A non-nil slice, including an empty one, is authoritative: Cwd
	// must not leak into a client-scoped session when none of its roots bind.
	Roots []string
	// DBOverride, when non-empty, short-circuits to Attach semantics: exactly
	// this store, no discovery, no ingest (D2 precedence, zero regression).
	DBOverride string
	// Socket, when non-empty, short-circuits to a daemon client.
	Socket string
	// Progress, when non-nil, receives ingest progress events.
	Progress func(ingest.ProgressEvent)
}

// ErrNoRepository is returned when a zero-config session cannot bind a real
// repository. Serving an empty in-memory graph in that situation makes valid
// requests look successful while answering over the wrong state, so callers
// must surface this error or wait for a transport-provided root.
var ErrNoRepository = errors.New("no repository could be bound")

// Runtime owns one session's resources. Close is idempotent and releases them
// exactly once, in reverse construction order; Done is closed when the Runtime
// is closed (the daemon wait seam).
type Runtime struct {
	// Client is the surface client bound to this session.
	Client client.Client
	// Root is the bound repository root; empty only in Attach mode, where a
	// caller selected a store/socket rather than a repository.
	Root string

	store  graphstore.Graphstore
	ing    *ingest.Ingester
	broker *observe.Broker

	closeOnce sync.Once
	closers   []func()
	done      chan struct{}
}

// Store exposes the session store (read-only wiring like the zeroconfig wiki).
func (r *Runtime) Store() graphstore.Graphstore { return r.store }

// Broker exposes the session's observe broker (nil in Attach mode).
func (r *Runtime) Broker() *observe.Broker { return r.broker }

// Done is closed when the Runtime has been closed.
func (r *Runtime) Done() <-chan struct{} { return r.done }

// Close releases every owned resource exactly once, reverse of construction.
func (r *Runtime) Close() {
	r.closeOnce.Do(func() {
		for i := len(r.closers) - 1; i >= 0; i-- {
			r.closers[i]()
		}
		close(r.done)
	})
}

func newRuntime() *Runtime { return &Runtime{done: make(chan struct{})} }

// Attach builds a client with the pre-RUN-01 semantics, owned by a Runtime:
// daemon socket → remote client; else the given (or in-memory) store with the
// analysis + review + (embedder-aware) search wiring every CLI verb used via
// makeClientOrOpenMeta. It never discovers and never ingests.
func Attach(dbPath, socket, metaDir string) (*Runtime, error) {
	rt := newRuntime()
	if socket != "" {
		rt.Client = daemon.NewClient(socket, "")
		return rt, nil
	}
	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	rt.store = store
	rt.closers = append(rt.closers, func() { _ = store.Close() })
	asvc := analysis.NewDefaultService(store)
	rt.Client = client.NewDirect(query.New(store), NewSearchService(store, metaDir)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc))
	return rt, nil
}

// OpenSession opens the ADR 0002 session. Precedence (D4): an explicit
// DBOverride/Socket behaves exactly like Attach (zero regression for callers
// that pre-index and pass -db); otherwise transport roots are tried in order,
// and only when Roots is nil does the cwd walk decide. A session that cannot
// bind a repository fails closed with ErrNoRepository; it never masquerades as
// a successful empty graph.
func OpenSession(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.DBOverride != "" || opts.Socket != "" {
		return Attach(opts.DBOverride, opts.Socket, "")
	}
	root, err := resolveRepositoryRoot(opts)
	if err != nil {
		return nil, err
	}

	p, err := state.Resolve(root)
	if err != nil {
		return nil, fmt.Errorf("resolve session paths: %w", err)
	}
	if err := state.Ensure(p); err != nil {
		return nil, fmt.Errorf("ensure session state: %w", err)
	}

	// The ingest lock is taken BEFORE the store/sidecar even open: on a fresh
	// state dir, concurrent schema creation races SQLite's deadlock avoidance
	// (an in-transaction lock upgrade returns SQLITE_BUSY without consulting
	// busy_timeout), so serializing only the ingest would still let a second
	// session's open fail spuriously. Under the lock the whole open → recover
	// → ingest sequence is single-flight per repo state; the waiter then opens
	// an already-initialized store and warm-starts over the certified graph.
	release, err := acquireIngestLock(ctx, p.Meta)
	if err != nil {
		return nil, fmt.Errorf("acquire ingest lock: %w", err)
	}
	defer release()

	rt := newRuntime()
	rt.Root = root
	store, err := graphstore.OpenSQLite(p.DB)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}
	rt.store = store
	rt.closers = append(rt.closers, func() { _ = store.Close() })

	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), p.Meta)
	if err != nil {
		rt.Close()
		return nil, fmt.Errorf("open session ingester: %w", err)
	}
	rt.ing = ing
	rt.closers = append(rt.closers, func() { _ = ing.Close() })
	rt.broker = observe.New()
	ing.WithBroker(rt.broker)
	if opts.Progress != nil {
		ing.WithProgress(opts.Progress)
	}

	// D3 (sync-before-serve, the U1 default): the session is READY — recovered
	// and ingested — before OpenSession returns, so a successful construction
	// already means every stable operation answers over the real graph. The
	// sync additionally stamps the sync metadata, so `graphi status` sees
	// MCP-driven syncs too. The unlocked variant is used because this session
	// already holds the ingest lock (taken above, around store construction).
	if _, err := syncRepoLocked(ctx, ing, store, root, opts.Progress); err != nil {
		rt.Close()
		return nil, fmt.Errorf("session ingest: %w", err)
	}

	asvc := analysis.NewDefaultService(store)
	rt.Client = client.NewDirect(query.New(store), NewSearchService(store, p.Meta)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc))
	return rt, nil
}

// resolveRepositoryRoot enforces session scoping. MCP roots are authoritative
// when present: choosing the process cwd after a client explicitly supplied an
// empty or unrelated root set would cross workspace boundaries. With no
// transport roots (nil), legacy/non-roots-capable clients retain the cwd walk.
func resolveRepositoryRoot(opts Options) (string, error) {
	if opts.Roots != nil {
		for _, candidate := range opts.Roots {
			if root, ok := state.DetectRepo(candidate); ok {
				return root, nil
			}
		}
		return "", fmt.Errorf("%w: none of %d client root(s) contains .git, go.work, or go.mod", ErrNoRepository, len(opts.Roots))
	}
	if root, ok := state.DetectRepo(opts.Cwd); ok {
		return root, nil
	}
	return "", fmt.Errorf("%w: cwd %q contains no .git, go.work, or go.mod", ErrNoRepository, opts.Cwd)
}

// OpenStore opens the durable SQLite store at dbPath, or an in-memory store
// when dbPath is empty (the historical CLI fallback).
func OpenStore(dbPath string) (graphstore.Graphstore, error) {
	if dbPath == "" {
		return graphstore.NewMemStore(), nil
	}
	return graphstore.OpenSQLite(dbPath)
}

// NewSearchService builds the shared search service (moved verbatim from
// cmd/graphi's newSearchService — one implementation, owned here). Lexical
// search is always available. Semantic search is OPTIONAL and OFF by default:
// it is enabled ONLY when GRAPHI_EMBEDDER explicitly selects a (recognized)
// embedder. An empty/unknown selector leaves the graceful-skip state (no
// embedder, no network). With a metaDir, durable vectors are reloaded (a pure
// local read) so `search -semantic` answers without re-embedding (SW-061).
func NewSearchService(store graphstore.Graphstore, metaDir string) *search.Service {
	svc := search.New(store)
	emb, err := embed.Constructor(os.Getenv(embed.EnvSelector), embed.DefaultConstructors())
	if err != nil {
		// Fail-closed (e.g. a non-loopback Ollama host): report and keep semantic
		// search OFF rather than constructing an unsafe embedder.
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return svc
	}
	if emb == nil {
		return svc // graceful skip: nothing configured
	}
	reg := embed.NewRegistry()
	reg.Register(emb)
	index := embed.NewIndex()
	if metaDir != "" {
		table, terr := embed.OpenSQLiteVectorTable(context.Background(), metaDir, emb.ID(), emb.Dim())
		if terr != nil {
			fmt.Fprintf(os.Stderr, "graphi: vectors reload disabled: %v\n", terr)
		} else {
			if rerr := index.Rebuild(context.Background(), table); rerr != nil {
				fmt.Fprintf(os.Stderr, "graphi: vectors reload failed: %v\n", rerr)
			}
			_ = table.Close()
		}
	}
	return svc.WithSemantic(reg, index, store)
}

// SyncStats describes what a warm-or-full ingest actually did, for the
// user-facing summary lines of `graphi sync` and the branch banners.
type SyncStats struct {
	// Full is true when the pass took (or fell back to) the full re-index; the
	// per-class counts below are then zero — a full pass has no delta to split.
	Full bool
	// Checked is the number of files hash-walked on the warm path.
	Checked int
	// Added/Changed/Removed split the warm-path delta by drift class.
	Added, Changed, Removed int
}

// WarmOrFullIngest brings the per-repo state up to date the cheap way when it
// can: a store already filled under the CURRENT ingest semantics is only
// drift-checked (hash walk), and just the changed/deleted files — plus their
// cascade — are re-ingested through the incremental path, whose result is
// byte-identical to a full pass by the SW-101 invariant. An empty drift means
// no ingest at all: bare `graphi` on an unchanged repo starts in seconds
// instead of re-parsing everything. Any warm-path failure (probe, drift walk,
// incremental error such as a file that no longer parses) falls back to the
// tolerant full pass — the warm start is an optimization, never a new failure
// mode. Cold stores and stores stamped by an older binary take the full pass,
// which re-certifies them.
func WarmOrFullIngest(ctx context.Context, ing *ingest.Ingester, root string, progress func(ingest.ProgressEvent)) error {
	_, err := warmOrFullIngestStats(ctx, ing, root, progress)
	return err
}

// warmOrFullIngestStats is WarmOrFullIngest returning what the pass did.
func warmOrFullIngestStats(ctx context.Context, ing *ingest.Ingester, root string, progress func(ingest.ProgressEvent)) (SyncStats, error) {
	emit := func(ev ingest.ProgressEvent) {
		if progress != nil {
			progress(ev)
		}
	}
	// ING-DEC (SW-118): replay any dirty units left by an interrupted
	// incremental pass BEFORE trusting the store. The dirty rows are durable by
	// design (phase 1 of ingestChanged commits them first), but nothing replayed
	// them at session open until now — a crashed incremental would otherwise
	// serve a divergent graph through a warm start. A recovery failure falls
	// through to the tolerant full pass below, which re-certifies from scratch.
	if err := ing.RecoverWithRoot(ctx, root); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: crash recovery failed (%v) — re-indexing from scratch\n", err)
		return SyncStats{Full: true}, ing.IngestAll(ctx, root)
	}
	if _, ok, err := ing.CanWarmStart(ctx, root); err == nil && ok {
		emit(ingest.ProgressEvent{Phase: ingest.PhaseDrift})
		var totalChecked int
		drift, derr := ing.DriftDetail(ctx, root, func(checked int) {
			totalChecked = checked
			if checked%64 == 0 {
				emit(ingest.ProgressEvent{Phase: ingest.PhaseDrift, Done: checked})
			}
		})
		if derr == nil {
			emit(ingest.ProgressEvent{Phase: ingest.PhaseDrift, Done: totalChecked})
			stats := SyncStats{Checked: totalChecked, Added: len(drift.Added), Changed: len(drift.Modified), Removed: len(drift.Deleted)}
			if drift.Total() == 0 {
				return stats, nil // up to date — the summary comes from the renderer
			}
			delta := append(append(append([]string{}, drift.Added...), drift.Modified...), drift.Deleted...)
			uerr := ing.IngestChangedWithProgress(ctx, root, delta, progress)
			if uerr == nil {
				return stats, nil
			}
			fmt.Fprintf(os.Stderr, "graphi: warm start failed (%v) — re-indexing from scratch\n", uerr)
		}
	}
	return SyncStats{Full: true}, ing.IngestAll(ctx, root)
}

// SyncRepo is the canonical "bring the graph up to date" pass shared by
// `graphi sync`, `graphi index`, bare `graphi`, and MCP session open: crash
// recovery → warm-or-full ingest → sync-metadata stamp. The stamp is written
// only after a successful ingest, so LastSync never reports a time whose
// graph didn't actually commit. The whole pass runs under the cross-process
// ingest lock: concurrently opened sessions on the same logical store (e.g.
// several MCP clients auto-starting `graphi mcp` for one workspace) wait for
// the first pass instead of each launching their own full index, and the
// waiter then warm-starts over the store the winner just certified.
func SyncRepo(ctx context.Context, ing *ingest.Ingester, store graphstore.Graphstore, root string, progress func(ingest.ProgressEvent)) (SyncStats, error) {
	release, err := acquireIngestLock(ctx, ing.MetaDir())
	if err != nil {
		return SyncStats{}, fmt.Errorf("acquire ingest lock: %w", err)
	}
	defer release()
	return syncRepoLocked(ctx, ing, store, root, progress)
}

// syncRepoLocked is SyncRepo's body without lock acquisition, for callers
// that already hold the ingest lock (OpenSession takes it around store
// construction; taking it twice from one process would self-deadlock).
func syncRepoLocked(ctx context.Context, ing *ingest.Ingester, store graphstore.Graphstore, root string, progress func(ingest.ProgressEvent)) (SyncStats, error) {
	stats, err := warmOrFullIngestStats(ctx, ing, root, progress)
	if err != nil {
		return stats, err
	}
	if err := StampSyncMetadata(ctx, store, root, time.Now()); err != nil {
		return stats, err
	}
	return stats, nil
}

// RebuildRepo is the canonical full re-index pass behind `graphi rebuild` and
// `graphi index --full`: an unconditional cold IngestAll plus the sync stamp,
// serialized under the same cross-process ingest lock as SyncRepo.
func RebuildRepo(ctx context.Context, ing *ingest.Ingester, store graphstore.Graphstore, root string) error {
	release, err := acquireIngestLock(ctx, ing.MetaDir())
	if err != nil {
		return fmt.Errorf("acquire ingest lock: %w", err)
	}
	defer release()
	if err := ing.IngestAll(ctx, root); err != nil {
		return err
	}
	return StampSyncMetadata(ctx, store, root, time.Now())
}

// acquireIngestLock serializes warm/full ingest passes over one logical store
// ACROSS PROCESSES. The lock is a dedicated SQLite database next to the
// ingester's durable sidecar, held via BEGIN IMMEDIATE for the duration of the
// pass: SQLite's file locking is portable across every release platform and
// needs no new dependency. Without it, N auto-started sessions (MCP clients,
// shells) racing on a cold or just-updated store each run their own full
// index of the same workspace simultaneously — N times the parse cost and
// peak memory. With it, one process indexes while the rest wait, then
// warm-start over the certified result. An empty metaDir (in-memory sidecar,
// tests) has no on-disk identity to contend on and takes no lock.
func acquireIngestLock(ctx context.Context, metaDir string) (release func(), err error) {
	if metaDir == "" {
		return func() {}, nil
	}
	// busy_timeout makes each acquisition attempt block INSIDE SQLite for up
	// to 5s; the loop below re-checks ctx between attempts, so a waiter can
	// still be cancelled while the winner's full index runs for minutes.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", filepath.ToSlash(filepath.Join(metaDir, "ingest.lock.db")))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ingest lock: %w", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open ingest lock connection: %w", err)
	}
	waiting := false
	for {
		_, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		if err == nil {
			break
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			_ = conn.Close()
			_ = db.Close()
			return nil, ctxErr
		}
		if !isLockBusy(err) {
			_ = conn.Close()
			_ = db.Close()
			return nil, fmt.Errorf("acquire ingest lock: %w", err)
		}
		if !waiting {
			waiting = true
			fmt.Fprintln(os.Stderr, "graphi: another graphi process is indexing this repository — waiting for it to finish")
		}
	}
	return func() {
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		_ = db.Close()
	}, nil
}

// isLockBusy reports whether err is SQLite's held-by-another-connection
// signal (SQLITE_BUSY/SQLITE_LOCKED families) rather than a real failure.
func isLockBusy(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "busy") || strings.Contains(msg, "locked")
}
