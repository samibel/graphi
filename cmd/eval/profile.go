package main

// SW-129 (P0-C6): a missed gate produces profiles, and a green run does not.
//
// FR-8 asks for two things at once — "a missed performance gate produces
// profiles" and "no optimisation without a profile" — and PRD §8.5 turns them
// into a process rule. A rule whose evidence has to be produced by hand
// afterwards is a request; the realistic sequence under one is optimise first,
// justify second, which is the habit P0 exists to break. So the profile is made
// a BY-PRODUCT of the failure: the run that read a gate as missed produces the
// profiles itself, before it exits, into the same run directory as its raw
// samples.
//
// The ordering constraint that shapes this whole file is AC-4. Profiling the
// measurement would distort the measurement, so the profiler is not merely
// "written to /dev/null on a green run" — it is NEVER STARTED. Nothing here
// executes, no directory is created and no runtime sampling rate is touched
// until a gate has already been read as FAIL. What is profiled is therefore a
// DIAGNOSTIC RE-EXECUTION of the affected scenario: same machine, same
// checkout, same binary, immediately after the verdict. That localises where
// the cost is; it is not a replay of the exact execution that missed, and the
// artifact says so rather than letting a reader assume otherwise.
//
// AC-5 is the other half of the honesty: a capture that fails is recorded,
// printed, and checked at the exit decision. A run whose profiles could not be
// produced must not read green — otherwise "no profile" and "no problem" look
// alike in CI, which is the exact confusion this story removes.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"syscall"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

// profileDisabledFlag is named in the log when the automation is turned off, so
// a reader of a job that missed a gate and produced no profiles can see which
// switch did it rather than suspecting a bug.
const profileDisabledFlag = "-profile-on-miss=false"

// Seams over the two pieces of PROCESS-GLOBAL state a capture touches. They are
// variables so a test can prove the strong form of AC-4 — that a green run does
// not merely produce no files, but never starts the profiler and never changes
// a sampling rate. A test that could only observe the absence of files would
// not distinguish "no profiling" from "profiling that wrote nowhere".
var (
	setBlockProfileRate = runtime.SetBlockProfileRate
	onProfileWindow     = func() {}
)

// profileWorkload is one scenario, re-executable under a profiler.
//
// The prepare/run split is the measurement boundary, exactly as in the warm
// query harness: everything that is not the profiled scenario (cloning,
// indexing before a query workload, opening stores) happens in prepare, OUTSIDE
// the window, so the profile describes the scenario and not its setup.
type profileWorkload struct {
	series string
	// scenario states, in the harness's own words, what is re-executed and how
	// it relates to the gate that was missed. It travels into the artifact.
	scenario string
	// prepare runs UNTIMED and UNPROFILED, and returns a cleanup.
	prepare func(ctx context.Context) (func(), error)
	// run is the profiled region, and only it.
	run func(ctx context.Context) error
}

// profileWorkloadBuilder resolves a series to the workload that re-executes it.
// An error means this run cannot re-execute that scenario — which is recorded
// as a profile failure, never as "no profile was needed".
type profileWorkloadBuilder func(series string) (profileWorkload, error)

type profileRunInput struct {
	// root is the profile root: <run-dir>/profiles in the canonical case.
	root string
	repo string
	// enabled is the -profile-on-miss switch. Default true; see AC-1.
	enabled bool
	build   profileWorkloadBuilder
	now     func() time.Time
}

// profileMissedGates is the whole automation: read the finished report, and for
// every scenario that missed a gate, re-execute it under the profiler.
//
// Returns one ProfileSet per affected scenario — including the ones that could
// not be captured, because a failure that leaves no record is indistinguishable
// from a run that needed no profile.
func profileMissedGates(ctx context.Context, in profileRunInput, report evalreport.FullRunReport, w io.Writer) []evalreport.ProfileSet {
	missed := evalreport.MissedGates(report)
	if len(missed) == 0 {
		// AC-4. This early return is the whole of "profiling is off on the
		// normal path": no directory, no sampling rate, no work.
		return nil
	}
	if !in.enabled {
		fmt.Fprintf(w, "eval: %d gate(s) missed (%s) but profiling is off (%s): PRD §8.5 asks a fix to cite a profile, and this run produced none\n",
			len(missed), strings.Join(missedGateIDs(missed), ", "), profileDisabledFlag)
		return nil
	}

	now := in.now
	if now == nil {
		now = time.Now
	}
	// Artifact paths are relative to the profile root's PARENT, so in the
	// canonical case they read as `profiles/<scenario>/cpu.pprof` from the run
	// directory a reader opened.
	prefix := filepath.Base(in.root)

	var sets []evalreport.ProfileSet
	for _, series := range evalreport.MissedGatesBySeries(missed) {
		gates := evalreport.GateRefsOf(missed, series)
		ids := make([]string, 0, len(gates))
		for _, g := range gates {
			ids = append(ids, g.ID)
		}
		rel := path.Join(prefix, series)
		fmt.Fprintf(w, "eval: gate(s) %s missed on %s — profiling a diagnostic re-run of that scenario into %s\n",
			strings.Join(ids, ", "), series, filepath.Join(in.root, series))

		set := evalreport.NewProfileSet(series, in.repo, "", rel, gates)
		workload, err := in.build(series)
		switch {
		case err != nil:
			set.Error = fmt.Sprintf("the %s scenario could not be re-executed: %v", series, err)
		default:
			set.Scenario = workload.scenario
			started := now()
			set.StartedAt = started.UTC().Format(time.RFC3339)
			artifacts, counters, runErr, captureErr := captureProfileSet(ctx, filepath.Join(in.root, series), rel, workload)
			set.DurationMS = now().Sub(started).Milliseconds()
			set.Artifacts = artifacts
			set.IOCounters = counters
			switch {
			case captureErr != nil:
				set.Error = captureErr.Error()
			case runErr != nil:
				// The profiles are still written and still worth reading — a
				// scenario that broke is exactly what one explains — but the set
				// does not read as clean.
				set.Error = fmt.Sprintf("the re-executed %s scenario failed: %v", series, runErr)
			}
			set.Complete = set.Error == "" && profilesAllWritten(artifacts)
		}
		printProfileSet(w, set)
		sets = append(sets, set)
	}

	// The profile root explains itself even when it is read on its own, away
	// from the run directory.
	if err := evalreport.WriteProfileIndex(in.root, evalreport.NewProfileIndex(sets)); err != nil {
		fmt.Fprintf(w, "eval: FAIL - profile index: %v\n", err)
	}
	return sets
}

// captureProfileSet re-executes one workload with all four profilers running
// and writes them into dir.
//
// It returns the workload's error and the capture's error separately: "the
// scenario broke" and "the profiles could not be written" are different facts
// and a reader acts on them differently.
func captureProfileSet(ctx context.Context, dir, rel string, workload profileWorkload) (artifacts []evalreport.ProfileArtifact, counters *evalreport.ProfileIOCounters, runErr, captureErr error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Detected BEFORE the scenario is re-executed. A capture that discovers
		// on the last write that it has nowhere to put anything has already
		// spent the cost of an index for nothing.
		return nil, nil, nil, fmt.Errorf("create profile directory %s: %w", dir, err)
	}

	files := make(map[string]*os.File, len(evalreport.ProfileKinds))
	artifacts = make([]evalreport.ProfileArtifact, 0, len(evalreport.ProfileKinds))
	closeAll := func() {
		for _, f := range files {
			_ = f.Close()
		}
	}
	for _, kind := range evalreport.ProfileKinds {
		name := evalreport.ProfileFileName(kind)
		if name == "" {
			closeAll()
			return nil, nil, nil, fmt.Errorf("no file name is defined for profile kind %q", kind)
		}
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			closeAll()
			return nil, nil, nil, fmt.Errorf("create %s profile: %w", kind, err)
		}
		files[kind] = f
		artifacts = append(artifacts, evalreport.ProfileArtifact{
			Kind: kind, File: path.Join(rel, name), Mechanism: evalreport.ProfileMechanism[kind],
		})
	}
	defer closeAll()

	if workload.prepare != nil {
		cleanup, err := workload.prepare(ctx)
		if cleanup != nil {
			defer cleanup()
		}
		if err != nil {
			return artifacts, nil, nil, fmt.Errorf("prepare the %s scenario: %w", workload.series, err)
		}
	}

	// ─── the profile window ─────────────────────────────────────────────────
	onProfileWindow()
	var before, after syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &before)

	cpuErr := pprof.StartCPUProfile(files[evalreport.ProfileCPU])
	// Rate 1 records every blocking event. The cost is real and it is paid
	// only here, in a diagnostic re-run that measures nothing.
	setBlockProfileRate(1)

	if workload.run != nil {
		runErr = workload.run(ctx)
	}

	setBlockProfileRate(0)
	if cpuErr == nil {
		pprof.StopCPUProfile()
	}
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &after)
	// ─── end of the profile window ──────────────────────────────────────────

	// A forced GC before the heap profile is what makes it a statement about
	// RETAINED memory rather than about whatever had not been collected yet.
	runtime.GC()

	writeLookup := func(kind, lookup string) error {
		p := pprof.Lookup(lookup)
		if p == nil {
			return fmt.Errorf("this Go runtime has no %q profile", lookup)
		}
		return p.WriteTo(files[kind], 0)
	}

	errs := map[string]error{evalreport.ProfileCPU: cpuErr}
	errs[evalreport.ProfileHeap] = writeLookup(evalreport.ProfileHeap, "heap")
	errs[evalreport.ProfileAllocs] = writeLookup(evalreport.ProfileAllocs, "allocs")
	// See evalreport.ProfileMechanism: Go has no file-I/O profile, and the block
	// profile is what `go tool pprof` can actually read for waiting.
	errs[evalreport.ProfileIO] = writeLookup(evalreport.ProfileIO, "block")

	for i := range artifacts {
		kind := artifacts[i].Kind
		if err := files[kind].Close(); err != nil && errs[kind] == nil {
			errs[kind] = err
		}
		delete(files, kind)
		if err := errs[kind]; err != nil {
			artifacts[i].Error = err.Error()
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, evalreport.ProfileFileName(kind)))
		if err != nil {
			artifacts[i].Error = err.Error()
			continue
		}
		if len(raw) == 0 {
			artifacts[i].Error = "the profile is empty"
			continue
		}
		artifacts[i].Bytes = int64(len(raw))
		artifacts[i].Digest = evalreport.ProfileDigest(raw)
		artifacts[i].Written = true
	}
	return artifacts, ioCountersBetween(before, after), runErr, nil
}

// ioCountersBetween reports what the operating system says the profiled
// scenario did to the block device. It is published because the `io` profile is
// a BLOCK profile and cannot answer this question; inferring I/O volume from
// goroutine blocking would be a guess wearing a measurement's clothes.
func ioCountersBetween(before, after syscall.Rusage) *evalreport.ProfileIOCounters {
	return &evalreport.ProfileIOCounters{
		BlockInputOps:  int64(after.Inblock) - int64(before.Inblock),
		BlockOutputOps: int64(after.Oublock) - int64(before.Oublock),
		Available:      true,
		Source:         "getrusage(RUSAGE_SELF) ru_inblock/ru_oublock, delta across the profiled scenario",
		Note: "block-device operations only: a read served from the page cache costs no ru_inblock, and some platforms " +
			"(macOS among them) report zero for both. A zero here is not a claim that no I/O happened.",
	}
}

// profilesAllWritten is the completeness rule: AC-1 asks for four profiles, so
// three is an incomplete set, not a partial success.
func profilesAllWritten(artifacts []evalreport.ProfileArtifact) bool {
	if len(artifacts) != len(evalreport.ProfileKinds) {
		return false
	}
	for _, a := range artifacts {
		if !a.Written {
			return false
		}
	}
	return true
}

// profileFailure is AC-5 at the exit decision: an incomplete profile set is on
// its own enough to keep a run from reading green.
//
// It is checked explicitly rather than left to the fact that a missed gate
// already fails the run. Today those coincide; a rule that holds only by
// coincidence is not a rule, and the coincidence would break the first time a
// gate is added whose miss is reported without failing the job.
func profileFailure(sets []evalreport.ProfileSet) (string, bool) {
	failures := evalreport.IncompleteProfileSets(sets)
	if len(failures) == 0 {
		return "", false
	}
	return strings.Join(failures, "; "), true
}

// missedGateIDs is the flat list of gate ids, for one log line.
func missedGateIDs(missed []evalreport.MissedGate) []string {
	out := make([]string, 0, len(missed))
	for _, m := range missed {
		out = append(out, m.ID)
	}
	return out
}

// printProfileSet makes one set readable in the job log — including, and
// especially, a set that failed (AC-5: visible, not merely recorded).
func printProfileSet(w io.Writer, set evalreport.ProfileSet) {
	if set.Error != "" {
		fmt.Fprintf(w, "eval:   FAIL profile %-16s %s\n", set.Series, set.Error)
	}
	written := make([]string, 0, len(set.Artifacts))
	for _, a := range set.Artifacts {
		if a.Written {
			written = append(written, fmt.Sprintf("%s (%d B)", a.Kind, a.Bytes))
			continue
		}
		fmt.Fprintf(w, "eval:   FAIL profile %-16s %s: %s\n", set.Series, a.Kind, a.Error)
	}
	if len(written) > 0 {
		fmt.Fprintf(w, "eval:   profile %-16s %s in %dms — open with: go tool pprof <file>\n",
			set.Series, strings.Join(written, ", "), set.DurationMS)
	}
}

// resolveProfileRoot decides where profile sets go.
//
// AC-2 puts them beside the raw data of the same run, so an exported run
// directory wins and the tool applies the convention rather than an operator
// remembering it. Without an export there is still a destination — the
// automation must not become conditional on a flag nobody passed — but the
// caller is told it is not the canonical one, so a reader is never left
// thinking a stray directory is part of the run record.
func resolveProfileRoot(explicit, exportRaw, outPath, runnerClass, date string) (root string, canonical bool, err error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, false, nil
	}
	if exportRaw != "" {
		dir, err := resolveExportDir(exportRaw, runnerClass, date)
		if err != nil {
			return "", false, err
		}
		return filepath.Join(dir, evalreport.ProfileDir), true, nil
	}
	base := "."
	if strings.TrimSpace(outPath) != "" {
		base = filepath.Dir(outPath)
	}
	return filepath.Join(base, evalreport.ProfileDir), false, nil
}
