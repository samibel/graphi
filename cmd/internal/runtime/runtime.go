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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/review"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/divergence"
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

	// comp is the SW-227 (AX-07) composition this Runtime's capabilities came
	// from: the frozen module contributions plus the surface client built over
	// them. It is nil on the daemon-socket attach (no local capabilities were
	// composed at all) and on the legacy composition path.
	comp *Composition

	closeOnce sync.Once
	closers   []func()
	done      chan struct{}
}

// Store exposes the session store (read-only wiring like the zeroconfig wiki).
func (r *Runtime) Store() graphstore.Graphstore { return r.store }

// Broker exposes the session's observe broker (nil in Attach mode).
func (r *Runtime) Broker() *observe.Broker { return r.broker }

// Composition exposes the SW-227 composition this Runtime was built from, or
// nil when none was composed (a daemon-socket attach, or the legacy path). It is
// read-only: everything reachable through it is frozen or a copy.
func (r *Runtime) Composition() *Composition { return r.comp }

// Done is closed when the Runtime has been closed.
func (r *Runtime) Done() <-chan struct{} { return r.done }

// Close releases every owned resource exactly once, reverse of construction.
//
// SW-245 put two things in front of that loop, in this order and for one
// reason each:
//
//  1. The deferred dual-run comparisons are drained. Since SW-245 the `shadow`
//     position answers the caller and compares afterwards, so at the moment a
//     session ends there can be comparisons that have not run — each of which
//     may be holding a divergence nobody has seen yet. AC-3 says process exit
//     must not discard a pending mismatch, and this is where it does not.
//     It has to happen BEFORE the closers, because the comparison reads through
//     the very store the closers close; a comparison that ran against a closed
//     store would not merely fail, it would manufacture a false
//     "error-presence" divergence out of the shutdown.
//  2. The divergence record is flushed. The store writes the first observation
//     and every mismatch immediately but coalesces the rest on a two-second
//     interval, so a process that exits without this loses the counts it was
//     still holding. That loss predates SW-245 (it is disclosed in
//     docs/executor-seam-rollback.md §5), and deferring the comparison would
//     have widened it — a comparison that completes during the drain writes
//     into exactly that buffer. Flushing here closes the widened window and, as
//     a side effect, the original one for any session that closes its Runtime.
func (r *Runtime) Close() {
	r.closeOnce.Do(func() {
		drainShadowComparisons()
		FlushDivergenceRecord()
		for i := len(r.closers) - 1; i >= 0; i-- {
			r.closers[i]()
		}
		close(r.done)
	})
}

// shadowDrainBudget bounds how long Close waits for outstanding comparisons.
//
// It is generous relative to the work — the queue holds at most 64 jobs and one
// job costs about one legacy call — and finite because a wedged comparison must
// not become a CLI that will not exit. A drain that runs out of budget records
// what it abandoned as skipped, so the coverage disclosure stays true rather
// than the shutdown going quiet (SW-245 AC-4).
const shadowDrainBudget = 5 * time.Second

// drainShadowComparisons waits for the deferred dual runs, then lets go.
//
// A drain that times out is not escalated into a session failure: the caller
// has already been served, the abandoned comparisons are counted and disclosed,
// and refusing to exit because a diagnostic was slow would be the wrong trade —
// the same one installDivergenceRecorder makes when a store cannot be built.
//
// The empty check in front is not an optimisation for its own sake. The queue
// is process-global while Runtimes are not: Attach's error path, OpenSession's
// retries and a roots-change rebind all close a Runtime that never dispatched
// anything, and each of those would otherwise build a timer, a context and a
// watcher goroutine to wait on a queue that is empty. With the check, a Close
// with nothing outstanding costs one mutex acquisition.
func drainShadowComparisons() {
	if client.CanaryShadowPending() == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shadowDrainBudget)
	defer cancel()
	_ = client.DrainCanaryShadow(ctx)
}

// FlushDivergenceRecord writes any buffered divergence counts to disk.
//
// It is exported because the composition root is not the only place a graphi
// process ends: a verb that never builds a Runtime, or a test that wants the
// record on disk without tearing a session down, needs the same barrier.
// Calling it with no store installed is a no-op.
func FlushDivergenceRecord() {
	installedDivergence.mu.Lock()
	store := installedDivergence.store
	installedDivergence.mu.Unlock()
	if store != nil {
		_ = store.Flush()
	}
}

func newRuntime() *Runtime { return &Runtime{done: make(chan struct{})} }

// Attach builds a client with the pre-RUN-01 semantics, owned by a Runtime:
// daemon socket → remote client; else the given (or in-memory) store with the
// analysis + review + (embedder-aware) search wiring every CLI verb used via
// makeClientOrOpenMeta. It never discovers and never ingests.
func Attach(dbPath, socket, metaDir string) (*Runtime, error) {
	if err := ApplyCanaryMode(); err != nil {
		return nil, err
	}
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

	// SW-227 (AX-07): one composition. No git provider on the attach path — no
	// repository root was resolved — so the git-consuming analyzers keep their
	// graceful empty results, exactly as before.
	if CompositionModeSetting() == CompositionLegacy {
		attachLegacy(rt, store, metaDir)
		return rt, nil
	}
	comp, cerr := NewBuilder(store).WithMetaDir(metaDir).Build()
	if cerr != nil {
		rt.Close()
		return nil, cerr
	}
	rt.comp = comp
	rt.Client = comp.Client()
	return rt, nil
}

// attachLegacy is the pre-AX-07 attach composition, preserved verbatim so the
// rollback in compositionmode.go is a real path and not a claim (AC-6). It is
// exercised by the cross-composition characterization test.
func attachLegacy(rt *Runtime, store graphstore.Graphstore, metaDir string) {
	// SW-222 (AX-02): no git provider on the attach path, so analyzer
	// composition is complete here.
	asvc := analysis.NewDefaultService(store).Freeze()
	rt.Client = client.NewDirect(query.New(store), NewSearchService(store, metaDir)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc))
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
	if err := ApplyCanaryMode(); err != nil {
		return nil, err
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

	// SW-227 (AX-07): the session's whole capability wiring is composed HERE,
	// once, before anything consumes it — parsers for the ingester, analyzers
	// and the operation catalog for the surfaces. Every registry is frozen by
	// the time this returns; the surface client is composed further down, at the
	// point the pre-AX-07 code composed it (see Composition.Client).
	var comp *Composition
	if CompositionModeSetting() != CompositionLegacy {
		built, cerr := NewBuilder(store).WithMetaDir(p.Meta).WithRepoRoot(root).Build()
		if cerr != nil {
			rt.Close()
			return nil, cerr
		}
		comp = built
		rt.comp = comp
	}

	ing, err := ingest.New(store, ingest.NewNotebookParser(sessionParsers(comp)), p.Meta)
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

	if comp != nil {
		rt.Client = comp.Client()
		return rt, nil
	}
	openSessionLegacy(rt, store, p.Meta, root)
	return rt, nil
}

// sessionParsers returns the parser registry the session ingester parses
// through: the module set's frozen registry on the AX-07 path, and the
// stand-alone default registry on the legacy one. Both hold the same parsers in
// the same registration order (core/parse.DefaultParsers is the single list).
func sessionParsers(comp *Composition) *parse.Registry {
	if comp == nil {
		return parse.NewDefaultRegistry()
	}
	return comp.Parsers()
}

// openSessionLegacy is the pre-AX-07 session composition, preserved verbatim so
// the rollback in compositionmode.go is a real path and not a claim (AC-6).
func openSessionLegacy(rt *Runtime, store graphstore.Graphstore, metaDir, root string) {
	// The surface-boundary git-history provider: bounded local `git log`,
	// injected into both the analysis service (git-history, suggest-reviewers,
	// pr-signals) and the labs agent-intelligence tools. The engine itself
	// stays exec-free; only this composition root hands it the seam.
	gp := gitlog.New(root)
	// SW-222 (AX-02): Freeze ends the chain — WithGitProvider is the last
	// composition step, and nothing may re-arm an analyzer after it.
	asvc := analysis.NewDefaultService(store).WithGitProvider(gp).Freeze()
	rt.Client = client.NewDirect(query.New(store), NewSearchService(store, metaDir)).
		WithAnalysis(asvc).
		WithReview(review.NewService(asvc)).
		WithRepoRoot(root).
		WithGitProvider(gp)
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

// EnvCanaryModePrefix is the prefix of the SW-226 (AX-06) kill-switch
// variables. Each migrated operation has one, named for the operation:
// GRAPHI_CANARY_DEAD_CODE, GRAPHI_CANARY_COMPOUND, GRAPHI_CANARY_SEARCH_AST and
// so on. Each takes "legacy", "shadow" or "active".
//
// SW-228 (AX-08) made the switch per operation, which is what
// GRAPHI_CANARY_DEAD_CODE's name always claimed it was. Its spelling and its
// meaning are unchanged for the operation it names; what changed is that it no
// longer also moves nine other operations.
//
// It is an environment variable and not only a source constant BECAUSE it is a
// kill switch. SW-225's descriptorSource is deliberately source-only, and the
// reason does not transfer: descriptors are advertised wire contract, so two
// processes on the same version must advertise identically. The switch changes
// which internal path produces the bytes, and AC-3 requires all three positions
// to produce the SAME bytes — so an operator flipping it changes nothing a
// client can observe, and being able to turn it off without waiting for a
// release is the entire point of the mechanism.
//
// It is read HERE, in the composition root, rather than in surfaces/client:
// reading it at the point of use would mean consulting the environment on every
// dispatch, and the position would be able to change under an in-flight session.
const EnvCanaryModePrefix = "GRAPHI_CANARY_"

// EnvCanaryModeAll selects the position for EVERY migrated operation that has
// no variable of its own. It exists so an operator can roll the whole seam back
// (or turn the whole comparison on while investigating) in one action instead of
// ten, and a per-operation variable always wins over it.
const EnvCanaryModeAll = EnvCanaryModePrefix + "ALL"

// EnvCanaryMode is the dead_code operation's kill switch, kept as a named
// constant because SW-226's evidence, the latency gate and the release notes all
// refer to it by name. It is spelled out rather than derived because a constant
// cannot call a function; canarymode_test.go asserts it equals
// EnvCanaryModeFor(client.CanaryOperation), so the two cannot drift apart.
const EnvCanaryMode = EnvCanaryModePrefix + "DEAD_CODE"

// EnvCanaryModeFor names the kill-switch variable for one operation.
func EnvCanaryModeFor(operation string) string {
	return EnvCanaryModePrefix + strings.ToUpper(operation)
}

// ApplyCanaryMode installs the kill-switch positions from the environment
// before any client is built.
//
// An unrecognised value FAILS the session rather than falling back to the
// default. Silently ignoring a typo would leave an operator who set
// GRAPHI_CANARY_DEAD_CODE=lecacy believing they had rolled back when they had
// not — the fail-closed rule this project applies to every other operator input.
//
// It resets first. Applying overrides onto whatever a previous call left behind
// would make "unset the variable and restart the session" a no-op in a process
// that runs more than one session (the daemon, and every test binary), which is
// a kill switch that cannot be turned off.
func ApplyCanaryMode() error {
	client.ResetCanaryModes()
	if raw, ok := os.LookupEnv(EnvCanaryModeAll); ok {
		mode, err := client.ParseCanaryMode(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", EnvCanaryModeAll, err)
		}
		if err := client.SetCanaryModeDefault(mode); err != nil {
			return fmt.Errorf("%s: %w", EnvCanaryModeAll, err)
		}
	}
	for _, operation := range client.MigratedOperations() {
		name := EnvCanaryModeFor(operation)
		raw, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		mode, err := client.ParseCanaryMode(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if err := client.SetCanaryModeFor(operation, mode); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	installDivergenceRecorder()
	return nil
}

// installedDivergence remembers the recorder THIS process installed, together
// with the configuration it was installed for.
//
// It exists because installation is NOT a one-shot event. ApplyCanaryMode runs
// on every Attach and every OpenSession, and `graphi mcp` calls OpenSession
// again — mid-session, in the same already-serving process — whenever the
// client announces a roots-list change (surfaces/mcp/session.go). Rebuilding a
// divergence.Store on each of those calls would hand the seam a store with an
// empty buffer and drop whatever the outgoing one had coalesced but not yet
// written: up to one flush interval of agreement observations. Mismatches flush
// immediately and were never at risk, but the observation COUNT is half of what
// AC-1 promises, and a record that quietly loses counts on a supported
// lifecycle event is exactly the silent loss the read path refuses to commit
// when it discloses unreadable and pruned segments as a lower bound.
//
// So installation is idempotent for an unchanged configuration — the live store
// keeps its buffer across a rebind — and every replacement flushes the outgoing
// store before letting go of it.
var installedDivergence struct {
	mu    sync.Mutex
	store *divergence.Store
	key   string
}

// installDivergenceRecorder wires the durable divergence record (SW-232 AC-1)
// into the executor seam, or removes it again.
//
// It is installed HERE, in the composition root, for the reason the doctor
// readout is read here too: surfaces/client composes capabilities and must not
// grow a dependency on the state directory, while internal/divergence owns the
// files and knows nothing about surfaces. This function is the only place that
// imports both.
//
// It installs ONLY when at least one operation is off the shipped `legacy`
// position. On the shipped configuration the seam never compares anything, so a
// recorder would be handed nothing to record — and a graphi that touched the
// state directory on every session for a feature nobody enabled would be paying
// for observability it cannot produce.
//
// A store that cannot be built (no resolvable state directory) is not a session
// failure: the operator asked for a dual run, not for a file, and refusing to
// serve requests because a diagnostic could not be opened would be the wrong
// trade. The seam keeps its in-process counter in that case.
func installDivergenceRecorder() {
	key := divergenceRecorderKey()
	installedDivergence.mu.Lock()
	defer installedDivergence.mu.Unlock()

	if key == "" {
		drainBeforeRecorderChange()
		retireDivergenceStoreLocked()
		client.SetDivergenceRecorder(nil)
		return
	}
	if installedDivergence.store != nil && installedDivergence.key == key {
		// Same dual set, same state directory: the store already installed is
		// still the right one. Keeping it is what carries its buffered
		// observations through a roots-change rebind instead of dropping them.
		return
	}
	drainBeforeRecorderChange()
	store, err := divergence.NewStore(state.StateDir())
	if err != nil {
		retireDivergenceStoreLocked()
		client.SetDivergenceRecorder(nil)
		return
	}
	retireDivergenceStoreLocked()
	installedDivergence.store, installedDivergence.key = store, key
	client.SetDivergenceRecorder(store)
}

// drainBeforeRecorderChange empties the deferred-comparison queue before the
// installed recorder is replaced or removed.
//
// Without it, jobs queued under the OUTGOING recorder run after the swap and
// record through the incoming one — into a different state directory, or, when
// the new position set is all-`legacy`, into nothing at all. Neither outcome is
// observed durably and neither is counted as skipped, which is the one hole
// AC-4's observed + skipped == dispatched equation forbids. Draining first
// makes every job that was queued under a recorder record through that
// recorder.
//
// It runs while installedDivergence.mu is held. That is safe because nothing on
// the worker's path takes that mutex: the worker reaches the recorder through
// the pointer surfaces/client holds, not through this package.
//
// The window it closes is narrow — it needs the recorder key to change inside a
// live process, which today means the environment moving between two
// ApplyCanaryMode calls — so it shares Close's budget rather than getting a
// longer one, and it is a no-op (one mutex acquisition) when nothing is queued,
// which is every startup.
func drainBeforeRecorderChange() {
	drainShadowComparisons()
}

// retireDivergenceStoreLocked flushes and forgets the installed store. The
// flush is the whole point: the buffer is the only place a coalesced
// observation lives, so a store dropped without one takes those counts with it.
// A flush that fails is not escalated — the store records its own LastError and
// the seam's job is still to answer the request, not to enforce the record.
func retireDivergenceStoreLocked() {
	if installedDivergence.store != nil {
		_ = installedDivergence.store.Flush()
	}
	installedDivergence.store, installedDivergence.key = nil, ""
}

// divergenceRecorderKey identifies the configuration a recorder is installed
// for: the state directory it writes into, plus the set of operations in a
// position that produces a comparison. Only `shadow` runs both paths; `active`
// runs one, so it has nothing to compare and nothing honest to record.
//
// An empty key means "no recorder wanted". The state directory is part of the
// key because it is not a constant — the environment can repoint it between two
// ApplyCanaryMode calls in one process — and a store reused after that repoint
// would keep writing into the previous directory.
func divergenceRecorderKey() string {
	var dual []string
	for _, p := range client.CanaryPositions() {
		if p.Mode == client.CanaryModeShadow {
			dual = append(dual, p.Operation)
		}
	}
	if len(dual) == 0 {
		return ""
	}
	sort.Strings(dual)
	return state.StateDir() + "\x00" + strings.Join(dual, ",")
}

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
// embedder, no network). With a metaDir, the durable GenerationStore is
// opened and asked for the active generation's typed state, so the search
// service can serve nothing from a non-ready generation (SW-261 AC-10).
func NewSearchService(store graphstore.Graphstore, metaDir string) *search.Service {
	emb, err := embed.Constructor(os.Getenv(embed.EnvSelector), embed.DefaultConstructors())
	if err != nil {
		// Fail-closed (e.g. a non-loopback Ollama host): report and keep semantic
		// search OFF rather than constructing an unsafe embedder.
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return search.New(store)
	}
	if emb == nil {
		return search.New(store) // graceful skip: nothing configured
	}
	return NewSearchServiceWithEmbedder(store, metaDir, emb)
}

// NewSearchServiceWithEmbedder is NewSearchService with the embedder supplied
// directly. Production callers go through NewSearchService (which reads
// GRAPHI_EMBEDDER); tests use this to exercise the configured-but-no-meta path
// (CRITICAL 1, SW-261 review round 2) without depending on the env or on a
// registered scheme constructor.
func NewSearchServiceWithEmbedder(store graphstore.Graphstore, metaDir string, emb embed.Embedder) *search.Service {
	svc := search.New(store)
	reg := embed.NewRegistry()
	if err := reg.Register(emb); err != nil {
		// Unreachable on a fresh registry; reported rather than dropped so a
		// future freeze-ordering mistake surfaces instead of silently turning
		// semantic search off.
		fmt.Fprintf(os.Stderr, "graphi: embedder disabled: %v\n", err)
		return svc
	}
	reg.Freeze() // SW-222 (AX-02): embedder composition is complete here.
	index := embed.NewIndex()
	if metaDir != "" {
		// Open the durable GenerationStore. The store handles its own
		// v1-migration on first open (AC-8); we then query Active for the
		// typed state and pass it to WithSemantic so a non-ready state
		// returns the typed unavailable response with reason naming the
		// state (AC-10).
		semanticState := loadSemanticState(context.Background(), store, metaDir, emb)
		// Reload the in-memory index from the active generation ONLY when
		// the state is ready. A non-ready generation is NEVER served
		// (fail-closed): the index stays empty so the configured path has
		// no vectors to rank.
		if semanticState.State == embed.StateReady {
			table, terr := embed.OpenSQLiteGenerationStore(context.Background(), metaDir)
			if terr != nil {
				fmt.Fprintf(os.Stderr, "graphi: vectors reload disabled: %v\n", terr)
			} else {
				defer func() { _ = table.Close() }()
				gen, _, aerr := table.Active(context.Background(), semanticState.Requested, nil)
				if aerr == nil && gen.ID != "" {
					rows, lerr := table.Load(context.Background(), gen.ID)
					if lerr != nil {
						fmt.Fprintf(os.Stderr, "graphi: vectors reload failed: %v\n", lerr)
					} else {
						vecs := make([]embed.Vector, len(rows))
						for i, r := range rows {
							vecs[i] = embed.Vector{NodeID: r.NodeID, Values: r.Vector}
						}
						if rerr := index.Rebuild(context.Background(), vecs); rerr != nil {
							fmt.Fprintf(os.Stderr, "graphi: vectors reload failed: %v\n", rerr)
						}
					}
				}
			}
		}
		return svc.WithSemantic(reg, index, store).WithSemanticState(semanticState)
	}
	// metaDir == "" — the configured-but-no-store path (CRITICAL 1, SW-261
	// review round 2). A configured embedder with no meta directory is
	// semantically MISSING, not ready: the production runtime ALWAYS opens
	// the meta sidecar, so this branch is reached only by Attach-mode tests
	// and the explicit -db/-meta CLI paths. Returning a service with no
	// plumbed state would let StateUnset slip through IsZero() and serve
	// vectors over an empty index — exactly the fail-open AC-7 forbids. We
	// instead synthesise a StateMissing SemanticState with the missing
	// reason so SemanticSearch answers unavailable.
	missingFP := embed.Fingerprint{
		ModelID:        emb.ID(),
		Dim:            emb.Dim(),
		DocumentSchema: embed.DocumentSchema,
	}
	return svc.WithSemantic(reg, index, store).
		WithSemanticState(search.SemanticState{
			State:     embed.StateMissing,
			Requested: missingFP,
			Reason:    search.ReasonUnavailable,
		})
}

// loadSemanticState computes the fingerprint the runtime would build
// (using the graphstore's graph_generation metadata) and queries the
// durable GenerationStore for the typed active-generation state. The
// function is split out so tests can drive it without spinning up the
// runtime. It is a pure helper — it does not log.
//
// LoadSemanticStateForTest is the exported form used by the runtime's
// semantic-state conformance tests. It calls loadSemanticState directly
// so the test exercises the production code path.
func LoadSemanticStateForTest(ctx context.Context, store graphstore.Graphstore, metaDir string, emb embed.Embedder) search.SemanticState {
	return loadSemanticState(ctx, store, metaDir, emb)
}

func loadSemanticState(ctx context.Context, store graphstore.Graphstore, metaDir string, emb embed.Embedder) search.SemanticState {
	fp := embed.Fingerprint{
		ModelID:        emb.ID(),
		Dim:            emb.Dim(),
		DocumentSchema: embed.DocumentSchema,
	}
	graphGen, gerr := graphGenerationFromStore(ctx, store)
	if gerr != nil || graphGen == "" {
		// The fingerprint's graph_generation field falls back to the
		// documented placeholder; the orchestrator is expected to report
		// this as an open finding (see report).
		graphGen = embed.GraphGenerationPlaceholder
	}
	fp.GraphGeneration = graphGen

	table, err := embed.OpenSQLiteGenerationStore(ctx, metaDir)
	if err != nil {
		return search.SemanticState{
			State:     embed.StateMissing,
			Requested: fp,
			Reason:    search.ReasonUnavailable,
		}
	}
	defer func() { _ = table.Close() }()

	// An embedder that discovers its dimension by making a request reports 0
	// here: reload constructs a fresh instance and MUST NOT dial (zero
	// requests on reload is pinned by internal/canary). Adopt the dimension
	// the build persisted for this exact model instead of re-discovering it,
	// so the reload reconstructs the same canonical the build wrote. Without
	// this, every generation built by such an embedder — Ollama is the only
	// real one — reloaded as permanently stale and the ready state was
	// unreachable in production (SW-261 review round 3).
	//
	// This is deliberately NOT a wildcard: the dimension stays a compared
	// field, an embedder that knows its own dimension keeps using it, and a
	// disagreement still reads stale. See GenerationStore.DimForModel for the
	// one case it does not detect (a model swapped behind an unchanged id).
	if fp.Dim == 0 {
		d, ok, derr := table.DimForModel(ctx, emb.ID())
		switch {
		case derr != nil:
			// The lookup itself failed — a sidecar we cannot read is not the
			// same thing as a fingerprint that does not match. Reporting it
			// as `stale` (which the exact comparison would do, since fp.Dim
			// stays 0) would tell the user to re-index when the real problem
			// is an unreadable store. Both are fail-closed; only one is true.
			return search.SemanticState{
				State:     embed.StateCorrupt,
				Requested: fp,
				Reason:    fmt.Sprintf("semantic index unreadable: %v", derr),
			}
		case ok:
			fp.Dim = d
		}
	}
	_, state, aerr := table.Active(ctx, fp, embed.NodeReferencerFromGraphLookup(store.GetNode))
	if aerr != nil {
		// An Active error is treated as "corrupt" so the runtime surfaces
		// a precise reason rather than the engine's internal error.
		return search.SemanticState{
			State:     embed.StateCorrupt,
			Requested: fp,
			Reason:    fmt.Sprintf("semantic index corrupt: %v", aerr),
		}
	}
	return search.SemanticState{
		State:     state,
		Requested: fp,
		Reason:    search.ReasonForState(state),
	}
}

// graphGenerationFromStore reads the graphstore's "index.commit_generation"
// metadata key. The ingest pipeline writes this key as part of every
// committed graph mutation (full pass and incremental), so it is the
// stable current-graph identity the SW-261 fingerprint embeds. The
// historical "index.full_ingest_generation" key only advances on full
// passes — it would leave vectors classified ready after an incremental
// mutation even though the graph had moved, which is precisely the
// embedding-space mixing the story exists to prevent. This key is the
// fix; the fingerprint's graph_generation field is now load-bearing on
// every graph change.
//
// Fallback: a store that has never seen a graphi pass (no full pass, no
// incremental) does not yet have an index.commit_generation entry. We
// fall through to the historical full-pass generation key so a fresh
// store does not silently read as ready, and finally to the documented
// placeholder so a still-fresh store is visibly flagged. The orchestrator
// surfaces this in the report.
func graphGenerationFromStore(ctx context.Context, store graphstore.Graphstore) (string, error) {
	v, err := store.Metadata(ctx, "index.commit_generation")
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
		return "", err
	}
	v, err = store.Metadata(ctx, "index.full_ingest_generation")
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, graphstore.ErrNotFound) {
		return "", err
	}
	return "", nil
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

// BuildSemanticGeneration runs the SW-261 semantic-generation pass under
// the cross-process ingest lock. The lock spans the entire
// Begin → Upsert → Commit/Abort sequence, which is the AC-5/AC-6 cross-
// process guarantee the SW-261 review demanded: a second graphi
// process on the same meta directory cannot observe a live foreign
// staging row and delete it as a stale leftover, because the lock is
// held until the winner commits. The lock is the same SQLite lock the
// canonical SyncRepo / RebuildRepo take, so semantic generation
// serialises against every other indexing caller (it must not run
// concurrently with a full pass, which would race the
// commit_generation bump).
//
// The function takes the ingester only for its MetaDir() (the lock's
// identity); the actual ingest work has already happened in the
// caller's prior SyncRepo / RebuildRepo call. The embed.Registry and
// embed.GenerationStore are supplied so the helper can drive the
// Begin/Commit lifecycle; a nil store or unconfigured registry is a
// programming error (the caller has the precondition for an explicit
// --semantic opt-in).
func BuildSemanticGeneration(
	ctx context.Context,
	ing *ingest.Ingester,
	graphStore graphstore.Graphstore,
	reg *embed.Registry,
	generationStore embed.GenerationStore,
	nodes []model.Node,
	docs embed.DocumentSource,
	index embed.VectorIndex,
	progress func(done, total int),
) (embed.GenerateResult, error) {
	if ing == nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGeneration: nil ingester")
	}
	if reg == nil || !reg.Configured() {
		// Graceful skip mirrors the unconfigured-registry path of
		// GenerateAndPersist. The build does nothing; the active
		// pointer is unchanged. A lock acquisition is unnecessary
		// here — the unconfigured path does no work that another
		// indexing caller could race with.
		return embed.GenerateResult{Configured: false}, nil
	}
	if generationStore == nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGeneration: nil generation store")
	}
	// Take the lock BEFORE the snapshot (CRITICAL 2b). The caller's
	// nodes slice was built outside the lock — re-snapshot here, under
	// the lock, so the embedded set is consistent with the
	// graph_generation this run will fingerprint. A concurrent ingest
	// between the call-site snapshot and the lock acquisition would
	// otherwise produce a generation holding the OLD node set
	// fingerprinted as the NEW graph.
	release, err := acquireIngestLock(ctx, ing.MetaDir(), nil)
	if err != nil {
		return embed.GenerateResult{}, fmt.Errorf("acquire ingest lock for semantic: %w", err)
	}
	defer release()
	snap, err := graphStore.Nodes(ctx, graphstore.Query{})
	if err != nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGeneration: snapshot under lock: %w", err)
	}
	nodes = snap
	if len(nodes) > 0 {
		sort.SliceStable(nodes, func(i, j int) bool {
			if a, b := nodes[i].SourcePath(), nodes[j].SourcePath(); a != b {
				return a < b
			}
			return nodes[i].ID() < nodes[j].ID()
		})
	}
	// Source the fingerprint's graph_generation field from the same
	// graphstore key the reload path reads (index.commit_generation).
	// Build and reload now consume the same value, so a freshly built
	// generation reloads as StateReady unless the graph has moved in
	// the meantime (in which case the counter has advanced and the
	// next reload reads StateStale).
	// A read failure here is not cosmetic: the fingerprint would silently
	// fall back to the placeholder, and the generation would be published
	// under an identity that names no graph. Surface it instead — a build
	// that cannot establish which graph it belongs to should not produce a
	// generation at all.
	graphGen, gerr := graphGenerationFromStore(ctx, graphStore)
	if gerr != nil {
		return embed.GenerateResult{}, fmt.Errorf("runtime: BuildSemanticGeneration: read graph identity: %w", gerr)
	}
	return embed.GenerateAndPersistWithProgress(ctx, reg, nodes, docs, index, generationStore, progress, graphGen)
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
