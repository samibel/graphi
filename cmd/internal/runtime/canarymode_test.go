package runtime

import (
	"strings"
	"testing"

	"github.com/samibel/graphi/surfaces/client"
)

// SW-226 (AX-06): the kill switch reaches the composition root.
//
// The switch itself is tested in surfaces/client; what is tested here is the
// plumbing an operator actually touches — the environment variable — and the
// two properties that make it a kill switch rather than a suggestion: every
// declared position installs, and an undeclared one fails the session instead of
// being ignored.

func restoreCanaryMode(t *testing.T) {
	t.Helper()
	previous := client.CanaryModeSetting()
	t.Cleanup(func() {
		if err := client.SetCanaryMode(previous); err != nil {
			t.Fatalf("restore canary mode %q: %v", previous, err)
		}
	})
}

func TestApplyCanaryMode_InstallsEveryDeclaredPosition(t *testing.T) {
	restoreCanaryMode(t)
	for _, mode := range client.CanaryModes() {
		t.Setenv(EnvCanaryMode, string(mode))
		if err := ApplyCanaryMode(); err != nil {
			t.Fatalf("%s=%s: %v", EnvCanaryMode, mode, err)
		}
		if got := client.CanaryModeSetting(); got != mode {
			t.Errorf("%s=%s installed %q", EnvCanaryMode, mode, got)
		}
	}
}

func TestApplyCanaryMode_UnsetLeavesTheCompiledInDefault(t *testing.T) {
	restoreCanaryMode(t)
	if err := client.SetCanaryMode(client.CanaryModeShadow); err != nil {
		t.Fatalf("SetCanaryMode: %v", err)
	}
	before := client.CanaryModeSetting()
	if err := ApplyCanaryMode(); err != nil {
		t.Fatalf("ApplyCanaryMode with %s unset: %v", EnvCanaryMode, err)
	}
	if got := client.CanaryModeSetting(); got != before {
		t.Errorf("an unset %s changed the position from %q to %q", EnvCanaryMode, before, got)
	}
}

// TestApplyCanaryMode_RejectsATypo is the fail-closed half. An operator who
// mistypes the rollback value must be told, not quietly left running the
// position they were trying to leave.
func TestApplyCanaryMode_RejectsATypo(t *testing.T) {
	restoreCanaryMode(t)
	if err := client.SetCanaryMode(client.CanaryModeActive); err != nil {
		t.Fatalf("SetCanaryMode: %v", err)
	}
	for _, bad := range []string{"lecacy", "LEGACY", "off", "1", " shadow"} {
		t.Setenv(EnvCanaryMode, bad)
		err := ApplyCanaryMode()
		if err == nil {
			t.Fatalf("%s=%q was accepted; a mistyped rollback must fail the session, not be "+
				"ignored", EnvCanaryMode, bad)
		}
		if !strings.Contains(err.Error(), EnvCanaryMode) {
			t.Errorf("the failure does not name %s: %v", EnvCanaryMode, err)
		}
		if got := client.CanaryModeSetting(); got != client.CanaryModeActive {
			t.Errorf("a rejected value still moved the switch to %q", got)
		}
	}
}
