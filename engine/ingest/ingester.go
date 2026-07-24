package ingest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/observe"

	_ "modernc.org/sqlite" // ingest meta DB driver
)

// Ingester runs incremental and full ingestion.
type Ingester struct {
	store  graphstore.Graphstore
	parser Parser
	meta   *sql.DB
	linker *link.Linker

	// metaDir is the durable sidecar directory ("" = in-memory sidecar). It
	// identifies one logical store's state on disk, so cross-process
	// coordination (the runtime's ingest lock) can key on it.
	metaDir string

	// profile selects the speed/depth trade-off for this ingest pass.
	profile profile.Profile

	// lastLinkStats holds the linker observability counters of the most recent
	// linkFiles pass (WP-03), including ResolvedExternal — the number of
	// materialized interned external references. Surfaced in the done-summary so
	// external materialization is observable. Touched only from the single
	// ingesting goroutine.
	lastLinkStats link.Stats

	// bounds are the fail-closed parse-time resource bounds (SW-055 AC#6) applied
	// to untrusted inputs: max file size (checked on the root-confined descriptor
	// and enforced again while reading at MaxFileSize+1),
	// parse timeout (context.WithTimeout on the Parse ctx), and recursion depth
	// (enforced inside core/parse). On any breach the offending file is SKIPPED
	// with a structured diagnostic and ingestion continues — never parse-anyway,
	// never silently truncate. Defaulted in New to parse.DefaultResourceBounds().
	bounds parse.ResourceBounds

	// skipMu guards skipped. skipped accumulates the fail-closed skip diagnostics
	// of the most recent ingest pass (oversize / timeout / depth breaches). It
	// carries ONLY structured provenance, never raw source bytes.
	skipMu  sync.Mutex
	skipped []SkipDiagnostic

	// broker, when set, is notified of ingest lifecycle events (e.g.
	// ingest-completed) so surfaces (HTTP SSE) can stream freshness updates to
	// clients. Nil = no-op (default); existing callers are unaffected.
	broker *observe.Broker

	// progress, when set, receives full-ingest phase/per-file events (see
	// progress.go). lastProgressPub/lastProgressPh throttle the mirrored
	// "ingest-progress" broker publishes; both are touched only from the
	// single ingesting goroutine, so they need no lock.
	progress         func(ProgressEvent)
	lastProgressPub  time.Time
	lastProgressPh   Phase
	lastProgressTime time.Time

	// heartbeatMode selects the heartbeat cadence (TTY vs non-TTY).
	heartbeatMode HeartbeatMode
	// heartbeatInterval is the maximum silence between progress events for the
	// currently active phase.
	heartbeatInterval time.Duration
	// clock provides time for deterministic heartbeat tests.
	clock Clock

	// parseWorkers overrides the full-ingest parse-pool width; 0 = GOMAXPROCS
	// (see parseUnitsParallel).
	parseWorkers int

	// ignore caches the opt-in index-scope config per root (see ignore.go).
	ignore ignoreState

	// readOnly marks an observer built by NewReadOnly: the meta sidecar is
	// opened mode=ro and every mutating entry point fails with ErrReadOnly.
	readOnly bool

	// test hooks
	failAfterDirtyMark error
	scheduleHook       func(relPath string)
	hookMu             sync.Mutex
}

// WithBroker attaches an event broker and returns the receiver for chaining.
// When attached, IngestAll/IngestChanged publish a lifecycle event on success.
// Without a broker ingest behaves exactly as before.
func (i *Ingester) WithBroker(b *observe.Broker) *Ingester {
	i.broker = b
	return i
}

// WithProfile selects the index profile for this ingest pass and returns the
// receiver for chaining. The profile is persisted to store metadata on a
// successful full ingest.
func (i *Ingester) WithProfile(p profile.Profile) *Ingester {
	i.profile = p
	return i
}

// LastLinkStats returns the linker observability counters of the most recent
// linkFiles pass (WP-03). ResolvedExternal is the number of materialized interned
// external references (Go stdlib / 3rd-party call/ref targets that used to be
// silently dropped). Surfaces/tests read this to report external materialization
// in the ingest done-summary. It reflects the single ingesting goroutine's last
// pass and is not safe to read concurrently with an in-flight ingest.
func (i *Ingester) LastLinkStats() link.Stats { return i.lastLinkStats }

// notifyIngest publishes a loss-tolerant lifecycle event. It is nil-safe and
// never returns an error — a publish failure must not fail ingestion.
func (i *Ingester) notifyIngest(ctx context.Context, kind string, files int) {
	if i.broker == nil {
		return
	}
	payload, _ := json.Marshal(map[string]int{"files": files})
	i.broker.Publish(ctx, observe.Event{Type: kind, Ts: time.Now(), Payload: payload})
}

// memDBSeq gives each in-memory meta sidecar a process-unique database name so
// shared-cache in-memory Ingesters stay isolated from one another (see New).
var memDBSeq uint64

// New constructs an Ingester. metaDir receives a SQLite sidecar for cache,
// reverse-deps, and dirty flags. If metaDir is empty, an in-memory sidecar is
// used (testing only).
func New(store graphstore.Graphstore, parser Parser, metaDir string) (*Ingester, error) {
	// Plain ":memory:" opens a private, independent database PER pooled
	// connection (SQLite in-memory databases are not shared across connections
	// by default). With the default (unbounded) connection pool, a schema
	// created by initSchema on one connection is invisible to a later query
	// issued on a different connection, surfacing as a nondeterministic "no such
	// table" error. The "file::memory:?cache=shared" URI form shares one
	// in-memory database across every connection opened from this *sql.DB
	// instead.
	//
	// Sharing the cache alone isn't enough, though: metaTx opens a write *sql.Tx
	// on one connection, and helpers invoked from inside it (e.g.
	// cachedNodeIDs) query i.meta directly, i.e. on a SECOND connection, while
	// that write transaction is still open. On disk this is harmless — WAL
	// readers never block on a writer — but WAL is not available for
	// ":memory:" (SQLite silently keeps the default rollback-journal locking
	// there), under which any reader blocks until the writer commits. Since
	// both connections are driven by the same goroutine, the writer never gets
	// a chance to commit and the process deadlocks. read_uncommitted lets a
	// shared-cache connection read another connection's in-flight (even
	// uncommitted) writes instead of blocking on the writer's lock, which is
	// exactly what's needed here — everything routes through this single
	// process/goroutine anyway, so there is no other writer whose isolation
	// could be violated.
	//
	// The shared cache MUST be scoped per-Ingester, not process-global. An
	// unnamed "file::memory:?cache=shared" resolves to ONE database shared by
	// every such connection in the whole process, so two in-memory Ingesters
	// alive at once (e.g. makeEditorClient's primary ingester plus the
	// parser-consistency checker's throwaway ingester) would silently share one
	// meta sidecar and cross-contaminate each other's content cache / reverse
	// deps / dirty flags. Giving each Ingester a unique database name keeps the
	// cache shared across THIS Ingester's pooled connections while staying
	// isolated from every other in-memory Ingester.
	dbPath := fmt.Sprintf("file:ingest-meta-%d?mode=memory&cache=shared", atomic.AddUint64(&memDBSeq, 1))
	extraPragma := "&_pragma=read_uncommitted(1)"
	if metaDir != "" {
		if err := os.MkdirAll(metaDir, 0o700); err != nil {
			return nil, fmt.Errorf("ingest: create meta dir: %w", err)
		}
		dbPath = filepath.Join(metaDir, "ingest-meta.db")
		extraPragma = ""
	}
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	db, err := sql.Open("sqlite", dbPath+sep+"_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"+extraPragma)
	if err != nil {
		return nil, fmt.Errorf("ingest: open meta db: %w", err)
	}
	i := &Ingester{store: store, parser: parser, meta: db, linker: link.New(), metaDir: metaDir, bounds: parse.DefaultResourceBounds(), clock: realClock{}, heartbeatMode: HeartbeatNonTTY, heartbeatInterval: heartbeatModeInterval(HeartbeatNonTTY), lastProgressTime: time.Now()}
	// Apply the fail-closed recursion-depth bound to the shared parse path
	// (process-wide; core/parse reads it per Extract). Size + timeout are enforced
	// at this ingest boundary directly.
	parse.SetMaxParseDepth(i.bounds.MaxDepth)
	if err := i.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	// PRIV-01 (SW-119): the sidecar caches file hashes, reverse-deps and edit
	// provenance derived from potentially private source — owner-only, and a
	// pre-existing too-wide sidecar is migrated on open. (metaDir itself is
	// created 0700 above; in-memory sidecars have no file to tighten.)
	if metaDir != "" {
		if err := graphstore.TightenDBFileModes(dbPath); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("ingest: %w", err)
		}
	}
	return i, nil
}

// MetaDir returns the durable sidecar directory this Ingester was constructed
// with, or "" for an in-memory sidecar. It names one logical store's on-disk
// state and is therefore the key the runtime's cross-process ingest lock
// serializes on.
func (i *Ingester) MetaDir() string { return i.metaDir }

// SkipReason categorizes why a file was skipped fail-closed.
type SkipReason string

const (
	// SkipOversize: file exceeded ResourceBounds.MaxFileSize. Descriptor size is
	// checked before reading and a MaxFileSize+1 reader catches concurrent growth.
	SkipOversize SkipReason = "oversize"
	// SkipTimeout: parse exceeded ResourceBounds.ParseTimeout.
	SkipTimeout SkipReason = "timeout"
	// SkipMaxDepth: input exceeded ResourceBounds.MaxDepth (nesting/recursion).
	SkipMaxDepth SkipReason = "max-depth"
	// SkipUnreadable: the root-confined path could not be opened and validated as
	// a regular file. Final symlinks, outside-root intermediate symlinks, special
	// files, concurrent path replacement, broken links, and permission failures
	// land here. This is fail-closed: skip and record, never parse path-resolved
	// bytes and never abort ingestion of unrelated files.
	SkipUnreadable SkipReason = "unreadable"
	// SkipParseError: the file has a registered parser but is not valid source
	// for it — a genuine syntax error. This is a property of the FILE, not a
	// failure of the ingester, so (like the resource-bound reasons above) it is
	// fail-closed: skip and record, never abort the whole ingest. A single such
	// file must not sink indexing of the rest of the repo. The canonical trigger
	// is a WireMock __files response body that uses Handlebars response-templating
	// (e.g. `{{...}}` at a structural position) and is therefore not valid strict
	// JSON. Unlike SkipNoParser (a non-source asset, silently untracked), a
	// malformed source file IS diagnostic-worthy: the user asked to index it, so
	// we record why it was skipped.
	SkipParseError SkipReason = "parse-error"
)

// SkipDiagnostic is the structured, source-free record of a fail-closed skip. It
// carries ONLY provenance (path, reason, the observed size for oversize) — never
// raw source bytes (SW-055 AC#6 default-deny source sanitization).
type SkipDiagnostic struct {
	Path   string     // repo-relative path of the skipped file
	Reason SkipReason // why it was skipped
	Size   int64      // observed size in bytes (oversize only; 0 otherwise)
}

// WithResourceBounds overrides the fail-closed parse-time resource bounds and
// returns the receiver for chaining. It also applies the depth bound to the
// process-wide parse path. Passing the zero ResourceBounds disables all bounds.
func (i *Ingester) WithResourceBounds(b parse.ResourceBounds) *Ingester {
	i.bounds = b
	parse.SetMaxParseDepth(b.MaxDepth)
	return i
}

// SkippedDiagnostics returns a copy of the fail-closed skip diagnostics recorded
// during the most recent ingest pass.
func (i *Ingester) SkippedDiagnostics() []SkipDiagnostic {
	i.skipMu.Lock()
	defer i.skipMu.Unlock()
	out := make([]SkipDiagnostic, len(i.skipped))
	copy(out, i.skipped)
	// Canonical order: skips accumulate in completion order, which under the
	// parallel parse pool is scheduling-dependent — sort so the API stays
	// deterministic regardless of worker interleaving.
	sort.Slice(out, func(a, b int) bool {
		if out[a].Path != out[b].Path {
			return out[a].Path < out[b].Path
		}
		return out[a].Reason < out[b].Reason
	})
	return out
}

// recordSkip appends a fail-closed skip diagnostic (concurrency-safe).
func (i *Ingester) recordSkip(d SkipDiagnostic) {
	i.skipMu.Lock()
	i.skipped = append(i.skipped, d)
	i.skipMu.Unlock()
}

// resetSkips clears skip diagnostics at the start of an ingest pass.
func (i *Ingester) resetSkips() {
	i.skipMu.Lock()
	i.skipped = nil
	i.skipMu.Unlock()
}

// MetaDB exposes the ingest-meta SQLite sidecar handle so a sibling engine
// side-channel (SW-038's change_record audit/undo store in engine/edit) can own
// its OWN table in the SAME sidecar that already holds edit_provenance,
// file_content_cache, reverse_deps, and dirty_units — never in core/graphstore
// (which would poison the AC-1 marshalled-graph digest). The returned handle is
// read/write but the caller MUST confine itself to its own table(s); the ingest
// pipeline owns every table declared in initSchema. It is exposed at the engine
// layer only (engine/edit consumes it); no surface ever touches it.
func (i *Ingester) MetaDB() *sql.DB { return i.meta }

// Close releases resources.
func (i *Ingester) Close() error {
	if i.meta != nil {
		return i.meta.Close()
	}
	return nil
}

// SetFailAfterDirtyMarkHook arms a one-shot fault injected after dirty-mark but
// before commit. Test-only.
func (i *Ingester) SetFailAfterDirtyMarkHook(err error) {
	i.hookMu.Lock()
	i.failAfterDirtyMark = err
	i.hookMu.Unlock()
}

func (i *Ingester) takeFailHook() error {
	i.hookMu.Lock()
	defer i.hookMu.Unlock()
	err := i.failAfterDirtyMark
	i.failAfterDirtyMark = nil
	return err
}
