package main

// SW-129 (P0-C6): what "the affected scenario" means, per series.
//
// A profile is only actionable if it describes the work whose gate was missed.
// So each measured series maps to a workload that RE-EXECUTES that series'
// scenario through the same code the harness measured — the same ingester over
// the same checkout, the same warm operations through the same FixtureEngine,
// the same change driver — rather than through a profiling-only reimplementation
// that would eventually describe something else.
//
// Everything that is not the scenario happens in prepare, outside the profile
// window: cloning, and the index a query or freshness scenario needs before it
// can run at all. A profile whose window contains the setup would attribute the
// index to the query gate, which is worse than no profile.
//
// A series this run did not measure has no workload. That is an error, not an
// empty result: "we could not re-run it" and "there was nothing to re-run" are
// different statements and only the first is true.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/agenttools/resolve"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/scenario"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/evalreport"
)

// profileIncrementalChanges is how many changes the freshness scenario applies
// in a diagnostic re-run. It is deliberately far below FR-8's 100-change floor:
// this is not a measurement, and a profile of ten changes shows the same call
// graph as a profile of a hundred at a tenth of the wallclock.
const profileIncrementalChanges = 10

// profileWorkloadInput is everything a diagnostic re-run needs to reconstruct a
// scenario.
type profileWorkloadInput struct {
	repo string
	// repoDir is the measured checkout when this process still has it. The cold
	// SERIES path deletes each run's tree as it goes, so it is empty there and
	// the checkout is materialised again in prepare.
	repoDir string
	// scratch is where a re-run may create stores, meta directories and clones.
	scratch     string
	manifestDir string
	entry       corpus.Entry
	plan        queryLatencyPlan
	// changes is the freshness scenario's change count for this run; 0 means the
	// run never measured freshness, so it cannot be re-executed.
	changes int
}

// profileWorkloadBuilderFor maps a series to its re-executable scenario.
func profileWorkloadBuilderFor(in profileWorkloadInput) profileWorkloadBuilder {
	// checkout is resolved at most once and shared by every workload: two
	// scenarios profiled in one run must describe the same tree.
	var resolved string
	checkout := func(ctx context.Context) (string, error) {
		if resolved != "" {
			return resolved, nil
		}
		dir, err := profileCheckout(ctx, in)
		if err != nil {
			return "", err
		}
		resolved = dir
		return dir, nil
	}

	return func(series string) (profileWorkload, error) {
		switch series {
		case evalreport.RawSeriesCold:
			return coldIndexProfileWorkload(in, series, checkout,
				"a cold full index of the measured checkout into a fresh SQLite store — the same ingest pass the cold-index, "+
					"peak-RSS and DB-size gates are read over, re-executed under the profiler"), nil
		case evalreport.RawSeriesStalls:
			return coldIndexProfileWorkload(in, series, checkout,
				"a cold full index of the measured checkout — the pass the progress-stall gate watches; a stall is a gap in "+
					"THIS work, so this is the pass to profile"), nil
		case evalreport.RawSeriesQuery:
			return queryLatencyProfileWorkload(in, checkout), nil
		case evalreport.RawSeriesIncremental:
			if in.changes <= 0 {
				return profileWorkload{}, errors.New("this run measured no incremental changes, so the freshness scenario cannot be re-executed")
			}
			return incrementalProfileWorkload(in, checkout), nil
		default:
			return profileWorkload{}, fmt.Errorf("no profilable scenario is defined for series %q", series)
		}
	}
}

// measuredCheckoutDir is WHERE a manifest entry's tree is measured: the clone
// destination for URL entries, the repo-root-relative path for local ones.
//
// It is shared with fullRepoRun rather than re-derived here. A profile of "the
// affected scenario" taken over a different directory from the one the run
// measured would be worse than no profile, and two copies of this rule is
// exactly how that happens.
func measuredCheckoutDir(e corpus.Entry, manifestDir, workDir string) (string, error) {
	switch {
	case e.URL != "":
		return filepath.Join(workDir, e.Name), nil
	case e.Path != "":
		return filepath.Join(filepath.Dir(manifestDir), filepath.FromSlash(e.Path)), nil
	default:
		return "", errors.New("neither url nor path set")
	}
}

// profileCheckout resolves the tree the re-run measures. It prefers the
// checkout this process already measured; when there is none (the cold series
// removes each run's tree as it goes), it materialises the same pinned ref
// again, which is the same tree by construction.
func profileCheckout(ctx context.Context, in profileWorkloadInput) (string, error) {
	if in.repoDir != "" {
		if _, err := os.Stat(in.repoDir); err == nil {
			return in.repoDir, nil
		}
	}
	if in.entry.URL == "" {
		return measuredCheckoutDir(in.entry, in.manifestDir, in.scratch)
	}
	dir := filepath.Join(in.scratch, "profile-checkout", in.entry.Name)
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}
	if err := cloneAt(ctx, in.entry.URL, in.entry.Ref, dir); err != nil {
		return "", fmt.Errorf("re-clone %s at %s: %w", in.entry.Name, in.entry.Ref, err)
	}
	return dir, nil
}

// lookupCorpusEntry finds one manifest entry by name. The cold-series path
// needs it only when a gate was missed and the measured tree has to be
// reproduced, so it reads the manifest again rather than threading the entry
// through a measurement that does not use it.
func lookupCorpusEntry(manifestPath, name string) (corpus.Entry, error) {
	m, err := corpus.LoadManifest(manifestPath)
	if err != nil {
		return corpus.Entry{}, err
	}
	for _, e := range m.Entries {
		if e.Name == name {
			return e, nil
		}
	}
	return corpus.Entry{}, fmt.Errorf("%q is not in %s", name, manifestPath)
}

// profileIndex opens a fresh store over dir and indexes it. It is the shared
// setup of every scenario that needs an index before it can run — and, for the
// cold scenario, it IS the scenario.
func profileIndex(ctx context.Context, scratch, name, repoDir string) (graphstore.Graphstore, *ingest.Ingester, func(), error) {
	base := filepath.Join(scratch, "profile-"+name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, nil, nil, err
	}
	store, err := graphstore.OpenSQLite(filepath.Join(base, "index.db"))
	if err != nil {
		return nil, nil, nil, err
	}
	ing, err := ingest.New(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), filepath.Join(base, "meta"))
	if err != nil {
		_ = store.Close()
		return nil, nil, nil, err
	}
	closer := func() {
		_ = ing.Close()
		_ = store.Close()
	}
	return store, ing, closer, nil
}

// coldIndexProfileWorkload profiles the cold full index itself. Nothing is
// prepared inside the window beyond opening the store, because opening a store
// IS part of a cold index.
func coldIndexProfileWorkload(in profileWorkloadInput, series string, checkout func(context.Context) (string, error), scenarioNote string) profileWorkload {
	var repoDir string
	return profileWorkload{
		series:   series,
		scenario: scenarioNote,
		prepare: func(ctx context.Context) (func(), error) {
			dir, err := checkout(ctx)
			repoDir = dir
			return nil, err
		},
		run: func(ctx context.Context) error {
			store, ing, closer, err := profileIndex(ctx, in.scratch, series, repoDir)
			if err != nil {
				return err
			}
			defer closer()
			_ = store
			return ing.IngestAll(ctx, repoDir)
		},
	}
}

// queryLatencyProfileWorkload profiles the warm operations, and only them. The
// index that has to exist first is built in prepare: attributing a cold index
// to a warm-latency gate would point every reader at the wrong code.
func queryLatencyProfileWorkload(in profileWorkloadInput, checkout func(context.Context) (string, error)) profileWorkload {
	var ops []warmOperation
	return profileWorkload{
		series: evalreport.RawSeriesQuery,
		scenario: "the warm stable operations replayed over a freshly indexed store through the same engine/scenario " +
			"FixtureEngine the latency harness measures them with; the index itself is built before the profiler starts, so " +
			"the profile describes the queries and not the ingest that made them possible",
		prepare: func(ctx context.Context) (func(), error) {
			repoDir, err := checkout(ctx)
			if err != nil {
				return nil, err
			}
			store, ing, closer, err := profileIndex(ctx, in.scratch, evalreport.RawSeriesQuery, repoDir)
			if err != nil {
				return nil, err
			}
			if err := ing.IngestAll(ctx, repoDir); err != nil {
				closer()
				return nil, err
			}
			eng := scenario.NewFixtureEngine(resolve.Deps{Query: query.New(store), Search: search.New(store)})
			eng.RepoRoot = repoDir
			eng.ProjectName = in.entry.Name

			sampler, ok := any(store).(graphstore.DegreeSamplePort)
			if !ok {
				closer()
				return nil, errors.New("DegreeSamplePort unavailable: the measured symbol sample cannot be reproduced")
			}
			nodes, err := sampler.DegreeStratifiedSymbols(ctx, in.plan.symbolSample)
			if err != nil {
				closer()
				return nil, err
			}
			if len(nodes) == 0 {
				closer()
				return nil, errors.New("the re-index produced no symbols to query")
			}
			ids := make([]string, 0, len(nodes))
			for _, n := range nodes {
				ids = append(ids, string(n.ID()))
			}
			agentSymbols := min(in.plan.agentSymbols, len(ids))
			ops = buildWarmOperations(ctx, eng, in.entry, ids, agentSymbols)
			for i := range ops {
				// One timed pass per operation and no warmup: the profile needs
				// the call graph, not a distribution, and re-running the floor's
				// thousand executions per class would cost the wallclock of a
				// second measurement for no extra signal.
				ops[i].executions = 1
				ops[i].warmup = 0
			}
			sort.Slice(ops, func(i, j int) bool { return ops[i].op < ops[j].op })
			return closer, nil
		},
		run: func(ctx context.Context) error {
			if len(ops) == 0 {
				return errors.New("no warm operations were prepared")
			}
			for _, op := range ops {
				// Outcomes are deliberately unchecked here: this is a profile of
				// where the time goes, and the semantic gate that decides whether
				// a measurement counts belongs to the harness that measures.
				executeWarmOperation(op, func(warmExecution, string, error) bool { return true })
			}
			return nil
		},
	}
}

// incrementalProfileWorkload profiles the change → incremental-update →
// converge loop the freshness gates are read over.
//
// It applies changes to the checkout, which is why it is only ever built for a
// run that already measured freshness: that run's own measurement mutated the
// same tree last, after every other number in the report was taken.
func incrementalProfileWorkload(in profileWorkloadInput, checkout func(context.Context) (string, error)) profileWorkload {
	var setup incrementalSetup
	return profileWorkload{
		series: evalreport.RawSeriesIncremental,
		scenario: fmt.Sprintf("%d changes driven through the same apply → incremental-update → converge loop the freshness "+
			"harness measures (a diagnostic subset of the measured sequence, not the FR-8 floor); the index the changes are "+
			"applied to is built before the profiler starts", profileIncrementalChanges),
		prepare: func(ctx context.Context) (func(), error) {
			repoDir, err := checkout(ctx)
			if err != nil {
				return nil, err
			}
			store, ing, closer, err := profileIndex(ctx, in.scratch, evalreport.RawSeriesIncremental, repoDir)
			if err != nil {
				return nil, err
			}
			if err := ing.IngestAll(ctx, repoDir); err != nil {
				closer()
				return nil, err
			}
			setup = incrementalSetup{
				repo:     in.repo,
				root:     repoDir,
				store:    store,
				ing:      ing,
				querySvc: query.New(store),
				searcher: search.New(store),
				changes:  min(profileIncrementalChanges, in.changes),
			}
			return closer, nil
		},
		run: func(ctx context.Context) error {
			_, err := measureIncremental(ctx, setup)
			return err
		},
	}
}
