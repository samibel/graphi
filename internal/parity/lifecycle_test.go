package parity

// HERMETIC TESTS FOR THE THREE LIFECYCLE ROWS (SW-158).
//
// Same split as the rest of this package: everything here proves HARNESS LOGIC
// and runs on every PR inside `go run ./cmd/testgate`. None of it clones, none
// reaches the network, and none is a substitute for the real-repo matrix. The
// end-to-end row below drives the REAL binary over a REAL git repository — one
// built in a t.TempDir(), so it is a real git journey without being a network
// dependency.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samibel/graphi/internal/corpus"
	"github.com/samibel/graphi/internal/parityreport"
)

// ---------------------------------------------------------------------------
// The lifecycle registry binds to the declared matrix, in both directions.
// ---------------------------------------------------------------------------

// TestLifecycleTable_BindsToDeclaredMatrix is the drift guard for the lifecycle
// rows, in the same bidirectional shape TestClassTable_BindsToDeclaredMatrix
// uses for the change classes.
//
//	MISSING  every row docs/rc/parity-classes.yaml defers to SW-158 has a driver
//	         here. Without this direction SW-158 could land two of its three rows
//	         and nothing would notice the third had quietly stayed deferred.
//	PHANTOM  every driver's id is a declared row. `id` is a frozen wire
//	         identifier; a driver for an id nobody declared would publish a row
//	         the matrix does not contain.
func TestLifecycleTable_BindsToDeclaredMatrix(t *testing.T) {
	rows, err := LoadClasses(filepath.Join("..", "..", ClassesPath))
	if err != nil {
		t.Fatalf("LoadClasses: %v", err)
	}
	declared := map[string]ClassRow{}
	for _, r := range rows {
		declared[r.ID] = r
	}

	t.Run("MISSING", func(t *testing.T) {
		for _, r := range rows {
			if r.DeferredTo != "SW-158" {
				continue
			}
			if _, ok := lifecycleRowByID(r.ID); !ok {
				t.Errorf("MISSING: %s defers %q to SW-158 but no lifecycle driver produces it", ClassesPath, r.ID)
			}
		}
	})

	t.Run("PHANTOM", func(t *testing.T) {
		for _, id := range LifecycleIDs() {
			d, ok := declared[id]
			if !ok {
				t.Errorf("PHANTOM: lifecycle driver %q has no matching id in %s", id, ClassesPath)
				continue
			}
			if d.DeferredTo != "SW-158" {
				t.Errorf("lifecycle driver %q is declared with deferred_to %q, not SW-158", id, d.DeferredTo)
			}
		}
	})

	t.Run("KIND accounting is separate for the two kinds", func(t *testing.T) {
		// FR-7 declares 15 change classes; Delta §9 adds 2 crash conditions.
		// Counting them together is the conflation that produced
		// backlog.md:55's "16 change classes", so the two counters must stay
		// separate even now that both kinds actually run.
		if got := CountChangeClasses(rows); got != 15 {
			t.Errorf("CountChangeClasses = %d, want 15", got)
		}
		if got := CountCrashConditions(rows); got != 2 {
			t.Errorf("CountCrashConditions = %d, want 2", got)
		}
	})
}

// TestKillPointCitations_UseTheADRVocabulary pins AC-4: a recovery row cites the
// ADR 0004 kill point it corresponds to, in the ADR's OWN names, and never
// invents parallel vocabulary for the same boundary.
func TestKillPointCitations_UseTheADRVocabulary(t *testing.T) {
	const adr = "docs/adr/0004-ingest-recovery-disposition.md"
	for _, row := range lifecycleRows() {
		if !row.NeedsSignal {
			// The branch-switch row corresponds to no kill point, and AC-4
			// requires it to SAY so rather than be silently uncited.
			if !strings.Contains(row.KillPointCitation, "no kill point") {
				t.Errorf("%s: a non-crash row must state that it corresponds to no kill point", row.ID)
			}
			continue
		}
		if !strings.Contains(row.KillPointCitation, adr) {
			t.Errorf("%s: kill-point citation does not name %s: %q", row.ID, adr, row.KillPointCitation)
		}
		found := false
		for _, k := range []string{"K1", "K3", "K5", "K6", "K7"} {
			if strings.Contains(row.KillPointCitation, k) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: kill-point citation names no ADR kill point: %q", row.ID, row.KillPointCitation)
		}
	}

	// The one boundary this method CANNOT address must be disclaimed, not
	// quietly omitted — a row that silently claimed K2 would be the same class
	// of overclaim as one that invented a new name for it.
	full, _ := lifecycleRowByID("interrupted_full_pass")
	if !strings.Contains(full.KillPointCitation, "K2") || !strings.Contains(full.KillPointCitation, "NOT claimed") {
		t.Errorf("interrupted_full_pass must state that K2 is not claimed by this row: %q", full.KillPointCitation)
	}
}

// ---------------------------------------------------------------------------
// Scoring: fail-closed over the whole sample.
// ---------------------------------------------------------------------------

func rep(n int, equal bool, incDigest string, landed bool) lifecycleRep {
	return lifecycleRep{Repetition: parityreport.Repetition{
		N: n, KillPointID: "parse", ADRKillPoint: "K1", KillLanded: landed,
		SnapshotIncSHA256: incDigest, SnapshotFullSHA256: "full", Equal: equal,
	}}
}

// TestScoreRepetitions_FailsClosed proves the harness can distinguish every
// outcome it must, including the two a green-only test cannot see: a diverging
// repetition and a signal that never landed.
func TestScoreRepetitions_FailsClosed(t *testing.T) {
	cases := []struct {
		name string
		reps []lifecycleRep
		want string
		// wantReproducible is checked only where the verdict allows it.
		wantReproducible bool
	}{
		{
			name:             "every repetition converged and the sample reproduced",
			reps:             []lifecycleRep{rep(1, true, "a", true), rep(2, true, "a", true), rep(3, true, "a", true)},
			want:             parityreport.VerdictPass,
			wantReproducible: true,
		},
		{
			name: "ONE diverging repetition fails the whole row",
			// This is the anti-retry rule (AC-8) as data: two of three passing
			// must NOT be reported as a pass, or "run it again" becomes the way
			// past a §12.3 gate.
			reps: []lifecycleRep{rep(1, true, "a", true), rep(2, false, "b", true), rep(3, true, "a", true)},
			want: parityreport.VerdictFail,
		},
		{
			name: "a signal that never landed is an ERROR, not a pass",
			// A journey whose crash never happened cannot be evidence that a
			// crash recovers. Reporting it as PASS would be the worst failure
			// mode this harness has: a vacuous green.
			reps: []lifecycleRep{rep(1, true, "a", true), rep(2, true, "a", false)},
			want: parityreport.VerdictError,
		},
		{
			name: "a row that ran nothing decided nothing",
			reps: nil,
			want: parityreport.VerdictError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := parityreport.ClassResult{ID: "x", Kind: parityreport.KindCrashCondition}
			var report parityreport.Report
			scoreRepetitions(&res, &report, tc.reps)
			if res.Verdict != tc.want {
				t.Fatalf("verdict = %s, want %s (detail: %s)", res.Verdict, tc.want, res.Detail)
			}
			if tc.want == parityreport.VerdictPass && res.Reproducible != tc.wantReproducible {
				t.Errorf("reproducible = %v, want %v", res.Reproducible, tc.wantReproducible)
			}
		})
	}
}

// TestScoreRepetitions_ReportsAVaryingSampleAsVarying is the regression guard on
// the lesson SW-144 paid for on grpc-go: a stable VERDICT over a varying
// measurement is not a reproducible result, and the report must say so rather
// than let a reader quote one run's number as the figure.
func TestScoreRepetitions_ReportsAVaryingSampleAsVarying(t *testing.T) {
	res := parityreport.ClassResult{ID: "x", Kind: parityreport.KindCrashCondition}
	var report parityreport.Report
	// All three converged — the verdict is PASS — but the incremental side
	// settled on three different snapshots.
	scoreRepetitions(&res, &report, []lifecycleRep{
		rep(1, true, "a", true), rep(2, true, "b", true), rep(3, true, "c", true),
	})
	if res.Verdict != parityreport.VerdictPass {
		t.Fatalf("verdict = %s, want PASS", res.Verdict)
	}
	if res.DistinctIncDigests != 3 {
		t.Errorf("distinct incremental digests = %d, want 3", res.DistinctIncDigests)
	}
	if res.Reproducible {
		t.Error("a sample with three distinct snapshots must not be reported as reproducible")
	}
	if !strings.Contains(res.Detail, "DID NOT REPRODUCE") {
		t.Errorf("a varying sample must say so in the row detail: %q", res.Detail)
	}
}

// TestCorroborateFullPassKill_PublishesTheStoreNotTheAim pins the arbitration
// rule the signal journey rests on: the marker says where a signal was AIMED,
// the crashed store says what had committed when it LANDED, and the published
// kill point is the reconciliation of the two. A SIGKILL is asynchronous — on a
// small repository the parse loop can finish and the first graph batches can
// commit between the milestone line entering the pipe and the signal arriving —
// so a repetition that kept claiming K1 over a store holding committed rows
// would publish exactly the contradiction
// TestLifecycleRow_SignalJourneyOnAGitFixture checks for on a real journey.
func TestCorroborateFullPassKill_PublishesTheStoreNotTheAim(t *testing.T) {
	kill := func(adr string, nodes, edges int, note string) lifecycleRep {
		return lifecycleRep{Repetition: parityreport.Repetition{
			N: 1, KillPointID: "parse", ADRKillPoint: adr, KillLanded: true,
			CrashedNodes: nodes, CrashedEdges: edges, CrashedStoreNote: note,
		}}
	}

	t.Run("a K1 aim contradicted by committed rows is published as the store's window", func(t *testing.T) {
		rp := kill("K1", 6, 4, "")
		corroborateFullPassKill(&rp)
		if rp.ADRKillPoint != "K2-K4" {
			t.Fatalf("adr_kill_point = %q, want K2-K4", rp.ADRKillPoint)
		}
		for _, want := range []string{"AIMED AT K1", "6 nodes / 4 edges"} {
			if !strings.Contains(rp.Detail, want) {
				t.Errorf("the reclassification must disclose %q: %s", want, rp.Detail)
			}
		}
	})
	t.Run("a corroborated K1 stands untouched", func(t *testing.T) {
		rp := kill("K1", 0, 0, "")
		corroborateFullPassKill(&rp)
		if rp.ADRKillPoint != "K1" || rp.Detail != "" {
			t.Errorf("an empty crashed store corroborates K1; got %q with detail %q", rp.ADRKillPoint, rp.Detail)
		}
	})
	t.Run("an unreadable crashed store leaves the aim standing with its note", func(t *testing.T) {
		rp := kill("K1", 0, 0, "crashed store unreadable after SIGKILL: locked")
		corroborateFullPassKill(&rp)
		if rp.ADRKillPoint != "K1" {
			t.Errorf("an unverifiable mapping must keep the aim and its note, not invent a window: %q", rp.ADRKillPoint)
		}
	})
	t.Run("K3 cannot be refuted by counts and stands", func(t *testing.T) {
		// PhaseResolve is emitted after the commits K3 names, and a signal never
		// lands before its marker — so committed rows are consistent with the
		// aim, not evidence against it.
		rp := kill("K3", 6, 7, "")
		corroborateFullPassKill(&rp)
		if rp.ADRKillPoint != "K3" {
			t.Errorf("K3's aim is consistent with committed rows and must stand: %q", rp.ADRKillPoint)
		}
	})
}

// ---------------------------------------------------------------------------
// AC-9: a platform limit is disclosed AND costs publishability.
// ---------------------------------------------------------------------------

// TestCoverageLimit_IsDisclosedAndCostsPublishability pins both halves of AC-9.
// Disclosure alone would be a loophole: "record the limit" must not become the
// cheap way past the gate, so a recorded limit also refuses publication.
func TestCoverageLimit_IsDisclosedAndCostsPublishability(t *testing.T) {
	p := parityreport.NewProvenance("deadbeef")
	p.WorktreeClean, p.ProductDiffEmpty, p.RunnerClass = true, true, "ubuntu-latest"
	r := parityreport.Report{Provenance: p}
	for i := 0; i < 15; i++ {
		r.Classes = append(r.Classes, parityreport.ClassResult{
			ID: "c" + string(rune('a'+i)), Kind: parityreport.KindChangeClass, Verdict: parityreport.VerdictPass})
	}
	r.Classes = append(r.Classes,
		parityreport.ClassResult{ID: "interrupted_full_pass", Kind: parityreport.KindCrashCondition,
			Verdict: parityreport.VerdictSkipped, CoverageLimit: "no SIGKILL on this platform"},
		parityreport.ClassResult{ID: "restart_and_recovery", Kind: parityreport.KindCrashCondition,
			Verdict: parityreport.VerdictSkipped, CoverageLimit: "no SIGKILL on this platform"})
	r.CoverageLimits = []parityreport.CoverageLimit{
		{Row: "interrupted_full_pass", Platform: "windows/amd64", Reason: "no SIGKILL"},
		{Row: "restart_and_recovery", Platform: "windows/amd64", Reason: "no SIGKILL"},
	}
	r.Finalize(15, 2)

	if r.Publishable {
		t.Fatal("a run with a recorded coverage limit must refuse publication")
	}
	if r.Outcome == parityreport.OutcomePass {
		t.Fatal("a skipped lifecycle row must never read as a pass")
	}
	joined := strings.Join(r.NotPublishableBecause, " | ")
	for _, want := range []string{"interrupted_full_pass", "restart_and_recovery", "windows/amd64"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal must name %q so the limit is actionable: %s", want, joined)
		}
	}
}

// TestCrashConditionFailure_IsBlocking is the regression guard on the hole
// SW-158 closed in Finalize. Before it, the scoring loop skipped every
// kind: "crash_condition" row, so a FAILING recovery row would have left the run
// reading PASS — a §12.3 gate silently blind to the rows it exists for.
func TestCrashConditionFailure_IsBlocking(t *testing.T) {
	p := parityreport.NewProvenance("deadbeef")
	p.WorktreeClean, p.ProductDiffEmpty, p.RunnerClass = true, true, "ubuntu-latest"
	r := parityreport.Report{Provenance: p}
	for i := 0; i < 15; i++ {
		r.Classes = append(r.Classes, parityreport.ClassResult{
			ID: "c" + string(rune('a'+i)), Kind: parityreport.KindChangeClass, Verdict: parityreport.VerdictPass})
	}
	r.Classes = append(r.Classes,
		parityreport.ClassResult{ID: "interrupted_full_pass", Kind: parityreport.KindCrashCondition, Verdict: parityreport.VerdictPass},
		parityreport.ClassResult{ID: "restart_and_recovery", Kind: parityreport.KindCrashCondition, Verdict: parityreport.VerdictFail})
	r.Finalize(15, 2)

	if r.Outcome != parityreport.OutcomeFail {
		t.Fatalf("a FAILING crash condition must fail the run: outcome = %s", r.Outcome)
	}
	if !r.Publishable {
		t.Fatal("a FAIL is legitimate published evidence, not a reason to withhold the report")
	}
}

// TestGateNote_NamesBothStoriesAndDoesNotClaimWP6 pins AC-16 and AC-17 in the
// machine-readable artifact, not only in the prose: every report says row 13
// needs both stories, and says that these rows are an INPUT to WP6's
// recovery conjunct rather than a settlement of it.
func TestGateNote_NamesBothStoriesAndDoesNotClaimWP6(t *testing.T) {
	var r parityreport.Report
	r.Finalize(15, 2)
	for _, want := range []string{"SW-144", "SW-158", "TOGETHER", "WP6", "90-day"} {
		if !strings.Contains(r.GateNote, want) {
			t.Errorf("gate note must mention %q: %s", want, r.GateNote)
		}
	}
	if strings.Contains(r.GateNote, "DEFERRED here") {
		t.Error("the gate note still describes the lifecycle rows as deferred, which SW-158 falsified")
	}
}

// ---------------------------------------------------------------------------
// The kill markers track the product's actual output.
// ---------------------------------------------------------------------------

// TestKillMarkers_MatchTheProductsProgressLines pins the wire text the signal
// mechanism depends on.
//
// This is the harness's most brittle seam and the one whose breakage would be
// SILENT in the worst way: if cmd/graphi/progress.go's non-TTY phrasing changed,
// no marker would ever match, the pass would complete unkilled — and the row
// would then read ERROR rather than pass vacuously (scoreRepetitions), but the
// cause would be a mystery. Pinning the strings here turns that into a failing
// test with an obvious diagnosis.
func TestKillMarkers_MatchTheProductsProgressLines(t *testing.T) {
	// Exactly the lines cmd/graphi/progress.go plainPhaseLine / the parse
	// milestone emit on a non-TTY stream.
	const (
		drift     = "graphi: checking for changes…"
		walk      = "graphi: scanning repo…"
		parse     = "graphi: indexing 36 files…"
		parseOne  = "graphi: indexing 1 file…"
		milestone = "graphi: indexing… 25% (9/36)"
		link      = "graphi: linking cross-file references…"
		resolve   = "graphi: resolving types…"
	)
	checks := []struct {
		name  string
		fn    func(string) bool
		match []string
		miss  []string
	}{
		{"drift", lineIsDriftPhase, []string{drift}, []string{walk, parse, link, resolve, milestone}},
		{"parse phase entry", lineIsParsePhase, []string{parse, parseOne}, []string{drift, walk, link, resolve, milestone}},
		{"parse milestone", lineHasParseMilestone, []string{milestone}, []string{drift, walk, parse, link, resolve}},
		{"link", lineIsLinkPhase, []string{link}, []string{drift, walk, parse, resolve, milestone}},
		{"resolve", lineIsResolvePhase, []string{resolve}, []string{drift, walk, parse, link, milestone}},
	}
	for _, c := range checks {
		for _, line := range c.match {
			if !c.fn(line) {
				t.Errorf("%s marker does not match the product's line %q — the kill would never land", c.name, line)
			}
		}
		for _, line := range c.miss {
			if c.fn(line) {
				t.Errorf("%s marker also matches %q — the kill would land at the wrong phase", c.name, line)
			}
		}
	}
	for _, line := range []string{drift, walk, parse, milestone, link, resolve} {
		if !isPhaseLine(line) {
			t.Errorf("isPhaseLine does not recognise %q, so the ingest-lock probe would never fire", line)
		}
	}
}

// ---------------------------------------------------------------------------
// End to end, hermetically: a real git repository, the real binary, no network.
// ---------------------------------------------------------------------------

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not available")
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=parity", "GIT_AUTHOR_EMAIL=parity@example.invalid",
		"GIT_COMMITTER_NAME=parity", "GIT_COMMITTER_EMAIL=parity@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// writeGitFixtureRepo builds a real git repository with two commits whose Go
// source differs — the minimum a branch-switch row needs. It is a REAL git
// journey; only the network is absent.
func writeGitFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/switchfixture\n\ngo 1.21\n")
	write("store/store.go", `package store

// Put writes a value.
func Put(k, v string) string { return k + "=" + v }
`)
	write("app/app.go", `package app

import "example.com/switchfixture/store"

// Run uses the store.
func Run() string { return store.Put("a", "b") }
`)
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-qm", "first")

	// The second commit changes real Go structure, so the branch switch has a
	// graph delta to converge over rather than being vacuous.
	write("store/store.go", `package store

// Put writes a value.
func Put(k, v string) string { return k + "=" + v }

// Delete removes a value.
func Delete(k string) string { return k }
`)
	write("app/app.go", `package app

import "example.com/switchfixture/store"

// Run uses the store.
func Run() string { return store.Put("a", "b") }

// Clear uses the new API.
func Clear() string { return store.Delete("a") }
`)
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-qm", "second")
	return dir
}

// TestLifecycleRow_BranchSwitchEndToEndOnAGitFixture drives the REAL binary
// through the whole branch-switch row over a REAL git repository: index at ref
// A, `git checkout` ref B, `graphi sync`, and snapshot-byte comparison against a
// fresh full index at ref B.
//
// It also pins AC-5 and AC-6 mechanically: both refs are recorded, and the row
// runs the SHIPPED verb rather than a test-only shortcut.
func TestLifecycleRow_BranchSwitchEndToEndOnAGitFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the built binary")
	}
	gitOrSkip(t)
	bin := buildGraphi(t)
	src := writeGitFixtureRepo(t)

	rows := []ClassRow{{
		ID: "branch_switch", Kind: kindChangeClass, Label: "branch switch",
		HarnessRow: harnessDeferred, DeferredTo: "SW-158",
	}}
	// URL is a filesystem path: `git clone` accepts one, so the harness's real
	// clone path is exercised with no network at all.
	m := corpus.Manifest{Entries: []corpus.Entry{{
		Name: "gitfixture", URL: src, Ref: "main", Tier: 1, Language: "go",
	}}}

	r := &Runner{
		Binary: bin, WorkDir: t.TempDir(), MaxTier: 1,
		PerClassTimeout: 5 * time.Minute, RunnerClass: "test", LifecycleRepeat: 2,
		Log: func(f string, a ...any) { t.Logf(f, a...) },
	}
	prov := parityreport.NewProvenance("test-sha")
	prov.WorktreeClean, prov.ProductDiffEmpty = true, true

	report, err := r.Run(context.Background(), m, rows, prov)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var row parityreport.ClassResult
	for _, c := range report.Classes {
		if c.ID == "branch_switch" {
			row = c
		}
	}
	t.Logf("branch_switch = %s refA=%s refB=%s detail=%s", row.Verdict, row.RefA, row.RefB, row.Detail)
	for _, rp := range row.Repetitions {
		t.Logf("  #%d equal=%v inc=%d/%d full=%d/%d %s", rp.N, rp.Equal, rp.IncNodes, rp.IncEdges, rp.FullNodes, rp.FullEdges, rp.Detail)
	}

	if row.Verdict != parityreport.VerdictPass {
		t.Fatalf("branch_switch = %s, want PASS. %s", row.Verdict, row.Detail)
	}
	if len(row.Repetitions) != 2 {
		t.Fatalf("want 2 repetitions, got %d", len(row.Repetitions))
	}
	// AC-5: both refs recorded, so the row is reproducible from the record.
	if row.RefA == "" || row.RefB == "" || row.RefA == row.RefB {
		t.Errorf("both refs must be recorded and distinct: A=%q B=%q", row.RefA, row.RefB)
	}
	// AC-6: the shipped verb, whose announcement is captured as a diagnostic —
	// which is the whole of what cmd/graphi/sync_test.go:33 asserts, and this
	// row asserts the graph on top of it.
	if !strings.Contains(row.Repetitions[0].Detail, "printBranchSwitch announced the switch: true") {
		t.Errorf("the shipped verb's branch-switch announcement was not observed: %q", row.Repetitions[0].Detail)
	}
	// The row must never be reported as deferred again once a driver exists.
	if row.Verdict == parityreport.VerdictDeferred || row.DeferredTo != "" {
		t.Error("branch_switch is delivered by SW-158 and must no longer report as deferred")
	}
}

// TestLifecycleRow_SignalJourneyOnAGitFixture drives the interrupted-full-pass
// row end to end against the real binary: a real SIGKILL to a real subprocess,
// then convergence to a fresh full index's snapshot bytes.
//
// It asserts the three things that make the row evidence rather than ceremony:
// the signal LANDED, the ingest lock was HELD by the subprocess and FREE after
// it died, and the crashed store's shape corroborates the ADR kill point the row
// claims (K1 = no graph batch committed).
func TestLifecycleRow_SignalJourneyOnAGitFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("drives the built binary")
	}
	if !SignalJourneysSupported {
		t.Skipf("no faithful SIGKILL equivalent on %s — the real matrix records this as a coverage limit", runtimeGOOS())
	}
	gitOrSkip(t)
	bin := buildGraphi(t)
	src := writeGitFixtureRepo(t)

	rows := []ClassRow{{
		ID: "interrupted_full_pass", Kind: kindCrashCondition, Label: "interrupted full pass",
		HarnessRow: harnessDeferred, DeferredTo: "SW-158",
	}}
	m := corpus.Manifest{Entries: []corpus.Entry{{
		Name: "gitfixture", URL: src, Ref: "main", Tier: 1, Language: "go",
	}}}
	r := &Runner{
		Binary: bin, WorkDir: t.TempDir(), MaxTier: 1,
		PerClassTimeout: 5 * time.Minute, RunnerClass: "test", LifecycleRepeat: 1,
		Log: func(f string, a ...any) { t.Logf(f, a...) },
	}
	prov := parityreport.NewProvenance("test-sha")
	prov.WorktreeClean, prov.ProductDiffEmpty = true, true

	report, err := r.Run(context.Background(), m, rows, prov)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var row parityreport.ClassResult
	for _, c := range report.Classes {
		if c.ID == "interrupted_full_pass" {
			row = c
		}
	}
	for _, rp := range row.Repetitions {
		t.Logf("  #%d %s/%s landed=%v equal=%v lock %s->%s crashed=%d/%d %s",
			rp.N, rp.KillPointID, rp.ADRKillPoint, rp.KillLanded, rp.Equal,
			rp.LockDuringPass, rp.LockAfterKill, rp.CrashedNodes, rp.CrashedEdges, rp.Detail)
	}
	if row.Verdict != parityreport.VerdictPass {
		t.Fatalf("interrupted_full_pass = %s, want PASS. %s", row.Verdict, row.Detail)
	}
	if len(row.Repetitions) == 0 {
		t.Fatal("the row produced no repetitions")
	}
	for _, rp := range row.Repetitions {
		if !rp.KillLanded {
			t.Errorf("#%d: the signal did not land — the journey never crashed and proves nothing", rp.N)
		}
		// AC-2's cross-process evidence: a real OS lock, held by a real process,
		// released by the OS when that process died.
		if rp.LockDuringPass != "held" {
			t.Errorf("#%d: ingest lock during the pass = %q, want \"held\"", rp.N, rp.LockDuringPass)
		}
		if rp.LockAfterKill != "free" {
			t.Errorf("#%d: ingest lock after SIGKILL = %q, want \"free\" — the OS must drop it with the process", rp.N, rp.LockAfterKill)
		}
		// The ADR mapping, corroborated by the store rather than by the marker
		// alone: a PUBLISHED K1 means "before any graph batch", so nothing may
		// have committed. corroborateFullPassKill is what keeps this true even
		// on a fixture this small, where the signal can land after the parse
		// loop finished — such a landing is published as the K2-K4 window the
		// store supports, never as K1.
		if rp.ADRKillPoint == "K1" && rp.CrashedStoreNote == "" && rp.CrashedNodes != 0 {
			t.Errorf("#%d: claims K1 (before any graph batch) but the crashed store holds %d nodes",
				rp.N, rp.CrashedNodes)
		}
	}
}
