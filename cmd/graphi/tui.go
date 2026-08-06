package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
	"github.com/samibel/graphi/surfaces/tui"
)

// runTUI launches the interactive terminal surface (SW-047). It consumes the
// shared Engine over the SW-044 HTTP/SSE surface via the loopback HTTP/SSE
// client adapter — NOT an in-process client — so the TUI reuses the single
// network-facing contract and stays byte-identical to the web/VS Code surfaces
// (parity by construction). Local-first: the target is loopback-only (fail
// closed); the TUI package itself imports no engine/* package.
//
//	graphi tui                                  # self-contained: own backend, free port
//	graphi tui -addr http://127.0.0.1:9000      # attach to a running `graphi http`
//
// Without -addr the command is SELF-CONTAINED: it starts the same HTTP/SSE
// server `graphi http` runs, on a kernel-assigned free port, points the TUI at
// it, and tears it down on exit.
//
// That default exists because the previous one could not work. The TUI used to
// default to port 8080 while `graphi http` defaults to :0 (a free port chosen
// by the kernel), so the two could never meet: a user had to start the server,
// read the port it printed, and pass it back by hand. Hard-coding a matching
// port on both sides would only trade that for a collision with whatever else
// holds 8080.
//
// An explicit -addr keeps the attach behaviour unchanged, which is what you
// want when the server is indexing a repository (`graphi http -root <repo>`)
// or is shared with the web/VS Code surfaces.
func runTUI(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	addr := fs.String("addr", "", "loopback HTTP/SSE engine address to attach to (empty = start a private backend on a free port)")
	dbPath := fs.String("db", "", "SQLite graphstore path for the private backend (ignored with -addr; empty = auto-managed session store)")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}

	target := *addr
	if target == "" {
		stop, resolved, err := startTUIBackend(*dbPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
			return 1
		}
		defer stop()
		target = resolved
	}

	// NewHTTP fails closed on a non-loopback target (mirrors
	// httpsrv.AssertLoopback), so the TUI can never be pointed at a remote
	// Engine — including through -addr.
	eng, err := client.NewHTTP(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}
	if err := tui.Run(context.Background(), eng, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: tui: %v\n", err)
		return 1
	}
	return 0
}

// startTUIBackend brings up a private HTTP/SSE server for one TUI session and
// returns its shutdown func plus the loopback URL to attach to.
//
// The port is ALWAYS kernel-assigned (":0"): a private backend has no reason to
// claim a well-known port, and asking the kernel is the only way to be certain
// the port is free. The chosen address is returned rather than printed, because
// nothing outside this process needs it.
//
// It reads the repository's auto-managed session store, exactly as the other
// read-only commands do, so `graphi tui` shows the graph the working directory
// already has. It does NOT index: bringing up a UI must never silently start a
// full ingest. An unindexed repository opens an empty graph — run `graphi sync`
// first, or attach to `graphi http -root <repo>` with -addr.
func startTUIBackend(dbOverride string) (stop func(), url string, err error) {
	db := dbOverride
	if db == "" {
		db, _ = resolveSession(getwd(), "", "")
	}
	store, err := openStore(db)
	if err != nil {
		return nil, "", fmt.Errorf("open store: %w", err)
	}

	ln, err := httpsrv.ListenLoopback("127.0.0.1:0")
	if err != nil {
		_ = store.Close()
		return nil, "", err
	}

	asvc := analysis.NewDefaultService(store)
	c := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	srv := httpsrv.New(c, observe.New()).WithWiki(store).WithDescriptors(asvc.Names())

	// The server owns the listener from here; ServeContext returns when the
	// context is cancelled, which the returned stop func does.
	srvCtx, cancel := context.WithCancel(context.Background())
	// Signals must reach the server too: a Ctrl-C that only tears down the TUI
	// would leave the backend holding the port until the process exits.
	sigCtx, stopSignals := signal.NotifyContext(srvCtx, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ServeContext(sigCtx, ln)
	}()

	stop = func() {
		stopSignals()
		cancel()
		<-done
		_ = store.Close()
	}
	return stop, "http://" + tuiHostPort(ln.Addr()), nil
}

// tuiHostPort renders the bound address for the client URL. A listener on
// "127.0.0.1:0" reports the concrete port the kernel assigned; the literal
// host is kept (rather than reformatted) so the loopback assertion in
// client.NewHTTP sees exactly what was bound.
func tuiHostPort(a net.Addr) string {
	if tcp, ok := a.(*net.TCPAddr); ok {
		return net.JoinHostPort(tcp.IP.String(), fmt.Sprint(tcp.Port))
	}
	return a.String()
}
