package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/evalreport"
)

// profileProbe replaces the two pieces of process-global state a capture
// touches with recorders, and restores them when the test ends. It is what
// makes AC-4's strong form assertable: a green run must not merely produce no
// files, it must never start the profiler.
func profileProbe(t *testing.T) (*int, *[]int) {
	t.Helper()
	windows := 0
	var rates []int
	prevWindow, prevRate := onProfileWindow, setBlockProfileRate
	onProfileWindow = func() { windows++ }
	setBlockProfileRate = func(rate int) {
		rates = append(rates, rate)
		prevRate(rate)
	}
	t.Cleanup(func() { onProfileWindow, setBlockProfileRate = prevWindow, prevRate })
	return &windows, &rates
}

func corpusEntryForTest(name string) corpus.Entry {
	return corpus.Entry{Name: name, Tier: 1}
}

// busyWorkload is a stand-in scenario: it burns a little CPU, allocates, and
// blocks on a channel, so all four profiles have something to describe. It is
// NOT a measurement — it exists so the capture machinery can be tested without
// indexing a repository.
func busyWorkload(series string, ran *int) profileWorkload {
	return profileWorkload{
		series:   series,
		scenario: "a synthetic workload standing in for " + series,
		run: func(ctx context.Context) error {
			if ran != nil {
				*ran++
			}
			var wg sync.WaitGroup
			ch := make(chan []byte)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range 200 {
					b := make([]byte, 4096)
					b[i%len(b)] = byte(i)
					ch <- b
				}
				close(ch)
			}()
			sink := 0
			for b := range ch {
				time.Sleep(time.Microsecond)
				sink += len(b)
			}
			wg.Wait()
			if sink == 0 {
				return errors.New("workload did nothing")
			}
			return nil
		},
	}
}

func failedGateReport(series, gateID string) evalreport.FullRunReport {
	gate := evalreport.GateResult{
		ID: gateID, Threshold: 120, Unit: "s", Measured: 190,
		HasMeasurement: true, Status: evalreport.StatusFail, Reason: "190 s > 120 s",
	}
	report := evalreport.FullRunReport{}
	switch series {
	case evalreport.RawSeriesCold:
		report.ColdSeries = &evalreport.ColdRunSeries{Gates: []evalreport.GateResult{gate}}
	case evalreport.RawSeriesQuery:
		report.Repo.QueryLatency = &evalreport.QueryLatencySeries{Gates: []evalreport.GateResult{gate}}
	case evalreport.RawSeriesIncremental:
		report.Repo.Incremental = &evalreport.IncrementalSeries{Gates: []evalreport.GateResult{gate}}
	case evalreport.RawSeriesStalls:
		report.Repo.Stalls = &evalreport.StallSeries{Gates: []evalreport.GateResult{gate}}
	}
	return report
}

// AC-4, the load-bearing half: a green run does not merely produce no files —
// the profiler is never started, so there is no overhead to argue about. That
// is a mechanical claim and a stronger one than any statistical comparison of
// two noisy timings could be.
func TestProfileMissedGates_AGreenRunNeverStartsTheProfiler(t *testing.T) {
	root := filepath.Join(t.TempDir(), "profiles")
	built := 0
	ran := 0

	report := evalreport.FullRunReport{
		ColdSeries: &evalreport.ColdRunSeries{Gates: []evalreport.GateResult{
			{ID: "cold_index_p50", Status: evalreport.StatusPass},
			{ID: "peak_rss", Status: evalreport.StatusUnknown},
		}},
	}

	var log bytes.Buffer
	windows, rates := profileProbe(t)
	sets := profileMissedGates(context.Background(), profileRunInput{
		root:    root,
		enabled: true,
		build: func(series string) (profileWorkload, error) {
			built++
			return busyWorkload(series, &ran), nil
		},
	}, report, &log)

	if len(sets) != 0 {
		t.Fatalf("a green run produced %d profile set(s): %+v", len(sets), sets)
	}
	if built != 0 || ran != 0 {
		t.Fatalf("a green run built %d workload(s) and ran %d: the profiling code path executed", built, ran)
	}
	if *windows != 0 {
		t.Fatalf("a green run opened %d profile window(s), want 0", *windows)
	}
	if len(*rates) != 0 {
		t.Fatalf("a green run changed the block profile rate %v; the measured process must be left exactly as it was", *rates)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("a green run created %s; nothing may be written when nothing was missed (err=%v)", root, err)
	}
}

// AC-1 + AC-2: a missed gate produces all four profiles for the affected
// scenario, in a directory that names the scenario, associated with the gate.
func TestProfileMissedGates_AMissedGateProducesFourProfilesTiedToThatGate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run", evalreport.ProfileDir)
	ran := 0
	var log bytes.Buffer
	windows, rates := profileProbe(t)

	sets := profileMissedGates(context.Background(), profileRunInput{
		root:    root,
		repo:    "cobra",
		enabled: true,
		build: func(series string) (profileWorkload, error) {
			return busyWorkload(series, &ran), nil
		},
	}, failedGateReport(evalreport.RawSeriesQuery, "warm_p95_structural"), &log)

	if len(sets) != 1 {
		t.Fatalf("got %d profile set(s), want 1: %+v", len(sets), sets)
	}
	set := sets[0]
	if !set.Complete || set.Error != "" {
		t.Fatalf("set is not complete: %+v", set)
	}
	if ran != 1 || *windows != 1 {
		t.Fatalf("workload ran %d time(s) in %d profile window(s), want 1 and 1", ran, *windows)
	}
	// The block rate is raised for the window and put back afterwards: a
	// diagnostic re-run must not leave the process sampling.
	if len(*rates) != 2 || (*rates)[0] != 1 || (*rates)[1] != 0 {
		t.Fatalf("block profile rate calls = %v, want [1 0]", *rates)
	}
	if set.Series != evalreport.RawSeriesQuery || len(set.Gates) != 1 || set.Gates[0].ID != "warm_p95_structural" {
		t.Fatalf("the set is not tied to the gate that was missed: %+v", set)
	}
	if set.Gates[0].Threshold != 120 || set.Gates[0].Measured != 190 {
		t.Fatalf("the set lost the numbers that make the profile actionable: %+v", set.Gates[0])
	}
	if set.Trigger != evalreport.ProfileTriggerMissedGate {
		t.Fatalf("trigger = %q", set.Trigger)
	}
	if !strings.Contains(set.Method, "NOT profiled") {
		t.Fatalf("the set does not say the measured run was unprofiled: %q", set.Method)
	}

	if len(set.Artifacts) != len(evalreport.ProfileKinds) {
		t.Fatalf("got %d artifact(s), want %d: %+v", len(set.Artifacts), len(evalreport.ProfileKinds), set.Artifacts)
	}
	for i, kind := range evalreport.ProfileKinds {
		a := set.Artifacts[i]
		if a.Kind != kind || !a.Written {
			t.Fatalf("artifact %d = %+v, want a written %s profile", i, a, kind)
		}
		path := filepath.Join(filepath.Dir(root), filepath.FromSlash(a.File))
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s profile at %s: %v", kind, path, err)
		}
		if fi.Size() == 0 || fi.Size() != a.Bytes {
			t.Fatalf("%s profile is %d bytes on disk, %d in the record", kind, fi.Size(), a.Bytes)
		}
		if a.Digest == "" || a.Mechanism == "" {
			t.Fatalf("%s profile has no digest or no mechanism: %+v", kind, a)
		}
	}
	if set.IOCounters == nil {
		t.Fatal("the set carries no I/O counters; the block profile cannot answer how much I/O happened")
	}

	// The profile root explains itself to a reader who has only the files.
	index, err := evalreport.ReadProfileIndex(root)
	if err != nil {
		t.Fatalf("ReadProfileIndex: %v", err)
	}
	if len(index.Sets) != 1 || index.Sets[0].Gates[0].ID != "warm_p95_structural" {
		t.Fatalf("profiles.json does not record the association: %+v", index)
	}
	if !strings.Contains(log.String(), "warm_p95_structural") {
		t.Fatalf("the job log does not say which gate was profiled:\n%s", log.String())
	}
}

// AC-3: demonstrated, not assumed. The profiles are opened with the real
// `go tool pprof`, which is the tool the acceptance criterion names.
func TestProfiles_AreReadableByGoToolPprof(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH, cannot demonstrate `go tool pprof`: %v", err)
	}
	root := filepath.Join(t.TempDir(), "run", evalreport.ProfileDir)
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, enabled: true,
		build: func(series string) (profileWorkload, error) { return busyWorkload(series, nil), nil },
	}, failedGateReport(evalreport.RawSeriesCold, "cold_index_p95"), &log)
	if len(sets) != 1 {
		t.Fatalf("got %d set(s)", len(sets))
	}

	for _, a := range sets[0].Artifacts {
		path := filepath.Join(filepath.Dir(root), filepath.FromSlash(a.File))
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		cmd := exec.CommandContext(ctx, goBin, "tool", "pprof", "-raw", path)
		// pprof consults these when it is asked to symbolize remotely; the
		// profiles here are local files and must need neither.
		cmd.Env = append(os.Environ(), "PPROF_TMPDIR="+t.TempDir())
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("go tool pprof -raw %s (%s profile) failed: %v\n%s", path, a.Kind, err, out)
		}
		if !bytes.Contains(out, []byte("PeriodType")) && !bytes.Contains(out, []byte("Samples")) {
			t.Fatalf("go tool pprof read the %s profile but reported no profile structure:\n%s", a.Kind, out)
		}
	}
}

// AC-5: profile generation failing is visible, recorded, and cannot leave the
// run reading green. The unwritable path is the story's own test note.
func TestProfileMissedGates_AnUnwritableOutputPathIsVisibleAndNotGreen(t *testing.T) {
	// A regular file where the profile root must be: creating the directory
	// cannot succeed.
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("occupied"), 0o644); err != nil {
		t.Fatal(err)
	}
	ran := 0
	var log bytes.Buffer
	windows, _ := profileProbe(t)

	sets := profileMissedGates(context.Background(), profileRunInput{
		root: filepath.Join(blocked, evalreport.ProfileDir), enabled: true,
		build: func(series string) (profileWorkload, error) { return busyWorkload(series, &ran), nil },
	}, failedGateReport(evalreport.RawSeriesStalls, "progress_stall_p95"), &log)

	if len(sets) != 1 {
		t.Fatalf("a failed capture must still produce a RECORD: got %+v", sets)
	}
	if sets[0].Complete || sets[0].Error == "" {
		t.Fatalf("a failed capture recorded itself as fine: %+v", sets[0])
	}
	if ran != 0 || *windows != 0 {
		t.Fatalf("the workload ran (%d) despite an unusable output path; the failure must be detected before the work", ran)
	}
	reason, failed := profileFailure(sets)
	if !failed {
		t.Fatal("profileFailure says nothing went wrong")
	}
	if !strings.Contains(reason, evalreport.RawSeriesStalls) {
		t.Fatalf("the failure does not name the scenario: %q", reason)
	}
	if !strings.Contains(log.String(), "profile") || !strings.Contains(strings.ToUpper(log.String()), "FAIL") {
		t.Fatalf("the failure is not visible in the job log:\n%s", log.String())
	}
}

// AC-5, the exit rule itself: an incomplete profile set is enough on its own to
// keep a run from reading green. It is checked at the exit decision rather than
// left to the coincidence that a missed gate already fails the run — a rule that
// holds only by coincidence is not a rule.
func TestProfileFailure_IsEnoughOnItsOwnToStopAGreenVerdict(t *testing.T) {
	if _, failed := profileFailure(nil); failed {
		t.Fatal("no profiles is not a profile failure")
	}
	if _, failed := profileFailure([]evalreport.ProfileSet{{Series: "cold_index", Complete: true}}); failed {
		t.Fatal("a complete set is not a failure")
	}
	reason, failed := profileFailure([]evalreport.ProfileSet{
		{Series: "cold_index", Complete: true},
		{Series: "query_latency", Complete: false, Error: "boom"},
	})
	if !failed || !strings.Contains(reason, "boom") {
		t.Fatalf("profileFailure = %q, %v", reason, failed)
	}
}

// A workload that cannot be built is a profile failure too — it must not read
// as "no profiles were needed".
func TestProfileMissedGates_AnUnbuildableWorkloadIsRecordedAsAFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), evalreport.ProfileDir)
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, enabled: true,
		build: func(series string) (profileWorkload, error) {
			return profileWorkload{}, fmt.Errorf("no checkout to re-run %s over", series)
		},
	}, failedGateReport(evalreport.RawSeriesIncremental, "freshness_p95"), &log)

	if len(sets) != 1 || sets[0].Complete {
		t.Fatalf("sets = %+v", sets)
	}
	if !strings.Contains(sets[0].Error, "no checkout") {
		t.Fatalf("the reason was lost: %q", sets[0].Error)
	}
	if _, failed := profileFailure(sets); !failed {
		t.Fatal("an unbuildable workload left the run green")
	}
}

// A workload that FAILS while being profiled is a different fact from a capture
// that failed: the profiles are still written (they are what explains the
// failure) and the error travels with them.
func TestProfileMissedGates_AFailingWorkloadStillLeavesItsProfiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), evalreport.ProfileDir)
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, enabled: true,
		build: func(series string) (profileWorkload, error) {
			return profileWorkload{
				series:   series,
				scenario: "a scenario that breaks",
				run:      func(ctx context.Context) error { return errors.New("index aborted") },
			}, nil
		},
	}, failedGateReport(evalreport.RawSeriesCold, "cold_index_p95"), &log)

	if len(sets) != 1 {
		t.Fatalf("sets = %+v", sets)
	}
	written := 0
	for _, a := range sets[0].Artifacts {
		if a.Written {
			written++
		}
	}
	if written != len(evalreport.ProfileKinds) {
		t.Fatalf("%d of %d profiles written for a failing workload", written, len(evalreport.ProfileKinds))
	}
	if !strings.Contains(sets[0].Error, "index aborted") {
		t.Fatalf("the workload error was swallowed: %+v", sets[0])
	}
	if _, failed := profileFailure(sets); !failed {
		t.Fatal("a scenario that could not be re-executed left the run green")
	}
}

// AC-1's "automatically": the automation is on unless it is deliberately turned
// off, and turning it off is loud rather than silent.
func TestProfileMissedGates_DisablingIsDeliberateAndVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), evalreport.ProfileDir)
	ran := 0
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, enabled: false,
		build: func(series string) (profileWorkload, error) { return busyWorkload(series, &ran), nil },
	}, failedGateReport(evalreport.RawSeriesCold, "cold_index_p95"), &log)

	if len(sets) != 0 || ran != 0 {
		t.Fatalf("profiling ran while disabled: %+v", sets)
	}
	if !strings.Contains(log.String(), "cold_index_p95") || !strings.Contains(log.String(), profileDisabledFlag) {
		t.Fatalf("a disabled run does not say what it did not profile:\n%s", log.String())
	}
}

// AC-2: the canonical home is the run directory the raw samples went to, and
// the convention is applied by the tool rather than remembered by an operator.
func TestResolveProfileRoot_PutsProfilesBesideTheRawData(t *testing.T) {
	root, canonical, err := resolveProfileRoot("", "runs/cold-index", "eval-cold-cobra.json", "ubuntu-latest", "2026-07-28")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := filepath.ToSlash(root), "runs/cold-index/"+evalreport.ProfileDir; got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
	if !canonical {
		t.Fatal("a profile root inside the run directory IS the convention")
	}

	// -export-raw auto resolves through the same convention the raw data uses,
	// so the profiles cannot end up beside a different run's samples.
	root, canonical, err = resolveProfileRoot("", exportAuto, "", "ubuntu-latest", "2026-07-28")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(evalreport.RunsRoot, "2026-07-28-ubuntu-latest", evalreport.ProfileDir)
	if root != want || !canonical {
		t.Fatalf("root = %q (canonical=%v), want %q", root, canonical, want)
	}

	// Without an export there is still a place to write to — the automation is
	// not allowed to become conditional — but the reader is told it is not the
	// canonical one.
	root, canonical, err = resolveProfileRoot("", "", "out/eval-full-cobra.json", "local", "2026-07-28")
	if err != nil {
		t.Fatal(err)
	}
	if canonical {
		t.Fatal("a root outside the run directory must not claim to be the convention")
	}
	if filepath.Dir(root) != "out" {
		t.Fatalf("root = %q, want it beside the report", root)
	}

	// An explicit request always wins.
	root, _, err = resolveProfileRoot("/tmp/mine", "runs/cold-index", "", "ubuntu-latest", "2026-07-28")
	if err != nil || root != "/tmp/mine" {
		t.Fatalf("root = %q, err = %v", root, err)
	}
}

// The four measured scenarios all have a re-executable workload. A gate that
// missed with no way to re-run its scenario would be a silent hole in AC-1.
func TestProfileWorkloadFor_CoversEveryMeasuredScenario(t *testing.T) {
	build := profileWorkloadBuilderFor(profileWorkloadInput{
		repo:    "fixture",
		repoDir: t.TempDir(),
		scratch: t.TempDir(),
		entry:   corpusEntryForTest("fixture"),
		plan:    newQueryLatencyPlan(0, nil),
		changes: 5,
	})
	for _, series := range evalreport.RawSeriesNames {
		w, err := build(series)
		if err != nil {
			t.Fatalf("series %s has no profilable workload: %v", series, err)
		}
		if w.run == nil {
			t.Fatalf("series %s built a workload that does nothing", series)
		}
		if strings.TrimSpace(w.scenario) == "" {
			t.Fatalf("series %s does not say what it re-executes", series)
		}
	}
	if _, err := build("accuracy"); err == nil {
		t.Fatal("an unknown series must not silently produce a workload")
	}
}

// A run that never measured a scenario cannot re-execute it, and must say so
// rather than profiling something adjacent.
func TestProfileWorkloadFor_RefusesAScenarioThisRunDidNotMeasure(t *testing.T) {
	build := profileWorkloadBuilderFor(profileWorkloadInput{
		repo: "fixture", repoDir: t.TempDir(), scratch: t.TempDir(),
		entry: corpusEntryForTest("fixture"), plan: newQueryLatencyPlan(0, nil),
	})
	if _, err := build(evalreport.RawSeriesIncremental); err == nil {
		t.Fatal("the freshness scenario was re-runnable although this run never measured it")
	}
}

// AC-1 end to end over the REAL scenario: a cold index of a real tree, profiled
// because a gate was missed, producing four profiles the toolchain can open.
func TestColdIndexProfileWorkload_ProfilesARealIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a real tree")
	}
	repoDir := t.TempDir()
	writeProfileFixture(t, repoDir)

	root := filepath.Join(t.TempDir(), "run", evalreport.ProfileDir)
	scratch := t.TempDir()
	build := profileWorkloadBuilderFor(profileWorkloadInput{
		repo: "fixture", repoDir: repoDir, scratch: scratch,
		entry: corpusEntryForTest("fixture"), plan: newQueryLatencyPlan(0, nil),
	})
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, repo: "fixture", enabled: true, build: build,
	}, failedGateReport(evalreport.RawSeriesCold, "cold_index_p95"), &log)

	if len(sets) != 1 || !sets[0].Complete {
		t.Fatalf("profiling a real cold index did not complete: %+v\n%s", sets, log.String())
	}
	if sets[0].DurationMS <= 0 {
		t.Fatalf("the profiled scenario reports no duration: %+v", sets[0])
	}
	for _, a := range sets[0].Artifacts {
		if !a.Written || a.Bytes == 0 {
			t.Fatalf("%s profile: %+v", a.Kind, a)
		}
	}
}

// writeProfileFixture lays down a tiny Go tree the ingest pipeline can index.
func writeProfileFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		src := fmt.Sprintf("package fixture\n\nfunc Helper%d(n int) int {\n\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn Helper%d(n-1) + n\n}\n", i, i)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.go", i)), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// AC-2: the profiles land beside the raw data of the SAME run, and the run
// index points at them — so a reader who opens the run directory finds the
// profiles without knowing they exist.
func TestExportRunDir_CarriesTheProfilesOfTheRunThatMissed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	report := failedGateReport(evalreport.RawSeriesCold, "cold_index_p95")
	report.ColdSeries.Runs = []evalreport.ColdRunSample{{Run: 1, Status: evalreport.ColdRunCompleted}}

	root := filepath.Join(dir, evalreport.ProfileDir)
	var log bytes.Buffer
	sets := profileMissedGates(context.Background(), profileRunInput{
		root: root, repo: "cobra", enabled: true,
		build: func(series string) (profileWorkload, error) { return busyWorkload(series, nil), nil },
	}, report, &log)
	report.Profiles = sets

	got, _, err := exportRunDir(exportOptions{
		target: dir, runnerClass: "ubuntu-latest", repo: "cobra",
		workDir: t.TempDir(), date: "2026-07-28", profiles: sets,
	}, report)
	if err != nil {
		t.Fatalf("exportRunDir: %v", err)
	}
	if got != dir {
		t.Fatalf("export dir = %q, want %q", got, dir)
	}

	index, _, _, err := evalreport.ReadRunDir(dir)
	if err != nil {
		t.Fatalf("ReadRunDir: %v", err)
	}
	if len(index.Profiles) != 1 {
		t.Fatalf("run.json lists %d profile set(s): %+v", len(index.Profiles), index.Profiles)
	}
	ref := index.Profiles[0]
	if ref.Dir != evalreport.ProfileDir+"/"+evalreport.RawSeriesCold || !ref.Complete {
		t.Fatalf("profile ref = %+v", ref)
	}
	if len(ref.Gates) != 1 || ref.Gates[0] != "cold_index_p95" {
		t.Fatalf("the run index does not say which gate the profiles answer for: %+v", ref)
	}
	if ref.Written != ref.Expected || ref.Expected != len(evalreport.ProfileKinds) {
		t.Fatalf("profile ref counts = %d/%d", ref.Written, ref.Expected)
	}

	// And the published report itself carries the association, so report.json
	// alone answers "which gate produced this profile".
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("cold_index_p95")) || !bytes.Contains(raw, []byte("profiles/cold_index/cpu.pprof")) {
		t.Fatalf("report.json does not reference the profiles it produced:\n%s", raw)
	}
	// Every referenced file is really there.
	for _, a := range sets[0].Artifacts {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(a.File))); err != nil {
			t.Fatalf("%s profile is referenced but absent: %v", a.Kind, err)
		}
	}
}

// The cold SERIES profiles a missed gate once, itself. A child that profiled
// would write into a tree the series deletes as soon as that run finishes.
func TestColdRunArgv_ChildrenDoNotProfile(t *testing.T) {
	argv := coldRunArgv("/bin/eval", coldSeriesOptions{
		manifestPath: "corpus/manifest.json", repoName: "grpc-go", runnerClass: "ubuntu-latest",
	}, "/tmp/run-01", "/tmp/run-01.json", 0)

	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, profileDisabledFlag) {
		t.Fatalf("a child run may profile: %s", joined)
	}
}

// AC-1's "automatically" is a default, not a flag an operator has to know.
func TestProfileOnMiss_IsOnByDefault(t *testing.T) {
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "cmd", "eval", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `flag.Bool("profile-on-miss", true,`) {
		t.Fatal("-profile-on-miss is not defaulted to true: the automation would have to be asked for")
	}
}

// AC-6: the protocol records the link to the PRD §8.5 rule — a fix responding
// to a gate cites the profile from that run. A rule that lives only in a story
// ticket is not a protocol.
func TestHeroProtocol_RecordsTheProfileCitationRule(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "eval", "hero-protocol.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	for _, want := range []string{
		"SW-129",
		"§8.5",
		"go tool pprof",
		evalreport.ProfileDir + "/",
		profileDisabledFlag,
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the protocol does not mention %q", want)
		}
	}
	for _, kind := range evalreport.ProfileKinds {
		if !strings.Contains(doc, evalreport.ProfileFileName(kind)) {
			t.Errorf("the protocol does not name the %s profile file", kind)
		}
	}
	lower := strings.ToLower(doc)
	if !strings.Contains(lower, "cite") {
		t.Error("the protocol does not require a fix to CITE its profile")
	}
	// The honest caveats have to be in the protocol too, not only in the code:
	// the profiled run is a re-run, and `io` is a block profile.
	if !strings.Contains(lower, "re-run") && !strings.Contains(lower, "re-execut") {
		t.Error("the protocol does not say the profiles come from a diagnostic re-execution")
	}
	if !strings.Contains(lower, "block profile") {
		t.Error("the protocol does not say what the io profile actually is")
	}
}

// The budget → scenario attribution has to cover every metric the budget
// evaluator can emit. A new ceiling must force a decision about which scenario
// explains it rather than silently inheriting the fallback.
func TestBudgetSeries_KnowsEveryBudgetMetricTheEvaluatorEmits(t *testing.T) {
	manifest := fullBudgetManifest{
		SchemaVersion: budgetSchemaVersion, RunnerClass: "ubuntu-latest",
		Historical: true, HistoricalReason: "test fixture",
	}
	manifest.RealRepos.Selection = []string{"fixture"}
	limit := budgetThreshold{Baseline: 1, Budget: 1_000_000}
	manifest.RealRepos.PerRepo = map[string]fullRepoBudget{"fixture": {
		IndexWallclockMS: limit, PeakRSSMB: limit, DBSizeMB: limit,
		WarmP95US: map[string]budgetThreshold{"structural": limit, "search": limit, "agent_tools": limit},
	}}
	run := evalreport.FullRepoRun{
		Name:            "fixture",
		Index:           evalreport.IndexMetrics{WallclockMS: 10, PeakRSSMB: 10, DBSizeBytes: 1024},
		StablePeakRSSMB: 10,
		WarmP95US:       map[string]int64{"structural": 1, "search": 1, "agent_tools": 1},
		WarmSamples:     map[string]int{"structural": 1, "search": 1, "agent_tools": 1},
		WarmOps:         map[string][]string{"structural": {"callers"}, "search": {"search"}, "agent_tools": {"agent_brief"}},
	}

	checks, err := evaluateFullRunBudgets(manifest, "ubuntu-latest", run)
	if err != nil {
		t.Fatalf("evaluateFullRunBudgets: %v", err)
	}
	if len(checks) == 0 {
		t.Fatal("the evaluator produced no checks to map")
	}
	for _, check := range checks {
		series, known := evalreport.BudgetSeries(check.Name)
		if !known {
			t.Errorf("budget metric %q has no scenario to profile; it would fall back to %s", check.Name, series)
		}
	}
}

// The CPU profile has to actually RECORD, not merely exist. A capture that
// closed the file before StopCPUProfile flushed it, or that stopped the
// profiler before the scenario ran, would still leave a valid pprof file with
// nothing in it — and a reader would find "no CPU time here" where the truth is
// "nobody looked". Go's CPU profiler samples at 100 Hz, so this burns real CPU
// rather than sleeping.
func TestCaptureProfileSet_TheCPUProfileRecordsRealSamples(t *testing.T) {
	if testing.Short() {
		t.Skip("burns CPU for a second")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "profiles", "cpu-check")
	artifacts, _, runErr, captureErr := captureProfileSet(context.Background(), dir, "profiles/cpu-check", profileWorkload{
		series:   "cpu-check",
		scenario: "a CPU-bound loop",
		run: func(ctx context.Context) error {
			deadline := time.Now().Add(time.Second)
			x := 1.0
			for time.Now().Before(deadline) {
				for i := range 200000 {
					x += float64(i%7) * 1.0000001
				}
			}
			if x == 0 {
				return errors.New("optimised away")
			}
			return nil
		},
	})
	if captureErr != nil || runErr != nil {
		t.Fatalf("capture=%v run=%v", captureErr, runErr)
	}

	var cpu evalreport.ProfileArtifact
	for _, a := range artifacts {
		if a.Kind == evalreport.ProfileCPU {
			cpu = a
		}
	}
	if !cpu.Written {
		t.Fatalf("no CPU profile: %+v", cpu)
	}
	out, err := exec.Command(goBin, "tool", "pprof", "-top", "-nodecount=5",
		filepath.Join(dir, evalreport.ProfileFileName(evalreport.ProfileCPU))).CombinedOutput()
	if err != nil {
		t.Fatalf("go tool pprof: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte("Total samples = 0")) {
		t.Fatalf("the CPU profile recorded no samples over a second of CPU-bound work:\n%s", out)
	}
}

// A profile CI throws away may as well not have been produced. The matrix job
// is the likeliest place a ceiling is exceeded — it is the one that enforces
// the historical per-repo budgets — and it writes its profiles outside a run
// directory, so its upload has to name them explicitly. The four
// reference-scenario jobs upload their whole run directory, which already
// contains profiles/.
func TestProfiles_SurviveTheWorkflowThatProducesThem(t *testing.T) {
	root := repoRoot(t)
	jobs := workflowJobs(t, readWorkflow(t, filepath.Join(root, ".github", "workflows", "eval-full.yml")))

	matrix, ok := jobs["full-run"]
	if !ok {
		for name := range jobs {
			if strings.Contains(name, "full") && !strings.Contains(name, "series") {
				matrix = jobs[name]
				ok = true
				break
			}
		}
	}
	if !ok {
		t.Fatal("eval-full.yml no longer has the per-repo matrix job")
	}
	if !strings.Contains(matrix, "-profile-dir") {
		t.Error("the matrix job does not give its profiles a directory of their own, so they cannot be uploaded")
	}
	upload := matrix[strings.LastIndex(matrix, "upload-artifact"):]
	if !strings.Contains(upload, "profiles-") {
		t.Errorf("the matrix job does not upload the profiles a blown budget produces:\n%s", upload)
	}

	for _, name := range []string{"cold-index-series", "query-latency-series", "freshness-series", "progress-stall-series"} {
		job, ok := jobs[name]
		if !ok {
			t.Fatalf("eval-full.yml no longer has a %s job", name)
		}
		// These export a run directory and upload it whole; profiles/ lives
		// inside it, so the upload covers them by construction. Assert the
		// directory itself is uploaded, which is what makes that true.
		if !strings.Contains(job[strings.LastIndex(job, "upload-artifact"):], "runs/") {
			t.Errorf("the %s job does not upload its run directory, so its profiles would be lost", name)
		}
	}
}
