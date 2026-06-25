package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/observe"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	httpsrv "github.com/samibel/graphi/surfaces/http"
)

// browserOpenCmd returns the platform command (and any fixed leading args) used
// to open a URL in the user's default browser. The URL itself is appended by
// the caller. This is a LOCAL subprocess launcher only — it never dials.
func browserOpenCmd(goos string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", nil
	case "windows":
		// `cmd /c start "" <url>` — the empty "" is the window-title arg `start`
		// consumes, so the URL is not mistaken for a title.
		return "cmd", []string{"/c", "start", ""}
	default:
		return "xdg-open", nil
	}
}

// isHeadless reports whether the environment looks like it has no usable
// desktop to pop a browser into. On darwin/windows we always assume a GUI is
// present. On other platforms (Linux/BSD) it is headless when neither an X11
// DISPLAY nor a WAYLAND_DISPLAY is set, OR when we are inside an SSH session.
func isHeadless(goos string, getenv func(string) string) bool {
	switch goos {
	case "darwin", "windows":
		return false
	default:
		if getenv("SSH_CONNECTION") != "" {
			return true
		}
		return getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == ""
	}
}

// setupZeroConfig wires the zero-config run WITHOUT serving. It detects the cwd
// repo (returning notRepo=true with no error when cwd is not a code repo),
// opens the auto-managed per-repo state DB (SW-068), incrementally ingests the
// repo through the existing engine/ingest path (unchanged, so the
// full-vs-incremental golden holds), and constructs the same loopback HTTP
// surface runHTTP builds. The caller is responsible for srv.Serve(ln) and for
// invoking cleanup. The construction shape mirrors runHTTP exactly.
func setupZeroConfig(cwd string) (srv *httpsrv.Server, ln net.Listener, url string, c client.Client, store graphstore.Graphstore, cleanup func(), notRepo bool, err error) {
	cleanup = func() {}
	root, ok := state.DetectRepo(cwd)
	if !ok {
		return nil, nil, "", nil, nil, cleanup, true, nil
	}

	p, perr := state.Resolve(cwd)
	if perr != nil {
		return nil, nil, "", nil, nil, cleanup, false, perr
	}
	if err = state.Ensure(p); err != nil {
		return nil, nil, "", nil, nil, cleanup, false, err
	}

	store, err = graphstore.OpenSQLite(p.DB)
	if err != nil {
		return nil, nil, "", nil, nil, cleanup, false, err
	}

	ing, ierr := ingest.New(store, parse.NewDefaultRegistry(), p.Meta)
	if ierr != nil {
		_ = store.Close()
		return nil, nil, "", nil, nil, cleanup, false, ierr
	}
	broker := observe.New()
	ing.WithBroker(broker)
	cleanup = func() { _ = ing.Close(); _ = store.Close() }

	if err = ing.IngestAll(context.Background(), root); err != nil {
		cleanup()
		cleanup = func() {}
		return nil, nil, "", nil, nil, cleanup, false, err
	}

	ln, err = httpsrv.ListenLoopback("127.0.0.1:0")
	if err != nil {
		cleanup()
		cleanup = func() {}
		return nil, nil, "", nil, nil, cleanup, false, err
	}

	asvc := analysis.NewDefaultService(store)
	c = client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	srv = httpsrv.New(c, broker).WithWiki(store).WithDescriptors(asvc.Names())
	url = "http://" + ln.Addr().String() + "/"
	return srv, ln, url, c, store, cleanup, false, nil
}

// runZeroConfig is the bare-`graphi` entrypoint (SW-067, EP-010 Task D). It
// indexes the cwd repo into its auto-managed state DB, serves the embedded UI
// on a free loopback port, opens the browser (or prints the URL when headless /
// --no-browser / $GRAPHI_NO_BROWSER), prints a savings readout, then blocks in
// Serve. Returns a process exit code.
func runZeroConfig() int {
	cwd, _ := os.Getwd()
	srv, ln, url, c, _, cleanup, notRepo, err := setupZeroConfig(cwd)
	defer cleanup()
	if notRepo {
		fmt.Fprintln(os.Stderr, "graphi: this directory doesn't look like a code repository.")
		fmt.Fprintln(os.Stderr, "  graphi indexes a repo (a directory with a .git, go.work, or go.mod marker)")
		fmt.Fprintln(os.Stderr, "  and serves a local code-intelligence UI on a loopback port.")
		fmt.Fprintln(os.Stderr, "  cd into a code repository and run `graphi` again, or run `graphi help`.")
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: %v\n", err)
		return 1
	}

	fmt.Printf("graphi: serving %s\n", url)

	// Savings readout — best-effort. On a fresh repo with no ledger yet,
	// RunSavings has nothing to print; surface a friendly baseline so the
	// headline "savings" line is never silently absent (EP-010 AC#5).
	if err := cli.RunSavings(context.Background(), c, os.Stdout, io.Discard); err != nil {
		fmt.Println("Saved $0.00 this session — savings accrue as you query graphi (e.g. via Claude Code).")
	}

	if shouldOpenBrowser(os.Args[1:]) {
		name, args := browserOpenCmd(runtime.GOOS)
		args = append(args, url)
		// Fire-and-forget LOCAL subprocess opening a 127.0.0.1 URL — not a dial.
		_ = exec.Command(name, args...).Start() //nolint:gosec // fixed launcher name + a loopback URL
		fmt.Printf("(open your browser at %s if it didn't pop up)\n", url)
	} else {
		fmt.Printf("(open your browser at %s)\n", url)
	}

	// First-run, consent-gated offer to connect every detected local MCP client
	// (SW-070 generalized, EP-010 Task G). The isTTY gate guarantees no read (and
	// no write) in non-TTY runs.
	maybeOfferClients(os.Stdin, os.Stdout, isStdinTTY(), clientsMemoPath(), detectClients, applyClients)

	if serr := srv.Serve(ln); serr != nil {
		fmt.Fprintf(os.Stderr, "graphi: serve: %v\n", serr)
		return 1
	}
	return 0
}

// shouldOpenBrowser decides whether to launch the browser. We suppress it on a
// headless environment, when GRAPHI_NO_BROWSER is set, or when a --no-browser
// flag is present.
func shouldOpenBrowser(args []string) bool {
	if isHeadless(runtime.GOOS, os.Getenv) {
		return false
	}
	if os.Getenv("GRAPHI_NO_BROWSER") != "" {
		return false
	}
	for _, a := range args {
		if a == "--no-browser" {
			return false
		}
	}
	return true
}
