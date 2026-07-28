package main

// SW-124 (P0-C1): the gate arithmetic and the OOM verdict.
//
// These are pure functions on purpose. The OOM gate's rules are the part of
// this story that cannot be exercised on a developer machine — no cgroup v2, no
// systemd — so the rules themselves are tested here and the wiring is kept as
// thin as it can be. If the rules were only reachable through a real 8 GB
// scope, "the gate works" would itself be an unverifiable claim.

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/internal/evalreport"
)

func referenceSeries(aggregates map[string]evalreport.Aggregate) evalreport.ColdRunSeries {
	return evalreport.ColdRunSeries{
		Repo:              "grpc-go",
		RunsRequested:     10,
		RunsCompleted:     10,
		MinimumRuns:       evalreport.ColdRunMinimum,
		Sufficient:        true,
		RunnerClass:       "ubuntu-latest",
		RunnerRole:        roleReference,
		ReferenceScenario: true,
		CandidateSHA:      "abc123",
		MeasuredSHA:       "abc123",
		CandidateMatch:    true,
		Aggregates:        aggregates,
	}
}

func agg(metric string, p50, p95, max float64) evalreport.Aggregate {
	return evalreport.Aggregate{Metric: metric, N: 10, Min: p50, P50: p50, P95: p95, Max: max}
}

// The gate reads the contract's threshold and the series' own aggregate, and
// converts units explicitly — ms→s and MB→GB are exactly where a silent factor
// of 1000 would turn a failing gate green.
func TestEvaluateColdGate_ThresholdsAndUnitConversion(t *testing.T) {
	series := referenceSeries(map[string]evalreport.Aggregate{
		evalreport.MetricIndexWallclockMS: agg(evalreport.MetricIndexWallclockMS, 45_000, 89_000, 89_000),
		evalreport.MetricStablePeakRSSMB:  agg(evalreport.MetricStablePeakRSSMB, 1024, 1536, 1536),
		evalreport.MetricDBSizeBytes:      agg(evalreport.MetricDBSizeBytes, 100<<20, 120<<20, 120<<20),
	})

	cases := []struct {
		gate     gateMapping
		want     string
		measured float64
	}{
		{gateMapping{ID: "cold_index_p50", Threshold: 90, Unit: "s", Comparison: "lte"}, evalreport.StatusPass, 45},
		{gateMapping{ID: "cold_index_p95", Threshold: 120, Unit: "s", Comparison: "lte"}, evalreport.StatusPass, 89},
		{gateMapping{ID: "peak_rss", Threshold: 2, Unit: "GB", Comparison: "lte"}, evalreport.StatusPass, 1.5},
		{gateMapping{ID: "db_size", Threshold: 300, Unit: "MB", Comparison: "lte"}, evalreport.StatusPass, 120},
	}
	for _, tc := range cases {
		t.Run(tc.gate.ID, func(t *testing.T) {
			got := evaluateColdGate(tc.gate, series, "")
			if got.Status != tc.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Reason, tc.want)
			}
			if !got.HasMeasurement {
				t.Fatal("a PASS without has_measurement is a gate that passed on nothing")
			}
			if diff := got.Measured - tc.measured; diff > 0.001 || diff < -0.001 {
				t.Errorf("measured = %v, want %v (unit conversion)", got.Measured, tc.measured)
			}
			if got.Aggregate == "" {
				t.Error("the gate does not name the aggregate it read, so the input cannot be recomputed")
			}
		})
	}
}

// A gate that is exceeded FAILS with the numbers in the reason — the point of a
// threshold is that it can be crossed.
func TestEvaluateColdGate_FailsWhenExceeded(t *testing.T) {
	series := referenceSeries(map[string]evalreport.Aggregate{
		evalreport.MetricIndexWallclockMS: agg(evalreport.MetricIndexWallclockMS, 95_000, 130_000, 130_000),
	})
	got := evaluateColdGate(gateMapping{ID: "cold_index_p50", Threshold: 90, Unit: "s", Comparison: "lte"}, series, "")
	if got.Status != evalreport.StatusFail {
		t.Fatalf("status = %s, want FAIL for a 95 s p50 against a 90 s threshold", got.Status)
	}
	if !strings.Contains(got.Reason, "95.000 s > 90.000 s") {
		t.Errorf("reason = %q, want the measured and threshold values", got.Reason)
	}
}

// A measurement that was never taken is UNKNOWN, not a pass on a zero.
func TestEvaluateColdGate_MissingAggregateIsUnknownNotZero(t *testing.T) {
	got := evaluateColdGate(gateMapping{ID: "db_size", Threshold: 300, Unit: "MB", Comparison: "lte"}, referenceSeries(nil), "")
	if got.Status != evalreport.StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN — a 0 MB DB would otherwise pass a 300 MB ceiling", got.Status)
	}
	if got.HasMeasurement {
		t.Error("has_measurement is true without an aggregate")
	}
}

// If the contract's unit ever moves, the harness refuses to compare rather than
// silently applying the old conversion.
func TestEvaluateColdGate_RefusesAUnitItDoesNotMeasure(t *testing.T) {
	series := referenceSeries(map[string]evalreport.Aggregate{
		evalreport.MetricIndexWallclockMS: agg(evalreport.MetricIndexWallclockMS, 45_000, 89_000, 89_000),
	})
	got := evaluateColdGate(gateMapping{ID: "cold_index_p50", Threshold: 90000, Unit: "ms", Comparison: "lte"}, series, "")
	if got.Status != evalreport.StatusUnknown {
		t.Fatalf("status = %s, want UNKNOWN when the declared unit is not the measured one", got.Status)
	}
	if !strings.Contains(got.Reason, "unit") {
		t.Errorf("reason = %q, want it to name the unit mismatch", got.Reason)
	}
}

// The four preconditions, in priority order. Each one is a distinct reason a
// number cannot answer a PRD §12.2 gate, and each has to be stated rather than
// collapsed into a bare UNKNOWN.
func TestColdGatePrecondition_EachBlockerIsNamed(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*evalreport.ColdRunSeries)
		wantSub string
	}{
		{"not the reference scenario", func(s *evalreport.ColdRunSeries) { s.ReferenceScenario = false }, "not the reference scenario"},
		{"dirty worktree", func(s *evalreport.ColdRunSeries) { s.WorktreeDirty = true }, "dirty worktree"},
		{"not the candidate", func(s *evalreport.ColdRunSeries) { s.CandidateMatch = false }, "not the frozen candidate"},
		{"too few runs", func(s *evalreport.ColdRunSeries) { s.Sufficient = false; s.RunsCompleted = 7 }, "only 7 of the required 10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := referenceSeries(nil)
			tc.mutate(&s)
			got := coldGatePrecondition(s)
			if !strings.Contains(got, tc.wantSub) {
				t.Fatalf("precondition = %q, want it to mention %q", got, tc.wantSub)
			}
			gate := evaluateColdGate(gateMapping{ID: "cold_index_p50", Threshold: 90, Unit: "s", Comparison: "lte"}, s, got)
			if gate.Status != evalreport.StatusUnknown {
				t.Errorf("gate status = %s, want UNKNOWN", gate.Status)
			}
		})
	}
	if got := coldGatePrecondition(referenceSeries(nil)); got != "" {
		t.Fatalf("a complete reference series must have no blocker, got %q", got)
	}
}

// The §17 stop rule is wider than the 2 GB gate and applies to every scenario.
func TestEvaluateStopRule(t *testing.T) {
	rule := stopRule{ID: "peak_rss_stop_rule", ThresholdGB: 4}

	within := evaluateStopRule(rule, referenceSeries(map[string]evalreport.Aggregate{
		evalreport.MetricStablePeakRSSMB: agg(evalreport.MetricStablePeakRSSMB, 1024, 1536, 1536),
	}))
	if within.Triggered || within.Status != evalreport.StatusPass {
		t.Errorf("1.5 GB peak triggered the 4 GB stop rule: %+v", within)
	}

	over := evaluateStopRule(rule, referenceSeries(map[string]evalreport.Aggregate{
		evalreport.MetricStablePeakRSSMB: agg(evalreport.MetricStablePeakRSSMB, 8192, 9216, 9216),
	}))
	if !over.Triggered || over.Status != evalreport.StatusFail {
		t.Errorf("a 9 GB peak must trigger the 4 GB stop rule: %+v", over)
	}

	unmeasured := evaluateStopRule(rule, referenceSeries(nil))
	if unmeasured.Triggered || unmeasured.Status != evalreport.StatusUnknown {
		t.Errorf("no peak sample must read UNKNOWN, not untriggered: %+v", unmeasured)
	}
}

// ─── AC-4: the OOM verdict ──────────────────────────────────────────────────

const oomLimit = int64(8589934592)

func verifiedCgroup() *evalreport.CgroupLimits {
	return &evalreport.CgroupLimits{
		Available: true, Path: "/sys/fs/cgroup/x.scope",
		MemoryMax: "8589934592", MemorySwapMax: "0",
		OOMKill: 0, OOMKillCollected: true,
	}
}

func collectedKernelLog(fired bool) oomSignal {
	return oomSignal{name: "kernel OOM log", collected: true, fired: fired, detail: "checked"}
}

func TestEvaluateOOM_PassRequiresAVerifiedLimitACompletedRunAndAllSignals(t *testing.T) {
	got := evaluateOOM(
		evalreport.OOMResult{GateID: oomGateID, RequiredLimitBytes: oomLimit},
		oomObservation{
			limitBytes: oomLimit, cgroup: verifiedCgroup(),
			exit: coldRunExit{exitCode: 0}, indexCompleted: true,
			kernelLog: collectedKernelLog(false),
		})
	if got.Status != evalreport.StatusPass {
		t.Fatalf("status = %s (%s), want PASS", got.Status, got.Reason)
	}
	if !got.LimitVerified || !got.RunCompleted {
		t.Errorf("a PASS must record a verified limit and a completed run: %+v", got)
	}
}

func TestEvaluateOOM_UnknownAndFailPaths(t *testing.T) {
	cases := []struct {
		name    string
		obs     oomObservation
		want    string
		wantSub string
	}{
		{
			// The limit is the whole claim: 16 GB would also let the run
			// finish, and would say nothing about 8.
			name: "wrong memory.max invalidates the check",
			obs: oomObservation{
				limitBytes: oomLimit,
				cgroup:     &evalreport.CgroupLimits{Available: true, MemoryMax: "17179869184", MemorySwapMax: "0", OOMKillCollected: true},
				exit:       coldRunExit{exitCode: 0}, indexCompleted: true, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusUnknown, wantSub: "not verified",
		},
		{
			// With swap the process is throttled instead of killed, so a green
			// result would be green for the wrong reason.
			name: "swap available invalidates the check",
			obs: oomObservation{
				limitBytes: oomLimit,
				cgroup:     &evalreport.CgroupLimits{Available: true, MemoryMax: "8589934592", MemorySwapMax: "max", OOMKillCollected: true},
				exit:       coldRunExit{exitCode: 0}, indexCompleted: true, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusUnknown, wantSub: "not verified",
		},
		{
			name: "no cgroup at all is UNKNOWN, never PASS",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: nil,
				exit: coldRunExit{exitCode: 0}, indexCompleted: true, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusUnknown, wantSub: "not verified",
		},
		{
			name: "an uncollected signal blocks a PASS",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: verifiedCgroup(),
				exit: coldRunExit{exitCode: 0}, indexCompleted: true,
				kernelLog: oomSignal{name: "kernel OOM log", collected: false, detail: "dmesg: permission denied"},
			},
			want: evalreport.StatusUnknown, wantSub: "could not be collected",
		},
		{
			name: "a run that did not complete for a non-OOM reason is UNKNOWN",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: verifiedCgroup(),
				exit: coldRunExit{exitCode: 2}, indexCompleted: false, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusUnknown, wantSub: "did not complete",
		},
		{
			name: "exit 137 is a FAIL",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: verifiedCgroup(),
				exit: coldRunExit{exitCode: 137, signal: "killed"}, indexCompleted: false, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusFail, wantSub: "process exit status",
		},
		{
			name: "a cgroup oom_kill counter is a FAIL even with a clean exit",
			obs: oomObservation{
				limitBytes: oomLimit,
				cgroup:     &evalreport.CgroupLimits{Available: true, MemoryMax: "8589934592", MemorySwapMax: "0", OOMKill: 1, OOMKillCollected: true},
				exit:       coldRunExit{exitCode: 0}, indexCompleted: true, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusFail, wantSub: "oom_kill=1",
		},
		{
			name: "a kernel-log kill is a FAIL",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: verifiedCgroup(),
				exit: coldRunExit{exitCode: 0}, indexCompleted: true, kernelLog: collectedKernelLog(true),
			},
			want: evalreport.StatusFail, wantSub: "kernel OOM log",
		},
		{
			// A kill under an unverified limit is still a kill.
			name: "a kill beats an unverified limit",
			obs: oomObservation{
				limitBytes: oomLimit, cgroup: nil,
				exit: coldRunExit{exitCode: 137, signal: "killed"}, indexCompleted: false, kernelLog: collectedKernelLog(false),
			},
			want: evalreport.StatusFail, wantSub: "OOM kill observed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := evaluateOOM(evalreport.OOMResult{GateID: oomGateID, RequiredLimitBytes: oomLimit}, tc.obs)
			if got.Status != tc.want {
				t.Fatalf("status = %s (%s), want %s", got.Status, got.Reason, tc.want)
			}
			if !strings.Contains(got.Reason, tc.wantSub) {
				t.Errorf("reason = %q, want it to mention %q", got.Reason, tc.wantSub)
			}
			if got.Status == evalreport.StatusPass {
				t.Fatal("this case must never render as PASS")
			}
		})
	}
}

func TestOOMLimitVerified_RequiresTheExactContractFigure(t *testing.T) {
	if oomLimitVerified(verifiedCgroup(), oomLimit) != true {
		t.Error("the exact contract figure with no swap must verify")
	}
	if oomLimitVerified(&evalreport.CgroupLimits{Available: false, MemoryMax: "8589934592", MemorySwapMax: "0"}, oomLimit) {
		t.Error("an unavailable cgroup must not verify")
	}
	if oomLimitVerified(verifiedCgroup(), 0) {
		t.Error("a zero required limit must not verify")
	}
}

// The kernel-log match is deliberately narrow: a substring hit against a
// timestamp would manufacture a FAIL out of an unrelated message.
func TestKernelOOMForPID(t *testing.T) {
	log := strings.Join([]string{
		"[12345.678] systemd: started something",
		"[12345.900] Out of memory: Killed process 4242 (eval) total-vm:9000000kB",
		"[12346.100] oom-kill:constraint=CONSTRAINT_MEMCG,oom_memcg=/x.scope,task_memcg=/x.scope,task=eval,pid=4242,uid=1000",
	}, "\n")

	if !kernelOOMForPID(log, 4242) {
		t.Error("an OOM kill for the measured pid must be detected")
	}
	if kernelOOMForPID(log, 424) {
		t.Error("pid 424 must not match 'process 4242' — a prefix is not the pid")
	}
	if kernelOOMForPID(log, 12345) {
		t.Error("a pid that only appears in a timestamp must not fire the signal")
	}
	if kernelOOMForPID(log, 999) {
		t.Error("an unrelated pid must not match")
	}
	if kernelOOMForPID("", 4242) || kernelOOMForPID(log, 0) {
		t.Error("no log or no pid must not fire the signal")
	}
}

// The imposed limit is built from the contract's byte figure and always
// disables swap.
func TestImposeMemoryLimit(t *testing.T) {
	argv := strings.Join(imposeMemoryLimit(oomLimit), " ")
	for _, want := range []string{"systemd-run", "--scope", "MemoryMax=8589934592", "MemorySwapMax=0"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv %q is missing %q", argv, want)
		}
	}
}
