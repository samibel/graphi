package main

// SW-124 (P0-C1): ten cold runs instead of one.
//
// `-full-run` measures a single cold index. FR-8 asks for at least ten per
// reference scenario, reported as p50 AND p95 — and one sample is not a
// distribution. `-cold-runs N` repeats the existing measurement N times and
// keeps every sample, so nothing published here is a number a reader has to
// take on trust.
//
// WHY EACH RUN IS ITS OWN PROCESS. getrusage MAXRSS is a process-LIFETIME peak.
// Ten runs inside one process would make peak RSS monotone: run 2..10 would
// inherit run 1's peak and the "distribution" would be an artefact of the
// measurement, not of the software. The same argument already forced one repo
// per process in fullrun.go; repetition forces one RUN per process. The child
// is this same binary invoked with the pre-existing single-run flags, so the
// repeated path and the PR path execute identical measurement code — there is
// no second harness to keep honest (AC-7).
//
// WHAT COUNTS AS A COMPLETED RUN. A child that produced a cold-index
// measurement contributes its sample even if its warm semantic checks failed:
// the cold numbers are valid and the child's own verdict travels with them.
// A child that produced no readable index measurement is an ABORT — counted,
// named, and kept in the report, never silently dropped from the distribution
// (AC-5).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
	"github.com/samibel/graphi/internal/evidence"
)

// defaultCandidateIndexPath is the checked-in evidence index, which is where
// the FROZEN candidate SHA is cited (SW-116/SW-121). The harness reads it
// rather than accepting a SHA on the command line: a candidate a run can
// declare for itself is not a frozen candidate.
const defaultCandidateIndexPath = "docs/rc/evidence-index.yaml"

// coldSeriesNotes explains the artifact to a reader who has only the JSON.
const coldSeriesNotes = "SW-124 cold-index series: N independent cold runs, ONE PROCESS PER RUN (getrusage MAXRSS is a " +
	"process-lifetime peak, so in-process repetition would publish run 1's peak for every later run). `repo` is one named " +
	"sample kept for comparability with single-run reports; every distributional claim is in `cold_series`. NO gate — the " +
	"OOM gate included — is read unless the run is the reference scenario on the reference class and from the frozen " +
	"candidate; the percentile gates additionally require at least minimum_runs completed runs. Otherwise they are " +
	"UNKNOWN, which is not a PASS (PRD §8.2)."

type coldSeriesOptions struct {
	manifestPath  string
	repoName      string
	workDir       string
	runnerClass   string
	outPath       string
	budgetPath    string
	scenarioPath  string
	candidatePath string
	runs          int
	dropCaches    bool
	oomCheck      bool
	// exportRaw is SW-128's raw-sample run directory, or "" for no export.
	exportRaw string
	// profileOnMiss and profileDir are SW-129. The SERIES owns the profiling,
	// not its children: each child run's tree is deleted the moment that run
	// finishes, so a child's profiles would be written beside a directory that
	// no longer exists — and ten children profiling the same missed gate would
	// produce nine redundant re-runs. Children are therefore launched with
	// profiling off (see execColdRun) and this process profiles the assembled
	// series once.
	profileOnMiss bool
	profileDir    string
}

// coldRunExit is what the operating system said about one child run.
type coldRunExit struct {
	argv     []string
	pid      int
	exitCode int
	signal   string
}

// coldRunExecutor performs ONE cold run in a separate process and returns the
// report that run wrote. limitBytes > 0 asks for the run to be executed under
// an imposed memory limit (the SW-123 OOM method).
//
// It is a parameter so the series logic — abort classification, aggregation,
// gate reading, OOM verdict — is testable without spawning real indexes; the
// production implementation is execColdRun.
type coldRunExecutor func(ctx context.Context, run int, outPath string, limitBytes int64) (evalreport.FullRunReport, coldRunExit, error)

// coldRunExecutorFactory binds an executor to the resolved working directory.
// It is a parameter of runColdSeries so a test can substitute the whole
// child-process layer; production always passes execColdRun.
type coldRunExecutorFactory func(o coldSeriesOptions, workDir string) coldRunExecutor

// runColdSeries is `-full-run <repo> -cold-runs N`. Exit codes match the
// single-run path: 0 measured (PASS or UNKNOWN), 1 a gate FAILED or the series
// could not be measured at all, 2 a usage or configuration error.
//
// UNKNOWN exits 0 deliberately, following `cmd/evidence -check`: an UNKNOWN row
// is honest reporting, not a broken job. It can never be mistaken for a pass
// because the artifact and the stderr summary both say UNKNOWN.
func runColdSeries(o coldSeriesOptions, newExecutor coldRunExecutorFactory) int {
	ctx := context.Background()

	if o.runs < 2 {
		fmt.Fprintf(os.Stderr, "eval: -cold-runs %d: a series needs at least 2 runs (use the plain -full-run path for one)\n", o.runs)
		return 2
	}
	class, isReference, err := resolveRunnerClass(o.scenarioPath, o.manifestPath, o.runnerClass, o.repoName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: %v\n", err)
		return 2
	}
	// AC-8: the candidate is cited, never asserted. An unreadable evidence
	// index is a configuration failure, not a run without a candidate — a
	// series that cannot say what it measured is not evidence.
	candidateSHA, candidateSource, err := loadCandidateSHA(o.candidatePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: candidate: %v\n", err)
		return 2
	}

	workDir := o.workDir
	if workDir == "" {
		workDir, err = os.MkdirTemp("", "graphi-eval-cold")
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: workdir: %v\n", err)
			return 2
		}
		defer os.RemoveAll(workDir)
	}

	execRun := newExecutor(o, workDir)

	measured := resolveCommit()
	series := evalreport.ColdRunSeries{
		Repo:              o.repoName,
		RunsRequested:     o.runs,
		MinimumRuns:       evalreport.ColdRunMinimum,
		RunnerClass:       o.runnerClass,
		RunnerRole:        class.Role,
		ReferenceScenario: isReference,
		CandidateSHA:      candidateSHA,
		CandidateSource:   candidateSource,
		MeasuredSHA:       measured,
		AggregateMethod:   evalreport.AggregateMethodNote,
	}
	series.WorktreeDirty = strings.HasSuffix(measured, "+dirty")
	series.CandidateMatch = !series.WorktreeDirty && strings.EqualFold(strings.TrimSuffix(measured, "+dirty"), candidateSHA)
	if !series.CandidateMatch {
		series.Warnings = append(series.Warnings, fmt.Sprintf(
			"measured revision %s is not the frozen candidate %s (cited from %s): these numbers are not evidence about the candidate artifact",
			measured, candidateSHA, candidateSource))
	}

	for run := 1; run <= o.runs; run++ {
		reportPath := filepath.Join(workDir, fmt.Sprintf("cold-run-%02d.json", run))
		started := time.Now().UTC().Format(time.RFC3339)
		report, exit, err := execRun(ctx, run, reportPath, 0)
		sample := coldRunSampleFrom(run, started, reportPath, o.runnerClass, report, exit, err)
		if sample.Status == evalreport.ColdRunAborted {
			series.RunsAborted++
			series.Warnings = append(series.Warnings, fmt.Sprintf("run %d aborted: %s", run, sample.Error))
			fmt.Fprintf(os.Stderr, "eval: run %d/%d ABORTED: %s\n", run, o.runs, sample.Error)
		} else {
			series.RunsCompleted++
			fmt.Fprintf(os.Stderr, "eval: run %d/%d index %dms rss %dMB db %dB cold=%v\n",
				run, o.runs, sample.Index.WallclockMS, sample.StablePeakRSSMB, sample.Index.DBSizeBytes, sample.Cold.Verified)
			if !sample.Cold.Verified {
				series.Warnings = append(series.Warnings, fmt.Sprintf("run %d was not verified cold: %s", run, sample.Cold.Reason))
			}
			if sample.Commit != "" && sample.Commit != measured {
				series.Warnings = append(series.Warnings, fmt.Sprintf(
					"run %d was measured by revision %s, but the series is stamped %s: a series assembled from mixed revisions is not one measurement",
					run, sample.Commit, measured))
			}
		}
		series.Runs = append(series.Runs, sample)
	}
	series.Sufficient = series.RunsCompleted >= series.MinimumRuns
	series.Aggregates = evalreport.RecomputeColdAggregates(series.Runs)

	// The provenance blocker is computed before the OOM check runs, from the
	// fields that are already final at this point (reference scenario, dirty
	// worktree, candidate match), so the OOM verdict is subject to the same
	// "is this about the candidate at all" rule as every other gate.
	series.OOMCheck = runOOMCheck(ctx, o, workDir, execRun, coldGateProvenanceBlocker(series))
	series.Gates, series.StopRule = readColdGates(o.scenarioPath, series)
	series.Status = coldSeriesStatus(&series)
	series.Notes = coldSeriesNotes

	report := evalreport.FullRunReport{
		Header:            evalreport.NewHeader("0.0.0-dev", measured),
		RunnerClass:       o.runnerClass,
		RunnerRole:        class.Role,
		ReferenceScenario: isReference,
		ScenarioSource:    o.scenarioPath,
		Notes:             fullRunNotes,
		ColdSeries:        &series,
		Cgroup:            observedCgroupLimits(),
	}
	report.Header.CorpusVersion = manifestVersion(o.manifestPath)
	// `repo` stays a single comparable sample so the report shape historical
	// consumers read is still present, and repo_run_index says which run it is.
	for i, sample := range series.Runs {
		if sample.Status == evalreport.ColdRunCompleted {
			if full, err := readFullRunReport(sample.ReportPath); err == nil {
				report.Repo = full.Repo
				report.RepoRunIndex = series.Runs[i].Run
			}
			break
		}
	}

	outPath := o.outPath
	if outPath == "" {
		outPath = "eval-cold-" + o.repoName + ".json"
	}

	// SW-129: the series' gates are final here, so a missed one is a fact and
	// the scenario behind it is re-executed under the profilers. Nothing runs
	// when nothing was missed (AC-4). The checkout is gone by now — each run's
	// tree is removed as it completes — so the workload materialises the same
	// pinned ref again; that is the same tree by construction, and it is done
	// outside the profile window.
	date := time.Now().UTC().Format("2006-01-02")
	profileRoot, canonical, err := resolveProfileRoot(o.profileDir, o.exportRaw, outPath, o.runnerClass, date)
	if err != nil {
		fmt.Fprintf(os.Stderr, "eval: %v\n", err)
		return 2
	}
	entry, entryErr := lookupCorpusEntry(o.manifestPath, o.repoName)
	report.Profiles = profileMissedGates(ctx, profileRunInput{
		root:    profileRoot,
		repo:    o.repoName,
		enabled: o.profileOnMiss,
		build: func(series string) (profileWorkload, error) {
			if entryErr != nil {
				return profileWorkload{}, fmt.Errorf("the measured tree cannot be reproduced: %w", entryErr)
			}
			return profileWorkloadBuilderFor(profileWorkloadInput{
				repo:        o.repoName,
				scratch:     workDir,
				manifestDir: filepath.Dir(o.manifestPath),
				entry:       entry,
				plan:        newQueryLatencyPlan(0, nil),
			})(series)
		},
	}, report, os.Stderr)
	if len(report.Profiles) > 0 && !canonical {
		fmt.Fprintf(os.Stderr, "eval: NOTE - the profiles went to %s; the convention is <run-dir>/%s, which needs -export-raw\n",
			profileRoot, evalreport.ProfileDir)
	}
	profileBroken, profilesFailed := profileFailure(report.Profiles)
	if profilesFailed {
		fmt.Fprintf(os.Stderr, "eval: FAIL - profile generation: %s\n", profileBroken)
	}

	if err := evalreport.WriteFullRunJSON(report, outPath); err != nil {
		fmt.Fprintf(os.Stderr, "eval: write report: %v\n", err)
		return 2
	}
	fmt.Fprintf(os.Stderr, "eval: wrote cold-run series to %s\n", outPath)

	// SW-128, exported before the verdict for the same reason the single-run
	// path does it: an aborted or failing series is when the individual samples
	// matter most.
	if o.exportRaw != "" {
		dir, sets, err := exportRunDir(exportOptions{
			target:          o.exportRaw,
			runnerClass:     o.runnerClass,
			runnerRole:      class.Role,
			repo:            o.repoName,
			workDir:         workDir,
			date:            date,
			candidateSHA:    series.CandidateSHA,
			candidateSource: series.CandidateSource,
			measuredSHA:     series.MeasuredSHA,
			candidateMatch:  series.CandidateMatch,
			worktreeDirty:   series.WorktreeDirty,
			profiles:        report.Profiles,
		}, report)
		if err != nil {
			fmt.Fprintf(os.Stderr, "eval: export raw samples: %v\n", err)
			return 2
		}
		printExportSummary(os.Stderr, dir, sets)
	}

	printColdSeriesSummary(os.Stderr, &series)

	if series.Status == evalreport.StatusFail {
		return 1
	}
	// SW-129 AC-5: profiles that could not be produced never leave a green run.
	if profilesFailed {
		return 1
	}
	return 0
}

// coldRunSampleFrom classifies one child run. The classification rule is the
// load-bearing part: a run counts as COMPLETED when it produced a cold-index
// wallclock, regardless of the child's exit code, because a warm semantic
// check failing downstream does not invalidate the cold measurement that
// preceded it — and requiring exit 0 would silently drop every sample from a
// repository with a known warm defect.
func coldRunSampleFrom(run int, started, reportPath, runnerClass string, report evalreport.FullRunReport, exit coldRunExit, err error) evalreport.ColdRunSample {
	sample := evalreport.ColdRunSample{
		Run:         run,
		StartedAt:   started,
		RunnerClass: runnerClass,
		ReportPath:  reportPath,
	}
	abort := func(reason string) evalreport.ColdRunSample {
		sample.Status = evalreport.ColdRunAborted
		sample.Error = reason
		return sample
	}
	if err != nil {
		return abort(err.Error())
	}
	if exit.signal != "" {
		return abort(fmt.Sprintf("child was killed by %s (argv: %s)", exit.signal, strings.Join(exit.argv, " ")))
	}
	if report.Repo.Index.WallclockMS <= 0 {
		reason := "the child produced no cold-index measurement"
		if len(report.Repo.Failures) > 0 {
			reason += ": " + strings.Join(report.Repo.Failures, "; ")
		} else if exit.exitCode != 0 {
			reason += fmt.Sprintf(" (exit %d)", exit.exitCode)
		}
		return abort(reason)
	}

	sample.Status = evalreport.ColdRunCompleted
	sample.Commit = report.Header.Commit
	sample.RepoSHA = report.Repo.SHA
	sample.CloneMS = report.Repo.CloneMS
	sample.Cold = report.Repo.Cold
	sample.Index = report.Repo.Index
	sample.StablePeakRSSMB = report.Repo.StablePeakRSSMB
	sample.BytesPerEdge = evalreport.BytesPerEdge(report.Repo.Index)
	sample.Cgroup = report.Cgroup
	sample.RunPass = report.Repo.Pass
	sample.RunFailures = report.Repo.Failures
	return sample
}

// coldSeriesStatus applies PRD §8.2 to the series. FAIL beats UNKNOWN beats
// PASS: a gate that failed is a failure even if another gate was unmeasured,
// and anything unmeasured stops the series from reading green.
func coldSeriesStatus(s *evalreport.ColdRunSeries) string {
	if s.RunsCompleted == 0 {
		return evalreport.StatusFail
	}
	if s.StopRule != nil && s.StopRule.Triggered {
		return evalreport.StatusFail
	}
	for _, g := range s.Gates {
		if g.Status == evalreport.StatusFail {
			return evalreport.StatusFail
		}
	}
	if !s.Sufficient || !s.CandidateMatch || s.RunsAborted > 0 {
		return evalreport.StatusUnknown
	}
	for _, g := range s.Gates {
		if g.Status != evalreport.StatusPass {
			return evalreport.StatusUnknown
		}
	}
	if len(s.Gates) == 0 {
		return evalreport.StatusUnknown
	}
	return evalreport.StatusPass
}

func printColdSeriesSummary(w *os.File, s *evalreport.ColdRunSeries) {
	fmt.Fprintf(w, "eval: cold series over %s — %d/%d runs completed, %d aborted (minimum %d, sufficient=%v)\n",
		s.Repo, s.RunsCompleted, s.RunsRequested, s.RunsAborted, s.MinimumRuns, s.Sufficient)
	if wall, ok := s.Aggregates[evalreport.MetricIndexWallclockMS]; ok {
		fmt.Fprintf(w, "eval:   cold index  p50 %.0f ms  p95 %.0f ms  (min %.0f, max %.0f, n=%d)\n",
			wall.P50, wall.P95, wall.Min, wall.Max, wall.N)
	}
	if rss, ok := s.Aggregates[evalreport.MetricStablePeakRSSMB]; ok {
		fmt.Fprintf(w, "eval:   peak RSS    p50 %.0f MB  max %.0f MB\n", rss.P50, rss.Max)
	}
	for _, g := range s.Gates {
		fmt.Fprintf(w, "eval:   gate %-20s %-8s %s\n", g.ID, g.Status, g.Reason)
	}
	fmt.Fprintf(w, "eval:   oom gate %s: %s\n", s.OOMCheck.Status, s.OOMCheck.Reason)
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "eval:   WARNING %s\n", warning)
	}
	fmt.Fprintf(w, "eval: %s - cold series over %s\n", s.Status, s.Repo)
}

// loadCandidateSHA cites the frozen candidate from the evidence index. A blank
// SHA is an error: the index's own CI check already forbids it, and a series
// that recorded an empty candidate would make every run trivially "not the
// candidate" for the wrong reason.
func loadCandidateSHA(path string) (sha, source string, err error) {
	if path == "" {
		path = defaultCandidateIndexPath
	}
	idx, err := evidence.Load(path)
	if err != nil {
		return "", "", err
	}
	sha = strings.TrimSpace(idx.Candidate.SHA)
	if sha == "" {
		return "", "", fmt.Errorf("%s cites no candidate sha", path)
	}
	source = idx.Candidate.Source
	if source == "" {
		source = path
	}
	return sha, source, nil
}

func readFullRunReport(path string) (evalreport.FullRunReport, error) {
	var report evalreport.FullRunReport
	raw, err := os.ReadFile(path)
	if err != nil {
		return report, err
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return report, fmt.Errorf("parse %s: %w", path, err)
	}
	return report, nil
}

// execColdRun is the production executor: it re-invokes THIS binary on the
// pre-existing single-run path. Reusing the shipped flags rather than an
// internal entry point is what keeps the repeated measurement and the PR-path
// measurement the same code.
func execColdRun(o coldSeriesOptions, workDir string) coldRunExecutor {
	return func(ctx context.Context, run int, outPath string, limitBytes int64) (evalreport.FullRunReport, coldRunExit, error) {
		self, err := os.Executable()
		if err != nil {
			return evalreport.FullRunReport{}, coldRunExit{}, fmt.Errorf("locate the eval binary: %w", err)
		}
		// A per-run working directory is what makes the store and meta paths
		// genuinely absent before the run — the coldness prepareCold then
		// verifies rather than assumes.
		runWorkDir := filepath.Join(workDir, fmt.Sprintf("run-%02d", run))
		if err := os.MkdirAll(runWorkDir, 0o755); err != nil {
			return evalreport.FullRunReport{}, coldRunExit{}, fmt.Errorf("run %d workdir: %w", run, err)
		}
		argv := coldRunArgv(self, o, runWorkDir, outPath, limitBytes)

		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stderr = os.Stderr
		runErr := cmd.Run()
		exit := coldRunExit{argv: argv}
		if cmd.Process != nil {
			exit.pid = cmd.Process.Pid
		}
		exit.exitCode, exit.signal = classifyExit(runErr, cmd)

		// The clone and the SQLite store of a finished run are not evidence —
		// the report is — and ten of them would exhaust a hosted runner's disk
		// on the reference scenario. The report lives outside runWorkDir, so
		// dropping the tree here loses nothing. Errors are ignored: a run under
		// `sudo systemd-run` leaves root-owned files behind, and failing the
		// measurement over cleanup would be the wrong trade.
		defer func() { _ = os.RemoveAll(runWorkDir) }()

		report, readErr := readFullRunReport(outPath)
		if readErr != nil {
			// No report at all: report the OS-level outcome, which is the only
			// thing left to say about the run.
			detail := readErr.Error()
			if runErr != nil {
				detail = runErr.Error() + " (" + detail + ")"
			}
			return evalreport.FullRunReport{}, exit, fmt.Errorf("run %d produced no readable report: %s", run, detail)
		}
		return report, exit, nil
	}
}

// coldRunArgv is the command one child run is executed as. It is a function of
// its own so the rules encoded in it — which flags a child inherits, and which
// it must NOT — are assertable without spawning a process.
func coldRunArgv(self string, o coldSeriesOptions, runWorkDir, outPath string, limitBytes int64) []string {
	argv := []string{
		self,
		"-manifest", o.manifestPath,
		"-full-run", o.repoName,
		"-runner-class", o.runnerClass,
		"-workdir", runWorkDir,
		"-out", outPath,
		// SW-129: the series profiles a missed gate ONCE, itself. A child's
		// tree is deleted the moment that child finishes, so its profiles would
		// describe a directory that no longer exists — and ten children reading
		// the same missed gate would pay for nine redundant re-runs.
		"-profile-on-miss=false",
	}
	if o.scenarioPath != "" {
		argv = append(argv, "-reference-scenario", o.scenarioPath)
	}
	if o.budgetPath != "" {
		argv = append(argv, "-budgets", o.budgetPath)
	}
	if o.dropCaches {
		argv = append(argv, "-drop-caches")
	}
	if limitBytes > 0 {
		argv = append(imposeMemoryLimit(limitBytes), argv...)
	}
	return argv
}

// classifyExit turns a *exec.ExitError into (code, signal). A signalled child
// is reported as a signal rather than as exit code -1, because "killed by
// SIGKILL" is exactly the OOM gate's failure signal.
func classifyExit(runErr error, cmd *exec.Cmd) (int, string) {
	if cmd.ProcessState != nil {
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal()), status.Signal().String()
		}
		return cmd.ProcessState.ExitCode(), ""
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode(), ""
	}
	if runErr != nil {
		return -1, ""
	}
	return 0, ""
}

// imposeMemoryLimit builds the SW-123 method's wrapper argv. The limit is
// constructed from the contract's byte figure rather than parsed out of its
// prose, and MemorySwapMax=0 is not optional: with swap the process would be
// throttled instead of killed and the gate would pass for the wrong reason.
//
// Whether the wrapper actually took effect is never inferred from this argv —
// the limit is read back from inside the constrained process and compared to
// the contract's exact byte figure (see evaluateOOM).
func imposeMemoryLimit(limitBytes int64) []string {
	argv := []string{
		"systemd-run", "--scope", "--collect", "--quiet",
		"--property=MemoryMax=" + strconv.FormatInt(limitBytes, 10),
		"--property=MemorySwapMax=0",
		"--",
	}
	if os.Geteuid() != 0 {
		// The contract's method is `sudo systemd-run …`; -n keeps a scheduled
		// job from hanging on a password prompt.
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	return argv
}
