package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/surfaces/cli"
	"github.com/samibel/graphi/surfaces/client"
	"github.com/samibel/graphi/surfaces/daemon"
	"github.com/samibel/graphi/surfaces/forge"
)

// runPrComment launches the SW-042 sticky PR-comment + merge-gate surface. Usage:
//
//	graphi pr-comment [-db path] [-daemon socket] -diff <unified-diff> | -diff-path <file>
//	  [-pr ref] [-provenance summary|full] [-gate] [-gate-threshold N] [-publish]
func runPrComment(args []string) int {
	dbPath, socket, rest := extractFlags(args)
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
	if err := cli.RunPrComment(context.Background(), c, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: pr-comment: %v\n", err)
		return 1
	}
	return 0
}

// runListPRs launches the SW-105 read-only forge PR-enumeration surface. Usage:
//
//	graphi list-prs [-db path] [-daemon socket]
//
// The forge client is resolved from the GitHub Actions environment
// (forge.FromEnv reads GITHUB_TOKEN/GITHUB_REPOSITORY/GITHUB_API_URL — never
// argv). Absent a token the forge is unwired and the surface reports the typed
// "forge unavailable" error (local-first: no network without explicit config).
func runListPRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunListPRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: list-prs: %v\n", err)
		return 1
	}
	return 0
}

// runTriagePRs launches the SW-105 single-pass graph-derived PR triage ranking.
// Usage: graphi triage-prs [-db path] [-daemon socket]. Enumeration is the only
// egress (forge.FromEnv); ranking is a zero-egress pass over the local graph.
func runTriagePRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunTriagePRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: triage-prs: %v\n", err)
		return 1
	}
	return 0
}

// runConflictsPRs launches the SW-106 inter-PR conflict detection. Usage:
// graphi conflicts-prs [-db path] [-daemon socket]. Enumeration is the only egress
// (forge.FromEnv); conflict detection is a zero-egress pass over the local graph.
func runConflictsPRs(args []string) int {
	dbPath, socket, _ := extractFlags(args)
	c := makeForgeClient(dbPath, socket)
	if c == nil {
		return 1
	}
	if err := cli.RunConflictsPRs(context.Background(), c, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: conflicts-prs: %v\n", err)
		return 1
	}
	return 0
}

// runSuggestReviewers launches the SW-107 reviewer recommender. Usage:
//
//	graphi suggest-reviewers [-db path] -diff <unified-diff> | -diff-path <file>
//
// The ranking is a zero-egress pass over the local graph + git history; the diff
// is local-first untrusted input resolved through the reused EP-007 kernel.
func runSuggestReviewers(args []string) int {
	dbPath, _, rest := extractFlags(args)
	fs := flag.NewFlagSet("suggest-reviewers", flag.ContinueOnError)
	diff := fs.String("diff", "", "unified diff or line-oriented refs of the change")
	diffPath := fs.String("diff-path", "", "read the diff/refs from this local file instead of -diff")
	if err := fs.Parse(rest); err != nil {
		return 1
	}
	payload := *diff
	if *diffPath != "" {
		b, err := os.ReadFile(*diffPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: suggest-reviewers: read diff: %v\n", err)
			return 1
		}
		payload = string(b)
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	c := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
	if err := cli.RunSuggestReviewers(context.Background(), c, payload, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: suggest-reviewers: %v\n", err)
		return 1
	}
	return 0
}

// runCompareBranches launches the SW-107 graph-level branch comparator. Usage:
//
//	graphi compare-branches -base <db-path> -head <db-path>
//
// Each ref is a path to an already-built graph state (a graphi SQLite snapshot).
// Materialization happens HERE, above the surface boundary, by opening each
// persisted state; the engine compare-branches analyzer receives the two read-only
// states and performs a pure local set-diff with ZERO egress (it never resolves a
// git ref). An unknown/empty ref materializes to an empty state (well-defined diff).
func runCompareBranches(args []string) int {
	fs := flag.NewFlagSet("compare-branches", flag.ContinueOnError)
	base := fs.String("base", "", "base branch graph-state path (graphi SQLite snapshot)")
	head := fs.String("head", "", "head branch graph-state path (graphi SQLite snapshot)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	m := newSnapshotMaterializer()
	defer m.Close()
	// The compare analyzer ignores the service reader (it diffs the two Params
	// states), so an empty in-memory store backs the service.
	store := graphstore.NewMemStore()
	c := client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store)).
		WithBranchStates(m)
	if err := cli.RunCompareBranches(context.Background(), c, *base, *head, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: compare-branches: %v\n", err)
		return 1
	}
	return 0
}

// runCritiqueReview launches the SW-108 review critique (the EP-018 capstone). Usage:
//
//	graphi critique-review [-db path] -diff <unified-diff>|-diff-path <file> \
//	  [-pr N] [-review <json>|-review-path <file>]
//
// The EXISTING review is supplied inline (-review/-review-path, read once HERE at the
// surface) or fetched from the forge for -pr via the net-new read-only review-fetch
// egress (resolved from the environment; unwired when no token). The critique itself
// is a ZERO-egress pass over the local graph: it replays the EP-007 oracle and runs
// the three-way gap/over_flag/unsupported_claim diff against the review.
func runCritiqueReview(args []string) int {
	dbPath, _, rest := extractFlags(args)
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	d := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(analysis.NewDefaultService(store))
	// Wire the net-new read-only review-fetch egress from the environment so a -pr
	// with no inline review can fetch the existing review. When no token is present
	// the fetcher stays unwired (local-first default) — an inline review still works.
	if gh, ferr := forge.FromEnv(); ferr == nil && gh != nil {
		d = d.WithReviewFetcher(gh)
	}
	// The CLI surface parses -diff/-diff-path/-pr/-review/-review-path and reads any
	// local files ONCE at the surface (no engine file I/O / remote fetch on the hot path).
	if err := cli.RunCritiqueReview(context.Background(), d, rest, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: critique-review: %v\n", err)
		return 1
	}
	return 0
}

// snapshotMaterializer is the cmd-side BranchStateMaterializer: it materializes a
// branch ref → read-only graph state by opening the persisted graphi state at that
// path (reusing the existing graphstore persistence path). An empty ref yields an
// empty in-memory state (a degenerate side → well-defined empty diff, no error).
// Opened stores are tracked and closed together after the single command runs, so
// both states stay live across the dispatch. This stays ABOVE the surface boundary;
// the engine never sees a ref.
type snapshotMaterializer struct {
	opened []graphstore.Graphstore
}

func newSnapshotMaterializer() *snapshotMaterializer { return &snapshotMaterializer{} }

func (m *snapshotMaterializer) StateForRef(_ context.Context, ref string) (query.Reader, error) {
	if strings.TrimSpace(ref) == "" {
		return graphstore.NewMemStore(), nil // empty/unknown ref → empty state
	}
	store, err := graphstore.OpenSQLite(ref)
	if err != nil {
		return nil, err
	}
	m.opened = append(m.opened, store)
	return store, nil
}

func (m *snapshotMaterializer) Close() {
	for _, s := range m.opened {
		_ = s.Close()
	}
}

// makeForgeClient builds an in-process client wired with the read-only forge
// PR-enumeration boundary (SW-105). The forge is resolved from the environment;
// when no token is present the forge stays unwired and the triage surface reports
// the typed unavailable error rather than dialing anything (local-first default).
func makeForgeClient(dbPath, socket string) client.Client {
	if socket != "" {
		// The daemon path has no forge RPC yet; it reports forge-unavailable.
		return daemon.NewClient(socket, "")
	}
	store, err := openStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: open store: %v\n", err)
		return nil
	}
	asvc := analysis.NewDefaultService(store)
	d := client.NewDirect(query.New(store), search.New(store)).WithAnalysis(asvc)
	if gh, ferr := forge.FromEnv(); ferr == nil && gh != nil {
		d = d.WithForge(gh)
	}
	return d
}
