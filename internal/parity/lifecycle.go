package parity

// THE THREE LIFECYCLE ROWS — SW-158.
//
// Three of FR-7's requirements are LIFECYCLE EVENTS rather than content edits,
// and none is provable by the change-class machinery in classes.go: FR-7 :824's
// Branch-Wechsel, and Delta §9's "interrupted full pass" and "restart and
// recovery" (:1068-1069). They live here because they differ from a change class
// in the one way that matters — the thing under test is not WHAT changed but
// WHAT HAPPENED TO THE PROCESS.
//
// WHAT THIS IS THE COMPLEMENT OF, AND WHAT IT DELIBERATELY DOES NOT REDO.
// engine/ingest/faultmatrix_test.go (SW-118) already kills the pipeline at every
// cross-DB boundary and proves convergence to a never-crashed store's snapshot
// bytes, and docs/adr/0004-ingest-recovery-disposition.md:32-41 dispositions kill
// points K1-K8 on that evidence. That layer is settled and is NOT re-implemented
// here (AC-3): a fault-injecting store wrapper in this package would duplicate
// batchFaultStore/commitFaultStore/writeFaultStore and prove nothing new. What
// ADR 0004 does not claim is REAL-PROCESS, REAL-REPOSITORY coverage — it
// reserves it, at :92-94: "ING-REWRITE stays untriggered unless the EVAL-02
// real-repo gates surface resource/recovery failures the synthetic matrix
// cannot." These rows are that reserved complement, so every one of them cites
// the ADR kill point it corresponds to rather than inventing parallel vocabulary
// for the same boundary (AC-4).
//
// HOW A KILL POINT IS ADDRESSED FROM OUTSIDE THE PROCESS. The harness may not
// add a product hook to make the kill easier — that would be a product-byte
// change. The honest lever is the one the project's own standards already
// require to exist: "a slow index must stay observable and interruptible", so
// the binary emits ingest.ProgressEvent on its own stream. The harness reads
// that stream, and when the pass announces the phase it is waiting for it sends
// a real SIGKILL to the real subprocess. The observed phase is recorded per
// repetition (see killPoint below for the exact ADR mapping, including the one
// kill point this method CANNOT address).
//
// WHY EVERY ROW RUNS MORE THAN ONCE. A single passing execution is not evidence
// of convergence. SW-144 established that on this very matrix: over six
// executions of one pinned tree with one binary, grpc-go's incremental side
// produced three distinct snapshots while the full side produced one. So each
// lifecycle row runs its journey Repeat times per kill point, publishes EVERY
// repetition, and reports how many distinct digests the sample contained. The
// verdict is FAIL if any repetition diverged: a repetition is never retried into
// green (AC-8).

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/ingestlock"
	"github.com/samibel/graphi/internal/parityreport"
)

// DefaultLifecycleRepeat is how many times each lifecycle journey runs per kill
// point. Three, not one, for the reason in the package note above; three, not
// ten, because these are the slowest rows in the matrix and the budget is real.
const DefaultLifecycleRepeat = 3

// ---------------------------------------------------------------------------
// Kill points, and the ADR 0004 boundaries they do — and do not — reach.
// ---------------------------------------------------------------------------

// killPoint is an observable moment in the binary's own progress stream, plus
// the docs/adr/0004-ingest-recovery-disposition.md kill point a signal sent at
// that moment lands on.
//
// THE MAPPING IS EXACT, NOT APPROXIMATE, AND HERE IS WHY IT CAN BE. It is read
// off engine/ingest/ingest.go's emission order, which pins what has and has not
// committed when a given line appears:
//
//	FULL PASS (IngestAll)
//	  :62/:79  PhaseParse  — the parse loop runs to completion BEFORE the first
//	                         graphstore.BeginBatch at :144, so a signal here is
//	                         K1 "before any graph batch". No graph batch exists
//	                         to have committed.
//	  :246     PhaseResolve — emitted after the WRITE batch commit (:200) AND the
//	                         LINK batch commit (:240), before the TYPERESOLVE
//	                         batch begins (:248). That is K3 exactly.
//
//	INCREMENTAL PASS (ingestChanged)
//	  :563     PhaseParse  — the phase-1 dirty-flag meta transaction has already
//	                         committed (:531-546) and BeginBatch is still ahead
//	                         at :581. That is K5 "after phase-1 dirty-mark
//	                         commit, before any graph write".
//	  :699     PhaseLink   — emitted after the durable graph batch commit at
//	                         :665 and inside the still-open meta transaction.
//	                         That is K6 "after a DURABLE graph write mid-phase-2,
//	                         before the meta commit".
//
// K2 IS NOT ADDRESSABLE FROM OUTSIDE THE PROCESS, and the rows say so instead of
// claiming it. K2 is the window between the WRITE batch commit (:200) and the
// LINK batch commit (:240). IngestAll announces PhaseLink at :186 — BEFORE the
// WRITE batch commits — and emits no further progress event until PhaseResolve
// at :246, well past the LINK commit. So no observable marker separates those
// two commits, and a signal aimed at PhaseLink would land in the K1-K2 window
// rather than in K2. Rather than publish a probabilistic claim, this harness
// addresses K1 and K3 precisely and states that K2's real-process coverage is
// absent here, and remains covered synthetically by faultmatrix_test.go's
// kill-before-batch-2 subtest. That is AC-4's rule applied in the direction it
// does not literally name: a row must not silently claim a boundary it cannot
// reach any more than it may invent a new name for one it can.
type killPoint struct {
	// ID is the harness's name for the moment (recorded per repetition).
	ID string
	// ADR is the docs/adr/0004 kill point, in the ADR's own vocabulary.
	ADR string
	// marker recognises the progress line to kill on.
	marker func(line string) bool
	// requireDrift gates a kill on having first seen the warm-start drift scan,
	// which is what proves the pass took the INCREMENTAL path rather than
	// falling back to a tolerant full pass. Without it, a fallback full pass
	// would be killed and reported as a crashed incremental.
	requireDrift bool
}

// The progress lines these markers match are cmd/graphi/progress.go's non-TTY
// plainPhaseLine / milestone output. They are matched as WIRE TEXT read off the
// subprocess's stream — the harness observes the artifact, it does not import
// the producer's vocabulary (the same rule snapshot.go applies to node kinds).
func lineHasParseMilestone(line string) bool {
	return strings.Contains(line, "graphi: indexing…") && strings.Contains(line, "% (")
}

// lineIsParsePhase matches the parse PHASE-ENTRY line, and it must tolerate
// BOTH pluralizations. cmd/graphi/progress.go renders the count through
// filesWord, which emits "1 file" for a delta of one — so a matcher written only
// for "files…" silently misses every single-file incremental pass, which is the
// commonest shape there is. That would not produce a wrong result (the row would
// read ERROR, "the signal did not land") but it would make the K5 kill point
// unreachable on any repository whose cascade happens to be one file.
func lineIsParsePhase(line string) bool {
	return strings.HasPrefix(line, "graphi: indexing ") &&
		(strings.HasSuffix(line, "files…") || strings.HasSuffix(line, "file…"))
}
func lineIsResolvePhase(line string) bool { return strings.Contains(line, "resolving types…") }
func lineIsLinkPhase(line string) bool {
	return strings.Contains(line, "linking cross-file references…")
}
func lineIsDriftPhase(line string) bool { return strings.Contains(line, "checking for changes…") }

// ---------------------------------------------------------------------------
// The row registry.
// ---------------------------------------------------------------------------

// lifecycleRow binds a declared row id from docs/rc/parity-classes.yaml to its
// driver. The ids are the SAME frozen wire identifiers the change-class table
// uses, so the drift guard can bind both directions.
type lifecycleRow struct {
	ID string
	// NeedsSignal marks a row that cannot run without a real SIGKILL, and is
	// therefore platform-gated with a DISCLOSED coverage limit (AC-9).
	NeedsSignal bool
	// KillPointCitation is carried verbatim into the report row (AC-4).
	KillPointCitation string
	run               func(*Runner, context.Context, *lifecycleEnv) []lifecycleRep
}

func lifecycleRows() []lifecycleRow {
	return []lifecycleRow{
		{
			ID: "branch_switch",
			KillPointCitation: "no kill point — this row is a git lifecycle event, not a crash. " +
				"It is declared kind: change_class in " + ClassesPath + " and carries no " +
				"docs/adr/0004-ingest-recovery-disposition.md citation for that reason.",
			run: (*Runner).driveBranchSwitch,
		},
		{
			ID:          "interrupted_full_pass",
			NeedsSignal: true,
			KillPointCitation: "K1 (docs/adr/0004-ingest-recovery-disposition.md:34, full pass before any " +
				"graph batch) and K3 (:36, after the LINK batch commit, before TYPERESOLVE). " +
				"K2 (:35) is NOT claimed by this row: IngestAll announces PhaseLink before the " +
				"WRITE batch commits, so no observable marker separates the WRITE and LINK " +
				"commits from outside the process. K2 remains covered synthetically by " +
				"engine/ingest/faultmatrix_test.go kill-before-batch-2.",
			run: (*Runner).driveInterruptedFullPass,
		},
		{
			ID:          "restart_and_recovery",
			NeedsSignal: true,
			KillPointCitation: "K5 (docs/adr/0004-ingest-recovery-disposition.md:38, after the phase-1 " +
				"dirty-mark commit, before any graph write) and K6 (:39, after a durable graph " +
				"write mid-phase-2, before the meta commit), each followed by K7 (:40, a crashed " +
				"incremental followed by a session open) across a REAL process boundary. K7 is " +
				"the kill point that had ZERO production callers before SW-118 wired " +
				"RecoverWithRoot into warmOrFullIngest.",
			run: (*Runner).driveRestartAndRecovery,
		},
	}
}

// LifecycleIDs returns the declared ids this file drives, for the drift guard.
func LifecycleIDs() []string {
	out := make([]string, 0, 3)
	for _, row := range lifecycleRows() {
		out = append(out, row.ID)
	}
	sort.Strings(out)
	return out
}

func lifecycleRowByID(id string) (lifecycleRow, bool) {
	for _, row := range lifecycleRows() {
		if row.ID == id {
			return row, true
		}
	}
	return lifecycleRow{}, false
}

// lifecycleRep is a published Repetition plus the PRD §12.3 store-level counts
// taken over the two graphs it compared.
//
// The counts ride alongside rather than inside parityreport.Repetition because
// they are a RUN-level series in the report (Report.StoreCounts, labelled by
// repo/class/side), not a per-row field — duplicating them into every
// repetition would publish the same numbers twice in two shapes.
type lifecycleRep struct {
	parityreport.Repetition
	counts *repetitionCounts
}

type repetitionCounts struct{ full, inc parityreport.StoreCounts }

// lifecycleEnv is one row's working context: the repository it runs on, its
// isolated state dir, and the report row it is filling in.
type lifecycleEnv struct {
	entry    corpus.Entry
	repoDir  string
	stateDir string
	repeat   int
	res      *parityreport.ClassResult
}

func (e *lifecycleEnv) paths(tag string) (incDB, fullDB, incMeta, fullMeta, incSnap, fullSnap string) {
	d := filepath.Join(e.stateDir, tag)
	return filepath.Join(d, "inc.db"), filepath.Join(d, "full.db"),
		filepath.Join(d, "incmeta"), filepath.Join(d, "fullmeta"),
		filepath.Join(d, "inc.snapshot"), filepath.Join(d, "full.snapshot")
}

// ---------------------------------------------------------------------------
// The row driver.
// ---------------------------------------------------------------------------

// runLifecycleRow executes one lifecycle row end to end and decides its verdict
// from the repetitions.
func (r *Runner) runLifecycleRow(ctx context.Context, res *parityreport.ClassResult, row lifecycleRow,
	candidates []corpus.Entry, rep *parityreport.Report) {

	start := time.Now()
	defer func() { res.DurationMS = time.Since(start).Milliseconds() }()

	res.KillPoint = row.KillPointCitation

	// AC-9. A platform that cannot deliver the signal does not quietly drop the
	// row: it is SKIPPED, it names runtime.GOOS and the reason, and the limit is
	// published at run level as well as in the row. Finalize then refuses to
	// call the run publishable, so disclosure costs what it should.
	if row.NeedsSignal && !SignalJourneysSupported {
		reason := "the signal journey needs a real SIGKILL to a real subprocess; this platform " +
			"has no faithful equivalent (see internal/parity/kill_windows.go), so the row is " +
			"RECORDED AS SKIPPED rather than run in a weaker form or omitted"
		res.Verdict = parityreport.VerdictSkipped
		res.CoverageLimit = reason
		res.Detail = "COVERAGE LIMIT on " + runtimeGOOS() + ": " + reason
		rep.CoverageLimits = append(rep.CoverageLimits, parityreport.CoverageLimit{
			Row: row.ID, Platform: runtimeGOOS() + "/" + runtimeGOARCH(), Reason: reason,
		})
		r.logf("parity: %-24s SKIPPED coverage limit on %s", row.ID, runtimeGOOS())
		return
	}

	ctx, cancel := context.WithTimeout(ctx, r.PerClassTimeout)
	defer cancel()

	entry, repoDir, err := r.selectLifecycleRepo(ctx, candidates)
	if err != nil {
		res.Verdict, res.Detail = parityreport.VerdictError, err.Error()
		r.logf("parity: %-24s ERROR   %v", row.ID, err)
		return
	}
	if repoDir == "" {
		res.Verdict = parityreport.VerdictSkipped
		res.Detail = fmt.Sprintf("no repository within -max-tier %d could be materialized for a lifecycle row", r.MaxTier)
		return
	}
	res.Repo = entry.Name
	res.RepoHeadSHA, _ = gitHead(ctx, repoDir)
	// THE SELECTION RULE, STATED because it is also a scope limit. Lifecycle
	// rows take the SMALLEST in-cap repository rather than searching for one
	// that exhibits a structure — there is no structure to exhibit, the subject
	// is the process. That makes cobra the repository at every tier cap, which
	// is what keeps AC-11's local `-max-tier 1` run meaningful. The cost is
	// disclosed in the published record: these rows therefore do NOT exercise
	// PARITY-002's re-link divergence, which SW-144 observed on gin and grpc-go
	// and not on cobra.
	res.SelectedBecause = fmt.Sprintf(
		"smallest in-cap repository (tier %d, %d Go files); a lifecycle row tests the PROCESS, "+
			"not a source structure, so it does not roam looking for one", entry.Tier, goFileCount(entry))

	stateDir := filepath.Join(r.WorkDir, "state", entry.Name, row.ID)
	if err := os.RemoveAll(stateDir); err != nil {
		res.Verdict, res.Detail = parityreport.VerdictError, err.Error()
		return
	}
	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		res.Verdict, res.Detail = parityreport.VerdictError, "restore clone to the pinned tree: "+err.Error()
		return
	}

	repeat := r.LifecycleRepeat
	if repeat <= 0 {
		repeat = DefaultLifecycleRepeat
	}
	env := &lifecycleEnv{entry: entry, repoDir: repoDir, stateDir: stateDir, repeat: repeat, res: res}
	reps := row.run(r, ctx, env)
	for _, rp := range reps {
		res.Repetitions = append(res.Repetitions, rp.Repetition)
	}

	// Leave the clone pinned again for the next row, whatever happened.
	if err := r.restore(ctx, entry.Name, repoDir); err != nil {
		r.logf("parity: %s: WARNING restoring clone: %v", row.ID, err)
	}
	scoreRepetitions(res, rep, reps)
	r.logf("parity: %-24s %-8s %s (%d repetitions, %s)", row.ID, res.Verdict, entry.Name,
		len(res.Repetitions), firstLine(res.Detail))
}

// scoreRepetitions turns the sample into the row's verdict, fail-closed.
//
// The rules, in order, and each is a refusal rather than a tolerance:
//
//	no repetitions        -> ERROR. A row that ran nothing decided nothing.
//	a kill did not land   -> ERROR. A journey whose crash never happened cannot
//	                         be evidence that a crash recovers, and absorbing it
//	                         would turn a mis-timed signal into a silent pass.
//	any repetition unequal-> FAIL. Every mismatch is blocking (Delta :1092); no
//	                         row is retried into green.
//	all equal             -> PASS, reported WITH the distinct-digest counts, so
//	                         a reader can see whether the sample reproduced or
//	                         merely agreed on a verdict.
func scoreRepetitions(res *parityreport.ClassResult, rep *parityreport.Report, reps []lifecycleRep) {
	if len(reps) == 0 {
		if res.Verdict == "" {
			res.Verdict = parityreport.VerdictError
			res.Detail = "the row produced no repetitions: " + res.Detail
		}
		return
	}

	incSeen, fullSeen := map[string]bool{}, map[string]bool{}
	failed, notLanded := 0, 0
	for _, rp := range reps {
		if rp.SnapshotIncSHA256 != "" {
			incSeen[rp.SnapshotIncSHA256] = true
		}
		if rp.SnapshotFullSHA256 != "" {
			fullSeen[rp.SnapshotFullSHA256] = true
		}
		if !rp.Equal {
			failed++
		}
		if rp.KillPointID != "" && !rp.KillLanded {
			notLanded++
		}
	}
	res.DistinctIncDigests, res.DistinctFullDigests = len(incSeen), len(fullSeen)
	res.Reproducible = len(reps) > 1 && len(incSeen) == 1 && len(fullSeen) == 1

	sample := fmt.Sprintf("%d repetition(s): %d distinct incremental snapshot(s), %d distinct full snapshot(s)",
		len(reps), len(incSeen), len(fullSeen))
	if !res.Reproducible && len(reps) > 1 {
		sample += ". THE SAMPLE DID NOT REPRODUCE — no single figure from this row may be quoted " +
			"as the measurement; read the repetitions individually."
	}

	switch {
	case notLanded > 0:
		res.Verdict = parityreport.VerdictError
		res.Detail = fmt.Sprintf("HARNESS COULD NOT PLACE THE SIGNAL in %d of %d repetitions: the pass "+
			"completed before the target phase was announced. This is a harness condition, NOT a "+
			"product finding, and it is an ERROR rather than a skip because a journey that never "+
			"crashed proves nothing about recovery. %s %s", notLanded, len(reps), sample, res.Detail)
	case failed > 0:
		res.Verdict = parityreport.VerdictFail
		res.Detail = fmt.Sprintf("SNAPSHOT BYTES DIFFER in %d of %d repetitions. %s %s",
			failed, len(reps), sample, res.Detail)
	default:
		res.Verdict = parityreport.VerdictPass
		res.Detail = strings.TrimSpace(sample + " " + res.Detail)
	}

	// The two PRD §12.3 store-level counts, over the real repository graph this
	// row produced. Recorded for the first repetition, plus EVERY repetition
	// that does not pass them — so the report stays readable without a
	// non-zero count ever being able to hide behind a summary.
	for i, rp := range reps {
		if rp.counts == nil {
			continue
		}
		if i == 0 || !rp.counts.full.Pass || !rp.counts.inc.Pass {
			rep.StoreCounts = append(rep.StoreCounts, rp.counts.full, rp.counts.inc)
		}
	}
}

// selectLifecycleRepo returns the smallest in-cap Go repository that
// materializes. A pin failure is fatal (a moved upstream tag means the corpus no
// longer says what it claims); anything else lets the walk continue.
func (r *Runner) selectLifecycleRepo(ctx context.Context, candidates []corpus.Entry) (corpus.Entry, string, error) {
	for _, e := range candidates {
		dir, _, err := r.materialize(ctx, e)
		if err != nil {
			if isPinFailure(err) {
				return corpus.Entry{}, "", err
			}
			continue
		}
		return e, dir, nil
	}
	return corpus.Entry{}, "", nil
}

// ---------------------------------------------------------------------------
// Row (a) — interrupted full pass.
// ---------------------------------------------------------------------------

// driveInterruptedFullPass kills a real `graphi rebuild` subprocess mid-pass and
// requires a subsequent `graphi rebuild` to converge to snapshot-byte equality
// with a fresh full index of the same tree.
//
// THE RECOVERY VERB IS `graphi rebuild`, DELIBERATELY. AC-1 permits either verb.
// A full pass is the self-healing path K2/K3's store-derived purge fix exists
// for, and it keeps this row clear of PARITY-002, which SW-144 characterised as
// triggered by `sync` RE-LINKING a file. Mixing the two would make a FAIL here
// ambiguous between a recovery defect and an already-filed re-link defect —
// which is precisely the ambiguity the restart-and-recovery row below has to
// handle with a control, and which this row can simply avoid.
//
// THE TREE IS NOT MUTATED BETWEEN CRASH AND RETRY. faultmatrix_test.go's
// kill-before-batch-2/3 subtests already do that, and the "tree changed in
// between" property is what ADR 0004's store-derived purge fix is proven on
// synthetically. This row is the REAL-PROCESS complement of the same kill
// points; it does not restate the synthetic row's stronger construction.
func (r *Runner) driveInterruptedFullPass(ctx context.Context, env *lifecycleEnv) []lifecycleRep {
	kps := []killPoint{
		{ID: "parse", ADR: "K1", marker: lineHasParseMilestone},
		{ID: "resolve", ADR: "K3", marker: lineIsResolvePhase},
	}
	var out []lifecycleRep
	n := 0
	for _, kp := range kps {
		for i := 0; i < env.repeat; i++ {
			n++
			out = append(out, r.oneInterruptedFullPass(ctx, env, kp, n))
		}
	}
	return out
}

func (r *Runner) oneInterruptedFullPass(ctx context.Context, env *lifecycleEnv, kp killPoint, n int) lifecycleRep {
	start := time.Now()
	rp := lifecycleRep{Repetition: parityreport.Repetition{N: n, KillPointID: kp.ID, ADRKillPoint: kp.ADR}}
	defer func() { rp.DurationMS = time.Since(start).Milliseconds() }()

	tag := fmt.Sprintf("%s-%02d", kp.ID, n)
	incDB, fullDB, incMeta, fullMeta, incSnap, fullSnap := env.paths(tag)
	if err := os.MkdirAll(filepath.Join(env.stateDir, tag), 0o755); err != nil {
		rp.Detail = err.Error()
		return rp
	}
	if err := r.restore(ctx, env.entry.Name, env.repoDir); err != nil {
		rp.Detail = "restore: " + err.Error()
		return rp
	}

	// 1. A real full pass, killed by a real signal at the target phase.
	k, err := r.graphiKillAt(ctx, env.repoDir, incMeta, kp,
		"rebuild", "-root", env.repoDir, "-db", incDB, "-meta", incMeta)
	rp.KillLanded, rp.ObservedPhase = k.landed, k.observedPhase
	rp.LockDuringPass, rp.LockAfterKill = k.lockDuring, k.lockAfter
	if err != nil {
		rp.Detail = "interrupted rebuild: " + err.Error()
		return rp
	}
	if !k.landed {
		rp.Detail = "the pass completed before the " + kp.ID + " marker was seen (last phase: " + k.lastPhase + ")"
		return rp
	}

	recordCrashedShape(&rp, incDB, filepath.Join(env.stateDir, tag, "crashed.snapshot"))

	// 2. Recovery: a second full pass over the crashed store, in a NEW process.
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", incDB, "-meta", incMeta); err != nil {
		rp.Detail = "recovery rebuild after the kill FAILED: " + err.Error() + "\n" + tail(out)
		return rp
	}

	// 3. A fresh full index of the same tree, into a store that never crashed.
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", fullDB, "-meta", fullMeta); err != nil {
		rp.Detail = "reference rebuild: " + err.Error() + "\n" + tail(out)
		return rp
	}

	compareInto(&rp, env, incDB, fullDB, incSnap, fullSnap)
	return rp
}

// ---------------------------------------------------------------------------
// Row (b) — restart and recovery.
// ---------------------------------------------------------------------------

// driveRestartAndRecovery kills a real `graphi sync` mid-INCREMENTAL-pass and
// then opens a FRESH PROCESS over the same state, which is ADR 0004's K7 seam:
// warmOrFullIngest calls RecoverWithRoot before it trusts the store. It requires
// the recovered graph to converge to snapshot-byte equality with a fresh full
// index.
//
// THE CROSS-PROCESS INGEST LOCK IS PART OF THE ROW, not scenery (AC-2). The lock
// is a SQLite database at meta/ingest.lock.db held with BEGIN IMMEDIATE for the
// duration of a pass, so it is held by the OS on behalf of the process and is
// released BY THE OS when SIGKILL destroys it — unlike the durable dirty rows,
// which survive precisely so the next process can replay them. Each repetition
// probes the real lock through internal/ingestlock (the same package
// internal/doctor and `graphi status` probe it with) while the subprocess is
// mid-pass and again after it dies, and records both states. "held" then "free"
// is what makes this a genuine cross-process journey rather than two sequential
// invocations that happen to share a directory.
//
// THE CONTROL EXISTS BECAUSE A FAIL HERE WOULD OTHERWISE BE AMBIGUOUS. This row
// must drive an incremental pass, and SW-144 filed PARITY-002: `graphi sync`
// re-linking any file can settle a different `imports` edge set from `rebuild`
// over the identical tree. A divergence here could therefore be a recovery
// defect OR that already-filed re-link defect. So each repetition also runs the
// SAME journey with NO kill, and records whether the crashed-and-recovered graph
// matches the uninterrupted one. That comparison DIAGNOSES; it never decides the
// row — the verdict is always the snapshot bytes against the fresh full index.
func (r *Runner) driveRestartAndRecovery(ctx context.Context, env *lifecycleEnv) []lifecycleRep {
	kps := []killPoint{
		{ID: "parse", ADR: "K5 -> K7", marker: lineIsParsePhase, requireDrift: true},
		{ID: "link", ADR: "K6 -> K7", marker: lineIsLinkPhase, requireDrift: true},
	}
	var out []lifecycleRep
	n := 0
	for _, kp := range kps {
		for i := 0; i < env.repeat; i++ {
			n++
			out = append(out, r.oneRestartAndRecovery(ctx, env, kp, n))
		}
	}
	return out
}

func (r *Runner) oneRestartAndRecovery(ctx context.Context, env *lifecycleEnv, kp killPoint, n int) lifecycleRep {
	start := time.Now()
	rp := lifecycleRep{Repetition: parityreport.Repetition{N: n, KillPointID: kp.ID, ADRKillPoint: kp.ADR}}
	defer func() { rp.DurationMS = time.Since(start).Milliseconds() }()

	tag := fmt.Sprintf("%s-%02d", kp.ID, n)
	incDB, fullDB, incMeta, fullMeta, incSnap, fullSnap := env.paths(tag)
	if err := os.MkdirAll(filepath.Join(env.stateDir, tag), 0o755); err != nil {
		rp.Detail = err.Error()
		return rp
	}
	if err := r.restore(ctx, env.entry.Name, env.repoDir); err != nil {
		rp.Detail = "restore: " + err.Error()
		return rp
	}

	// The edit that gives the incremental pass work to do. It is a REAL edit to
	// REAL source, planned by the same modify_file planner the change-class
	// matrix uses, so the row is not exercising a synthetic change shape.
	model, err := discover(env.repoDir)
	if err != nil {
		rp.Detail = "model: " + err.Error()
		return rp
	}
	mut, err := planModifyFile(model)
	if err != nil {
		rp.Detail = "plan the incremental edit: " + err.Error()
		return rp
	}

	// 1. Baseline full index — the state the incremental pass updates FROM.
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", incDB, "-meta", incMeta); err != nil {
		rp.Detail = "baseline rebuild: " + err.Error() + "\n" + tail(out)
		return rp
	}
	if err := applyMutation(env.repoDir, mut); err != nil {
		rp.Detail = "apply the incremental edit: " + err.Error()
		return rp
	}

	// 2. The incremental pass, killed by a real signal inside phase 2.
	k, err := r.graphiKillAt(ctx, env.repoDir, incMeta, kp,
		"sync", "-root", env.repoDir, "-db", incDB, "-meta", incMeta)
	rp.KillLanded, rp.ObservedPhase = k.landed, k.observedPhase
	rp.LockDuringPass, rp.LockAfterKill = k.lockDuring, k.lockAfter
	if err != nil {
		rp.Detail = "interrupted sync: " + err.Error()
		return rp
	}
	if !k.landed {
		rp.Detail = "the incremental pass completed before the " + kp.ID +
			" marker was seen (last phase: " + k.lastPhase + "; drift seen: " + fmt.Sprint(k.sawDrift) + ")"
		return rp
	}

	recordCrashedShape(&rp, incDB, filepath.Join(env.stateDir, tag, "crashed.snapshot"))

	// 3. THE FRESH PROCESS OPEN — ADR 0004 K7. `graphi sync` goes through
	//    SyncRepo -> warmOrFullIngest, which calls RecoverWithRoot BEFORE
	//    CanWarmStart, so the dirty units the killed pass left durable are
	//    replayed before any trust decision.
	if out, err := r.graphi(ctx, env.repoDir, "sync", "-root", env.repoDir, "-db", incDB, "-meta", incMeta); err != nil {
		rp.Detail = "session-open recovery sync FAILED: " + err.Error() + "\n" + tail(out)
		return rp
	}

	// 4. The reference: a fresh full index of the same final tree.
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", fullDB, "-meta", fullMeta); err != nil {
		rp.Detail = "reference rebuild: " + err.Error() + "\n" + tail(out)
		return rp
	}

	compareInto(&rp, env, incDB, fullDB, incSnap, fullSnap)

	// 5. THE CONTROL — diagnosis only. The identical journey with no kill:
	//    baseline, same edit, one uninterrupted sync. If the crashed-and-
	//    recovered graph equals the uninterrupted one, recovery was transparent
	//    and any residual divergence from the full index is NOT the crash's
	//    doing. This never touches the verdict.
	if ctrl := r.uninterruptedControl(ctx, env, tag, mut); ctrl != "" {
		if env.res.Control == "" {
			env.res.Control = ctrl
		}
		if rp.SnapshotIncSHA256 != "" && strings.Contains(ctrl, rp.SnapshotIncSHA256) {
			rp.Detail = strings.TrimSpace("CONTROL: the crashed-and-recovered graph is byte-identical to " +
				"an UNINTERRUPTED sync of the same edit, so recovery was transparent. " + rp.Detail)
		}
	}
	return rp
}

// uninterruptedControl runs the same edit through one clean `graphi sync` and
// returns the resulting snapshot digest as a diagnostic string.
func (r *Runner) uninterruptedControl(ctx context.Context, env *lifecycleEnv, tag string, mut *Mutation) string {
	dir := filepath.Join(env.stateDir, tag+"-control")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "control unavailable: " + err.Error()
	}
	db, meta, snap := filepath.Join(dir, "ctrl.db"), filepath.Join(dir, "ctrlmeta"), filepath.Join(dir, "ctrl.snapshot")
	if err := r.restore(ctx, env.entry.Name, env.repoDir); err != nil {
		return "control unavailable: restore: " + err.Error()
	}
	if _, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", db, "-meta", meta); err != nil {
		return "control unavailable: baseline: " + err.Error()
	}
	if err := applyMutation(env.repoDir, mut); err != nil {
		return "control unavailable: edit: " + err.Error()
	}
	if _, err := r.graphi(ctx, env.repoDir, "sync", "-root", env.repoDir, "-db", db, "-meta", meta); err != nil {
		return "control unavailable: sync: " + err.Error()
	}
	if err := emitSnapshot(db, snap); err != nil {
		return "control unavailable: " + err.Error()
	}
	_, b, err := readGraph(snap)
	if err != nil {
		return "control unavailable: " + err.Error()
	}
	return "uninterrupted-sync control snapshot " + digest(b)
}

// ---------------------------------------------------------------------------
// Row (c) — branch switch.
// ---------------------------------------------------------------------------

// driveBranchSwitch indexes at one pinned ref, checks out a second, runs the
// SHIPPED verb `graphi sync`, and requires the result to equal a fresh full
// index at the second ref.
//
// WHAT THIS GOES BEYOND (AC-6). cmd/graphi/sync_test.go:33
// TestRunSync_LifecycleAndBranchSwitch rewrites .git/HEAD and asserts ONE stdout
// line — printBranchSwitch's announcement at cmd/graphi/sync.go:165-169. No file
// content changes with that switch, so no graph delta exists and no
// full-vs-incremental comparison is even attempted. This row changes the working
// tree for real and asserts the GRAPH, which is the property a user relies on
// every time they change branches. The announcement is still captured, as a
// diagnostic, so the two are visibly not the same claim.
//
// THE TWO REFS ARE REAL AND BOTH ARE RECORDED (AC-5). Ref B is the manifest's
// pinned tag. Ref A is chosen by a deterministic rule — the nearest ancestor of
// the pin whose diff to the pin touches at least one .go file — and local branch
// names are created AT those two existing upstream commits. Nothing is committed
// into the clone: inventing history would make the row unreproducible, whereas
// naming two commits that already exist is reproducible from the record alone.
func (r *Runner) driveBranchSwitch(ctx context.Context, env *lifecycleEnv) []lifecycleRep {
	refA, refB, err := prepareBranchRefs(ctx, env.repoDir)
	if err != nil {
		return []lifecycleRep{{Repetition: parityreport.Repetition{N: 1, Detail: "prepare refs: " + err.Error()}}}
	}
	env.res.RefA = branchRefDesc(refA)
	env.res.RefB = branchRefDesc(refB)
	defer func() { _ = cleanupBranchRefs(context.WithoutCancel(ctx), env.repoDir, refB) }()

	var out []lifecycleRep
	for i := 1; i <= env.repeat; i++ {
		out = append(out, r.oneBranchSwitch(ctx, env, refA, refB, i))
	}
	return out
}

func (r *Runner) oneBranchSwitch(ctx context.Context, env *lifecycleEnv, refA, refB gitRef, n int) lifecycleRep {
	start := time.Now()
	rp := lifecycleRep{Repetition: parityreport.Repetition{N: n, ObservedPhase: "git checkout " + refA.Branch + " -> " + refB.Branch}}
	defer func() { rp.DurationMS = time.Since(start).Milliseconds() }()

	tag := fmt.Sprintf("switch-%02d", n)
	incDB, fullDB, incMeta, fullMeta, incSnap, fullSnap := env.paths(tag)
	if err := os.MkdirAll(filepath.Join(env.stateDir, tag), 0o755); err != nil {
		rp.Detail = err.Error()
		return rp
	}

	// 1. Index at ref A.
	if err := gitCheckout(ctx, env.repoDir, refA.Branch); err != nil {
		rp.Detail = "checkout ref A: " + err.Error()
		return rp
	}
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", incDB, "-meta", incMeta); err != nil {
		rp.Detail = "index at ref A: " + err.Error() + "\n" + tail(out)
		return rp
	}

	// 2. Switch to ref B and run the SHIPPED verb.
	if err := gitCheckout(ctx, env.repoDir, refB.Branch); err != nil {
		rp.Detail = "checkout ref B: " + err.Error()
		return rp
	}
	out, err := r.graphi(ctx, env.repoDir, "sync", "-root", env.repoDir, "-db", incDB, "-meta", incMeta)
	if err != nil {
		rp.Detail = "sync across the branch switch: " + err.Error() + "\n" + tail(out)
		return rp
	}
	// Diagnostic only: did the shipped verb also ANNOUNCE the switch? This is
	// the whole of what sync_test.go:33 proves, captured here so the record can
	// show that this row asserts something strictly stronger.
	announced := strings.Contains(string(out), "Branch switch detected")
	rp.Detail = fmt.Sprintf("printBranchSwitch announced the switch: %v (diagnostic; the assertion is the snapshot bytes). ", announced)

	// 3. The reference: a fresh full index at ref B.
	if out, err := r.graphi(ctx, env.repoDir, "rebuild", "-root", env.repoDir, "-db", fullDB, "-meta", fullMeta); err != nil {
		rp.Detail += "index at ref B: " + err.Error() + "\n" + tail(out)
		return rp
	}

	compareInto(&rp, env, incDB, fullDB, incSnap, fullSnap)
	return rp
}

// gitRef is one side of a branch switch: an existing upstream commit and the
// local branch name created at it.
type gitRef struct {
	SHA     string
	Branch  string
	Subject string
}

func branchRefDesc(g gitRef) string {
	return g.Branch + " @ " + g.SHA + " (" + g.Subject + ")"
}

const (
	branchRefAName = "parity-ref-a"
	branchRefBName = "parity-ref-b"
	// branchDeepen is how much history to fetch into the shallow clone. The
	// corpus clones with --depth 1, so a branch-switch row has no second commit
	// to switch to until the clone is deepened.
	branchDeepen = 50
)

// prepareBranchRefs deepens the shallow clone and creates two local branches at
// two commits that ALREADY EXIST upstream.
//
// The ancestor rule is deterministic and is stated in the published record: walk
// back from the pin and take the FIRST commit whose diff to the pin touches at
// least one .go file. A ref pair that differed only in documentation would give
// the incremental pass nothing to do and would make the row vacuously green.
func prepareBranchRefs(ctx context.Context, dir string) (gitRef, gitRef, error) {
	if shallow := strings.TrimSpace(gitOut(ctx, dir, "rev-parse", "--is-shallow-repository")); shallow == "true" {
		if out, err := exec.CommandContext(ctx, "git", "-C", dir, "fetch", "-q",
			"--deepen", fmt.Sprint(branchDeepen)).CombinedOutput(); err != nil {
			return gitRef{}, gitRef{}, fmt.Errorf("deepen the shallow clone: %v: %s", err, tail(out))
		}
	}
	head, err := gitHead(ctx, dir)
	if err != nil {
		return gitRef{}, gitRef{}, err
	}
	revs := strings.Fields(gitOut(ctx, dir, "rev-list", "--max-count="+fmt.Sprint(branchDeepen), "HEAD"))
	if len(revs) < 2 {
		return gitRef{}, gitRef{}, fmt.Errorf(
			"the clone has %d reachable commit(s): a branch-switch row needs two, and history could not be deepened", len(revs))
	}
	ancestor := ""
	for _, rev := range revs[1:] {
		files := gitOut(ctx, dir, "diff", "--name-only", rev, head)
		if hasGoFile(files) {
			ancestor = rev
			break
		}
	}
	if ancestor == "" {
		return gitRef{}, gitRef{}, fmt.Errorf(
			"no ancestor within %d commits differs from the pin in any .go file", branchDeepen)
	}

	refA := gitRef{SHA: ancestor, Branch: branchRefAName, Subject: gitSubject(ctx, dir, ancestor)}
	refB := gitRef{SHA: head, Branch: branchRefBName, Subject: gitSubject(ctx, dir, head)}
	for _, g := range []gitRef{refA, refB} {
		_ = exec.CommandContext(ctx, "git", "-C", dir, "branch", "-D", g.Branch).Run()
		if out, err := exec.CommandContext(ctx, "git", "-C", dir, "branch", g.Branch, g.SHA).CombinedOutput(); err != nil {
			return gitRef{}, gitRef{}, fmt.Errorf("create branch %s at %s: %v: %s", g.Branch, g.SHA, err, tail(out))
		}
	}
	return refA, refB, nil
}

// cleanupBranchRefs returns the clone to a detached HEAD at the pin and removes
// the two temporary branches, so the next row sees the corpus as it found it.
func cleanupBranchRefs(ctx context.Context, dir string, refB gitRef) error {
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "checkout", "-q", "--detach", refB.SHA).CombinedOutput(); err != nil {
		return fmt.Errorf("re-detach at the pin: %v: %s", err, tail(out))
	}
	for _, b := range []string{branchRefAName, branchRefBName} {
		_ = exec.CommandContext(ctx, "git", "-C", dir, "branch", "-D", b).Run()
	}
	return nil
}

func gitCheckout(ctx context.Context, dir, ref string) error {
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "checkout", "-q", "-f", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %v: %s", ref, err, tail(out))
	}
	// A branch switch must not leave the previous ref's build outputs behind:
	// the graph would then be of a tree that is neither ref.
	if out, err := exec.CommandContext(ctx, "git", "-C", dir, "clean", "-qfd").CombinedOutput(); err != nil {
		return fmt.Errorf("git clean after checkout %s: %v: %s", ref, err, tail(out))
	}
	return nil
}

func gitSubject(ctx context.Context, dir, rev string) string {
	return strings.TrimSpace(gitOut(ctx, dir, "log", "-1", "--format=%s", rev))
}

func hasGoFile(nameList string) bool {
	for _, line := range strings.Split(nameList, "\n") {
		if strings.HasSuffix(strings.TrimSpace(line), ".go") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// The signal mechanism.
// ---------------------------------------------------------------------------

// killObservation is what the harness could actually see about one killed pass.
type killObservation struct {
	landed        bool
	observedPhase string
	lastPhase     string
	sawDrift      bool
	lockDuring    string
	lockAfter     string
	output        string
}

// graphiKillAt runs the graphi binary and SIGKILLs it the moment its own
// progress stream announces the target phase.
//
// The stream is the product's, unmodified: cmd/graphi/progress.go degrades to
// sparse plain phase lines when stderr is not a TTY, which it is not behind a
// pipe — and TERM=dumb is set as well so no escape sequences can appear even if
// the pipe were misdetected. Nothing was added to the product to make this
// possible; that would have been a product-byte change.
func (r *Runner) graphiKillAt(ctx context.Context, cwd, metaDir string, kp killPoint, args ...string) (killObservation, error) {
	var obs killObservation

	pr, pw, err := os.Pipe()
	if err != nil {
		return obs, err
	}
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=dumb")
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return obs, err
	}
	// The parent's copy of the write end must go, or the reader never sees EOF.
	_ = pw.Close()

	var buf strings.Builder
	probedDuring := false
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		buf.WriteString(line)
		buf.WriteByte('\n')
		if lineIsDriftPhase(line) {
			obs.sawDrift = true
		}
		if isPhaseLine(line) {
			obs.lastPhase = line
		}
		// Probe the REAL cross-process ingest lock once, on the first progress
		// line, which is after the pass has acquired it and well before the
		// kill. Probing immediately before the signal instead would add a
		// SQLite round trip to the kill latency and could push the signal past
		// the window it is aimed at.
		if !probedDuring && isPhaseLine(line) {
			probedDuring = true
			obs.lockDuring = probeLock(ctx, metaDir)
		}
		if !obs.landed && kp.marker(line) && (!kp.requireDrift || obs.sawDrift) {
			if err := killHard(cmd.Process); err != nil {
				_ = pr.Close()
				_ = cmd.Wait()
				return obs, fmt.Errorf("deliver SIGKILL: %w", err)
			}
			obs.landed = true
			obs.observedPhase = line
		}
	}
	_ = pr.Close()
	// A killed process exits non-zero; that is the expected outcome and not an
	// error. Only a FAILURE TO KILL is one, and that is what obs.landed reports.
	_ = cmd.Wait()
	obs.output = tail([]byte(buf.String()))
	obs.lockAfter = probeLock(ctx, metaDir)
	return obs, nil
}

// isPhaseLine recognises any of the product's non-TTY progress lines.
func isPhaseLine(line string) bool {
	return lineIsDriftPhase(line) || lineIsParsePhase(line) || lineIsLinkPhase(line) ||
		lineIsResolvePhase(line) || lineHasParseMilestone(line) ||
		strings.Contains(line, "scanning repo…") || strings.Contains(line, "finishing up…")
}

// probeLock reports the state of the REAL cross-process ingest lock.
//
// internal/ingestlock is imported rather than reimplemented: it single-sources
// the lock's filename and its busy classification precisely so an out-of-process
// diagnostic cannot drift from the runtime that takes the lock, and it is the
// same package internal/doctor/indexcheck.go:44 and cmd/graphi/status.go:167
// probe with. It runs no ingest and opens no graph store, so it does not touch
// the instrument boundary this harness rests on.
func probeLock(ctx context.Context, metaDir string) string {
	st, err := ingestlock.Probe(ctx, metaDir)
	if err != nil {
		return "probe-error: " + firstLine(err.Error())
	}
	return string(st)
}

// ---------------------------------------------------------------------------
// The shared assertion.
// ---------------------------------------------------------------------------

// recordCrashedShape reads the store AS THE KILLED PROCESS LEFT IT.
//
// It runs BEFORE any recovery, and it is the independent evidence behind the ADR
// kill-point mapping: the progress stream says where the signal was aimed, this
// says what had actually committed when it arrived. For a full pass into a fresh
// store, K1 means no graph batch committed (0 nodes) and K3 means the WRITE and
// LINK batches did (nodes present) — a discriminator the marker alone cannot
// supply.
//
// It is BEST EFFORT by design. A SIGKILLed SQLite file may be unreadable, and
// that is a real outcome of the fault rather than a harness defect, so it is
// recorded as a note and never fails the repetition. Nothing here decides the
// row: the verdict is always the post-recovery snapshot bytes.
func recordCrashedShape(rp *lifecycleRep, dbPath, snapPath string) {
	if err := emitSnapshot(dbPath, snapPath); err != nil {
		rp.CrashedStoreNote = "crashed store unreadable after SIGKILL: " + firstLine(err.Error())
		return
	}
	g, _, err := readGraph(snapPath)
	if err != nil {
		rp.CrashedStoreNote = "crashed store snapshot unparseable: " + firstLine(err.Error())
		return
	}
	rp.CrashedNodes, rp.CrashedEdges = len(g.Nodes), len(g.Edges)
}

// compareInto performs THE ASSERTION for a lifecycle repetition: byte equality
// of the two portable snapshot envelopes, exactly as the change-class rows do
// it. Snapshot bytes decide; nothing else is consulted.
func compareInto(rp *lifecycleRep, env *lifecycleEnv, incDB, fullDB, incSnap, fullSnap string) {
	if err := emitSnapshot(incDB, incSnap); err != nil {
		rp.Detail += err.Error()
		return
	}
	if err := emitSnapshot(fullDB, fullSnap); err != nil {
		rp.Detail += err.Error()
		return
	}
	incGraph, incBytes, err := readGraph(incSnap)
	if err != nil {
		rp.Detail += err.Error()
		return
	}
	fullGraph, fullBytes, err := readGraph(fullSnap)
	if err != nil {
		rp.Detail += err.Error()
		return
	}
	rp.SnapshotIncSHA256, rp.SnapshotFullSHA256 = digest(incBytes), digest(fullBytes)
	rp.IncNodes, rp.IncEdges = len(incGraph.Nodes), len(incGraph.Edges)
	rp.FullNodes, rp.FullEdges = len(fullGraph.Nodes), len(fullGraph.Edges)
	rp.Equal = rp.SnapshotIncSHA256 == rp.SnapshotFullSHA256
	rp.counts = &repetitionCounts{
		full: storeCounts(env.entry.Name, env.res.ID, "full", fullGraph),
		inc:  storeCounts(env.entry.Name, env.res.ID, "incremental", incGraph),
	}
	if !rp.Equal {
		rp.Detail += fmt.Sprintf("incremental %d nodes / %d edges (%s) vs full %d nodes / %d edges (%s)",
			rp.IncNodes, rp.IncEdges, short(rp.SnapshotIncSHA256),
			rp.FullNodes, rp.FullEdges, short(rp.SnapshotFullSHA256))
	}
}
