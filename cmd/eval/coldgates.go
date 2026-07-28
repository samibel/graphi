package main

// SW-124 (P0-C1): reading a cold series against the PRD §12.2 gates, and
// executing the one gate that had no measurement point at all — "no OOM on an
// 8 GB host".
//
// The thresholds are not restated here. They are read from the SW-123 contract
// (docs/eval/reference-scenario.json), which is the single place a PRD number
// lives; this file only decides what a measurement means against them. The
// gates SW-124 owns are the ones the contract marks `measured_by: SW-124`.
//
// The honesty rules, in the order they are applied:
//
//   - a run that is not THE reference scenario on THE reference class reads
//     UNKNOWN, because §12.2 is scoped to that scenario and nothing else;
//   - a run from a revision that is not the frozen candidate reads UNKNOWN,
//     because a gate result about an artifact no user installs is not evidence
//     about the candidate;
//   - fewer than FR-8's minimum completed runs reads UNKNOWN, because a
//     distribution over too few samples is not the distribution asked for;
//   - a missing measurement reads UNKNOWN, never PASS.
//
// The first two are PROVENANCE rules and apply to every gate in this file,
// the OOM gate included — it has its own measurement method, but not its own
// exemption from being about the candidate. The third is a DISTRIBUTION rule
// and applies only to the gates read off a percentile; see coldGateBlockers.
//
// UNKNOWN is not a PASS (PRD §8.2) and is never silently upgraded.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samibel/graphi/internal/evalreport"
)

// coldGateBinding says which series aggregate answers a gate, which statistic
// of it, and how the aggregate's unit converts to the gate's unit.
type coldGateBinding struct {
	metric string
	stat   string
	// unit is the gate unit this binding produces. It is checked against the
	// contract's declared unit so a threshold whose unit changed cannot be
	// compared against a number in the old one.
	unit    string
	convert func(float64) float64
}

// coldGateBindings covers exactly the §12.2 rows the contract assigns to
// SW-124. Peak RSS and DB size bind to the MAXIMUM across the series, not a
// percentile: "peak" and a size ceiling are worst-case claims, and a p50 peak
// would be a contradiction in terms.
var coldGateBindings = map[string]coldGateBinding{
	"cold_index_p50": {
		metric: evalreport.MetricIndexWallclockMS, stat: "p50", unit: "s",
		convert: func(ms float64) float64 { return ms / 1000 },
	},
	"cold_index_p95": {
		metric: evalreport.MetricIndexWallclockMS, stat: "p95", unit: "s",
		convert: func(ms float64) float64 { return ms / 1000 },
	},
	"peak_rss": {
		metric: evalreport.MetricStablePeakRSSMB, stat: "max", unit: "GB",
		convert: func(mb float64) float64 { return mb / 1024 },
	},
	"db_size": {
		metric: evalreport.MetricDBSizeBytes, stat: "max", unit: "MB",
		convert: func(b float64) float64 { return b / (1024 * 1024) },
	},
}

// readColdGates evaluates the SW-124 gates and the §17 stop rule against a
// completed series. Without a contract there are no thresholds to read against,
// and the series stays UNKNOWN rather than inventing any.
func readColdGates(scenarioPath string, series evalreport.ColdRunSeries) ([]evalreport.GateResult, *evalreport.StopRuleResult) {
	if scenarioPath == "" {
		return nil, nil
	}
	rs, err := loadReferenceScenario(scenarioPath)
	if err != nil {
		return []evalreport.GateResult{{
			ID:     "reference_scenario_contract",
			Status: evalreport.StatusUnknown,
			Reason: "the reference-scenario contract could not be read: " + err.Error(),
		}}, nil
	}

	blockers := coldGateBlockersFor(series)
	var gates []evalreport.GateResult
	for _, g := range rs.Gates {
		if g.MeasuredBy != "SW-124" {
			continue
		}
		gates = append(gates, evaluateColdGate(g, series, blockers))
	}
	return gates, evaluateStopRule(rs.StopRule, series)
}

// coldGateBlockers are the reasons this series cannot answer a §12.2 gate,
// split by WHAT each one disqualifies. They are computed once so every gate
// reports the same reason rather than each deriving its own.
//
// provenance disqualifies EVERY gate, the OOM gate included: it says the
// measurement is not about the reference scenario, or not about the frozen
// candidate. A gate result about the wrong artifact is not evidence for the
// candidate whatever that gate measures, and a measurement method of its own
// is not an exemption from that.
//
// distribution additionally disqualifies the gates that are read off a
// percentile of the cold series. It deliberately does NOT reach the OOM gate:
// that gate is one constrained run, not a distribution, so refusing it for
// "FR-8's distribution was not produced" would attach a reason that is not
// true of it — and a gate blocked for a reason that does not apply is its own
// small dishonesty.
type coldGateBlockers struct {
	provenance   string
	distribution string
}

func coldGateBlockersFor(series evalreport.ColdRunSeries) coldGateBlockers {
	return coldGateBlockers{
		provenance:   coldGateProvenanceBlocker(series),
		distribution: coldGateDistributionBlocker(series),
	}
}

// forDistribution is the blocker a percentile-derived gate must obey:
// provenance first, since "these numbers are about something else" outranks
// "there are not enough of them".
func (b coldGateBlockers) forDistribution() string {
	if b.provenance != "" {
		return b.provenance
	}
	return b.distribution
}

// coldGateProvenanceBlocker returns the reason this series' numbers are not
// about the thing §12.2 is scoped to, or "" when they are.
func coldGateProvenanceBlocker(series evalreport.ColdRunSeries) string {
	switch {
	case !series.ReferenceScenario:
		return fmt.Sprintf("this run is %s on runner class %s (%s), which is not the reference scenario; PRD §12.2 is scoped to the reference scenario only",
			series.Repo, series.RunnerClass, series.RunnerRole)
	case series.WorktreeDirty:
		return fmt.Sprintf("the measuring binary was built from a dirty worktree (%s); a gate result that cannot be tied to a commit is not evidence", series.MeasuredSHA)
	case !series.CandidateMatch:
		return fmt.Sprintf("measured revision %s is not the frozen candidate %s; a gate result about a different artifact is not evidence for the candidate",
			series.MeasuredSHA, series.CandidateSHA)
	}
	return ""
}

// coldGateDistributionBlocker returns the reason this series' distribution is
// not the one FR-8 asked for, or "" when it is.
func coldGateDistributionBlocker(series evalreport.ColdRunSeries) string {
	if !series.Sufficient {
		return fmt.Sprintf("only %d of the required %d cold runs completed (%d aborted); FR-8's distribution was not produced",
			series.RunsCompleted, series.MinimumRuns, series.RunsAborted)
	}
	return ""
}

func evaluateColdGate(g gateMapping, series evalreport.ColdRunSeries, blockers coldGateBlockers) evalreport.GateResult {
	result := evalreport.GateResult{
		ID:         g.ID,
		PRDMetric:  g.PRDMetric,
		Threshold:  g.Threshold,
		Unit:       g.Unit,
		Comparison: g.Comparison,
		Status:     evalreport.StatusUnknown,
	}
	if g.ID == oomGateID {
		// The OOM gate has its own method and its own verdict; it is surfaced
		// here so the gate list is complete, never re-derived. What it does
		// NOT have is its own provenance rule: an OOM result measured off the
		// reference scenario or off the frozen candidate is no more evidence
		// about the candidate than a cold-index percentile would be, so the
		// same blocker is applied to it here — independently of runOOMCheck,
		// so a series assembled by any other route cannot route around it.
		oom := applyOOMProvenance(series.OOMCheck, blockers.provenance)
		result.Status = oom.Status
		result.Reason = oom.Reason
		return result
	}

	binding, ok := coldGateBindings[g.ID]
	if !ok {
		result.Reason = "no cold-series measurement is bound to this gate"
		return result
	}
	result.Aggregate = binding.metric + "." + binding.stat
	if binding.unit != g.Unit {
		result.Reason = fmt.Sprintf("the contract declares unit %q but the harness measures %q; a threshold whose unit moved cannot be read against an old conversion", g.Unit, binding.unit)
		return result
	}
	if blocker := blockers.forDistribution(); blocker != "" {
		result.Reason = blocker
		return result
	}
	agg, ok := series.Aggregates[binding.metric]
	if !ok {
		result.Reason = "aggregate " + binding.metric + " was not measured"
		return result
	}
	var raw float64
	switch binding.stat {
	case "p50":
		raw = agg.P50
	case "p95":
		raw = agg.P95
	case "max":
		raw = agg.Max
	default:
		result.Reason = "unknown statistic " + binding.stat
		return result
	}
	result.Measured = binding.convert(raw)
	result.HasMeasurement = true
	if result.Measured <= g.Threshold {
		result.Status = evalreport.StatusPass
		result.Reason = fmt.Sprintf("%.3f %s <= %.3f %s over %d runs", result.Measured, g.Unit, g.Threshold, g.Unit, agg.N)
	} else {
		result.Status = evalreport.StatusFail
		result.Reason = fmt.Sprintf("%.3f %s > %.3f %s over %d runs", result.Measured, g.Unit, g.Threshold, g.Unit, agg.N)
	}
	return result
}

// evaluateStopRule reads PRD §17's program-wide 4 GB peak-RSS rule. Unlike the
// gates it applies to EVERY measured scenario, so it deliberately does not
// require the reference scenario or the candidate — a repository that costs
// 9 GB costs 9 GB whoever measured it.
func evaluateStopRule(rule stopRule, series evalreport.ColdRunSeries) *evalreport.StopRuleResult {
	out := &evalreport.StopRuleResult{
		ID:          rule.ID,
		ThresholdGB: rule.ThresholdGB,
		Status:      evalreport.StatusUnknown,
	}
	agg, ok := series.Aggregates[evalreport.MetricStablePeakRSSMB]
	if !ok {
		out.Reason = "no peak-RSS sample was produced, so the stop rule is UNKNOWN rather than untriggered"
		return out
	}
	out.ObservedPeakGB = agg.Max / 1024
	if out.ObservedPeakGB > rule.ThresholdGB {
		out.Triggered = true
		out.Status = evalreport.StatusFail
		out.Reason = fmt.Sprintf("observed peak %.2f GB exceeds the %.0f GB stop rule over %d runs: scale claims stop until this is explained or fixed",
			out.ObservedPeakGB, rule.ThresholdGB, agg.N)
		return out
	}
	out.Status = evalreport.StatusPass
	out.Reason = fmt.Sprintf("observed peak %.2f GB is within the %.0f GB stop rule over %d runs", out.ObservedPeakGB, rule.ThresholdGB, agg.N)
	return out
}

// ─── the 8 GB OOM gate ──────────────────────────────────────────────────────

// oomSignal is one of the contract's three failure signals. `collected` is
// separate from `fired` on purpose: a signal that could not be observed cannot
// be asserted absent, and the contract requires ALL THREE to be absent before
// the gate may pass.
type oomSignal struct {
	name      string
	collected bool
	fired     bool
	detail    string
}

// oomObservation is everything the wiring managed to observe about the
// constrained run. evaluateOOM turns it into a verdict and is where every AC-4
// rule lives, so the rules are testable without a cgroup.
type oomObservation struct {
	limitBytes     int64
	cgroup         *evalreport.CgroupLimits
	exit           coldRunExit
	execErr        error
	indexCompleted bool
	kernelLog      oomSignal
}

// runOOMCheck exercises the SW-123 OOM method, or explains precisely why it
// could not be exercised. Every path out of this function that is not a real,
// verified, completed, signal-free run yields UNKNOWN — "not exercised" is
// never a PASS (AC-4).
//
// provenance is the series-level reason the numbers are not about the frozen
// candidate on the reference scenario, or "" when they are. The check is still
// EXERCISED when it is non-empty — a constrained run that proves the cgroup
// plumbing works is worth having, and the raw observations it produces are
// kept either way — but its verdict is downgraded to UNKNOWN, because the
// contract binds this gate to the reference scenario and §8.2 forbids calling
// a result about a different artifact a PASS.
func runOOMCheck(ctx context.Context, o coldSeriesOptions, workDir string, execRun coldRunExecutor, provenance string) evalreport.OOMResult {
	result := evalreport.OOMResult{GateID: oomGateID, Status: evalreport.StatusUnknown}

	if o.scenarioPath == "" {
		result.Reason = "no reference-scenario contract was supplied, so the SW-123 OOM method is undefined for this run"
		return result
	}
	rs, err := loadReferenceScenario(o.scenarioPath)
	if err != nil {
		result.Reason = "the reference-scenario contract could not be read: " + err.Error()
		return result
	}
	result.RequiredLimitBytes = rs.OOMCheck.LimitBytes
	result.Method = rs.OOMCheck.Impose

	if !o.oomCheck {
		result.Reason = "not exercised (-oom-check was not requested); PRD §8.2: an unexercised gate is UNKNOWN, never PASS"
		return result
	}
	if o.repoName != rs.OOMCheck.Repo {
		result.Reason = fmt.Sprintf("the OOM gate binds %q but this series measured %q; the gate is scoped to the reference scenario", rs.OOMCheck.Repo, o.repoName)
		return result
	}
	if runtime.GOOS != "linux" {
		result.Reason = "the method imposes the limit with cgroup v2 (systemd-run --scope), which exists on linux only; this host is " + runtime.GOOS
		return result
	}

	outPath := filepath.Join(workDir, "cold-run-oom.json")
	report, exit, execErr := execRun(ctx, 0, outPath, rs.OOMCheck.LimitBytes)
	result.Command = exit.argv

	sample := coldRunSampleFrom(0, time.Now().UTC().Format(time.RFC3339), outPath, o.runnerClass, report, exit, execErr)
	result.Run = &sample

	obs := oomObservation{
		limitBytes:     rs.OOMCheck.LimitBytes,
		cgroup:         report.Cgroup,
		exit:           exit,
		execErr:        execErr,
		indexCompleted: sample.Status == evalreport.ColdRunCompleted,
		kernelLog:      collectKernelOOM(ctx, exit.pid),
	}
	return applyOOMProvenance(evaluateOOM(result, obs), provenance)
}

// applyOOMProvenance downgrades an OOM verdict that is not about the frozen
// candidate on the reference scenario. The four percentile-derived gates get
// this from coldGateBlockers; the OOM gate computes its own verdict, so the
// same rule has to be applied to that verdict rather than inherited by it.
//
// A blocked verdict becomes UNKNOWN whatever it was. A PASS about the wrong
// artifact is exactly the failure mode this band exists to remove; a FAIL
// about the wrong artifact is not evidence about the candidate either, and
// letting one through would make the gate's meaning depend on which way it
// happened to land. Nothing is hidden by the downgrade: the observed verdict
// is named in the reason, and every raw observation (limit_verified,
// run_completed, exit_code, failure_signals, the run itself) stays in the
// record, so evaluateOOM's result is recomputable from the artifact.
//
// It is idempotent — an already-UNKNOWN result is returned untouched, so
// applying it in both runOOMCheck and evaluateColdGate cannot double-wrap a
// reason.
func applyOOMProvenance(result evalreport.OOMResult, provenance string) evalreport.OOMResult {
	if provenance == "" || result.Status == evalreport.StatusUnknown {
		return result
	}
	observed := result.Status
	result.Status = evalreport.StatusUnknown
	result.Reason = fmt.Sprintf("%s — so this gate is UNKNOWN, not %s; the constrained run itself observed %s: %s",
		provenance, observed, observed, result.Reason)
	return result
}

// evaluateOOM applies the contract's rules, in the contract's order:
//
//	FAIL   if any observed signal fired — a kill is a kill, even under a limit
//	       that could not be verified;
//	UNKNOWN if the imposed limit was not read back EXACTLY as specified, if the
//	       run did not complete for a non-OOM reason, or if any of the three
//	       signals could not be collected (absence cannot be asserted for a
//	       signal nobody looked at);
//	PASS   only with a verified limit, a completed run, and all three signals
//	       observed and absent.
func evaluateOOM(result evalreport.OOMResult, obs oomObservation) evalreport.OOMResult {
	if obs.cgroup != nil {
		result.ObservedMemoryMax = obs.cgroup.MemoryMax
		result.ObservedMemorySwapMax = obs.cgroup.MemorySwapMax
	}
	result.RunCompleted = obs.indexCompleted
	result.ExitCode = obs.exit.exitCode
	result.LimitVerified = oomLimitVerified(obs.cgroup, obs.limitBytes)

	signals := []oomSignal{oomExitSignal(obs), oomEventsSignal(obs.cgroup), obs.kernelLog}

	var fired, uncollected []string
	for _, s := range signals {
		switch {
		case s.fired:
			fired = append(fired, s.name+": "+s.detail)
		case !s.collected:
			uncollected = append(uncollected, s.name+" ("+s.detail+")")
		}
	}
	result.FailureSignals = fired

	if len(fired) > 0 {
		result.Status = evalreport.StatusFail
		result.Reason = "OOM kill observed — " + strings.Join(fired, "; ")
		return result
	}
	if !result.LimitVerified {
		result.Status = evalreport.StatusUnknown
		result.Reason = fmt.Sprintf("the imposed limit was not verified: memory.max=%q memory.swap.max=%q, want %d and \"0\"; an unverified limit proves nothing and never passes",
			result.ObservedMemoryMax, result.ObservedMemorySwapMax, obs.limitBytes)
		return result
	}
	if !obs.indexCompleted {
		result.Status = evalreport.StatusUnknown
		reason := "the constrained run did not complete, and not through an OOM kill"
		if obs.execErr != nil {
			reason += ": " + obs.execErr.Error()
		}
		result.Status = evalreport.StatusUnknown
		result.Reason = reason
		return result
	}
	if len(uncollected) > 0 {
		result.Status = evalreport.StatusUnknown
		result.Reason = "no kill was observed, but these failure signals could not be collected, so their absence cannot be asserted: " + strings.Join(uncollected, "; ")
		return result
	}
	result.Status = evalreport.StatusPass
	result.Reason = fmt.Sprintf("the run completed under a verified %d-byte limit with no swap, and all three failure signals were collected and absent", obs.limitBytes)
	return result
}

// oomLimitVerified compares the limit read back from inside the constrained
// process to the contract's exact byte figure. Exact, not "at least": a limit
// of 16 GB would also let the run finish, and would prove nothing about 8.
func oomLimitVerified(cgroup *evalreport.CgroupLimits, limitBytes int64) bool {
	if cgroup == nil || !cgroup.Available || limitBytes <= 0 {
		return false
	}
	return cgroup.MemoryMax == strconv.FormatInt(limitBytes, 10) && cgroup.MemorySwapMax == "0"
}

func oomExitSignal(obs oomObservation) oomSignal {
	s := oomSignal{name: "process exit status", collected: true}
	switch {
	case obs.exit.signal == syscall.SIGKILL.String() || obs.exit.exitCode == 137:
		s.fired = true
		s.detail = fmt.Sprintf("the measured process exited %d (signal %q), the kernel's OOM-kill signature", obs.exit.exitCode, obs.exit.signal)
	default:
		s.detail = fmt.Sprintf("exit %d", obs.exit.exitCode)
	}
	return s
}

func oomEventsSignal(cgroup *evalreport.CgroupLimits) oomSignal {
	s := oomSignal{name: "cgroup memory.events oom_kill"}
	if cgroup == nil || !cgroup.OOMKillCollected {
		s.detail = "memory.events was not readable from inside the measured process"
		return s
	}
	s.collected = true
	if cgroup.OOMKill > 0 {
		s.fired = true
		s.detail = fmt.Sprintf("oom_kill=%d in %s", cgroup.OOMKill, cgroup.Path)
		return s
	}
	s.detail = "oom_kill=0"
	return s
}

// collectKernelOOM reads the kernel log and looks for an OOM kill naming the
// measured process. dmesg is attempted non-interactively; when it is not
// readable the signal is recorded as UNCOLLECTED, which blocks a PASS rather
// than being treated as an absence.
func collectKernelOOM(ctx context.Context, pid int) oomSignal {
	s := oomSignal{name: "kernel OOM log"}
	if pid <= 0 {
		s.detail = "the measured process had no pid to search the kernel log for"
		return s
	}
	argv := []string{"dmesg"}
	if os.Geteuid() != 0 {
		argv = []string{"sudo", "-n", "dmesg"}
	}
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		s.detail = strings.Join(argv, " ") + ": " + err.Error()
		return s
	}
	s.collected = true
	if kernelOOMForPID(string(out), pid) {
		s.fired = true
		s.detail = fmt.Sprintf("the kernel log records an OOM kill for pid %d", pid)
		return s
	}
	s.detail = fmt.Sprintf("no OOM kill for pid %d in the kernel log", pid)
	return s
}

// kernelOOMForPID matches the kernel's OOM records for one pid. It matches the
// pid as a delimited token in the shapes the kernel actually emits
// ("Killed process 1234 (x)", "oom-kill:…,pid=1234,…") rather than searching
// for the digits anywhere in the line: a substring match against a timestamp
// would manufacture a FAIL out of an unrelated message.
func kernelOOMForPID(dmesgOutput string, pid int) bool {
	if pid <= 0 {
		return false
	}
	p := strconv.Itoa(pid)
	for _, line := range strings.Split(dmesgOutput, "\n") {
		l := strings.ToLower(line)
		if !strings.Contains(l, "out of memory") && !strings.Contains(l, "oom-kill") && !strings.Contains(l, "oom_reaper") {
			continue
		}
		for _, needle := range []string{"killed process " + p, "pid=" + p} {
			i := strings.Index(l, needle)
			if i < 0 {
				continue
			}
			rest := l[i+len(needle):]
			if rest == "" || !isASCIIDigit(rest[0]) {
				return true
			}
		}
	}
	return false
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }
