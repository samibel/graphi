package main

// SW-127 (P0-C4): the properties no unit test can prove — that a REAL cold
// index really emits the events the measurement depends on, that a real
// suppressed emitter really fails, that a real delayed emitter really surfaces,
// and that attaching the observation hook changes nothing about the graph
// ingest produces (AC-7).
//
// These run over a small Go tree built in a temp dir: real parse, real full
// index, real progress events. No network, no clone — the pinned corpus
// repositories are what the workflow measures (AC-3), and what a test needs is
// a tree big enough to emit more than a handful of events.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/internal/evalreport"
)

// stallTree writes a module with enough files that a full index emits per-file
// parse progress rather than only phase transitions.
func stallTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/stalls\n\ngo 1.22\n")
	for i := 0; i < 12; i++ {
		write(fmt.Sprintf("pkg%02d/unit.go", i), fmt.Sprintf(`package pkg%02d

// Fn%02d is a function so the file carries a symbol.
func Fn%02d() int { return %d }

// Fn%02dB calls its neighbour.
func Fn%02dB() int { return Fn%02d() + 1 }
`, i, i, i, i, i, i, i))
	}
	return root
}

// indexWithObserver performs a real cold index of root and returns the series
// the harness would publish. attach is what the production code does; passing
// false SUPPRESSES the emitter, which is the AC-5 case.
func indexWithObserver(t *testing.T, root string, attach bool, wrap func(func(ingest.ProgressEvent)) func(ingest.ProgressEvent)) (*evalreport.StallSeries, graphstore.Graphstore, func()) {
	t.Helper()
	work := t.TempDir()
	store, err := graphstore.OpenSQLite(filepath.Join(work, "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(work, "meta"))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	cleanup := func() {
		_ = ing.Close()
		_ = store.Close()
	}

	obs := newStallObserver()
	if attach {
		handler := obs.observe
		if wrap != nil {
			handler = wrap(obs.observe)
		}
		ing.WithHeartbeatMode(ingest.HeartbeatNonTTY).WithProgress(handler)
	}

	start := time.Now()
	obs.begin(start)
	if err := ing.IngestAll(context.Background(), root); err != nil {
		cleanup()
		t.Fatalf("IngestAll: %v", err)
	}
	obs.end(time.Now())
	return obs.series("stall-fixture", string(ingest.HeartbeatNonTTY)), store, cleanup
}

// AC-1/AC-2/AC-4/AC-6 over a real cold index: real events, real gaps, both
// boundaries named, the phases attributed, and every statistic reproducible.
func TestStallHarness_MeasuresARealColdIndex(t *testing.T) {
	series, _, cleanup := indexWithObserver(t, stallTree(t), true, nil)
	defer cleanup()

	if series == nil {
		t.Fatal("a watched cold index produced no series")
	}
	if !series.Observable {
		t.Fatalf("a real cold index reported itself unobservable: %s", series.SilenceReason)
	}
	if series.Events < evalreport.StallEventMinimum {
		t.Fatalf("events = %d, want at least %d from a real full index", series.Events, evalreport.StallEventMinimum)
	}
	if len(series.Intervals) != series.Events-1 {
		t.Fatalf("%d interval(s) from %d events: n gaps require n+1 events", len(series.Intervals), series.Events)
	}
	if got := evalreport.RecomputeStalls(series.Intervals).Stalls; got != series.Stalls {
		t.Errorf("recomputed %+v from the retained intervals, but the series published %+v", got, series.Stalls)
	}

	// AC-4: both boundaries are observed and neither is in the distribution.
	if !series.LeadInMeasured || !series.TailMeasured {
		t.Errorf("boundaries not measured: lead-in=%v tail=%v", series.LeadInMeasured, series.TailMeasured)
	}
	// The parts reconcile against the whole. Every interval is truncated to whole
	// microseconds, so the sum can fall short by at most one microsecond per
	// part — and may never EXCEED the window, which would mean the clock ran
	// twice over the same instant.
	total := series.LeadInUS + series.TailUS
	for _, in := range series.Intervals {
		total += in.US
	}
	slack := int64(len(series.Intervals) + 2)
	if total > series.IndexWallclockUS || series.IndexWallclockUS-total > slack {
		t.Errorf("lead-in + intervals + tail = %d µs against a %d µs window (slack allowed: %d µs for per-interval truncation)",
			total, series.IndexWallclockUS, slack)
	}

	// The real phases a full pass goes through are attributed, not pooled into
	// one anonymous distribution.
	seen := map[string]bool{}
	for _, p := range series.PerPhase {
		seen[p.Phase] = true
	}
	for _, want := range []string{string(ingest.PhaseParse), string(ingest.PhaseDone)} {
		if !seen[want] {
			t.Errorf("phase %q has no stall row; the per-phase table is %v", want, seen)
		}
	}

	// The artifact explains itself: definition, boundaries, timing, arithmetic
	// and scope all travel with the numbers (AC-4).
	for name, note := range map[string]string{
		"stall_definition":  series.StallDefinition,
		"boundary_handling": series.BoundaryHandling,
		"timing_method":     series.TimingMethod,
		"aggregate_method":  series.AggregateMethod,
		"scope_limitation":  series.ScopeLimitation,
		"notes":             series.Notes,
	} {
		if strings.TrimSpace(note) == "" {
			t.Errorf("the artifact publishes no %s", name)
		}
	}
	if series.HeartbeatMode != string(ingest.HeartbeatNonTTY) {
		t.Errorf("heartbeat_mode = %q; the cadence that produced the events must be recorded", series.HeartbeatMode)
	}
}

// AC-5, end to end and against a REAL index: a suppressed emitter must not come
// out green. This is the test the story says matters.
func TestStallHarness_ASuppressedEmitterDoesNotComeOutGreen(t *testing.T) {
	series, _, cleanup := indexWithObserver(t, stallTree(t), false, nil)
	defer cleanup()

	if series == nil {
		t.Fatal("the run was watched, so it must produce a series; nil would be indistinguishable from an index that never ran")
	}
	if series.Events != 0 {
		t.Fatalf("events = %d with the emitter suppressed, want 0", series.Events)
	}
	if series.Observable {
		t.Fatal("a silent index reported itself observable")
	}
	if series.Stalls.N != 0 {
		t.Fatalf("stalls.n = %d over a silent index", series.Stalls.N)
	}
	if got := stallStatus(series); got != evalreport.StatusFail {
		t.Fatalf("status = %s over a silent index, want FAIL — `0 stalls, passed` is the outcome this gate exists to prevent", got)
	}
	// And the gate itself, with impeccable provenance, still refuses to pass.
	gate := evaluateStallGate(stallGateMapping(), series, "")
	if gate.Status != evalreport.StatusFail || gate.HasMeasurement {
		t.Fatalf("gate = %s (has measurement=%v), want FAIL with no measurement", gate.Status, gate.HasMeasurement)
	}
}

// A delayed emitter — one that really goes quiet mid-pass — must surface in the
// reported maximum. The delay is injected around the handler so the silence is a
// real wall-clock gap in the real event stream, not a scripted clock.
//
// The maximum assertion below is one-sided and cannot flake: the observed gap can
// only be LONGER than the injected delay, never shorter. The anti-smearing check
// is NOT one-sided, and the original 150 ms threshold made it flaky — under
// `-race` on a loaded runner an *ambient* gap can also cross 150 ms, which counts
// as a second interval without anything having smeared (observed on main at
// 2e1e186: "2 interval(s) exceed the injected 150ms delay, want exactly 1"). The
// injected delay is therefore held an order of magnitude above the natural gaps
// these fixture trees produce (tens of milliseconds), so the count discriminates
// smearing rather than runner load. It costs ~1.35 s of wall clock and buys the
// signal-to-noise margin the assertion always assumed but never had.
func TestStallHarness_ADelayedEmitterSurfacesInTheMaximum(t *testing.T) {
	const delay = 1500 * time.Millisecond
	delayed := false
	wrap := func(inner func(ingest.ProgressEvent)) func(ingest.ProgressEvent) {
		return func(ev ingest.ProgressEvent) {
			// Sleep BEFORE recording, exactly once: the gap that closes on this
			// event is the one that must carry the silence.
			if !delayed && ev.Phase == ingest.PhaseParse {
				delayed = true
				time.Sleep(delay)
			}
			inner(ev)
		}
	}

	series, _, cleanup := indexWithObserver(t, stallTree(t), true, wrap)
	defer cleanup()

	if !delayed {
		t.Fatal("the delay was never injected: the index emitted no parse event")
	}
	if series.Stalls.MaxUS < delay.Microseconds() {
		t.Fatalf("longest stall = %d µs after a %v silence; a delayed emitter must surface in the maximum",
			series.Stalls.MaxUS, delay)
	}
	// It must be one gap, not the whole run smeared across the distribution.
	over := 0
	for _, in := range series.Intervals {
		if in.US >= delay.Microseconds() {
			over++
		}
	}
	if over != 1 {
		t.Errorf("%d interval(s) exceed the injected %v delay, want exactly 1", over, delay)
	}
}

// AC-7: attaching the observation hook must not change what ingest produces.
// The same tree indexed with and without the observer must yield byte-identical
// node and edge identities — the project's determinism rule applied to the one
// thing this story adds to the index path.
func TestStallHarness_TheObservationHookDoesNotChangeTheGraph(t *testing.T) {
	root := stallTree(t)

	watchedSeries, watchedStore, closeWatched := indexWithObserver(t, root, true, nil)
	defer closeWatched()
	watched := graphIdentity(t, watchedStore)

	_, plainStore, closePlain := indexWithObserver(t, root, false, nil)
	defer closePlain()
	plain := graphIdentity(t, plainStore)

	if watched != plain {
		t.Fatalf("indexing the same tree with the progress observer attached produced a different graph:\nwatched:\n%s\nplain:\n%s", watched, plain)
	}
	if !watchedSeries.Observable {
		t.Fatal("the watched run was not observable; the comparison above would be vacuous")
	}
}

// graphIdentity renders the store's node and edge identities in canonical order,
// so any difference in what ingest committed shows up as a string difference.
func graphIdentity(t *testing.T, store graphstore.Graphstore) string {
	t.Helper()
	scanner, ok := any(store).(graphstore.GraphScanner)
	if !ok {
		t.Fatal("the store does not implement GraphScanner")
	}
	ctx := context.Background()
	ids, err := scanner.NodeIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, id := range ids {
		b.WriteString(string(id))
		b.WriteByte('\n')
	}
	b.WriteString("--edges--\n")
	var edges []string
	if err := scanner.ScanEdges(ctx, func(e model.Edge) error {
		edges = append(edges, string(e.ID()))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(edges)
	for _, id := range edges {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	return b.String()
}

// The measurement is always on: unlike the query-execution floor and the change
// sequence, it needs no flag. A guard that has to be requested does not guard,
// so the default `-full-run` path publishes a series over the fixture too.
func TestStallHarness_TheDefaultFullRunPathPublishesASeries(t *testing.T) {
	rep := runFixtureFullRun(t, 0)
	series := rep.Repo.Stalls
	if series == nil {
		t.Fatal("the default full-run path published no stall series: a regression guard that must be requested does not guard")
	}
	if !series.Observable {
		t.Fatalf("the fixture index was not observable: %s", series.SilenceReason)
	}
	if series.Stalls.N == 0 || len(series.Intervals) == 0 {
		t.Fatalf("no intervals retained over a real fixture index: %+v", series.Stalls)
	}
	// No contract was supplied, so there is no threshold to read against and the
	// series must not invent one — but it is still not green.
	if len(series.Gates) != 0 {
		t.Errorf("gates were read without a reference-scenario contract: %+v", series.Gates)
	}
	if series.Status != evalreport.StatusUnknown {
		t.Errorf("status = %s with no gates read, want UNKNOWN", series.Status)
	}
}

// AC-3: the measurement runs against the REFERENCE SCENARIO — the large pinned
// repository where stalls actually occur — not only a small fixture.
func TestStallHarness_TheWorkflowMeasuresTheReferenceScenario(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval-full.yml")))

	series, ok := jobs["progress-stall-series"]
	if !ok {
		t.Fatal("eval-full.yml has no progress-stall-series job: the §12.2 stall gate is not exercised against the reference scenario anywhere")
	}
	for _, want := range []string{"grpc-go", "-reference-scenario", "-candidate", "-runner-class ubuntu-latest", "-drop-caches"} {
		if !strings.Contains(series, want) {
			t.Errorf("the progress-stall-series job does not pass %s", want)
		}
	}
	// grpc-go is not in the fail-closed budget selection; passing -budgets there
	// would fail the run for a configuration reason and hide the gate.
	if strings.Contains(series, "-budgets") {
		t.Error("the stall series must be read against the reference-scenario gates, not the historical hero budgets")
	}
	// The stall measurement mutates nothing and needs no sequence of changes; a
	// mutating run would be measuring a different tree.
	if strings.Contains(series, "-incremental-changes") {
		t.Error("the stall job must not mutate the checkout: it measures the cold index")
	}

	// And the contract really assigns the gate to this story, on that repository.
	rs, err := loadReferenceScenario(filepath.Join(root, "docs", "eval", "reference-scenario.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rs.Gates {
		if g.MeasuredBy != progressStallStory {
			continue
		}
		if g.Repo != "grpc-go" {
			t.Errorf("the contract maps %s to %q but the workflow measures grpc-go", g.ID, g.Repo)
		}
	}
}
