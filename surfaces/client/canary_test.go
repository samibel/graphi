package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/opcatalog"
)

// SW-226 (AX-06) unit evidence for the canary seam: the contribution is
// registered and checked (AC-1), the kill switch has exactly three positions and
// rejects a fourth (AC-4), dual-run returns the LEGACY result while comparing
// both (AC-2), and the comparison actually fires when the paths differ — proven
// against a deliberately divergent client rather than asserted (AC-2, AC-3).
//
// The fixture-level byte parity across real outcomes lives in
// surfaces/ax06_canary_test.go, where the real engine, the real MCP dispatch and
// the real HTTP handler are involved.

// canaryStub is a Client that only answers DeadCode. Every other method is left
// to the embedded nil interface and panics if reached, which is the point: the
// canary seam must not touch anything else.
type canaryStub struct {
	Client
	calls  int
	answer func(call int, p DeadCodeParams) ([]byte, error)
}

func (s *canaryStub) DeadCode(_ context.Context, p DeadCodeParams) ([]byte, error) {
	s.calls++
	return s.answer(s.calls, p)
}

// steady answers identically on every call — the parity case.
func steady(body []byte, err error) func(int, DeadCodeParams) ([]byte, error) {
	return func(_ int, p DeadCodeParams) ([]byte, error) {
		if err != nil {
			return nil, err
		}
		return append(append([]byte(nil), body...), []byte(fmt.Sprintf(`,"max_items":%d}`, p.MaxItems))...), nil
	}
}

// withCanaryMode installs a kill-switch position for one test and restores the
// previous one, so a t.Parallel-free suite cannot leak a position into its
// neighbours.
func withCanaryMode(t *testing.T, mode CanaryMode) {
	t.Helper()
	previous := CanaryModeSetting()
	if err := SetCanaryMode(mode); err != nil {
		t.Fatalf("SetCanaryMode(%q): %v", mode, err)
	}
	t.Cleanup(func() {
		if err := SetCanaryMode(previous); err != nil {
			t.Fatalf("restore canary mode %q: %v", previous, err)
		}
	})
}

// withCleanCanaryRecorder zeroes the mismatch recorder for one test.
func withCleanCanaryRecorder(t *testing.T) {
	t.Helper()
	ResetCanaryMismatches()
	t.Cleanup(ResetCanaryMismatches)
}

// TestCanary_ContributionIsRegistered is AC-1: the canary is a registered
// contribution — a catalog spec, an executor handler, and declared ports — and
// resolving it CHECKS the canary criteria rather than trusting them.
func TestCanary_ContributionIsRegistered(t *testing.T) {
	direct, _ := executorFixture(t)
	contribution, err := CanaryContribution(direct)
	if err != nil {
		t.Fatalf("CanaryContribution: %v", err)
	}
	if contribution.Operation != CanaryOperation {
		t.Errorf("contribution names %q, want %q", contribution.Operation, CanaryOperation)
	}

	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("Shadow: %v", err)
	}
	spec, ok := catalog.Lookup(CanaryOperation)
	if !ok {
		t.Fatalf("the catalog no longer declares %q", CanaryOperation)
	}
	if contribution.Version != spec.Version {
		t.Errorf("contribution version %q, catalog version %q — the contribution must not "+
			"invent a contract version", contribution.Version, spec.Version)
	}
	if contribution.Tier != opcatalog.TierLabs {
		t.Errorf("canary tier is %q; no Stable operation may be the canary", contribution.Tier)
	}
	if contribution.Determinism != opcatalog.DeterminismDeterministic {
		t.Errorf("canary determinism is %q; a canary must be deterministic or its two paths "+
			"may disagree for reasons that are not bugs", contribution.Determinism)
	}
	if len(contribution.Ports) == 0 {
		t.Error("contribution declares no required ports")
	}
	for _, permission := range contribution.Permissions {
		if permission != opcatalog.PermissionGraphRead {
			t.Errorf("canary requires %q; the canary must be read-only with no network or "+
				"edit side effects", permission)
		}
	}

	// And the handler half of the triple: the operation is executable through
	// the AX-04 executor, not merely described in the catalog.
	executor, err := NewExecutor(direct)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	var adapted bool
	for _, id := range executor.Adapted() {
		if id == CanaryOperation {
			adapted = true
		}
	}
	if !adapted {
		t.Fatalf("%q has no executor handler; Adapted() = %v", CanaryOperation, executor.Adapted())
	}
}

// rebuiltShadowCatalog returns the shadow catalog with the canary's spec passed
// through mutate. It is how the contribution checks are shown to FAIL: a
// validation that has never been observed rejecting anything is indistinguishable
// from no validation at all.
func rebuiltShadowCatalog(t *testing.T, mutate func(*opcatalog.OperationSpec) bool) *opcatalog.Catalog {
	t.Helper()
	source, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("Shadow: %v", err)
	}
	rebuilt := opcatalog.New()
	for _, spec := range source.All() {
		if spec.ID == CanaryOperation {
			if drop := mutate(&spec); drop {
				continue
			}
		}
		if err := rebuilt.Add(spec); err != nil {
			t.Fatalf("Add %q: %v", spec.ID, err)
		}
	}
	frozen, err := rebuilt.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return frozen
}

// TestCanary_ContributionChecksAreNotDecorative drives each of the five
// conditions CanaryContribution enforces into failure.
func TestCanary_ContributionChecksAreNotDecorative(t *testing.T) {
	direct, _ := executorFixture(t)

	for _, tc := range []struct {
		name   string
		mutate func(*opcatalog.OperationSpec) bool
		want   string
	}{
		{
			name:   "no catalog spec",
			mutate: func(*opcatalog.OperationSpec) bool { return true },
			want:   "does not declare",
		},
		{
			name: "promoted to Stable",
			mutate: func(s *opcatalog.OperationSpec) bool {
				s.Tier = opcatalog.TierStable
				return false
			},
			want: "no Stable operation may be the canary",
		},
		{
			name: "no longer deterministic",
			mutate: func(s *opcatalog.OperationSpec) bool {
				// A direct store open is still a graph.read permission, so this
				// mutation isolates the determinism criterion instead of also
				// tripping the read-only one — and it is a spec the catalog's
				// own validator accepts, which is what makes the case real.
				s.Ports = append(append([]opcatalog.Port(nil), s.Ports...), opcatalog.PortGraphStore)
				s.Permissions = opcatalog.PermissionsFor(s.Ports)
				s.Determinism = opcatalog.DeterminismEnvironmentDependent
				return false
			},
			want: "cannot prove byte parity",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := rebuiltShadowCatalog(t, tc.mutate)
			_, err := canaryContribution(direct, catalog)
			if err == nil {
				t.Fatalf("the contribution resolved against a catalog where %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rejection %q does not explain %q", err, tc.want)
			}
		})
	}

	// And the unmutated catalog still resolves, so the failures above are
	// attributable to the mutation and not to the rebuild.
	if _, err := canaryContribution(direct, rebuiltShadowCatalog(t, func(*opcatalog.OperationSpec) bool { return false })); err != nil {
		t.Fatalf("the unmutated rebuild failed to resolve: %v", err)
	}
}

// TestCanary_CriteriaRejectANonReadOnlyOperation covers the one clause the
// catalog-level cases above cannot reach: OperationSpec.Validate refuses to pair
// a non-graph-read port with determinism "deterministic", so a spec that passes
// through Catalog.Add fails the determinism clause first. The permission clause
// is checked directly, against a spec built by hand — which is exactly the shape
// a future rule pack or merged module set could hand to Catalog.Validate.
func TestCanary_CriteriaRejectANonReadOnlyOperation(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec opcatalog.OperationSpec
		want string
	}{
		{
			name: "reads git history",
			spec: opcatalog.OperationSpec{
				ID: CanaryOperation, Version: "1", Tier: opcatalog.TierLabs,
				Ports:       []opcatalog.Port{opcatalog.PortGitHistory},
				Determinism: opcatalog.DeterminismDeterministic,
			},
			want: "must be read-only",
		},
		{
			name: "reaches the network",
			spec: opcatalog.OperationSpec{
				ID: CanaryOperation, Version: "1", Tier: opcatalog.TierLabs,
				Ports:       []opcatalog.Port{opcatalog.PortForgeEnumerate},
				Determinism: opcatalog.DeterminismDeterministic,
			},
			want: "must be read-only",
		},
		{
			name: "writes source",
			spec: opcatalog.OperationSpec{
				ID: CanaryOperation, Version: "1", Tier: opcatalog.TierLabs,
				Ports:       []opcatalog.Port{opcatalog.PortSourceWrite},
				Determinism: opcatalog.DeterminismDeterministic,
			},
			want: "must be read-only",
		},
		{
			name: "declares no ports at all",
			spec: opcatalog.OperationSpec{
				ID: CanaryOperation, Version: "1", Tier: opcatalog.TierLabs,
				Determinism: opcatalog.DeterminismDeterministic,
			},
			want: "declares no required ports",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := canaryCriteria(tc.spec)
			if err == nil {
				t.Fatalf("the canary criteria accepted an operation that %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("rejection %q does not explain %q", err, tc.want)
			}
		})
	}
	// The live spec still passes, so the rejections above are about the specs
	// and not about the checker refusing everything.
	catalog, err := opcatalog.Shadow()
	if err != nil {
		t.Fatalf("Shadow: %v", err)
	}
	spec, ok := catalog.Lookup(CanaryOperation)
	if !ok {
		t.Fatalf("the catalog no longer declares %q", CanaryOperation)
	}
	if err := canaryCriteria(spec); err != nil {
		t.Fatalf("the live canary spec fails its own criteria: %v", err)
	}
}

// TestCanary_KillSwitchHasExactlyThreePositions is AC-4's first half: three
// declared positions, and anything else is rejected rather than defaulted.
func TestCanary_KillSwitchHasExactlyThreePositions(t *testing.T) {
	modes := CanaryModes()
	if len(modes) != 3 {
		t.Fatalf("CanaryModes() = %v, want exactly three positions", modes)
	}
	for _, mode := range modes {
		if !mode.Valid() {
			t.Errorf("declared position %q reports itself invalid", mode)
		}
		parsed, err := ParseCanaryMode(string(mode))
		if err != nil {
			t.Errorf("ParseCanaryMode(%q): %v", mode, err)
		}
		if parsed != mode {
			t.Errorf("ParseCanaryMode(%q) = %q", mode, parsed)
		}
	}
	for _, bad := range []string{"", "lecacy", "LEGACY", "on", "off", "shadow ", "active;"} {
		if _, err := ParseCanaryMode(bad); err == nil {
			t.Errorf("ParseCanaryMode(%q) accepted an unrecognised position — a typo in an "+
				"operator's environment must not select a behaviour they did not ask for", bad)
		}
		if err := SetCanaryMode(CanaryMode(bad)); err == nil {
			t.Errorf("SetCanaryMode(%q) accepted an unrecognised position", bad)
		}
	}
	// The rejection did not disturb the installed position.
	if got := CanaryModeSetting(); !got.Valid() {
		t.Fatalf("a rejected position left the switch at %q", got)
	}
}

// TestCanary_ShippedDefaultIsShadow pins the compiled-in position of record.
// Changing it is a deliberate behaviour change and has to edit this test, which
// is the review prompt.
func TestCanary_ShippedDefaultIsShadow(t *testing.T) {
	if canaryModeDefault != CanaryModeShadow {
		t.Fatalf("the compiled-in canary position is %q, want %q — `legacy` would ship a "+
			"canary that never runs, and `active` would make the executor authoritative "+
			"before parity is proven", canaryModeDefault, CanaryModeShadow)
	}
}

// TestCanary_ModesRunTheExpectedPaths is AC-2 and AC-4: legacy runs the legacy
// method once; shadow runs BOTH and returns the legacy result; active runs the
// executor path only.
func TestCanary_ModesRunTheExpectedPaths(t *testing.T) {
	withCleanCanaryRecorder(t)
	body := []byte(`{"tool":"dead_code"`)

	for _, tc := range []struct {
		mode      CanaryMode
		wantCalls int
	}{
		{CanaryModeLegacy, 1},
		{CanaryModeShadow, 2},
		{CanaryModeActive, 1},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			withCanaryMode(t, tc.mode)
			stub := &canaryStub{answer: steady(body, nil)}
			got, err := DispatchCanary(context.Background(), stub, &DeadCodeArgs{MaxItems: 7})
			if err != nil {
				t.Fatalf("DispatchCanary: %v", err)
			}
			if stub.calls != tc.wantCalls {
				t.Errorf("%q ran the legacy method %d times, want %d", tc.mode, stub.calls, tc.wantCalls)
			}
			// The argument survives every position — including the executor's
			// JSON round trip, which is where a dropped field would show.
			if want := `"max_items":7`; !strings.Contains(string(got), want) {
				t.Errorf("%q lost the argument: %s", tc.mode, got)
			}
		})
	}

	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("agreeing paths recorded %d mismatch(es): %s", count, last)
	}
}

// TestCanary_ShadowReturnsTheLegacyResult is the AC-2 sentence that matters
// most: in dual-run the CALLER receives the legacy result. It is proven with a
// client that answers differently on the second call, so "the two happen to
// agree" cannot masquerade as "the legacy one was returned".
func TestCanary_ShadowReturnsTheLegacyResult(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)

	stub := &canaryStub{answer: func(call int, _ DeadCodeParams) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"call":%d}`, call)), nil
	}}
	got, err := DispatchCanary(context.Background(), stub, &DeadCodeArgs{})
	if err != nil {
		t.Fatalf("DispatchCanary: %v", err)
	}
	if want := []byte(`{"call":1}`); !bytes.Equal(got, want) {
		t.Fatalf("shadow returned %s, want the LEGACY result %s", got, want)
	}
	if stub.calls != 2 {
		t.Fatalf("shadow ran %d call(s); dual-run means both paths execute", stub.calls)
	}
}

// TestCanary_ShadowMismatchIsRecorded is the non-vacuity half of AC-2. A
// comparison that cannot fail proves nothing, so this drives it with paths that
// genuinely differ — in bytes and in error class — and requires the recorder to
// fire each time.
func TestCanary_ShadowMismatchIsRecorded(t *testing.T) {
	withCanaryMode(t, CanaryModeShadow)

	for _, tc := range []struct {
		name     string
		answer   func(int, DeadCodeParams) ([]byte, error)
		wantKind string
	}{
		{
			name: "different bytes",
			answer: func(call int, _ DeadCodeParams) ([]byte, error) {
				return []byte(fmt.Sprintf(`{"call":%d}`, call)), nil
			},
			wantKind: "bytes",
		},
		{
			name: "one path fails",
			answer: func(call int, _ DeadCodeParams) ([]byte, error) {
				if call == 2 {
					return nil, ErrAgentIntelUnavailable
				}
				return []byte(`{"ok":true}`), nil
			},
			wantKind: "error-presence",
		},
		{
			name: "same message, different class",
			answer: func(call int, _ DeadCodeParams) ([]byte, error) {
				const message = "client: agent intelligence tools unavailable"
				if call == 2 {
					return nil, errors.New(message)
				}
				return nil, ErrAgentIntelUnavailable
			},
			wantKind: "error",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCleanCanaryRecorder(t)
			stub := &canaryStub{answer: tc.answer}
			_, _ = DispatchCanary(context.Background(), stub, &DeadCodeArgs{})
			count, last := CanaryMismatches()
			if count != 1 {
				t.Fatalf("diverging paths recorded %d mismatch(es), want 1 — the dual-run "+
					"comparison is not firing", count)
			}
			if last.Kind != tc.wantKind {
				t.Errorf("mismatch kind = %q, want %q (%s)", last.Kind, tc.wantKind, last)
			}
			if last.Operation != CanaryOperation {
				t.Errorf("mismatch names %q, want %q", last.Operation, CanaryOperation)
			}
		})
	}
}

// TestCanary_ShadowMismatchDoesNotFailTheCaller records the deliberate decision
// documented in canary.go: the caller already holds the correct (legacy) answer,
// so an experiment's disagreement must not become their outage.
func TestCanary_ShadowMismatchDoesNotFailTheCaller(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeShadow)

	stub := &canaryStub{answer: func(call int, _ DeadCodeParams) ([]byte, error) {
		if call == 2 {
			return nil, errors.New("the executor path is broken")
		}
		return []byte(`{"ok":true}`), nil
	}}
	got, err := DispatchCanary(context.Background(), stub, &DeadCodeArgs{})
	if err != nil {
		t.Fatalf("a shadow-path failure reached the caller: %v", err)
	}
	if want := []byte(`{"ok":true}`); !bytes.Equal(got, want) {
		t.Fatalf("caller received %s, want the legacy answer %s", got, want)
	}
	if count, _ := CanaryMismatches(); count != 1 {
		t.Fatalf("the divergence was swallowed: recorded %d mismatch(es)", count)
	}
}

// TestCanary_ErrorParityAcrossModes is AC-3's failure-class half at unit level:
// a capability sentinel travels back identically in every position, by class and
// by message.
func TestCanary_ErrorParityAcrossModes(t *testing.T) {
	withCleanCanaryRecorder(t)
	for _, mode := range CanaryModes() {
		t.Run(string(mode), func(t *testing.T) {
			withCanaryMode(t, mode)
			stub := &canaryStub{answer: steady(nil, ErrAgentIntelUnavailable)}
			got, err := DispatchCanary(context.Background(), stub, &DeadCodeArgs{})
			if !errors.Is(err, ErrAgentIntelUnavailable) {
				t.Fatalf("%q returned %v, want the legacy sentinel %v", mode, err, ErrAgentIntelUnavailable)
			}
			if err.Error() != ErrAgentIntelUnavailable.Error() {
				t.Errorf("%q error text %q differs from the legacy %q", mode, err, ErrAgentIntelUnavailable)
			}
			if len(got) != 0 {
				t.Errorf("%q returned %d bytes alongside an error", mode, len(got))
			}
		})
	}
	if count, last := CanaryMismatches(); count != 0 {
		t.Fatalf("identical failures recorded %d mismatch(es): %s", count, last)
	}
}

// TestCanary_DispatchIsOneOperationWide keeps the seam from becoming the bulk
// migration by accident: anything that is not the canary is rejected by name.
func TestCanary_DispatchIsOneOperationWide(t *testing.T) {
	withCanaryMode(t, CanaryModeShadow)
	stub := &canaryStub{answer: steady([]byte(`{}`), nil)}
	_, err := DispatchCanary(context.Background(), stub, &SearchArgs{Query: "x"})
	if err == nil {
		t.Fatal("DispatchCanary executed a non-canary operation; AX-06 migrates exactly one")
	}
	if !strings.Contains(err.Error(), CanaryOperation) {
		t.Errorf("the rejection does not name the canary operation: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("the rejected operation still reached a client method (%d calls)", stub.calls)
	}
	if _, err := DispatchCanary(context.Background(), stub, nil); err == nil {
		t.Error("DispatchCanary accepted nil arguments")
	}
}

// TestCanary_ActiveFailsClosedWhenTheExecutorCannotBeBuilt: an `active` canary
// that quietly answers from legacy would be a kill switch that lies about its
// position, so the failure is returned instead.
func TestCanary_ActiveFailsClosedWhenTheExecutorCannotBeBuilt(t *testing.T) {
	withCleanCanaryRecorder(t)
	withCanaryMode(t, CanaryModeActive)
	// A nil Client is the one construction failure reachable without breaking
	// the embedded catalog: NewExecutor rejects it.
	_, err := DispatchCanary(context.Background(), nil, &DeadCodeArgs{})
	if err == nil {
		t.Fatal("active mode silently degraded to the legacy path")
	}
}
