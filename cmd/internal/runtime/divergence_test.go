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
	t.Cleanup(func() { client.SetDivergenceRecorder(nil) })
	return state.StateDir()
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

// AC-6: the shipped configuration writes NOTHING. A graphi session on the
// default position must not create state for a feature nobody enabled — the
// same reason `graphi doctor` is read-only.
func TestSW232_LegacySessionWritesNothing(t *testing.T) {
	stateDir := withStateHome(t)
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}

	direct := divergenceFixture(t)
	if _, err := client.DispatchOperation(context.Background(), direct, &client.DeadCodeArgs{}); err != nil {
		t.Fatalf("DispatchOperation: %v", err)
	}
	if _, err := os.Stat(divergence.Dir(stateDir)); !os.IsNotExist(err) {
		t.Fatalf("the shipped legacy position created %s (err=%v)", divergence.Dir(stateDir), err)
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
