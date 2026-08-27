package runtime

import (
	"os"
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// SW-226 (AX-06): the kill switch reaches the composition root.
//
// The switch itself is tested in surfaces/client; what is tested here is the
// plumbing an operator actually touches — the environment variables — and the
// two properties that make it a kill switch rather than a suggestion: every
// declared position installs, and an undeclared one fails the session instead of
// being ignored.
//
// SW-228 (AX-08) made the switch per operation. Two things follow, and both are
// checked below: GRAPHI_CANARY_DEAD_CODE keeps its exact spelling and now moves
// exactly the operation it names, and GRAPHI_CANARY_ALL exists so an operator
// can still move the whole seam in one action.

// restoreCanaryMode gives one test a clean kill switch: no installed position,
// and no ambient GRAPHI_CANARY_* variable.
//
// The environment half was added by SW-232, whose rollback CI leg deliberately
// runs this package with GRAPHI_CANARY_* set — exercising a rollback is the
// leg's whole purpose. Without the clearing, a per-operation variable in the
// runner's environment would win over the GRAPHI_CANARY_ALL these tests set
// (that precedence is itself a tested property), and the suite would quietly be
// asserting against the runner instead of against its own inputs.
func restoreCanaryMode(t *testing.T) {
	t.Helper()
	clearCanaryEnv(t)
	t.Cleanup(client.ResetCanaryModes)
	client.ResetCanaryModes()
}

// TestEnvCanaryMode_MatchesTheDerivedName pins the one constant that is spelled
// out rather than derived, so the literal and the deriving function cannot
// drift apart.
func TestEnvCanaryMode_MatchesTheDerivedName(t *testing.T) {
	if got := EnvCanaryModeFor(client.CanaryOperation); got != EnvCanaryMode {
		t.Fatalf("EnvCanaryModeFor(%q) = %q, but EnvCanaryMode = %q",
			client.CanaryOperation, got, EnvCanaryMode)
	}
}

// TestApplyCanaryMode_EveryMigratedOperationHasAVariable is the AX-08 widening:
// ten operations dispatch through the seam, so ten switches must reach the
// composition root. A migrated operation with no environment variable is an
// operation that cannot be rolled back without a release.
func TestApplyCanaryMode_EveryMigratedOperationHasAVariable(t *testing.T) {
	restoreCanaryMode(t)
	operations := client.MigratedOperations()
	if len(operations) < 2 {
		t.Fatalf("MigratedOperations() = %v — AX-08 migrates a set", operations)
	}
	for _, operation := range operations {
		name := EnvCanaryModeFor(operation)
		if !strings.HasPrefix(name, EnvCanaryModePrefix) {
			t.Errorf("%q derives the variable name %q", operation, name)
		}
		t.Setenv(name, string(client.CanaryModeActive))
		if err := ApplyCanaryMode(); err != nil {
			t.Fatalf("%s=active: %v", name, err)
		}
		if got := client.CanaryModeFor(operation); got != client.CanaryModeActive {
			t.Errorf("%s=active installed %q for %q", name, got, operation)
		}
		// And only that operation moved.
		for _, other := range operations {
			if other == operation {
				continue
			}
			if got := client.CanaryModeFor(other); got != client.CanaryModeLegacy {
				t.Errorf("%s=active also moved %q to %q", name, other, got)
			}
		}
		// Remove it again before the next iteration: a loop that left every
		// earlier variable set would make the "only that operation moved"
		// assertion above pass for the wrong reason. t.Setenv has already
		// registered the restore, so unsetting here is safe.
		if err := os.Unsetenv(name); err != nil {
			t.Fatalf("unset %s: %v", name, err)
		}
	}
}

// unsetAndApply removes one kill-switch variable and re-runs the composition
// root's application step — the "operator removed the variable and restarted
// the session" sequence.
func unsetAndApply(name string) error {
	if err := os.Unsetenv(name); err != nil {
		return err
	}
	return ApplyCanaryMode()
}

func TestApplyCanaryMode_InstallsEveryDeclaredPosition(t *testing.T) {
	restoreCanaryMode(t)
	for _, mode := range client.CanaryModes() {
		t.Setenv(EnvCanaryMode, string(mode))
		if err := ApplyCanaryMode(); err != nil {
			t.Fatalf("%s=%s: %v", EnvCanaryMode, mode, err)
		}
		if got := client.CanaryModeFor(client.CanaryOperation); got != mode {
			t.Errorf("%s=%s installed %q", EnvCanaryMode, mode, got)
		}
	}
}

// TestApplyCanaryMode_AllMovesEveryOperation covers the whole-seam switch, and
// the precedence rule: a per-operation variable wins over it.
func TestApplyCanaryMode_AllMovesEveryOperation(t *testing.T) {
	restoreCanaryMode(t)
	t.Setenv(EnvCanaryModeAll, string(client.CanaryModeShadow))
	t.Setenv(EnvCanaryMode, string(client.CanaryModeLegacy))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	if got := client.CanaryModeFor(client.CanaryOperation); got != client.CanaryModeLegacy {
		t.Errorf("%s did not win over %s: %q", EnvCanaryMode, EnvCanaryModeAll, got)
	}
	for _, operation := range client.MigratedOperations() {
		if operation == client.CanaryOperation {
			continue
		}
		if got := client.CanaryModeFor(operation); got != client.CanaryModeShadow {
			t.Errorf("%s=shadow left %q at %q", EnvCanaryModeAll, operation, got)
		}
	}
}

func TestApplyCanaryMode_UnsetLeavesTheCompiledInDefault(t *testing.T) {
	restoreCanaryMode(t)
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode with %s unset: %v", EnvCanaryMode, err)
	}
	for _, operation := range client.MigratedOperations() {
		if got := client.CanaryModeFor(operation); got != client.CanaryModeLegacy {
			t.Errorf("with no variable set, %q reports %q — the compiled-in default is %q",
				operation, got, client.CanaryModeLegacy)
		}
	}
}

// TestApplyCanaryMode_ClearingAVariableRollsBack is the property the reset at
// the top of ApplyCanaryMode buys. A process that runs more than one session —
// the daemon, and every test binary — must not carry a previous session's
// position past the point where the operator removed it.
func TestApplyCanaryMode_ClearingAVariableRollsBack(t *testing.T) {
	restoreCanaryMode(t)
	t.Setenv(EnvCanaryMode, string(client.CanaryModeActive))
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode: %v", err)
	}
	if got := client.CanaryModeFor(client.CanaryOperation); got != client.CanaryModeActive {
		t.Fatalf("the position did not install: %q", got)
	}
	// The operator removes the variable and the session restarts.
	if err := unsetAndApply(EnvCanaryMode); err != nil {
		t.Fatalf("ApplyCanaryMode after unset: %v", err)
	}
	if got := client.CanaryModeFor(client.CanaryOperation); got != client.CanaryModeLegacy {
		t.Errorf("after the variable was removed the position is still %q — a kill switch "+
			"that cannot be turned off is not a kill switch", got)
	}
}

// TestApplyCanaryMode_RejectsATypo is the fail-closed half. An operator who
// mistypes the rollback value must be told, not quietly left running the
// position they were trying to leave.
func TestApplyCanaryMode_RejectsATypo(t *testing.T) {
	restoreCanaryMode(t)
	for _, name := range []string{EnvCanaryMode, EnvCanaryModeAll} {
		for _, bad := range []string{"lecacy", "LEGACY", "off", "1", " shadow"} {
			t.Setenv(name, bad)
			err := ApplyCanaryMode()
			if err == nil {
				t.Fatalf("%s=%q was accepted; a mistyped rollback must fail the session, not be "+
					"ignored", name, bad)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("the failure does not name %s: %v", name, err)
			}
		}
		t.Setenv(name, string(client.CanaryModeLegacy))
	}
}
