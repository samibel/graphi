package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// strictTierRank is the closed confidence order the strict filter admits
// against: lower rank = more trustworthy (mirrors engine/query/compare.go).
// A tier outside this set is never admitted — a strict filter fails closed.
var strictTierRank = map[string]int{
	"confirmed": 0,
	"derived":   1,
	"heuristic": 2,
}

// runQueryStrict is the Labs strict-query wrapper (PRD §28 option A): the
// stable query runs unchanged underneath, then result edges below -min-tier
// are excluded and the result is wrapped in an envelope that carries the
// exclusion count. Usage:
//
//	graphi query-strict <operation> -symbol <id> [-depth n] \
//	    [-min-tier confirmed|derived|heuristic] \
//	    [-policy exploratory|review|automated_change] [-db path]
//
// With -policy, a fail-closed trust preflight runs first: a FAIL or UNKNOWN
// verdict prints one explaining line and exits with the trust exit code —
// the query itself never runs on untrustworthy evidence. PASS and WARN
// proceed, with the verdict recorded in the envelope.
//
// The red gate this surface exists for (PRD §28): a result emptied by the
// filter is never presented as bare emptiness — whenever edges were
// excluded, the envelope carries an explicit limitation naming the count.
// Exit codes: 0 success; 1 query/store error; 2 input error; a blocked
// preflight exits with the trust code (3 FAIL, 4 UNKNOWN).
func runQueryStrict(args []string) int {
	return runQueryStrictAt(getwd(), args, os.Stdout)
}

// strictEnvelope is the query-strict wire document. Limitations is always
// present and never null; the wrapped result keeps the canonical query.Result
// shape verbatim (edges filtered, provenance untouched — no tier is ever
// rewritten).
type strictEnvelope struct {
	Operation string       `json:"operation"`
	Result    query.Result `json:"result"`
	Filter    struct {
		MinimumTier   string `json:"minimum_tier"`
		ExcludedEdges int    `json:"excluded_edges"`
	} `json:"filter"`
	Trust struct {
		PreflightVerdict string `json:"preflight_verdict"`
		SnapshotState    string `json:"snapshot_state"`
	} `json:"trust"`
	Limitations []string `json:"limitations"`
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
	policy := fs.String("policy", "", "optional trust policy preflight (exploratory|review|automated_change)")
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
		fmt.Fprintln(os.Stderr, "graphi: query-strict: -policy requires a value (exploratory|review|automated_change)")
		return 2
	}
	if *symbol == "" {
		fmt.Fprintln(os.Stderr, "graphi: query-strict: -symbol is required")
		return 2
	}
	minRank, ok := strictTierRank[*minTier]
	if !ok {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: invalid -min-tier %q (confirmed|derived|heuristic)\n", *minTier)
		return 2
	}

	ctx := context.Background()
	preVerdict, preState := "", ""
	if *policy != "" {
		root, okRepo := state.DetectRepo(cwd)
		if !okRepo {
			printNotARepo("query-strict")
			return 2
		}
		// The preflight must judge the SAME store the query runs against: an
		// explicit -db/-meta is forwarded verbatim, so a PASS minted on the
		// auto-managed store can never certify a query over another store.
		_, verdict, st, err := client.TrustReport(ctx, client.TrustReportOptions{
			Root: root, DBPath: dbPath, MetaDir: metaDir, Policy: *policy,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
			return 2
		}
		if verdict != trust.VerdictPass && verdict != trust.VerdictWarn {
			// Fail-closed preflight: FAIL and UNKNOWN block the query — running
			// it would dress untrustworthy evidence up as an answer.
			fmt.Fprintf(os.Stderr, "graphi: query-strict: policy %s verdict %s (snapshot %s) — query not executed\n",
				*policy, verdict, st)
			return trustExitCode(true, verdict, st)
		}
		preVerdict, preState = string(verdict), string(st)
	}

	if dbPath == "" && socket == "" {
		dbPath, socket = resolveSession(cwd, "", "")
	}
	c, cleanup := makeClientOrOpen(dbPath, socket)
	if c == nil {
		return 1
	}
	defer cleanup()
	if socket != "" {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: not available via daemon in this build\n")
		return 1
	}
	raw, err := c.Query(ctx, op, *symbol, *depth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 1
	}
	var res query.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: decode result: %v\n", err)
		return 1
	}

	kept := res.Edges[:0:0]
	excluded := 0
	for _, e := range res.Edges {
		if r, known := strictTierRank[string(e.Tier)]; known && r <= minRank {
			kept = append(kept, e)
			continue
		}
		excluded++
	}
	res.Edges = kept
	if excluded > 0 {
		// Drop nodes no longer justified by a surviving edge; the queried
		// symbol itself stays so the result remains anchored.
		used := map[string]bool{string(res.Symbol): true}
		for _, e := range res.Edges {
			used[string(e.From)] = true
			used[string(e.To)] = true
		}
		nodes := res.Nodes[:0:0]
		for _, n := range res.Nodes {
			if used[string(n.ID)] {
				nodes = append(nodes, n)
			}
		}
		res.Nodes = nodes
	}
	if res.Nodes == nil {
		res.Nodes = []query.ResultNode{}
	}
	if res.Edges == nil {
		res.Edges = []query.ResultEdge{}
	}

	env := strictEnvelope{Operation: op, Result: res, Limitations: []string{}}
	env.Filter.MinimumTier = *minTier
	env.Filter.ExcludedEdges = excluded
	env.Trust.PreflightVerdict = preVerdict
	env.Trust.SnapshotState = preState
	if excluded > 0 {
		env.Limitations = append(env.Limitations,
			fmt.Sprintf("%d edges below the %s tier were excluded — emptiness is filtered, not proven", excluded, *minTier))
	}

	buf := &bytes.Buffer{}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 1
	}
	if _, err := stdout.Write(append(bytes.TrimRight(buf.Bytes(), "\n"), '\n')); err != nil {
		fmt.Fprintf(os.Stderr, "graphi: query-strict: %v\n", err)
		return 1
	}
	return 0
}
