// SW-264 AC-1 / AC-8 — search_hybrid/2.
//
// The /2 path renders engine/retrieval.Result rows in the same C1 contract
// shape the existing /1 path uses, so MCP and HTTP see a uniform envelope
// across versions and the only discriminator a reader of the bytes needs is
// the version flag on the request. When Deps.Retrieval is nil (the default
// build, no embedder configured) or reports a non-ready state, /2 falls back
// to today's /1 bytes verbatim — the SW-257 §7.2 byte identity is the AC-1
// fallback contract, and `degradation` in the summary tells the reader the
// fallback ran without them having to consult a separate log.
//
// The shipped default stays /1 (byte-identical to today); /2 is opt-in via a
// version flag on every surface (MCP / CLI / HTTP) and reaches the engine
// through the same engine/agenttools/hybridsearch package so neither tool
// imports the other (AC-5).
package hybridsearch

import (
	"context"
	"fmt"
	"strings"

	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/agenttools/shape"
)

// MethodVersionV2 is the audit stamp search_hybrid/2 prints in every summary,
// distinct from the /1 MethodVersion so a reader of the bytes can tell which
// path produced the result.
const MethodVersionV2 = "search_hybrid/2"

// SearchV2 is the AC-1 versioned entry point. It is the same C1 contract as
// Search, dispatched on Deps.Retrieval:
//   - Retrieval == nil: the default-build graceful skip — falls back to
//     Search's /1 bytes verbatim and stamps `degradation: lexical_only` on
//     the summary so a reader of the bytes can tell the fallback ran. No
//     error (AC-8).
//   - Retrieval reports a non-ready state (generation_missing / stale /
//     corrupt / no embedder): same fallback to /1 bytes with the typed
//     degradation stamped (AC-8).
//   - Retrieval reports a ready state: render the retrieval rows as items
//     with the explain breakdown in every reason and the summary fingerprints
//     (retrieval version, weights hash, model + index fingerprints, strategy).
//
// SearchV2 is the surface SW-264 consumes; nothing in production calls it
// without the version flag.
func SearchV2(ctx context.Context, p Params) (*contract.Result, error) {
	if strings.TrimSpace(p.Query) == "" {
		return nil, fmt.Errorf("missing query text")
	}
	if !p.Deps.Available() || p.Deps.Search == nil {
		return shape.Unavailable(tool), nil
	}
	if p.Deps.Retrieval == nil {
		// AC-1 / AC-8 fallback path: the default build does not wire retrieval,
		// so /2 must answer exactly the /1 bytes. Stamp the lexical_only
		// degradation on the summary so a reader of the bytes can tell the
		// fallback ran (AC-8) — without that stamp, /2 would be byte-identical
		// to /1 and the reader could not tell which version produced the
		// output from a single byte slice.
		res, err := Search(ctx, p)
		if err != nil {
			return nil, err
		}
		if res != nil && res.Outcome == contract.OutcomeFound {
			res.Summary = decorateWithDegradation(res.Summary, "lexical_only")
		}
		return res, nil
	}

	res, err := p.Deps.Retrieval.Retrieve(ctx, resolve.RetrieverRequest{Query: p.Query, Limit: limitFromParams(p)})
	if err != nil {
		return nil, err
	}
	if res.Degradation != "ready" {
		// AC-8 typed non-ready fallback: the retrieval module reports a
		// non-ready state (generation_missing / stale / corrupt / no
		// embedder). Fall back to /1 bytes and stamp the typed degradation
		// verbatim. No error.
		lex, err := Search(ctx, p)
		if err != nil {
			return nil, err
		}
		if lex != nil && lex.Outcome == contract.OutcomeFound {
			lex.Summary = decorateWithDegradation(lex.Summary, res.Degradation)
		}
		return lex, nil
	}

	return renderRetrievalRows(ctx, p, res)
}

// renderRetrievalRows projects engine/retrieval rows into the C1 contract
// shape: each row becomes one item whose reason carries the SW-263 explain
// breakdown, and one evidence citation carrying the row's path / span. The
// summary stamps every audit fingerprint the AC-1 / AC-11 list names.
//
// The render is intentionally narrow: the retrieval module owns the row
// payload, the dispatcher owns the strategy tag, and this function is the
// one place the two views are unified into the contract the surface serializes.
func renderRetrievalRows(ctx context.Context, p Params, res resolve.RetrieverResult) (*contract.Result, error) {
	items := make([]contract.Item, 0, len(res.Rows))
	ev := shape.NewEvidenceSet()
	for _, r := range res.Rows {
		line := lineFromSpan(r.Span)
		evID := ev.AddWithSpan(r.Path, line, "match", r.Span)
		items = append(items, contract.Item{
			RefID:          r.NodeID,
			Rank:           r.Final,
			Reason:         retrievalReason(r),
			EvidenceRefIDs: []string{evID},
		})
	}
	if len(items) == 0 {
		return shape.Empty(tool, p.Query), nil
	}
	summary := fmt.Sprintf(
		"search_hybrid/2: %d result(s) for %q — strategy %s; weights %s; model %s; index %s; %s",
		len(items), p.Query,
		res.Summary.Strategy,
		res.Summary.WeightsHash,
		res.Summary.ModelFingerprint,
		res.Summary.IndexFingerprint,
		MethodVersionV2,
	)
	return &contract.Result{
		Outcome:  contract.OutcomeFound,
		Summary:  summary,
		Items:    items,
		Evidence: ev.List(),
		Confidence: contract.Confidence{
			Distribution: map[string]float64{"semantic": 1},
			Top:          "semantic",
			Method:       "retrieval_v2",
		},
	}, nil
}

// retrievalReason renders one retrieval row's explain fields into the per-row
// reason. The breakdown names every AC-1 deliverable explicitly so a reader
// of the bytes can audit the ranking without consulting the source graph.
//
// The "region:" line is what SW-263 AC-11 calls the audit tag for how the
// row entered the result (semantic_prefix / lexical_backfill / lexical_only
// / lexical_path_override / fused). Stamping it on every row makes the v2
// output trace back to the strategy named in the summary.
func retrievalReason(r resolve.RetrieverRow) string {
	return fmt.Sprintf(
		"match: %s [%s:%s] final %d [lexical_rank %d, semantic_rank %d, rrf %d, graph %d, classification %d; region: %s]",
		r.NodeID, r.Path, r.Span, r.Final,
		r.Explain.LexicalRank, r.Explain.SemanticRank, r.Explain.RRF, r.Explain.Graph, r.Explain.Classification,
		regionName(r.Region),
	)
}

// regionName returns a human-readable label for the retrieval row's region
// tag. Empty / unknown values get a stable "unspecified" stamp so the bytes
// are reproducible even when the retrieval module adds new regions.
func regionName(region string) string {
	if region == "" {
		return "unspecified"
	}
	return region
}

// decorateWithDegradation appends a `degradation: <state>` stamp to a /1
// summary. It is the single place the AC-8 marker is added so a reader of the
// bytes can tell /2 ran the fallback path (vs /1, which never adds the
// marker).
//
// The append happens verbatim after a "; " separator; the /1 summary itself
// is preserved so the byte-identity check against the SW-257 golden still
// matches modulo this appended trailer. A test that needs the v1 golden
// byte-for-byte calls Search (not SearchV2) and so never sees this stamp.
func decorateWithDegradation(v1Summary, state string) string {
	if state == "" {
		return v1Summary
	}
	return v1Summary + "; degradation: " + state
}

// lineFromSpan parses the engine-owned "start-end" span string back to a
// 1-based line. It is the inverse of retrieval.spanFromLine and lives here
// so the shape package does not have to import retrieval for the conversion.
func lineFromSpan(s string) int {
	if s == "" {
		return 0
	}
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			n := 0
			for j := 0; j < i; j++ {
				c := s[j]
				if c < '0' || c > '9' {
					return 0
				}
				n = n*10 + int(c-'0')
			}
			return n
		}
	}
	return 0
}

// limitFromParams is the v2 path's effective item cap. A non-positive
// MaxItems selects the engine's DefaultMaxItems (20), mirroring Search's own
// behavior so a caller passing MaxItems=0 sees the same cap on both paths.
func limitFromParams(p Params) int {
	if p.MaxItems <= 0 {
		return DefaultMaxItems
	}
	return p.MaxItems
}
