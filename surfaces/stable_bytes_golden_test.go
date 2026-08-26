package surfaces_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/internal/goldenfile"
	"github.com/samibel/graphi/surfaces/client"
)

// AX-00 (SW-220) AC-3 — canonical result bytes for the read-only Stable
// operations, recorded as committed artifacts.
//
// TestCharacterization_TwelveOps_ByteStableAcrossStoreConditions already proves
// the ops are REPRODUCIBLE (warm cache == rebuilt cache == fresh re-index) and
// TestCharacterization_TwelveOps_MemoryVsSQLiteConformance proves the two
// backends AGREE. Both compare a run against another run in the same process.
// Neither records WHAT the bytes are, so both would stay green if the strangler
// refactor changed every answer in exactly the same way everywhere — which is
// precisely the failure mode a generic executor (AX-04) and derived projections
// (AX-05) can introduce.
//
// This file closes that: the canonical bytes of each read-only Stable operation
// over the committed corpus/fixtures/go fixture are written to
// surfaces/testdata/stable-ops/ and compared byte-for-byte on every run.
//
// Scope note: the twelfth Stable operation, `index`, is a LIFECYCLE operation,
// not a read-only one, and its canonical output is the whole graph. It is out of
// scope for this AC by its wording ("read-only Stable operations"); it stays
// covered by the graph-level determinism assertions in
// characterization_golden_test.go, and its shape is recorded here only as a
// digest in the manifest so a graph-level change is still visible.
//
// The artifacts are the RAW canonical bytes — not re-indented — because the
// canonical serialization itself is the thing being frozen; pretty-printing
// would forgive a formatting change a client can observe. The manifest beside
// them carries the request that produced each artifact plus a sha256, so a
// reviewer can see at a glance which answers moved.
//
// Regeneration is explicit:
//
//	GRAPHI_UPDATE_GOLDEN=1 go test ./surfaces -run TestAX00

// readOnlyStableOps is the eleven read-only members of the frozen twelve, in
// canonical order. `index` is deliberately absent (see the file comment).
var readOnlyStableOps = []string{
	"agent_brief", "callees", "callers", "change_risk", "definition",
	"explain_symbol", "impact", "neighborhood", "references", "related_files",
	"search",
}

// stableOpsGoldenDir is where the committed canonical-byte artifacts live.
func stableOpsGoldenDir() string { return filepath.Join("testdata", "stable-ops") }

// manifestEntry records how one artifact was produced and what it hashes to, so
// the golden bytes are auditable without decoding them.
type manifestEntry struct {
	Operation string `json:"operation"`
	Request   string `json:"request"`
	Artifact  string `json:"artifact"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
}

type stableOpsManifest struct {
	Note        string          `json:"note"`
	Fixture     string          `json:"fixture"`
	Backend     string          `json:"backend"`
	OpCount     int             `json:"operation_count"`
	Operations  []manifestEntry `json:"operations"`
	GraphDigest manifestEntry   `json:"graph_digest"`
}

// runReadOnlyStableOps drives every read-only Stable operation through the same
// shared surface client the shipped binary uses and returns op → (request
// description, canonical bytes). Symbol anchors are resolved by canonical id
// (findFuncID sorts by NodeId), never by map order, so the recorded request is
// itself deterministic.
func runReadOnlyStableOps(t *testing.T, store graphstore.Graphstore) (map[string]string, map[string][]byte) {
	t.Helper()
	ctx := context.Background()
	c := charClient(store)

	hello := findFuncID(t, store, "Hello")
	chainA := findFuncID(t, store, "ChainA")

	requests := map[string]string{}
	payloads := map[string][]byte{}
	record := func(op, request string, b []byte, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("op %s: %v", op, err)
		}
		if len(b) == 0 {
			t.Fatalf("op %s produced no canonical bytes — a golden of nothing proves nothing", op)
		}
		requests[op] = request
		payloads[op] = b
	}

	sb, err := c.Search(ctx, "Hello", 20)
	record("search", `Search(query="Hello", limit=20)`, sb, err)

	// Anchors are chosen so the recorded bytes are REPRESENTATIVE rather than
	// merely present: Hello is called (by ChainC and EnglishGreeter.Greet) but
	// calls nothing, so `callees` is anchored on ChainA — which calls ChainB —
	// instead. `references` stays on Hello and records the `empty` outcome
	// honestly: the pinned Go fixture produces no reference edges, and the empty
	// envelope (outcome + empty node/edge arrays) is itself contractual wire
	// shape that a refactor must not reshape.
	for _, op := range []string{"definition", "callers", "references"} {
		b, err := c.Query(ctx, op, hello, 0)
		record(op, fmt.Sprintf("Query(op=%q, symbol=<Hello>, depth=0)", op), b, err)
	}
	cb, err := c.Query(ctx, "callees", chainA, 0)
	record("callees", `Query(op="callees", symbol=<ChainA>, depth=0)`, cb, err)

	nb, err := c.Query(ctx, "neighborhood", chainA, 2)
	record("neighborhood", `Query(op="neighborhood", symbol=<ChainA>, depth=2)`, nb, err)

	ib, err := c.Analyze(ctx, client.AnalyzeParams{Name: "impact", Symbol: hello})
	record("impact", `Analyze(name="impact", symbol=<Hello>)`, ib, err)

	bj, _, err := c.Brief(ctx, "Hello")
	record("agent_brief", `Brief(topic="Hello") — JSON half`, bj, err)

	rf, err := c.RelatedFiles(ctx, chainA, "both", 10)
	record("related_files", `RelatedFiles(target=<ChainA>, direction="both", limit=10)`, rf, err)

	ex, err := c.ExplainSymbol(ctx, hello, 20)
	record("explain_symbol", `ExplainSymbol(symbol=<Hello>, limit=20)`, ex, err)

	cr, err := c.ChangeRisk(ctx, hello, "", 20)
	record("change_risk", `ChangeRisk(target=<Hello>, diff="", limit=20)`, cr, err)

	if len(payloads) != len(readOnlyStableOps) {
		t.Fatalf("expected %d read-only stable op outputs, got %d", len(readOnlyStableOps), len(payloads))
	}
	return requests, payloads
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestAX00_StableOpCanonicalBytes_Golden(t *testing.T) {
	store, err := graphstore.SQLiteFactory(t.TempDir())
	if err != nil {
		t.Fatalf("SQLiteFactory: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	indexCharFixture(t, store)

	requests, payloads := runReadOnlyStableOps(t, store)

	entries := make([]manifestEntry, 0, len(readOnlyStableOps))
	for _, op := range readOnlyStableOps {
		raw, ok := payloads[op]
		if !ok {
			t.Fatalf("read-only stable op %q was not exercised", op)
		}
		artifact := op + ".canonical.json"
		goldenfile.Assert(t, filepath.Join(stableOpsGoldenDir(), artifact), raw)
		entries = append(entries, manifestEntry{
			Operation: op,
			Request:   requests[op],
			Artifact:  artifact,
			Bytes:     len(raw),
			SHA256:    digest(raw),
		})
	}

	graph := indexBytes(t, store)
	manifest := stableOpsManifest{
		Note:        "AX-00 baseline freeze: canonical result bytes of the eleven read-only Stable operations over the committed corpus/fixtures/go fixture. The .canonical.json artifacts are RAW canonical bytes (not re-indented) — the serialization itself is what is frozen. `index` is a lifecycle operation and is represented here only by the digest of the whole indexed graph.",
		Fixture:     "corpus/fixtures/go",
		Backend:     "core/graphstore SQLite (the shipped source of truth)",
		OpCount:     len(entries),
		Operations:  entries,
		GraphDigest: manifestEntry{Operation: "index", Request: "IngestAll(corpus/fixtures/go) → model.Graph.Marshal()", Artifact: "(not committed: whole-graph bytes)", Bytes: len(graph), SHA256: digest(graph)},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		t.Fatalf("encode manifest: %v", err)
	}
	goldenfile.Assert(t, filepath.Join(stableOpsGoldenDir(), "manifest.json"), buf.Bytes())
}

// TestAX00_StableOpCanonicalBytes_ReproducibleAcrossIndexes is the AC-3
// reproducibility half stated at the level this story needs: the recorded bytes
// must come back identical from an INDEPENDENTLY re-indexed store, not merely
// from a second read of the same one. That is the difference between "the cache
// is stable" and "the artifact is a baseline".
func TestAX00_StableOpCanonicalBytes_ReproducibleAcrossIndexes(t *testing.T) {
	run := func() map[string][]byte {
		store, err := graphstore.SQLiteFactory(t.TempDir())
		if err != nil {
			t.Fatalf("SQLiteFactory: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		indexCharFixture(t, store)
		_, payloads := runReadOnlyStableOps(t, store)
		return payloads
	}

	first, second := run(), run()
	for _, op := range readOnlyStableOps {
		if !bytes.Equal(first[op], second[op]) {
			t.Errorf("op %q is not byte-reproducible across two independent indexes of the same fixture:\n first =%s\n second=%s", op, first[op], second[op])
		}
	}
}
