package runtime

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/model"
	"github.com/samibel/graphi/engine/analysis"
	"github.com/samibel/graphi/engine/query"
	"github.com/samibel/graphi/engine/search"
	"github.com/samibel/graphi/internal/divergence"
	"github.com/samibel/graphi/internal/state"
	"github.com/samibel/graphi/surfaces/client"
)

// divergenceFixture is a Direct client that can actually answer dead_code, so
// the dual run has two real results to compare.
func divergenceFixture(t *testing.T) *client.Direct {
	t.Helper()
	ctx := context.Background()
	store := graphstore.NewMemStore()
	for i := 0; i < 6; i++ {
		n, err := model.NewNode("function", fmt.Sprintf("p.F%d", i), fmt.Sprintf("p/f%d.go", i), 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.PutNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}
	return client.NewDirect(query.New(store), search.New(store)).
		WithAnalysis(analysis.NewDefaultService(store))
}

// withStateHome points the state directory at a temp dir so the test never
// writes into the developer's real ~/.graphi, and clears any ambient
// kill-switch variable.
//
// The clearing is not tidiness. The rollback CI leg deliberately RUNS these
// packages with GRAPHI_CANARY_* set (that is the point of exercising a
// rollback), and a per-operation variable wins over GRAPHI_CANARY_ALL — so a
// test that installs a position through ALL alone would silently be testing the
// runner's environment instead of its own.
func withStateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_STATE_HOME", home)
	clearCanaryEnv(t)
	t.Cleanup(resetDivergenceInstall)
	return state.StateDir()
}

// resetDivergenceInstall is the teardown counterpart of
// installDivergenceRecorder: it drops the installed store WITHOUT flushing it.
// The production retire path flushes, on purpose; a test must not, because the
// state directory it was writing into is a t.TempDir that is about to be
// removed, and a flush would recreate it after the fact.
func resetDivergenceInstall() {
	installedDivergence.mu.Lock()
	defer installedDivergence.mu.Unlock()
	installedDivergence.store, installedDivergence.key = nil, ""
	client.SetDivergenceRecorder(nil)
}

// installedSegmentPath is the file the currently installed store owns. Test
// code reads the composition root's own bookkeeping deliberately: the identity
// of the store across two ApplyCanaryMode calls IS the property under test, and
// it is not observable from the recorded output alone.
func installedSegmentPath(t *testing.T) string {
	t.Helper()
	installedDivergence.mu.Lock()
	defer installedDivergence.mu.Unlock()
	if installedDivergence.store == nil {
		t.Fatal("no divergence store is installed")
	}
	return installedDivergence.store.Path()
}

// dispatchN runs the canary operation n times through the seam.
func dispatchN(t *testing.T, direct *client.Direct, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
			t.Fatalf("DispatchOperation #%d: %v", i, err)
		}
	}
}

// clearCanaryEnv unsets every kill-switch variable for the duration of one
// test. t.Setenv is called first with the value the variable already has, which
// is what registers the restore; the unset then happens on top of it.
func clearCanaryEnv(t *testing.T) {
	t.Helper()
	names := []string{EnvCanaryModeAll}
	for _, op := range client.MigratedOperations() {
		names = append(names, EnvCanaryModeFor(op))
	}
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			t.Setenv(name, value)
			if err := os.Unsetenv(name); err != nil {
				t.Fatalf("unset %s: %v", name, err)
			}
		}
	}
}

// SW-232 AC-1, end to end through the composition root: with the seam in
// `shadow`, a dispatch leaves a durable record that an independent reader finds
// on disk. This is the wiring test — surfaces/client proves it observes,
// internal/divergence proves it persists, and this proves the two are actually
// connected in a real graphi session.
func TestSW232_ShadowSessionWritesADurableRecord(t *testing.T) {
	stateDir := withStateHome(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeShadow))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}

	direct := divergenceFixture(t)
	if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}

	rep, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}
	doc := divergence.Assess(rep, client.MigratedOperations())
	if doc.Observations != 1 {
		t.Fatalf("observations = %d, want 1\n%+v", doc.Observations, doc.Operations)
	}
	if doc.State != divergence.StatePartial {
		t.Fatalf("state = %q, want %q (one operation observed, the rest never were)",
			doc.State, divergence.StatePartial)
	}
	for _, op := range doc.Operations {
		if op.Operation != client.CanaryOperation {
			continue
		}
		if op.State != divergence.StateAgreed || op.Mismatches != 0 {
			t.Fatalf("%s = %+v, want an observed agreement", op.Operation, op)
		}
	}
}

// SW-232 AC-6: a session in `legacy` writes NOTHING. A rolled-back seam must
// not create state for a comparison it is not performing — the same reason
// `graphi doctor` is read-only.
//
// SW-244 narrowed this test's claim without weakening it. It used to say "the
// SHIPPED configuration writes nothing" and reach that position by setting no
// variable at all; the shipped default is now `shadow`, which writes on
// purpose. The property that still has to hold — and the one an operator
// depends on mid-incident — is that the position they roll back TO is silent,
// so the position is now named explicitly rather than arrived at by default.
// Its SW-244 counterpart, TestSW244_ShippedDefaultSessionWritesTheRecord,
// asserts the other half: unset must now write.
func TestSW232_LegacyPositionWritesNothing(t *testing.T) {
	stateDir := withStateHome(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}

	direct := divergenceFixture(t)
	if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if _, err := os.Stat(divergence.Dir(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("the legacy position created %s (err=%v)", divergence.Dir(stateDir), err)
	}
	rep, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}
	doc := divergence.Assess(rep, client.MigratedOperations())
	if doc.State != divergence.StateUnknown {
		t.Fatalf("state = %q, want UNKNOWN — nothing was ever observed", doc.State)
	}
}

// TestSW244_ShippedDefaultSessionWritesTheRecord is AC-5 at the composition
// root: with NO kill-switch variable set — a plain install, the configuration a
// release actually ships — a dispatch must leave an observation on disk that an
// independent reader finds.
//
// This is the property the whole story turns on. SW-232 proved the record is
// durable when an operator opts in with GRAPHI_CANARY_ALL=shadow; what SW-238's
// release precondition needs is that it fills when NOBODY opts in, because
// nobody will. So the test sets no variable, which is the point, and asserts
// the read path reports observations rather than UNKNOWN.
//
// The end-to-end demonstration through the real binary and `graphi doctor
// -divergence` is in the story's verification record and in the
// executor-rollback workflow; this is its in-process guard, so a regression
// fails a unit run rather than waiting for someone to re-run the demo.
func TestSW244_ShippedDefaultSessionWritesTheRecord(t *testing.T) {
	stateDir := withStateHome(t)
	// Deliberately no t.Setenv: withStateHome has cleared every GRAPHI_CANARY_*
	// variable, so this is the compiled-in default and nothing else.
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	if got := client.CanaryModeFor(client.CanaryOperation); got != client.CanaryModeShadow {
		t.Fatalf("the shipped default installed %q, want %q", got, client.CanaryModeShadow)
	}

	direct := divergenceFixture(t)
	dispatchN(t, direct, 3)
	// The first observation flushes immediately, the next two coalesce in the
	// store's buffer. Rolling back retires the store, and retiring flushes —
	// the same sequence TestSW232_RollbackFlushesTheBufferedObservations pins,
	// used here to make the count exact rather than a lower bound.
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode (flush via rollback): %v", err)
	}

	rep, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}
	doc := divergence.Assess(rep, client.MigratedOperations())
	if doc.Observations != 3 {
		t.Fatalf("observations = %d, want 3 — the shipped default must fill the record "+
			"without an operator opting in\n%+v", doc.Observations, doc.Operations)
	}
	if doc.State == divergence.StateUnknown {
		t.Fatalf("state = UNKNOWN after 3 dispatches on the shipped default — " +
			"the release line would accrue no shadow evidence at all")
	}
	for _, op := range doc.Operations {
		if op.Operation != client.CanaryOperation {
			continue
		}
		if op.State != divergence.StateAgreed || op.Mismatches != 0 {
			t.Fatalf("%s = %+v, want an observed agreement", op.Operation, op)
		}
	}
}

// Rolling the kill switch back to `legacy` also UNINSTALLS the recorder, so the
// rollback documented in docs/executor-seam-rollback.md really does stop the
// writing rather than only stopping the dual run.
func TestSW232_RollbackToLegacyUninstallsTheRecorder(t *testing.T) {
	stateDir := withStateHome(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeShadow))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	direct := divergenceFixture(t)
	if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	before, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}

	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode (rollback): %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
			t.Fatalf("DispatchOperation after rollback: %v", err)
		}
	}
	after, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read after rollback: %v", err)
	}
	if total(after) != total(before) {
		t.Fatalf("observations after rollback = %d, want the pre-rollback %d — a rolled-back "+
			"seam must stop observing", total(after), total(before))
	}
}

func total(rep divergence.Report) int {
	sum := 0
	for _, op := range rep.Operations {
		sum += op.Observations
	}
	return sum
}

// A live `graphi mcp` server calls OpenSession — and therefore ApplyCanaryMode —
// AGAIN, mid-session, in the same already-serving process, every time the
// client announces a roots-list change (surfaces/mcp/mcp.go handles
// notifications/roots/list_changed; surfaces/mcp/session.go rebinds). That is a
// documented, supported, non-restart lifecycle event, and it lands on the
// recorder installation path with the canary configuration completely
// unchanged.
//
// Before the fix, each such call built a brand-new divergence.Store and swapped
// it in, and the outgoing store's coalesced-but-unflushed observations — up to
// one flush interval of them — were garbage collected. Mismatches were never at
// risk (they flush immediately), but the observation COUNT is half of what AC-1
// promises to persist, and losing it silently is precisely what the read path
// refuses to do when it discloses unreadable and pruned segments as a lower
// bound.
//
// This test is the shape no other test had: ApplyCanaryMode twice while STAYING
// dual. It fails three ways against the old code — the store identity changes,
// a second segment file appears, and two of the five observations are gone.
func TestSW232_RebindWhileStayingDualLosesNoObservation(t *testing.T) {
	stateDir := withStateHome(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeShadow))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	direct := divergenceFixture(t)

	// The first observation flushes immediately; the next two coalesce into the
	// store's in-memory buffer, which is the state a replacement would drop.
	dispatchN(t, direct, 3)

	before := installedSegmentPath(t)
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode (roots-change rebind): %v", err)
	}
	if after := installedSegmentPath(t); after != before {
		t.Fatalf("the rebind replaced the store (%s -> %s); its buffered observations went with it",
			before, after)
	}

	dispatchN(t, direct, 2)

	// Rolling back to `legacy` retires the store, which must flush what it is
	// still holding rather than let go of it.
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode (rollback): %v", err)
	}

	rep, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}
	if got := total(rep); got != 5 {
		t.Fatalf("observations = %d, want 5 — a mid-session rebind must not lose the "+
			"observations the outgoing store had buffered", got)
	}
	if rep.Segments != 1 {
		t.Fatalf("segments = %d, want 1 — the rebind must reuse this process's segment, "+
			"not start a second one", rep.Segments)
	}
	if rep.Pruned != 0 || rep.Unreadable != 0 {
		t.Fatalf("pruned = %d, unreadable = %d, want 0/0 — the totals above must be exact, "+
			"not a lower bound", rep.Pruned, rep.Unreadable)
	}
}

// The other half of the same finding: when the configuration genuinely DOES
// change, the outgoing store still holds observations that were never written.
// Retiring it has to flush, or a rollback to `legacy` — the one action the
// rollback document tells an operator to take — destroys the evidence it was
// taken to preserve.
func TestSW232_RollbackFlushesTheBufferedObservations(t *testing.T) {
	stateDir := withStateHome(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeShadow))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	direct := divergenceFixture(t)
	dispatchN(t, direct, 3)

	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode (rollback): %v", err)
	}

	rep, err := divergence.Read(stateDir)
	if err != nil {
		t.Fatalf("divergence.Read: %v", err)
	}
	if got := total(rep); got != 3 {
		t.Fatalf("observations after rollback = %d, want 3 — uninstalling the recorder must "+
			"flush its buffer, not drop it", got)
	}
}
