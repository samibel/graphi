package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// runQueryStrict is the Labs strict-query wrapper (PRD §28 option A): the
// stable query runs unchanged underneath, then result edges below -min-tier
// are excluded and the result is wrapped in an envelope that carries the
// exclusion count. Usage:
//
//	graphi query-strict <operation> -symbol <id> [-depth n] \
//	    [-min-tier confirmed|derived|heuristic] \
//	    [-policy exploratory-v1|review-v1|automated-change-v1] [-db path]
//
// With -policy, a fail-closed trust preflight runs first: a FAIL or UNVERIFIED
// verdict prints one explaining line and exits with the trust exit code —
// the query itself never runs on untrustworthy evidence. PASS and WARN
// proceed, with the verdict recorded in the envelope.
//
// The red gate this surface exists for (PRD §28): a result emptied by the
// filter is never presented as bare emptiness — whenever edges were
// excluded, the envelope carries an explicit limitation naming the count.
// Exit codes: 0 success; 1 query/store error; 2 input error; a blocked
// preflight exits with the trust code, which PRD v1.0 §6 makes 2 for both
// FAIL and UNVERIFIED (delta doc §A3).
//
// The filtering, the envelope and its encoder live in surfaces/client
// (ComposeStrictQuery), shared byte-for-byte with the strict_query MCP tool.
// This function owns flag parsing, client wiring and exit codes only.
func runQueryStrict(args []string) int {
	return runQueryStrictAt(getwd(), args, os.Stdout)
}

func runQueryStrictAt(cwd string, args []string, stdout io.Writer) int {
	dbPath, socket, metaDir, rest := extractFlagsMeta(args)
	if len(rest) < 1 || rest[0] == "" || rest[0][0] == '-' {
		fmt.Fprintln(os.Stderr, "usage: graphi query-strict <operation> -symbol <id> [-depth n] [-min-tier confirmed|derived|heuristic] [-policy <name>]")
		return 2
	}
	op := rest[0]
	fs := flag.NewFlagSet("query-strict", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	symbol := fs.String("symbol", "", "symbol (node) id to query")
	depth := fs.Int("depth", 1, "neighborhood hop depth (ignored by other operations)")
	minTier := fs.String("min-tier", "heuristic", "lowest confidence tier admitted into the result")
	policy := fs.String("policy", "", "optional trust policy preflight ("+strings.Join(trust.PolicyIDs(), "|")+")")
	if err := fs.Parse(rest[1:]); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		// The stdlib flag parser stops at the first positional token, so a
		// stray argument (or an explicit "--") would silently drop every flag
		// after it — including -policy and -min-tier, i.e. the trust posture
		// of the whole run. Leftover positionals are an input error, never a
		// silent downgrade.
		fmt.Fprintf(os.Stderr, "graphi: query-strict: unexpected argument %q — flags after it would be ignored\n", fs.Arg(0))
		return 2
	}
	policySet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "policy" {
			policySet = true
		}
	})
	if policySet && *policy == "" {
		// An explicitly empty -policy must not silently mean "no preflight":
		// the caller asked for a gate and named none.
		fmt.Fprintln(os.Stderr, "graphi: query-strict: -policy requires a value ("+strings.Join(trust.PolicyIDs(), "|")+")")
		return 2
	}
	if *symbol == "" {
		fmt.Fprintln(os.Stderr, "graphi: query-strict: -symbol is required")
		return 2
	}
	if _, ok := client.StrictTierRank[*minTier]; !ok {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: invalid -min-tier %q (confirmed|derived|heuristic)\n", *minTier)
		return 2
	}

	root := ""
	if *policy != "" {
		detected, okRepo := state.DetectRepo(cwd)
		if !okRepo {
			printNotARepo("query-strict")
			return 2
		}
		root = detected
	}

	ctx := context.Background()
	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(cwd, "", "")
	}
	if socket != "" {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: not available via daemon in this build\n")
		return 1
	}
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()

	doc, verdict, st, err := client.ComposeStrictQuery(ctx, c, client.StrictQueryOptions{
		Operation: op, Symbol: *symbol, Depth: *depth, MinimumTier: *minTier,
		// The preflight must judge the SAME store the query runs against: an
		// explicit -db/-meta is forwarded verbatim, so a PASS minted on the
		// auto-managed store can never certify a query over another store.
		Policy: *policy, Root: root, DBPath: dbPath, MetaDir: metaDir,
	})
	switch {
	case errors.Is(err, client.ErrStrictQueryBlocked):
		fmt.Fprintf(os.Stderr, "graphi: query-strict: policy %s verdict %s (snapshot %s) — query not executed\n",
			*policy, verdict, st)
		return trustExitCode(true, verdict, st)
	case errors.Is(err, client.ErrStrictQueryInput):
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 2
	case errors.Is(err, trust.ErrPolicyUnknown):
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 2
	case err != nil:
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 1
	}

	if _, err := stdout.Write(append(doc, '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 1
	}
	return 0
}
