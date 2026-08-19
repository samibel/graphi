package client

// This file is the SHARED strict-query composition (P1 Labs, PRD v1.0 §8 Phase
// 9): the ONE place a stable query result is tier-filtered and wrapped in the
// strict envelope. It follows the trust-report template — surface options ->
// one composition -> canonical bytes — so `graphi query-strict` and the
// `strict_query` MCP tool emit byte-identical documents for the same inputs.
//
// The stable query runs UNCHANGED underneath. Nothing here rewrites a tier,
// re-ranks a result, or touches a Stable-12 schema; filtering happens strictly
// after the canonical result is produced, and what was removed is reported
// rather than absorbed.
//
// The red gate this surface exists for: a result emptied BY THE FILTER must
// never read as a proven absence. Whenever edges were excluded the envelope
// carries an explicit limitation naming the count, so "no callers" and "no
// callers you asked to see" stay distinguishable.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"

	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/trust"
)

// StrictTierRank is the closed confidence order the strict filter admits
// against: lower rank = more trustworthy (mirrors engine/query/compare.go). A
// tier outside this set is never admitted — a strict filter fails closed.
var StrictTierRank = map[string]int{
	"confirmed": 0,
	"derived":   1,
	"heuristic": 2,
}

// Typed outcomes so every surface maps the same situation to the same code
// without re-parsing the document.
var (
	// ErrStrictQueryInput marks a caller mistake: unknown minimum tier, empty
	// symbol, empty operation. CLI exit 2 / MCP -32602.
	ErrStrictQueryInput = errors.New("client: strict query input")
	// ErrStrictQueryBlocked marks a policy preflight that refused to let the
	// query run. The returned verdict and state explain it; no result is
	// produced, because producing one would hand back evidence the policy just
	// judged unusable.
	ErrStrictQueryBlocked = errors.New("client: strict query blocked by policy preflight")
)

// StrictQueryOptions is the transport-agnostic input. Both surfaces construct
// the SAME options so the one composition receives identical inputs (parity by
// construction).
//
// Root/DBPath/MetaDir locate the repository's graph state for the optional
// trust preflight, exactly as TrustReportOptions does. The CLI forwards an
// explicit -db/-meta verbatim so a PASS minted on one store can never certify a
// query over another; MCP leaves them empty and the composition resolves the
// server process's own repository.
type StrictQueryOptions struct {
	Operation   string `json:"operation"`
	Symbol      string `json:"symbol"`
	Depth       int    `json:"depth"`
	MinimumTier string `json:"minimum_tier"`
	Policy      string `json:"policy"`

	Root    string `json:"root"`
	DBPath  string `json:"db_path"`
	MetaDir string `json:"meta_dir"`
}

// StrictEnvelope is the query-strict wire document. Limitations is always
// present and never null; the wrapped result keeps the canonical query.Result
// shape verbatim (edges filtered, provenance untouched — no tier is ever
// rewritten).
type StrictEnvelope struct {
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

// ComposeStrictQuery runs the optional trust preflight, then the stable query,
// then the tier filter, and returns the canonical envelope bytes plus the
// preflight verdict and snapshot state (both zero when no policy was
// requested) so a surface maps its outcome without re-parsing JSON.
//
// Order is load-bearing: the preflight runs BEFORE the query. A policy that
// refuses must prevent the query, not annotate its result — otherwise an agent
// holding a plausible-looking result list has to be trusted to honour a verdict
// buried inside it.
func ComposeStrictQuery(ctx context.Context, q QueryPort, opts StrictQueryOptions) ([]byte, trust.Verdict, trust.State, error) {
	if opts.Operation == "" {
		return nil, "", "", fmt.Errorf("%w: operation is required", ErrStrictQueryInput)
	}
	if opts.Symbol == "" {
		return nil, "", "", fmt.Errorf("%w: symbol is required", ErrStrictQueryInput)
	}
	minTier := opts.MinimumTier
	if minTier == "" {
		minTier = "heuristic"
	}
	minRank, ok := StrictTierRank[minTier]
	if !ok {
		return nil, "", "", fmt.Errorf("%w: invalid minimum tier %q (confirmed|derived|heuristic)", ErrStrictQueryInput, minTier)
	}
	depth := opts.Depth
	if depth <= 0 {
		depth = 1
	}

	var verdict trust.Verdict
	var state trust.State
	if opts.Policy != "" {
		_, v, st, err := composeTrustReport(ctx, TrustReportOptions{
			Root: opts.Root, DBPath: opts.DBPath, MetaDir: opts.MetaDir, Policy: opts.Policy,
		})
		if err != nil {
			return nil, "", "", err
		}
		verdict, state = v, st
		if v != trust.VerdictPass && v != trust.VerdictWarn {
			// Fail-closed preflight: FAIL and UNVERIFIED block the query —
			// running it would dress untrustworthy evidence up as an answer.
			return nil, v, st, fmt.Errorf("%w: policy %s verdict %s (snapshot %s)", ErrStrictQueryBlocked, opts.Policy, v, st)
		}
	}

	if q == nil {
		return nil, verdict, state, ErrAnalysisUnavailable
	}
	raw, err := q.Query(ctx, opts.Operation, opts.Symbol, depth)
	if err != nil {
		return nil, verdict, state, err
	}
	var res query.Result
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, verdict, state, fmt.Errorf("client: strict query decode result: %w", err)
	}

	res, excluded := filterByTier(res, minRank)

	env := StrictEnvelope{Operation: opts.Operation, Result: res, Limitations: []string{}}
	env.Filter.MinimumTier = minTier
	env.Filter.ExcludedEdges = excluded
	env.Trust.PreflightVerdict = string(verdict)
	env.Trust.SnapshotState = string(state)
	if excluded > 0 {
		env.Limitations = append(env.Limitations,
			fmt.Sprintf("%d edges below the %s tier were excluded — emptiness is filtered, not proven", excluded, minTier))
	}
	// Legible abstention (W0.g). The tier filter above reports what THIS
	// SURFACE withheld; this reports what the semantic binder refused to bind
	// in the first place. Both are ways a result can be smaller than the truth,
	// and a surface that reports only the first still hands back a confident
	// list with a silent gap behind it.
	//
	// Gated on the packages the result covers, so a repository whose binders
	// never abstained stays quiet — and read AFTER the filter so an empty
	// result is recognized as empty here.
	//
	// A NOT-FOUND result is excluded entirely, including from the
	// unavailability notice: the symbol never resolved, so there is no package
	// the abstention could be about and no claim about it to qualify. Warning
	// there would be noise, and noise on every miss is how a real notice stops
	// being read.
	if res.Found() {
		env.Limitations = append(env.Limitations,
			abstentionLimitations(
				readPackageAbstention(ctx, opts.Root, opts.DBPath, opts.MetaDir, resultPackages(res), res.Symbol),
				len(res.Edges) == 0)...)
	}

	b, err := encodeStrictEnvelope(env)
	if err != nil {
		return nil, verdict, state, err
	}
	return b, verdict, state, nil
}

// resultPackages returns the distinct package keys (repo-relative source
// directories, "." for the root — the unit key space the evidence rows use)
// the result's nodes live in, sorted. Empty when the result anchors nothing:
// a not-found result covers no package and must not attract an abstention
// notice, because there is no scope for one to be about.
func resultPackages(res query.Result) []string {
	seen := map[string]struct{}{}
	for _, n := range res.Nodes {
		if n.SourcePath == "" {
			continue
		}
		seen[path.Dir(n.SourcePath)] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// filterByTier drops edges below minRank and the nodes no longer justified by a
// surviving edge, returning the trimmed result and the excluded-edge count. An
// unknown tier is never admitted (fail closed).
func filterByTier(res query.Result, minRank int) (query.Result, int) {
	kept := res.Edges[:0:0]
	excluded := 0
	for _, e := range res.Edges {
		if r, known := StrictTierRank[string(e.Tier)]; known && r <= minRank {
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
	return res, excluded
}

// encodeStrictEnvelope is the ONE encoder for this document, following the
// repository's canonical-JSON recipe (HTML escaping off, no indentation,
// trailing newline stripped) so both surfaces emit identical bytes.
func encodeStrictEnvelope(env StrictEnvelope) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(env); err != nil {
		return nil, fmt.Errorf("client: encode strict query: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
