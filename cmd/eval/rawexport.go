package main

// SW-128 (P0-C5): writing the run directory — raw samples, environment, and the
// published report side by side.
//
// SW-124…SW-127 each retain their individual measurements inside their series.
// That is what makes this story cheap; it is NOT what makes the report
// checkable. A series carries the samples AND the percentiles derived from
// them, so "recompute the report from the raw data" over that shape would be
// comparing a number with a file that already contains it.
//
// So the export splits the two. `raw/` holds sample-only files
// (evalreport.RawSampleSet: measurements plus the membership lists a
// recomputation needs, and no derived statistic), `report.json` holds the
// published aggregate exactly as the harness produced it, and `run.json` ties
// them together with the format and harness versions. `-aggregate` then
// reproduces one from the other, and a directory whose report has drifted from
// its samples — hand-edited, half-refactored, or assembled from two runs — is
// caught rather than published.
//
// The published report still contains its own samples: SW-124…SW-127's artifact
// shapes are consumed elsewhere and this story does not narrow them. The
// separation that matters is that the RAW side carries nothing derived, which
// is what makes the check real.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

// exportAuto is the -export-raw value that asks for the SW-128 path
// convention — docs/eval/runs/<date>-<runner-class> — instead of a directory
// the caller names. AC-4 is that the convention is a rule the tool applies, not
// a habit an operator remembers.
const exportAuto = "auto"

// exportOptions is everything the run knows that the export cannot probe.
type exportOptions struct {
	// target is the -export-raw value: a directory, or exportAuto.
	target      string
	runnerClass string
	runnerRole  string
	repo        string
	// workDir is the directory that was measured; it is what the filesystem
	// type is read for.
	workDir string
	// date is the run's date in the directory convention's format. It is a
	// parameter so a test does not depend on today.
	date string

	candidateSHA    string
	candidateSource string
	measuredSHA     string
	candidateMatch  bool
	worktreeDirty   bool

	// profiles are SW-129's profile sets, when a missed gate produced any. They
	// are referenced from the run index rather than copied into it: the sets
	// themselves travel in the published report, which sits in this same
	// directory.
	profiles []evalreport.ProfileSet
}

// resolveExportDir applies the AC-4 path convention. It is separate from the
// export itself because SW-129's profiles have to land in the SAME directory as
// the raw samples, and two places computing that path would eventually disagree
// about which run a profile belongs to.
func resolveExportDir(target, runnerClass, date string) (string, error) {
	if target == exportAuto {
		if date == "" {
			date = time.Now().UTC().Format("2006-01-02")
		}
		dir := evalreport.RunDirPath(date, runnerClass)
		if dir == "" {
			return "", fmt.Errorf("-export-raw %s needs a runner class to name the directory with", exportAuto)
		}
		return dir, nil
	}
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("-export-raw needs a directory (or %q for the %s/<date>-<runner-class> convention)", exportAuto, evalreport.RunsRoot)
	}
	return target, nil
}

// exportRunDir writes the complete run directory and returns the path it wrote
// together with the raw sets it wrote there.
func exportRunDir(o exportOptions, report evalreport.FullRunReport) (string, map[string]evalreport.RawSampleSet, error) {
	dir, err := resolveExportDir(o.target, o.runnerClass, o.date)
	if err != nil {
		return "", nil, err
	}

	env := captureEnvironment(environmentInput{
		workDir:         o.workDir,
		runnerClass:     o.runnerClass,
		runnerRole:      o.runnerRole,
		cacheState:      observedCacheState(report),
		candidateSHA:    o.candidateSHA,
		candidateSource: o.candidateSource,
		measuredSHA:     o.measuredSHA,
		candidateMatch:  o.candidateMatch,
		worktreeDirty:   o.worktreeDirty,
	})

	sets := rawSetsFrom(report, env)
	// The compatibility check runs on the way OUT as well as on the way in: an
	// exporter that produced a set this build cannot describe must fail here,
	// where the run is still on the machine, rather than in a committed
	// directory nobody can aggregate.
	if err := evalreport.CheckRawCompatibility(evalreport.OrderedRawSets(sets)); err != nil {
		return "", nil, err
	}

	index := evalreport.RunIndex{
		Date:        o.date,
		RunnerClass: o.runnerClass,
		Repo:        o.repo,
		Report:      "report.json",
		Profiles:    evalreport.ProfileRefs(o.profiles),
		Environment: env,
	}
	if index.Date == "" {
		index.Date = time.Now().UTC().Format("2006-01-02")
	}
	if err := evalreport.WriteRunDir(dir, index, report, sets); err != nil {
		return "", nil, err
	}
	return dir, sets, nil
}

// observedCacheState reports the page-cache state the run actually REACHED, not
// the protocol it asked for. The series path is preferred because its per-run
// ColdState is the one that was verified ten times; the single-run path falls
// back to its own.
func observedCacheState(report evalreport.FullRunReport) string {
	if series := report.ColdSeries; series != nil {
		for _, run := range series.Runs {
			if run.Status == evalreport.ColdRunCompleted && run.Cold.PageCache != "" {
				return run.Cold.PageCache
			}
		}
	}
	return report.Repo.Cold.PageCache
}

// rawSetsFrom lifts the individual measurements out of a finished report into
// the sample-only raw format.
//
// A series that was not measured produces NO set — and a series that was
// measured and produced nothing produces an EMPTY one. The two are different
// claims (SW-127's silent index is the case that proves it) and the export is
// where they must not be collapsed.
func rawSetsFrom(report evalreport.FullRunReport, env evalreport.RunEnvironment) map[string]evalreport.RawSampleSet {
	repo := report.Repo.Name
	sets := map[string]evalreport.RawSampleSet{}

	if series := report.ColdSeries; series != nil {
		if repo == "" {
			repo = series.Repo
		}
		sets[evalreport.RawSeriesCold] = evalreport.NewRawColdSet(repo, env, series.Runs)
	}
	if series := report.Repo.QueryLatency; series != nil {
		ops, pools := rawQueryFrom(series)
		sets[evalreport.RawSeriesQuery] = evalreport.NewRawQuerySet(repo, env, ops, pools)
	}
	if series := report.Repo.Incremental; series != nil {
		sets[evalreport.RawSeriesIncremental] = evalreport.NewRawIncrementalSet(repo, env, series.Changes)
	}
	if series := report.Repo.Stalls; series != nil {
		intervals := series.Intervals
		if intervals == nil {
			// A measured series with no intervals is a SILENT index, which is a
			// result. Normalising nil to an empty slice keeps it Collected, so
			// the aggregator reads "ran, produced nothing" rather than "never
			// ran" — the distinction SW-127's gate depends on.
			intervals = []evalreport.StallInterval{}
		}
		sets[evalreport.RawSeriesStalls] = evalreport.NewRawStallSet(repo, env, intervals)
	}
	return sets
}

// rawQueryFrom splits SW-125's series into per-operation samples and the
// membership lists. The class and pool memberships are copied verbatim from the
// contract-driven series rather than reconstructed, because the aggregator must
// pool exactly what the report pooled — re-deriving membership would be a
// second definition of the operation → class mapping.
func rawQueryFrom(series *evalreport.QueryLatencySeries) ([]evalreport.RawQueryOperation, []evalreport.RawQueryPool) {
	var ops []evalreport.RawQueryOperation
	for _, op := range series.Operations {
		// An operation with no published distribution (the lifecycle one) has
		// nothing to reproduce and contributes no raw record.
		if op.Latency == nil {
			continue
		}
		ops = append(ops, evalreport.RawQueryOperation{
			Operation: op.Operation,
			Class:     op.Class,
			SamplesUS: op.SamplesUS,
		})
	}
	var pools []evalreport.RawQueryPool
	for _, c := range series.Classes {
		pools = append(pools, evalreport.RawQueryPool{ID: c.Class, Kind: evalreport.RawPoolClass, Operations: c.Operations})
	}
	for _, p := range series.Pools {
		pools = append(pools, evalreport.RawQueryPool{ID: p.GateID, Kind: evalreport.RawPoolGate, Operations: p.Operations})
	}
	return ops, pools
}

// printExportSummary makes the export readable in the job log: where it went,
// what a reader will find there, and the one command that checks it.
//
// A series that was not measured is PRINTED as not measured rather than left
// out — an absence nobody sees is the one that gets mistaken for a zero.
func printExportSummary(w io.Writer, dir string, sets map[string]evalreport.RawSampleSet) {
	fmt.Fprintf(w, "eval: wrote the raw-sample run directory to %s\n", dir)
	for _, series := range evalreport.RawSeriesNames {
		set, ok := sets[series]
		if !ok {
			fmt.Fprintf(w, "eval:   %-16s not measured by this run\n", series)
			continue
		}
		fmt.Fprintf(w, "eval:   %-16s %d raw sample(s) -> %s/%s\n", series, set.Samples, evalreport.RawDir, evalreport.RawFileName(series))
	}
	fmt.Fprintf(w, "eval:   reproduce it with: go run ./cmd/eval -aggregate %s\n", dir)
}
