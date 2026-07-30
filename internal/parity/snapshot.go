package parity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/parityreport"
)

// THE ASSERTION UNIT, AND WHY IT IS THIS ONE.
//
// PRD FR-7 :832 asks for comparison over "Nodes, Edges, Evidence, Confidence,
// IDs und relevante Metadaten", and the Delta PRD adds source anchors. The
// portable snapshot envelope is strictly stronger than that enumeration:
// model.Graph.Marshal sorts nodes by NodeId and edges by EdgeId and emits every
// one of those fields canonically, so BYTE parity over the envelope subsumes the
// field-by-field comparison. That is why the field walk is deliberately NOT
// re-implemented here.
//
// WHY THE STORE FILE IS NOT THE UNIT — measured, not assumed. Two full indexes
// of a byte-identical tree produce DIFFERENT .db bytes, and not merely because
// of SQLite page layout: kv_meta carries index.full_ingest_generation, a fresh
// random id minted on every full pass (observed as three distinct values over
// three passes of one unchanged tree). Byte-comparing store files can therefore
// never work, in principle. The envelope is the only canonical artifact.
//
// HOW THIS STAYS INSIDE THE AMENDED AC-1 BOUNDARY. core/graphstore is imported
// for exactly two calls — open a store file read-only, and write its envelope.
// No ingest runs in this process: every graph in this harness is produced by the
// graphi binary as a SUBPROCESS. The safety property was never "import nothing";
// it is "the harness cannot perturb what it measures", and that is about
// in-process INGEST, not in-process comparison. The denylist test in this
// package asserts the distinction mechanically, in both the normal and the
// -test dependency sets.

// emitSnapshot opens a store file and writes its portable snapshot envelope to
// outPath. The store is opened READ-ONLY: the harness must not be able to alter
// the artifact it is about to measure, even by accident.
func emitSnapshot(dbPath, outPath string) error {
	st, err := graphstore.OpenSQLiteReadOnly(dbPath)
	if err != nil {
		return fmt.Errorf("parity: open store %q read-only: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	if err := st.Snapshot(context.Background(), outPath); err != nil {
		return fmt.Errorf("parity: snapshot %q: %w", dbPath, err)
	}
	return nil
}

// envelope mirrors core/graphstore's portable snapshot container. It is decoded
// with local structs rather than by calling back into graphstore, because the
// only thing the harness needs from the payload is a node/edge enumeration for
// the two PRD §12.3 store-level counts — and decoding what was already written
// keeps the counts and the assertion over literally the same bytes.
type envelope struct {
	Magic              string          `json:"magic"`
	FormatVersion      int             `json:"format_version"`
	ModelSchemaVersion uint32          `json:"model_schema_version"`
	Graph              json.RawMessage `json:"graph"`
}

type graphPayload struct {
	Nodes []struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		QualifiedName string `json:"qualified_name"`
		SourcePath    string `json:"source_path"`
	} `json:"nodes"`
	Edges []struct {
		ID   string `json:"id"`
		From string `json:"from"`
		To   string `json:"to"`
		Kind string `json:"kind"`
	} `json:"edges"`
}

// readGraph decodes a snapshot envelope written by emitSnapshot.
func readGraph(path string) (graphPayload, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return graphPayload{}, nil, fmt.Errorf("parity: read snapshot %q: %w", path, err)
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return graphPayload{}, raw, fmt.Errorf("parity: parse snapshot envelope %q: %w", path, err)
	}
	var g graphPayload
	if err := json.Unmarshal(env.Graph, &g); err != nil {
		return graphPayload{}, raw, fmt.Errorf("parity: parse snapshot graph %q: %w", path, err)
	}
	return g, raw, nil
}

// digest renders the sha256 of the compared bytes, so a report row carries a
// verifiable identity for each side rather than only a boolean.
func digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// storeCounts computes the two PRD §12.3 store-level counts over a REAL
// repository graph.
//
// Both are counted over the actual graph, deliberately NOT inferred from the
// fixture-level proofs at engine/ingest/link_external_lifecycle_e2e_test.go:29
// and link_cascade_test.go:118. A synthetic proof that the sweep works is not a
// measurement that the sweep left nothing behind on 36 real files.
//
//	orphaned external nodes — a node of kind "external" is an INTERNED reference
//	  to a symbol outside the module. It exists only to be pointed at, so an
//	  external node with no incident edge is one the sweep failed to collect.
//	stale linker edges — an edge whose from or to endpoint is not a node of the
//	  graph. The snapshot is a closed world, so a dangling endpoint is a linker
//	  edge that outlived its node.
func storeCounts(repo, class string, g graphPayload) parityreport.StoreCounts {
	ids := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	incident := make(map[string]bool, len(g.Nodes))
	sc := parityreport.StoreCounts{Repo: repo, Class: class}
	for _, e := range g.Edges {
		incident[e.From] = true
		incident[e.To] = true
		if !ids[e.From] || !ids[e.To] {
			sc.StaleLinkerEdges++
			if len(sc.StaleSample) < 10 {
				sc.StaleSample = append(sc.StaleSample,
					fmt.Sprintf("%s %s -> %s (missing endpoint)", e.Kind, e.From, e.To))
			}
		}
	}
	var orphans []string
	for _, n := range g.Nodes {
		if n.Kind == externalNodeKind && !incident[n.ID] {
			sc.OrphanedExternalNodes++
			orphans = append(orphans, n.QualifiedName)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 10 {
		orphans = orphans[:10]
	}
	sc.OrphanSample = orphans
	sc.Pass = sc.OrphanedExternalNodes == 0 && sc.StaleLinkerEdges == 0
	return sc
}

// externalNodeKind is the node kind ingest assigns to an interned reference to a
// symbol outside the module (engine/ingest/linkfiles.go:239 tests the same
// literal). It is compared as a WIRE VALUE read out of the snapshot, not
// imported as a constant — the harness reads the artifact, it does not share the
// producer's vocabulary.
const externalNodeKind = "external"
