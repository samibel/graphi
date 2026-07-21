package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	rtime "github.com/samibel/graphi/cmd/internal/runtime"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/core/profile"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/engine/watch"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/mcp"
)

// runMCP launches the MCP stdio server. Usage:
//
//	graphi mcp [-db path] [-daemon socket] [-labs]
//
// RUN-01 (ADR 0002): with no explicit -db/-daemon Runtime construction is
// deferred until the MCP initialize lifecycle identifies the client workspace.
// Legacy rootUri/inline roots bind during initialize; roots-capable clients are
// queried via roots/list after initialized; only clients offering neither use
// the process-cwd fallback. The selected per-repo state is recovered and
// warm/full-ingested before any tool can run. An explicit -db keeps the exact
// pre-RUN-01 behavior (Attach; SW-110 pins it).
func runMCP(args []string) int {
	dbPath, socket, labs, err := extractMCPFlags(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: mcp: %v\n", err)
		return 1
	}
	var options []mcp.ServerOption
	if labs {
		options = append(options, mcp.WithLabs())
	}
	var srv *mcp.Server
	if dbPath != "" || socket != "" {
		rt, err := rtime.Attach(dbPath, socket, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: mcp: %v\n", err)
			return 1
		}
		defer rt.Close()
		srv = mcp.NewServerWithClient(rt.Client, options...)
	} else {
		cwd := getwd()
		srv = mcp.NewServerWithBinder(func(ctx context.Context, roots []string) (mcp.Binding, error) {
			rt, err := rtime.OpenSession(ctx, rtime.Options{Cwd: cwd, Roots: roots})
			if err != nil {
				return mcp.Binding{}, err
			}
			return mcp.Binding{Client: rt.Client, Close: rt.Close}, nil
		}, options...)
	}
	defer srv.Close()
	serveCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	if err := srv.Serve(serveCtx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: mcp: %v\n", err)
		return 1
	}
	return 0
}

func extractMCPFlags(args []string) (dbPath, socket string, labs bool, err error) {
	dbPath, socket, rest := extractFlags(args)
	for _, arg := range rest {
		switch arg {
		case "-labs", "--labs":
			labs = true
		default:
			return "", "", false, fmt.Errorf("unknown argument %q", arg)
		}
	}
	return dbPath, socket, labs, nil
}

// watchStatusProvider adapts the engine/watch Manager's read-only per-root health
// to the analysis.WatchStatusProvider seam (SW-104). It lives in cmd (the only
// layer that may import both engine/watch and engine/analysis), so the
// `watcher-status` operation surfaces live, honest watcher health — including the
// SW-101 Reconcile error — over the single dispatch path, with no surface→engine
// coupling. It is read-only and adds no timestamp/wall-clock field.
type watchStatusProvider struct {
	mgr *watch.Manager
}

func (p watchStatusProvider) WatchStatus(_ context.Context) analysis.WatcherStatusReport {
	statuses := p.mgr.Statuses()
	rep := analysis.WatcherStatusReport{
		Active: len(statuses) > 0,
		Roots:  make([]analysis.WatchRootStatus, 0, len(statuses)),
	}
	for _, st := range statuses {
		rep.Roots = append(rep.Roots, analysis.WatchRootStatus{
			Root:      st.Root,
			Watching:  st.Watching,
			Healthy:   st.LastError == "",
			LastError: st.LastError,
		})
	}
	return rep
}

// runDaemon runs the daemon lifecycle commands: start, stop, status.
func runDaemon(args []string) int {
	// The documented invocation is `graphi daemon start|stop|status [-socket
	// path] [-db path]` — the subcommand comes BEFORE the flags. flag.FlagSet.Parse
	// stops at the first non-flag argument, so parsing args as-is would silently
	// never see -socket/-db (cmd would be consumed as fs.Args()[0], but the flags
	// after it would never reach Parse). Split the leading subcommand off first,
	// then parse only the remainder as flags.
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: graphi daemon start|stop|status [-socket path] [-db path]")
		return 1
	}
	cmd := args[0]
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	socket := fs.String("socket", daemon.DefaultSocketPath(), "Unix socket path")
	dbPath := fs.String("db", "", "SQLite graphstore path (empty = in-memory)")
	if err := fs.Parse(args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: daemon: %v\n", err)
		return 1
	}
	switch cmd {
	case "start":
		store, err := openStore(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
			return 1
		}
		defer func() { _ = store.Close() }()
		// SW-101: wire a filesystem-watch Manager so `track`ing a workspace starts
		// a pure-Go watcher + bounded worker-pool that keeps THIS daemon's shared
		// store fresh (deterministic, canonical-ordered incremental apply) without
		// an explicit re-index. The factory builds a per-root ingester over the
		// shared store and performs the initial full index.
		watchMgr := watch.NewManager(func(root string) (watch.Indexer, func(), error) {
			metaDir, err := os.MkdirTemp("", "graphi-watch-meta-")
			if err != nil {
				return nil, nil, err
			}
			ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
			if err != nil {
				_ = os.RemoveAll(metaDir)
				return nil, nil, err
			}
			if err := ing.IngestAll(context.Background(), root); err != nil {
				_ = ing.Close()
				_ = os.RemoveAll(metaDir)
				return nil, nil, err
			}
			cleanup := func() { _ = ing.Close(); _ = os.RemoveAll(metaDir) }
			return ing, cleanup, nil
		}, watch.DefaultConfig())
		defer watchMgr.StopAll()
		// SW-104: give the daemon handler an analysis service so the four EP-017
		// operations (and every analyzer) route through the SAME single dispatch
		// path over the daemon RPC, and back `watcher-status` with a read-only
		// provider over the watch Manager so the daemon reports live, honest per-root
		// watcher health — including the SW-101 Reconcile error — rather than masking
		// it.
		asvc := analysis.NewDefaultServiceWithWatch(store, watchStatusProvider{mgr: watchMgr})
		handler := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
		srv := daemon.NewServerWithWatch(handler, watchMgr)
		if err := srv.Start(*socket); err != nil {
			fmt.Fprintf(os.Stderr, "graphi: daemon start: %v\n", err)
			return 1
		}
		fmt.Printf("daemon listening on %s\n", *socket)
		// RUN-01 (ADR 0002 D5): wait until the server is STOPPED — by the
		// `daemon stop` RPC (which calls srv.Stop, closing Done) or an operator
		// signal — then RETURN, so the deferred cleanups (watcher StopAll, store
		// Close) actually run and the process exits. This replaces the former
		// `select {}` that parked the process forever after `stop`.
		sigCtx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		select {
		case <-srv.Done():
		case <-sigCtx.Done():
			_ = srv.Stop()
		}
		return 0
	case "stop":
		// The daemon's "stop" RPC already exists server-side (surfaces/daemon.
		// Server.dispatch acks, then handleConn tears down the listener + socket
		// file) — send it here instead of requiring the caller to kill the process.
		if err := daemon.NewClient(*socket, "").Stop(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "graphi: daemon stop: %v\n", err)
			return 1
		}
		fmt.Printf("daemon at %s: stopped\n", *socket)
		return 0
	case "status":
		c := daemon.NewClient(*socket, "")
		if _, err := c.Query(context.Background(), "callers", "", 0); err != nil {
			fmt.Printf("daemon at %s: not responding (%v)\n", *socket, err)
			return 1
		}
		fmt.Printf("daemon at %s: responding\n", *socket)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "graphi: unknown daemon command %q\n", cmd)
		return 1
	}
}

// runHTTP runs the read-only HTTP REST + SSE surface (SW-039). It serves the
// shared query/search/analysis client over loopback HTTP with a versioned
// envelope, and (optionally) indexes a repo with the broker attached so SSE
// clients receive ingest-completed freshness events. Local-first: the listen
// address MUST be loopback; the surface makes zero outbound connections.
//
//	graphi http [-addr 127.0.0.1:8080] [-db path] [-root repo]
func runHTTP(args []string) int {
	fs := flag.NewFlagSet("http", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:0", "loopback listen address (host must be 127.0.0.1/localhost/::1)")
	dbPath := fs.String("db", "", "SQLite graphstore path (empty = in-memory)")
	root := fs.String("root", "", "optional repo root to ingest on startup (attaches the ingest-event producer for SSE)")
	metaDir := fs.String("meta", "", "ingest meta sidecar dir; defaults to an OS temp dir when -root is set")
	profileFlag := fs.String("profile", "", "index profile: fast|balanced|deep (overrides GRAPHI_INDEX_PROFILE)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: http: %v\n", err)
		return 1
	}
	runCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	prof, err := profile.ResolveProfile(profileFlag, os.Getenv(profile.EnvName))
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}

	store, err := openStore(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()

	broker := observe.New()
	cleanupIngest := func() {}
	if *root != "" {
		// ingest meta needs a real dir: the ":memory:" path opens a private DB
		// per pooled connection (tables vanish), so default to a temp dir.
		meta := *metaDir
		if meta == "" {
			td, err := os.MkdirTemp("", "graphi-http-meta-*")
			if err != nil {
				fmt.Fprintf(os.Stderr, "graphi: meta temp dir: %v\n", err)
				return 1
			}
			meta = td
			cleanupIngest = func() { _ = os.RemoveAll(td) }
		}
		ing, ierr := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), meta)
		if ierr != nil {
			fmt.Fprintf(os.Stderr, "graphi: ingest: %v\n", ierr)
			return 1
		}
		ing.WithProfile(prof)
		ing.WithHeartbeatMode(ingest.HeartbeatNonTTY)
		ing.WithBroker(broker)
		cleanupIngest = func() { _ = ing.Close(); _ = os.RemoveAll(meta) }
		prog := newIngestProgress(os.Stderr, isTerminal(os.Stderr))
		ing.WithProgress(prog.Handle)
		ierr = ing.IngestAll(runCtx, *root)
		prog.Finish(ierr)
		if ierr != nil {
			fmt.Fprintf(os.Stderr, "graphi: initial ingest: %v\n", ierr)
			return 1
		}
	}
	defer cleanupIngest()

	asvc := analysis.NewDefaultService(store)
	c := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	ln, err := httpsrv.ListenLoopback(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}
	defer func() { _ = ln.Close() }()
	fmt.Printf("graphi http listening on %s (schema_version=%d)\n", ln.Addr(), httpsrv.SchemaVersion)
	// Inject the analyzer names so /contract can advertise them for client
	// capability negotiation without the http package importing engine/analysis.
	srv := httpsrv.New(c, broker).WithWiki(store).WithDescriptors(asvc.Names())
	if err := srv.ServeContext(runCtx, ln); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: http serve: %v\n", err)
		return 1
	}
	return 0
}
