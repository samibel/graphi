package retrieval

import (
	"context"
	"fmt"
	"math"
	"os"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/agenttools/contract"
	"github.com/samibel/graphi/engine/agenttools/hybridsearch"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/embed"
	"github.com/samibel/graphi/engine/search"
)

// HybridSearchBridge is the production LexicalProvider for engine/retrieval:
// it delegates the lexical candidate retrieval to engine/agenttools/hybridsearch
// rather than re-deriving its per-token + graph-expansion pipeline.
//
// Rationale (SW-263 review): AC-4 explicitly says to REUSE hybridsearch's
// audited integer signals; AC-7 demands byte parity with search_hybrid
// when no embedder is configured. Two independent reimplementations of
// the same candidate-expansion + scoring will drift; one shared
// implementation cannot. The retrieval module is a deep module over the
// two EXISTING seams — not a parallel implementation of either.
//
// The bridge converts search_hybrid's contract.Item rows into the
// retrieval.LexicalHit shape. The score (search_hybrid's audit score) is
// carried on LexicalHit.Score so the rerank stage can reuse it without
// recomputation when the semantic path is empty (the AC-7 byte-parity
// path); when the semantic path is active, the union + RRF + rerank
// stages fuse the lexical and semantic scores on top of this base.
type HybridSearchBridge struct {
	// Deps is the resolve.Deps hybridsearch.Search consumes directly. The
	// engine/agenttools/resolve package declares Retriever, RetrieverRequest
	// and RetrieverResult locally; it does NOT import engine/retrieval, so
	// engine/retrieval is free to import it. There is no import cycle, and
	// no adapter struct is needed.
	Deps resolve.Deps
	// WeightsHash is the hybridsearch weights hash echoed into the
	// retrieval's summary so the audit discipline carries through.
	WeightsHash string
}

// Search implements LexicalProvider by calling hybridsearch.Search and
// converting its results. The limit is the per-source top-k from the
// retrieval's candidate-union stage.
func (b *HybridSearchBridge) Search(ctx context.Context, query string, limit int) ([]LexicalHit, error) {
	if b == nil {
		return nil, fmt.Errorf("retrieval: HybridSearchBridge is nil")
	}
	if !b.Deps.Available() || b.Deps.Search == nil {
		return nil, fmt.Errorf("retrieval: hybridsearch bridge has no available deps")
	}
	res, err := hybridsearch.Search(ctx, hybridsearch.Params{
		Query:    query,
		MaxItems: limit,
		Deps:     b.Deps,
	})
	if err != nil {
		return nil, err
	}
	if res == nil || res.Outcome == contract.OutcomeUnavailable || res.Outcome == contract.OutcomeEmpty {
		return nil, nil
	}
	// Index evidence by RefID so we can hydrate path/line/kind/qualified_name
	// per item in one pass.
	evByID := make(map[string]contract.Evidence, len(res.Evidence))
	for _, ev := range res.Evidence {
		evByID[ev.RefID] = ev
	}
	// Resolve the underlying nodes in one batch lookup so the lexical hits
	// carry the full provenance (kind, qualified_name) the retrieval's
	// rerank stage inspects.
	ids := make([]model.NodeId, 0, len(res.Items))
	for _, it := range res.Items {
		ids = append(ids, model.NodeId(it.RefID))
	}
	var byID map[model.NodeId]model.Node
	if b.Deps.Query != nil {
		if lookup, ok := b.Deps.Query.Reader().(graphstore.GraphLookup); ok {
			if ns, err := lookup.NodesByID(ctx, ids); err == nil {
				byID = make(map[model.NodeId]model.Node, len(ns))
				for _, n := range ns {
					byID[n.ID()] = n
				}
			}
		}
	}
	out := make([]LexicalHit, len(res.Items))
	for i, it := range res.Items {
		h := LexicalHit{
			NodeID: it.RefID,
			Score:  it.Rank,
		}
		if ev, ok := evByID[it.EvidenceRefIDs[0]]; ok {
			h.Path = ev.Path
			h.Line = ev.Line
		}
		if n, ok := byID[model.NodeId(it.RefID)]; ok {
			h.Kind = n.Kind()
			h.QualifiedName = n.QualifiedName()
			if h.Path == "" {
				h.Path = n.SourcePath()
				h.Line = n.Line()
			}
			h.Column = n.Column()
		}
		out[i] = h
	}
	return out, nil
}

// SearchServiceBridge adapts engine/search.Service into the retrieval
// module's semantic provider. It is the one place the SW-263 retrieval
// module reads from the SW-059 lexical service and the SW-261 typed
// semantic state.
//
// The bridge carries the model and index fingerprints the retrieval's
// Summary must stamp on the configured path (SW-263 review / item 4). The
// Fingerprints() method exposes them to the retrieval module's typed
// summary; the runtime wires them up at composition time from the
// GenerationStore.Active fingerprint and the embedder's identity.
type SearchServiceBridge struct {
	Service          *search.Service
	State            embed.State
	Reason           string
	ModelFingerprint string
	IndexFingerprint string
}

// Fingerprints is the typed seam the retrieval module's semanticOutcome
// type-asserts on to populate Summary.ModelFingerprint and
// Summary.IndexFingerprint. Both return "" on the lexical-only path; the
// bridge therefore returns the configured values verbatim when set, and
// empty when unset (the legacy fixture path). The retrieval module does
// not interpret the strings — it stamps them so an audit reader can
// verify which embedding space served the row set.
func (b *SearchServiceBridge) Fingerprints() (model, index string) {
	if b == nil {
		return "", ""
	}
	return b.ModelFingerprint, b.IndexFingerprint
}

// Available reports whether the underlying service has a configured
// semantic path.
func (b *SearchServiceBridge) Available() bool {
	if b == nil || b.Service == nil {
		return false
	}
	resp, err := b.Service.SemanticSearch(context.Background(), "", 0)
	if err != nil {
		return false
	}
	return resp.Available
}

// Search implements SemanticProvider.
func (b *SearchServiceBridge) Search(ctx context.Context, query string, limit int) (SemanticOutcome, error) {
	if b == nil || b.Service == nil {
		return SemanticOutcome{Available: false, Reason: search.UnavailableReason, State: StateLexicalOnly}, nil
	}
	resp, err := b.Service.SemanticSearch(ctx, query, limit)
	if err != nil {
		return SemanticOutcome{}, err
	}
	if !resp.Available {
		state := stateFromReason(resp.Reason)
		return SemanticOutcome{Available: false, Reason: resp.Reason, State: state}, nil
	}
	hits := make([]SemanticHit, len(resp.Hits))
	for i, h := range resp.Hits {
		hits[i] = SemanticHit{
			NodeID:        h.NodeID,
			Kind:          h.Kind,
			QualifiedName: h.QualifiedName,
			Path:          h.SourcePath,
			Line:          h.Line,
			Column:        h.Column,
			DocumentID:    h.DocumentID,
			CosineScore:   h.Score,
		}
	}
	return SemanticOutcome{Available: true, Reason: "", State: StateReady, Hits: hits}, nil
}

// stateFromReason maps the SW-261 typed reason text back to the
// retrieval.State vocabulary.
//
// AC-7 lists the generation states (missing | stale | corrupt) plus
// the no-embedder case. The StateLexicalOnly value subsumes the
// configured-but-no-typed-state and the no-embedder cases into a
// single typed value, so a retrieval constructed at the unconfigured
// path (the default build) carries the one degradation state AC-7
// names, not a separate "no embedder" string. The remaining three
// states come straight from the SW-261 reason vocabulary.
func stateFromReason(reason string) State {
	switch reason {
	case search.ReasonUnavailable:
		return StateGenerationMissing
	case search.ReasonStale:
		return StateGenerationStale
	case search.ReasonCorrupt:
		return StateGenerationCorrupt
	case search.UnavailableReason:
		return StateLexicalOnly
	}
	return StateLexicalOnly
}

// QuantiseScore implements AC-3: cosine values are quantised to
// int(round(cos*10000)) BEFORE the semantic list is ordered.
func QuantiseScore(cos float64) int {
	if math.IsNaN(cos) || math.IsInf(cos, 0) {
		return 0
	}
	if cos > 1.0 {
		cos = 1.0
	} else if cos < -1.0 {
		cos = -1.0
	}
	scaled := cos * 10000
	if scaled >= 0 {
		return int(scaled + 0.5)
	}
	return -int(-scaled + 0.5)
}

// mustStderr is a tiny helper used only when constructing a retrieval
// instance from the runtime — keeps the file standalone-testable.
func mustStderr(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}
