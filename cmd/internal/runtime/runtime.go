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
	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	"github.com/samibel/graphi/surfaces/gitlog"
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
	// Root, when non-empty, pins the repository root exactly — explicit intent
	// in the CLI -root sense: no detection walk, no home/filesystem-root guard,
	// and precedence over transport Roots and the Cwd fallback (only
	// DBOverride/Socket rank higher). It must name an existing directory;
	// OpenSession otherwise fails closed with ErrNoRepository.
	Root string
	// DBOverride, when non-empty, short-circuits to Attach semantics: exactly
	// this store, no discovery, no ingest (D2 precedence, zero regression).
	DBOverride string
	// Socket, when non-empty, short-circuits to a daemon client.
	Socket string
	// Progress, when non-nil, receives ingest progress events.
	Progress func(ingest.ProgressEvent)
	// Status, when non-nil, receives coarse OpenSession lifecycle observations
	// that Progress cannot express (root resolution, ingest-lock contention).
	// Called synchronously from the opening goroutine: handlers must be fast
	// and non-blocking.
	Status func(BindEvent)
}

// BindEventKind classifies coarse OpenSession lifecycle observations.
type BindEventKind string

const (
	// BindRootResolved fires once, after repository-root resolution succeeds.
	BindRootResolved BindEventKind = "root-resolved"
	// BindLockWaiting fires on every failed ingest-lock acquisition attempt
	// (roughly one per busy_timeout window) while another process holds the
	// lock for the same repository state.
	BindLockWaiting BindEventKind = "lock-waiting"
	// BindLockAcquired fires when the lock is taken after at least one
	// BindLockWaiting (an uncontended acquisition emits nothing).
	BindLockAcquired BindEventKind = "lock-acquired"
)

// BindEvent is one OpenSession lifecycle observation.
type BindEvent struct {
	Kind BindEventKind
	Root string
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
	// DBPath and MetaDir are the graph store and evidence sidecar this session
	// resolved for Root (or the explicit ones an Attach was given; both empty
	// for a daemon socket, which owns no local state).
	//
	// They are exported because a long-lived surface has to be able to name the
	// repository it is serving. Compositions that locate repository state on
	// their own resolve it from the process working directory when handed
	// nothing, and for an MCP session — routinely launched from outside the
	// repository it binds — that is a different repository (W0.g review round
	// 1, Critical 1).
	DBPath  string
	MetaDir string

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
	rt.DBPath, rt.MetaDir = dbPath, metaDir
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
// that pre-index and pass -db); otherwise an explicit Root pins the repository
// outright; otherwise transport roots are tried in order, and only when Roots
// is nil does the cwd walk decide. A session that cannot bind a repository
// fails closed with ErrNoRepository; it never masquerades as a successful
// empty graph.
func OpenSession(ctx context.Context, opts Options) (*Runtime, error) {
	if opts.DBOverride != "" || opts.Socket != "" {
		return Attach(opts.DBOverride, opts.Socket, "")
	}
	root, err := resolveRepositoryRoot(opts)
	if err != nil {
		return nil, err
	}
	emitStatus := func(ev BindEvent) {
		if opts.Status != nil {
			opts.Status(ev)
		}
	}
	emitStatus(BindEvent{Kind: BindRootResolved, Root: root})

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
	waited := false
	release, err := acquireIngestLock(ctx, p.Meta, func() {
		waited = true
		emitStatus(BindEvent{Kind: BindLockWaiting, Root: root})
	})
	if err != nil {
		return nil, fmt.Errorf("acquire ingest lock: %w", err)
	}
	defer release()
	if waited {
		emitStatus(BindEvent{Kind: BindLockAcquired, Root: root})
	}

	rt := newRuntime()
	rt.Root = root
	rt.DBPath, rt.MetaDir = p.DB, p.Meta
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

	// The surface-boundary git-history provider: bounded local `git log`,
	// injected into both the analysis service (git-history, suggest-reviewers,
	// pr-signals) and the labs agent-intelligence tools. The engine itself
	// stays exec-free; only this composition root hands it the seam.
	gp := gitlog.New(root)
	asvc := analysis.NewDefaultService(store).WithGitProvider(gp)
	rt.Client = client.NewDirect(query.New(store), NewSearchService(store, p.Meta)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc)).
		WithRepoRoot(root).
		WithGitProvider(gp)
	return rt, nil
}

// resolveRepositoryRoot enforces session scoping. An explicit Root pin wins
// outright (pinExplicitRoot). Below that, MCP roots are authoritative when
// present: choosing the process cwd after a client explicitly supplied an
// empty or unrelated root set would cross workspace boundaries. With no
// transport roots (nil), legacy/non-roots-capable clients retain the cwd walk.
// Either detected root passes guardAutoRoot: auto-binding the user's home
// directory (or the filesystem root) is always an accident.
func resolveRepositoryRoot(opts Options) (string, error) {
	if opts.Root != "" {
		return pinExplicitRoot(opts.Root)
	}
	if opts.Roots != nil {
		var guardErr error
		for _, candidate := range opts.Roots {
			root, ok := state.DetectRepo(candidate)
			if !ok {
				continue
			}
			if err := guardAutoRoot(root); err != nil {
				// A later candidate may still be a real repository; remember
				// the refusal so an all-refused set surfaces the useful error.
				if guardErr == nil {
					guardErr = err
				}
				continue
			}
			return root, nil
		}
		if guardErr != nil {
			return "", guardErr
		}
		return "", fmt.Errorf("%w: none of %d client root(s) contains .git, go.work, or go.mod", ErrNoRepository, len(opts.Roots))
	}
	root, ok := state.DetectRepo(opts.Cwd)
	if !ok {
		return "", fmt.Errorf("%w: cwd %q contains no .git, go.work, or go.mod", ErrNoRepository, opts.Cwd)
	}
	if err := guardAutoRoot(root); err != nil {
		return "", err
	}
	return root, nil
}

// pinExplicitRoot validates an Options.Root pin. Unlike the detection paths it
// performs no upward walk and no guardAutoRoot: an explicit root is deliberate
// intent, exactly like -root on the CLI verbs. It still fails closed on a path
// that does not exist or is not a directory — an MCP bind failure surfaces
// late and opaquely (as the retryable -32002), so a typo must be caught here.
func pinExplicitRoot(root string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("%w: explicit root %q: %v", ErrNoRepository, root, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w: explicit root %q does not exist", ErrNoRepository, abs)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: explicit root %q is not a directory", ErrNoRepository, abs)
	}
	return abs, nil
}

// EnvAllowHomeRoot opts a session into deliberately binding the user's home
// directory as the repository root (e.g. a genuinely home-rooted dotfiles
// setup someone wants indexed).
const EnvAllowHomeRoot = "GRAPHI_ALLOW_HOME_ROOT"

// EnvRoot is the environment fallback for an explicit repository root on
// `graphi mcp` (the -root flag wins): for MCP clients that launch the server
// outside the repository and supply no usable roots. It is read in cmd/graphi
// so Options stays explicit and unit-testable; this package never consults
// the environment for it.
const EnvRoot = "GRAPHI_ROOT"

// guardAutoRoot refuses the roots that auto-detection lands on ONLY by
// accident: the user's home directory and the filesystem root. A dotfiles
// `.git` (or a stray go.mod) directly in $HOME makes every upward walk from
// anywhere under it resolve to $HOME once no nearer marker exists — an MCP
// client that spawns `graphi mcp` outside the project (observed: Devin CLI
// launching from the home directory) then silently starts indexing the entire
// home tree, which never finishes and holds the ingest lock the whole time.
// Explicit intent still works: `-db` pins a store (Attach path, no detection),
// CLI verbs take `-root`, `graphi mcp -root` (or GRAPHI_ROOT) pins the session
// root without this guard, and GRAPHI_ALLOW_HOME_ROOT=1 overrides outright.
func guardAutoRoot(root string) error {
	if os.Getenv(EnvAllowHomeRoot) == "1" {
		return nil
	}
	clean := filepath.Clean(root)
	if clean == filepath.Dir(clean) {
		return fmt.Errorf("%w: refusing to auto-bind the filesystem root %q as the repository; run from inside a repository, supply explicit MCP roots, or pin the session with 'graphi mcp -db <path>'", ErrNoRepository, clean)
	}
	if home, err := os.UserHomeDir(); err == nil && sameDir(clean, home) {
		return fmt.Errorf("%w: refusing to auto-bind your home directory %q as the repository — a repository marker (.git, go.work, or go.mod) directly in it makes the upward walk land there, and indexing it would scan your entire home tree; run from inside a repository, supply explicit MCP roots, pin the session with 'graphi mcp -db <path>', or set %s=1 to index the home directory deliberately", ErrNoRepository, clean, EnvAllowHomeRoot)
	}
	return nil
}

// sameDir compares two directory paths after cleaning, falling back to a
// symlink-resolved comparison (macOS /var vs /private/var style aliases) when
// the literal paths differ.
func sameDir(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
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
// only after a successful ingest, so freshness.LastSync never reports a time whose
// graph didn't actually commit. The whole pass runs under the cross-process
// ingest lock: concurrently opened sessions on the same logical store (e.g.
// several MCP clients auto-starting `graphi mcp` for one workspace) wait for
// the first pass instead of each launching their own full index, and the
// waiter then warm-starts over the store the winner just certified.
func SyncRepo(ctx context.Context, ing *ingest.Ingester, store graphstore.Graphstore, root string, progress func(ingest.ProgressEvent)) (SyncStats, error) {
	release, err := acquireIngestLock(ctx, ing.MetaDir(), nil)
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
	release, err := acquireIngestLock(ctx, ing.MetaDir(), nil)
	if err != nil {
		return fmt.Errorf("acquire ingest lock: %w", err)
	}
	defer release()
	if err := ing.IngestAll(ctx, root); err != nil {
		return err
	}
	return StampSyncMetadata(ctx, store, root, time.Now())
}

// ingestLockBusyTimeoutMS bounds how long ONE acquisition attempt blocks
// inside SQLite before the loop re-checks ctx and notifies onBusy. A package
// var so tests can shrink the waiter cadence without holding a lock for
// seconds.
var ingestLockBusyTimeoutMS = 5000

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
//
// onBusy, when non-nil, is invoked once per failed (busy) acquisition attempt
// and OWNS user-facing rendering of the waiting state; the historical one-shot
// stderr line is printed only for nil-onBusy callers (the CLI verbs), so their
// output stays byte-identical.
func acquireIngestLock(ctx context.Context, metaDir string, onBusy func()) (release func(), err error) {
	if metaDir == "" {
		return func() {}, nil
	}
	// busy_timeout makes each acquisition attempt block INSIDE SQLite; the
	// loop below re-checks ctx between attempts, so a waiter can still be
	// cancelled while the winner's full index runs for minutes.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(%d)", filepath.ToSlash(ingestlock.Path(metaDir)), ingestLockBusyTimeoutMS)
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
		if !ingestlock.IsBusy(err) {
			_ = conn.Close()
			_ = db.Close()
			return nil, fmt.Errorf("acquire ingest lock: %w", err)
		}
		if onBusy != nil {
			onBusy()
		} else if !waiting {
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
