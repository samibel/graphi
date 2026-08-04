package trust_test

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/engine/typeresolve"
	"github.com/samibel/graphi/internal/freshness"
)

// buildSnapshot assembles the same logical snapshot through the builder
// helpers. reversed feeds every order-sensitive input backwards so the
// determinism test proves canonical ordering is by construction.
func buildSnapshot(t *testing.T, reversed bool) trust.Snapshot {
	t.Helper()

	kindEntries := []trust.KindCount{{Kind: "calls", Count: 3}, {Kind: "imports", Count: 1}, {Kind: "references", Count: 2}}
	boundaries := []graphstore.ExternalBoundary{
		{QualifiedName: "fmt", IncidentEdges: 3},
		{QualifiedName: "net/http", IncidentEdges: 3},
		{QualifiedName: "os", IncidentEdges: 1},
	}
	units := []typeresolve.UnitResult{
		{Dir: "a", Name: "a", TypeErrors: 2},
		{Dir: "a", Name: "a_test", Degraded: "type-checker panic", TypeErrors: 0},
		{Dir: "b", Name: "b", Degraded: "full parse failed", TypeErrors: 1},
	}
	paths := []string{"/abs/leak.go", "a/skip.go", "z/skip.json"}
	if reversed {
		for i, j := 0, len(kindEntries)-1; i < j; i, j = i+1, j-1 {
			kindEntries[i], kindEntries[j] = kindEntries[j], kindEntries[i]
		}
		for i, j := 0, len(boundaries)-1; i < j; i, j = i+1, j-1 {
			boundaries[i], boundaries[j] = boundaries[j], boundaries[i]
		}
		for i, j := 0, len(units)-1; i < j; i, j = i+1, j-1 {
			units[i], units[j] = units[j], units[i]
		}
		for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
			paths[i], paths[j] = paths[j], paths[i]
		}
	}
	kinds := make(map[string]int, len(kindEntries)+1)
	for _, e := range kindEntries {
		kinds[e.Kind] = e.Count
	}
	kinds["zero-dropped"] = 0

	stats := graphstore.TrustStats{
		NodesTotal:  10,
		EdgesTotal:  6,
		EdgesByKind: kinds,
		EdgesByTier: map[model.ConfidenceTier]int{
			model.TierConfirmed: 1,
			model.TierDerived:   2,
			model.TierHeuristic: 3,
		},
		ExternalNodes: 2,
		ExternalEdges: 4,
		TopBoundaries: boundaries,
	}

	edge, err := model.NewEdge("from0000from0000", "to000000to000000", "calls",
		model.TierConfirmed, 1.0, "confirmed by test fixture", []string{"a/a.go:1"})
	if err != nil {
		t.Fatalf("NewEdge: %v", err)
	}
	res := typeresolve.Result{
		Edges:          []model.Edge{edge},
		Units:          units,
		SkippedFiles:   []typeresolve.SkippedFile{{Path: "c/broken.go", Reason: "no unit"}},
		DroppedIntents: 5,
	}

	return trust.Snapshot{
		SchemaVersion:   trust.SnapshotSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		Generation: trust.GenerationRef{
			FullPassGeneration: "0123456789abcdef0123456789abcdef",
			SourceCommit:       "afa1b686de381dd455ab08e4bf33aaf9420d6aab",
			Branch:             "main",
			IndexProfile:       "balanced",
		},
		Graph:    trust.NewGraphFacts(stats),
		External: trust.NewExternalFacts(stats),
		Link: trust.NewLinkFacts(link.Stats{
			ResolvedDerived: 7, ResolvedHeuristic: 8, ResolvedExternal: 9, Skipped: 4, Ambiguous: 1,
		}),
		Parse:          trust.NewParseFacts(paths, map[string]int{"parse-error": 2, "oversize": 1, "unused": 0}),
		TypeResolution: trust.NewTypeResolutionFacts(res),
	}
}

func mustEncode(t *testing.T, s trust.Snapshot) []byte {
	t.Helper()
	b, err := trust.Encode(s)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

func TestBuildersCanonicalize(t *testing.T) {
	s := buildSnapshot(t, false)

	wantKinds := []trust.KindCount{{Kind: "calls", Count: 3}, {Kind: "imports", Count: 1}, {Kind: "references", Count: 2}}
	if !reflect.DeepEqual(s.Graph.EdgesByKind, wantKinds) {
		t.Errorf("EdgesByKind = %+v, want %+v", s.Graph.EdgesByKind, wantKinds)
	}
	if want := (trust.TierCounts{Confirmed: 1, Derived: 2, Heuristic: 3}); s.Graph.EdgesByTier != want {
		t.Errorf("EdgesByTier = %+v, want %+v", s.Graph.EdgesByTier, want)
	}
	wantBounds := []trust.Boundary{
		{QualifiedName: "fmt", IncidentEdges: 3},
		{QualifiedName: "net/http", IncidentEdges: 3},
		{QualifiedName: "os", IncidentEdges: 1},
	}
	if !reflect.DeepEqual(s.External.TopBoundaries, wantBounds) {
		t.Errorf("TopBoundaries = %+v, want %+v", s.External.TopBoundaries, wantBounds)
	}
	wantParse := trust.ParseFacts{
		Skipped:  3,
		ByReason: []trust.ReasonCount{{Reason: "oversize", Count: 1}, {Reason: "parse-error", Count: 2}},
		Paths:    []string{"a/skip.go", "z/skip.json"}, // absolute path dropped from the sample, kept in the count
	}
	if !reflect.DeepEqual(s.Parse, wantParse) {
		t.Errorf("Parse = %+v, want %+v", s.Parse, wantParse)
	}
	wantTR := trust.TypeResolutionFacts{
		UnitsTotal: 3, UnitsDegraded: 2, TypeErrors: 3, SkippedFiles: 1, DroppedIntents: 5, ConfirmedEdges: 1,
		DegradedUnits: []trust.DegradedUnit{
			{Dir: "a", Name: "a_test", Reason: "type-checker panic", TypeErrors: 0},
			{Dir: "b", Name: "b", Reason: "full parse failed", TypeErrors: 1},
		},
	}
	if !reflect.DeepEqual(s.TypeResolution, wantTR) {
		t.Errorf("TypeResolution = %+v, want %+v", s.TypeResolution, wantTR)
	}
}

func TestBuilderBounds(t *testing.T) {
	paths := make([]string, 0, trust.MaxParsePaths+10)
	for i := 0; i < trust.MaxParsePaths+10; i++ {
		paths = append(paths, fmt.Sprintf("dir/skip-%03d.go", i))
	}
	pf := trust.NewParseFacts(paths, nil)
	if pf.Skipped != trust.MaxParsePaths+10 {
		t.Errorf("Skipped = %d, want %d (count is never truncated)", pf.Skipped, trust.MaxParsePaths+10)
	}
	if len(pf.Paths) != trust.MaxParsePaths {
		t.Errorf("len(Paths) = %d, want cap %d", len(pf.Paths), trust.MaxParsePaths)
	}

	var res typeresolve.Result
	for i := 0; i < trust.MaxDegradedUnits+10; i++ {
		res.Units = append(res.Units, typeresolve.UnitResult{
			Dir: fmt.Sprintf("d%03d", i), Name: "p", Degraded: "full parse failed",
		})
	}
	tr := trust.NewTypeResolutionFacts(res)
	if tr.UnitsDegraded != trust.MaxDegradedUnits+10 {
		t.Errorf("UnitsDegraded = %d, want %d (count is never truncated)", tr.UnitsDegraded, trust.MaxDegradedUnits+10)
	}
	if len(tr.DegradedUnits) != trust.MaxDegradedUnits {
		t.Errorf("len(DegradedUnits) = %d, want cap %d", len(tr.DegradedUnits), trust.MaxDegradedUnits)
	}
}

func TestEncodeDeterminism(t *testing.T) {
	a := mustEncode(t, buildSnapshot(t, false))
	b := mustEncode(t, buildSnapshot(t, true))
	if !bytes.Equal(a, b) {
		t.Fatalf("Encode not byte-identical across input orderings:\n%s\n%s", a, b)
	}
	da, db := trust.Digest(a), trust.Digest(b)
	if da != db {
		t.Errorf("Digest mismatch: %s vs %s", da, db)
	}
	if !strings.HasPrefix(da, "sha256:") || len(da) != len("sha256:")+64 {
		t.Errorf("Digest form = %q, want sha256:<64 hex>", da)
	}
	if bytes.HasSuffix(a, []byte("\n")) {
		t.Error("Encode output must not carry a trailing newline")
	}
}

func TestEncodeNormalizesNilSlices(t *testing.T) {
	enc := mustEncode(t, trust.Snapshot{SchemaVersion: trust.SnapshotSchemaVersion, SnapshotVersion: trust.SnapshotVersion})
	if bytes.Contains(enc, []byte("null")) {
		t.Fatalf("Encode emitted null for an empty list: %s", enc)
	}
	for _, field := range []string{"edges_by_kind", "top_boundaries", "by_reason", "paths", "degraded_units"} {
		if !bytes.Contains(enc, []byte(`"`+field+`":[]`)) {
			t.Errorf("missing empty-array field %q in %s", field, enc)
		}
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	s := buildSnapshot(t, false)
	enc := mustEncode(t, s)
	dec, err := trust.Decode(enc)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(dec, s) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", dec, s)
	}
	re := mustEncode(t, dec)
	if !bytes.Equal(re, enc) {
		t.Errorf("re-encode not byte-identical:\n%s\n%s", re, enc)
	}
}

func TestDecodeRejectsUnknownSchema(t *testing.T) {
	s := buildSnapshot(t, false)
	s.SchemaVersion = trust.SnapshotSchemaVersion + 1
	enc := mustEncode(t, s)
	if _, err := trust.Decode(enc); !errors.Is(err, trust.ErrSchemaUnsupported) {
		t.Errorf("Decode(schema %d) err = %v, want ErrSchemaUnsupported", s.SchemaVersion, err)
	}
	if _, err := trust.Decode([]byte("{not json")); err == nil || errors.Is(err, trust.ErrSchemaUnsupported) {
		t.Errorf("Decode(malformed) err = %v, want a plain decode error", err)
	}
}

// deriveArgs is one DeriveState call with the all-good fixture as baseline.
// innerGen is the digest-protected binding INSIDE the snapshot document; a
// real publish always stamps it equal to the stored generation key (snapGen).
type deriveArgs struct {
	f         freshness.Report
	found     bool
	snapGen   string
	innerGen  string
	liveGen   string
	digestOK  bool
	liveNodes int
	liveEdges int
}

func goodArgs() deriveArgs {
	return deriveArgs{
		f: freshness.Report{
			Current: true,
			Index:   freshness.IndexState{Exists: true, WarmStartable: true},
		},
		found:     true,
		snapGen:   "gen-live",
		innerGen:  "gen-live",
		liveGen:   "gen-live",
		digestOK:  true,
		liveNodes: 10,
		liveEdges: 20,
	}
}

func derive(a deriveArgs) trust.State {
	snap := trust.Snapshot{
		Generation: trust.GenerationRef{FullPassGeneration: a.innerGen},
		Graph:      trust.GraphFacts{NodesTotal: 10, EdgesTotal: 20},
	}
	live := graphstore.TrustStats{NodesTotal: a.liveNodes, EdgesTotal: a.liveEdges}
	return trust.DeriveState(a.f, a.found, a.snapGen, a.liveGen, a.digestOK, live, snap)
}

func TestDeriveState(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*deriveArgs)
		want   trust.State
	}{
		{"all good", func(a *deriveArgs) {}, trust.StateCurrent},

		// UNAVAILABLE terms (fail closed).
		{"no graph", func(a *deriveArgs) { a.f.Index.Exists = false }, trust.StateUnavailable},
		{"snapshot missing", func(a *deriveArgs) { a.found = false }, trust.StateUnavailable},
		{"digest mismatch", func(a *deriveArgs) { a.digestOK = false }, trust.StateUnavailable},
		{"unbound live generation", func(a *deriveArgs) { a.liveGen = "" }, trust.StateUnavailable},

		// INCOMPLETE terms.
		{"full pass in progress", func(a *deriveArgs) { a.f.Index.FullPassInProgress = true }, trust.StateIncomplete},
		{"lock held", func(a *deriveArgs) { a.f.Index.LockHeld = true }, trust.StateIncomplete},
		{"not warm-startable", func(a *deriveArgs) { a.f.Index.WarmStartable = false }, trust.StateIncomplete},

		// STALE terms.
		{"generation mismatch", func(a *deriveArgs) { a.snapGen = "gen-old"; a.innerGen = "gen-old" }, trust.StateStale},
		{"inner generation binding mismatch", func(a *deriveArgs) { a.innerGen = "gen-old" }, trust.StateStale},
		{"drift", func(a *deriveArgs) {
			a.f.Current = false
			a.f.Drift = freshness.Drift{Changed: 1}
		}, trust.StateStale},
		{"node count mismatch", func(a *deriveArgs) { a.liveNodes = 11 }, trust.StateStale},
		{"edge count mismatch", func(a *deriveArgs) { a.liveEdges = 21 }, trust.StateStale},

		// Precedence: UNAVAILABLE > INCOMPLETE > STALE.
		{"missing snapshot beats drift", func(a *deriveArgs) {
			a.found = false
			a.f.Current = false
		}, trust.StateUnavailable},
		{"digest mismatch beats not warm-startable", func(a *deriveArgs) {
			a.digestOK = false
			a.f.Index.WarmStartable = false
		}, trust.StateUnavailable},
		{"full-pass marker beats generation mismatch", func(a *deriveArgs) {
			a.f.Index.FullPassInProgress = true
			a.snapGen = "gen-old"
			a.innerGen = "gen-old"
		}, trust.StateIncomplete},
		{"lock held beats count mismatch", func(a *deriveArgs) {
			a.f.Index.LockHeld = true
			a.liveNodes = 11
		}, trust.StateIncomplete},
		{"every stale term at once is stale", func(a *deriveArgs) {
			a.snapGen = "gen-old"
			a.innerGen = "gen-old"
			a.f.Current = false
			a.liveNodes = 11
			a.liveEdges = 21
		}, trust.StateStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := goodArgs()
			tc.mutate(&a)
			if got := derive(a); got != tc.want {
				t.Errorf("DeriveState = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestDeriveStateInvariants sweeps the full boolean input space and asserts
// the ADR 0006 D3 invariant — status-current=false can NEVER yield CURRENT —
// plus closed-set membership and the exact conjunction CURRENT requires.
func TestDeriveStateInvariants(t *testing.T) {
	bools := []bool{false, true}
	valid := map[trust.State]bool{
		trust.StateCurrent: true, trust.StateStale: true,
		trust.StateIncomplete: true, trust.StateUnavailable: true,
	}
	// dims spans every boolean input; the generation dimensions expand below.
	type dims struct {
		exists, fpip, lock, warm, current, found, digestOK bool
		genEmpty, genMatch, innerMatch, countMatch         bool
	}
	var sweep func(d dims, i int)
	flags := []func(*dims, bool){
		func(d *dims, v bool) { d.exists = v },
		func(d *dims, v bool) { d.fpip = v },
		func(d *dims, v bool) { d.lock = v },
		func(d *dims, v bool) { d.warm = v },
		func(d *dims, v bool) { d.current = v },
		func(d *dims, v bool) { d.found = v },
		func(d *dims, v bool) { d.digestOK = v },
		func(d *dims, v bool) { d.genEmpty = v },
		func(d *dims, v bool) { d.genMatch = v },
		func(d *dims, v bool) { d.innerMatch = v },
		func(d *dims, v bool) { d.countMatch = v },
	}
	sweep = func(d dims, i int) {
		if i < len(flags) {
			for _, v := range bools {
				flags[i](&d, v)
				sweep(d, i+1)
			}
			return
		}
		a := goodArgs()
		a.f.Index = freshness.IndexState{
			Exists: d.exists, WarmStartable: d.warm,
			FullPassInProgress: d.fpip, LockHeld: d.lock,
		}
		a.f.Current = d.current
		a.found = d.found
		a.digestOK = d.digestOK
		if d.genEmpty {
			a.liveGen = ""
		}
		a.snapGen = a.liveGen
		if !d.genMatch {
			a.snapGen = "gen-old"
		}
		a.innerGen = a.snapGen
		if !d.innerMatch {
			a.innerGen = "gen-inner-old"
		}
		if !d.countMatch {
			a.liveNodes = 11
		}
		got := derive(a)
		if !valid[got] {
			t.Fatalf("DeriveState returned %q outside the closed set", got)
		}
		if !d.current && got == trust.StateCurrent {
			t.Fatalf("invariant broken: f.Current=false yielded CURRENT (%+v)", a)
		}
		wantCurrent := d.exists && d.found && d.digestOK && !d.genEmpty &&
			!d.fpip && !d.lock && d.warm && d.genMatch && d.innerMatch &&
			d.current && d.countMatch
		if (got == trust.StateCurrent) != wantCurrent {
			t.Fatalf("DeriveState = %s, want current=%v for %+v", got, wantCurrent, a)
		}
	}
	sweep(dims{}, 0)
}

// TestDeriveStateAggregateCrossCheck pins the STALE term "the graph changed
// after the snapshot" (contract doc §1.6) against totals-preserving
// mutations: the whole recomputable aggregate is compared — per-kind,
// per-tier, and external counts — never just the two totals.
func TestDeriveStateAggregateCrossCheck(t *testing.T) {
	snap := trust.Snapshot{
		Generation: trust.GenerationRef{FullPassGeneration: "gen-live"},
		Graph: trust.GraphFacts{
			NodesTotal:  4,
			EdgesTotal:  2,
			EdgesByKind: []trust.KindCount{{Kind: "calls", Count: 1}, {Kind: "imports", Count: 1}},
			EdgesByTier: trust.TierCounts{Confirmed: 1, Heuristic: 1},
		},
		External: trust.ExternalFacts{Nodes: 1, Edges: 1},
	}
	f := freshness.Report{Current: true, Index: freshness.IndexState{Exists: true, WarmStartable: true}}
	liveBase := func() graphstore.TrustStats {
		return graphstore.TrustStats{
			NodesTotal: 4, EdgesTotal: 2,
			EdgesByKind:   map[string]int{"calls": 1, "imports": 1},
			EdgesByTier:   map[model.ConfidenceTier]int{model.TierConfirmed: 1, model.TierHeuristic: 1},
			ExternalNodes: 1, ExternalEdges: 1,
		}
	}
	cases := []struct {
		name   string
		mutate func(*graphstore.TrustStats)
		want   trust.State
	}{
		{"identical aggregate", func(s *graphstore.TrustStats) {}, trust.StateCurrent},
		{"kind distribution moved, totals preserved", func(s *graphstore.TrustStats) {
			s.EdgesByKind = map[string]int{"calls": 2}
		}, trust.StateStale},
		{"tier distribution moved, totals preserved", func(s *graphstore.TrustStats) {
			s.EdgesByTier = map[model.ConfidenceTier]int{model.TierConfirmed: 2}
		}, trust.StateStale},
		{"external counts moved, totals preserved", func(s *graphstore.TrustStats) {
			s.ExternalEdges = 0
		}, trust.StateStale},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := liveBase()
			tc.mutate(&live)
			if got := trust.DeriveState(f, true, "gen-live", "gen-live", true, live, snap); got != tc.want {
				t.Errorf("DeriveState = %s, want %s", got, tc.want)
			}
		})
	}
}
