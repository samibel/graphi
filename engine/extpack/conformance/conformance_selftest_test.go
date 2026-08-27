// The harness testing itself (SW-230 / AX-10, AC-5).
//
// Every check in this package is proven in BOTH directions here: an honest
// control contribution PASSES, and one deliberately broken in each specific way
// FAILS on the specific check that exists to catch it. A harness that has never
// been observed failing certifies nothing, and this file is the reason anybody
// may believe the reports it emits.
//
// The port cases are the ones worth reading closely. The broken handlers
// SWALLOW the gate's refusal — they call Use, discard the error, and carry on —
// because a real dishonest contribution would. If the harness only failed when a
// handler cooperatively propagated the error, it would be enforcing a convention
// rather than a boundary.
package conformance_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/extpack/conformance"
	"github.com/samibel/graphi/engine/opcatalog"
)

// hostAPI is the range every honest fixture declares: exactly this host.
func hostAPI() extpack.APIRange {
	return extpack.APIRange{Min: conformance.HostAPIVersion, Max: conformance.HostAPIVersion}
}

// honestSpec is a minimal, valid, read-only Labs operation spec.
func honestSpec() opcatalog.OperationSpec {
	return opcatalog.OperationSpec{
		ID:      "example_read",
		Version: "1",
		Tier:    opcatalog.TierLabs,
		Advertisement: opcatalog.Advertisement{
			Description: "example: a read-only contribution that exists to prove the conformance harness",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "item cap"},
				},
			},
			Annotations: map[string]any{"readOnlyHint": true},
		},
		Ports:         []opcatalog.Port{opcatalog.PortGraphQuery},
		Permissions:   []opcatalog.Permission{opcatalog.PermissionGraphRead},
		Determinism:   opcatalog.DeterminismDeterministic,
		HTTPResource:  "analyze/example_read",
		PortsEvidence: "engine/extpack/conformance/conformance_selftest_test.go: the fixture handler calls Use(graph.query) and nothing else",
	}
}

// honestContribution is the control. Every failing case below is this value with
// exactly one thing changed, so a failure is attributable to that change.
func honestContribution() conformance.Contribution {
	return conformance.Contribution{
		Spec:  honestSpec(),
		API:   hostAPI(),
		Ports: conformance.Ports{opcatalog.PortGraphQuery: "a graph reader"},
		Invoke: func(_ context.Context, host conformance.Host, args json.RawMessage) ([]byte, error) {
			reader, err := host.Use(opcatalog.PortGraphQuery)
			if err != nil {
				return nil, err
			}
			return []byte(fmt.Sprintf(`{"reader":%q,"args":%s}`, reader, argsOrNull(args))), nil
		},
		Fixtures: []conformance.Fixture{
			{Name: "default", Arguments: nil},
			{Name: "with_limit", Arguments: json.RawMessage(`{"limit":3}`)},
		},
	}
}

func argsOrNull(args json.RawMessage) string {
	if len(args) == 0 {
		return "null"
	}
	return string(args)
}

// mcpProjector renders the fields the harness's MCP contract checks.
func mcpProjector() conformance.Projector {
	return conformance.Projector{Surface: "mcp", Render: func(spec opcatalog.OperationSpec) (map[string]any, error) {
		description := spec.Description
		if spec.Tier != opcatalog.TierStable {
			description = "[labs] " + description
		}
		return map[string]any{
			"name":        spec.ID,
			"description": description,
			"inputSchema": spec.InputSchema,
		}, nil
	}}
}

func httpProjector() conformance.Projector {
	return conformance.Projector{Surface: "http", Render: func(spec opcatalog.OperationSpec) (map[string]any, error) {
		return map[string]any{
			"resource": spec.HTTPResource,
			"labs":     spec.Tier != opcatalog.TierStable,
		}, nil
	}}
}

// TestAX10_TheHonestContributionPasses is the non-vacuity control: if this ever
// fails, every failure below is proving something other than what it claims.
func TestAX10_TheHonestContributionPasses(t *testing.T) {
	c := honestContribution()
	c.Projectors = []conformance.Projector{mcpProjector(), httpProjector()}
	report := conformance.VerifyContribution(context.Background(), c)
	if err := report.Err(); err != nil {
		t.Fatalf("the honest control did not pass:\n%v\n%s", err, report)
	}
	for _, want := range []string{
		conformance.CheckSpec, conformance.CheckPermissions, conformance.CheckAPIVersion,
		conformance.CheckSurfaceMetadata, conformance.CheckSurfaceProjection,
		conformance.CheckDeterminism, conformance.CheckPortHonesty,
	} {
		if !ran(report, want) {
			t.Errorf("check %q did not run; the control does not cover the harness", want)
		}
	}
}

// TestAX10_ANonDeterministicContributionFails is the first half of AC-5's
// fail-closed proof. The handler is a counter — same inputs, different bytes.
func TestAX10_ANonDeterministicContributionFails(t *testing.T) {
	var calls atomic.Int64
	c := honestContribution()
	c.Invoke = func(_ context.Context, host conformance.Host, _ json.RawMessage) ([]byte, error) {
		if _, err := host.Use(opcatalog.PortGraphQuery); err != nil {
			return nil, err
		}
		return []byte(fmt.Sprintf(`{"call":%d}`, calls.Add(1))), nil
	}
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckDeterminism, "different results on two identical runs")
}

// TestAX10_AContributionTouchingAnUndeclaredPortFails is the second half. The
// handler reaches for git history, which the spec does not declare, and SWALLOWS
// the refusal — so the harness has to fail on what it did, not on what it
// admitted.
func TestAX10_AContributionTouchingAnUndeclaredPortFails(t *testing.T) {
	c := honestContribution()
	c.Invoke = func(_ context.Context, host conformance.Host, _ json.RawMessage) ([]byte, error) {
		if _, err := host.Use(opcatalog.PortGraphQuery); err != nil {
			return nil, err
		}
		// The refusal is discarded on purpose. A dishonest contribution would.
		_, _ = host.Use(opcatalog.PortGitHistory)
		return []byte(`{"ok":true}`), nil
	}
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckPortHonesty, "undeclared port")
}

// TestAX10_AnOverDeclaredPortFails is the quieter half of port honesty: the
// permission set a user is asked to grant is derived from the port list, so a
// port declared and never used over-states what the contribution takes.
func TestAX10_AnOverDeclaredPortFails(t *testing.T) {
	c := honestContribution()
	c.Spec.Ports = []opcatalog.Port{opcatalog.PortGraphQuery, opcatalog.PortGraphSearch}
	c.Ports[opcatalog.PortGraphSearch] = "a search service"
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckPortHonesty, "never used across the fixture suite")
}

// TestAX10_AMissingPortHandleIsReportedAsAHarnessGap keeps the harness's own
// setup failures distinguishable from a broken contribution.
func TestAX10_AMissingPortHandleIsReportedAsAHarnessGap(t *testing.T) {
	c := honestContribution()
	c.Ports = conformance.Ports{}
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckDeterminism, "")
	report := conformance.VerifyContribution(context.Background(), c)
	if !strings.Contains(report.String(), "no handle") {
		t.Errorf("the report does not say the handle was missing:\n%s", report)
	}
}

// TestAX10_AnOutOfRangeAPIIsRejected is AC-3's version-compatibility half.
func TestAX10_AnOutOfRangeAPIIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		api  extpack.APIRange
		want string
	}{
		{"above the host", extpack.APIRange{Min: "2.0", Max: "2.0"}, "this graphi speaks"},
		{"below the host", extpack.APIRange{Min: "0.1", Max: "0.9"}, "this graphi speaks"},
		{"undeclared", extpack.APIRange{}, "declares no api range"},
		{"inverted", extpack.APIRange{Min: "2.0", Max: "1.0"}, "is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := honestContribution()
			c.API = tc.api
			assertFails(t, conformance.VerifyContribution(context.Background(), c),
				conformance.CheckAPIVersion, tc.want)
		})
	}
}

// TestAX10_AWritePortIsOutsideTheReadOnlyEnvelope pins ADR 0013 I3.
func TestAX10_AWritePortIsOutsideTheReadOnlyEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name string
		port opcatalog.Port
	}{
		{"source write", opcatalog.PortSourceWrite},
		{"forge publish (egress)", opcatalog.PortForgePublish},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := honestContribution()
			c.Spec.Ports = []opcatalog.Port{tc.port}
			c.Spec.Permissions = opcatalog.PermissionsFor(c.Spec.Ports)
			c.Spec.Determinism = opcatalog.DeterminismEnvironmentDependent
			if tc.port == opcatalog.PortForgePublish {
				c.Spec.Determinism = opcatalog.DeterminismExternal
			}
			c.Ports = conformance.Ports{tc.port: "a handle"}
			c.Invoke = func(_ context.Context, host conformance.Host, _ json.RawMessage) ([]byte, error) {
				if _, err := host.Use(tc.port); err != nil {
					return nil, err
				}
				return []byte(`{"ok":true}`), nil
			}
			assertFails(t, conformance.VerifyContribution(context.Background(), c),
				conformance.CheckPermissions, "READ-ONLY")
		})
	}
}

// TestAX10_AnUnprojectableSpecFails covers the metadata contract.
func TestAX10_AnUnprojectableSpecFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*opcatalog.OperationSpec)
		want   string
	}{
		{"stored labs marker", func(s *opcatalog.OperationSpec) {
			s.Description = "[labs] " + s.Description
		}, "[labs] marker"},
		{"no input schema", func(s *opcatalog.OperationSpec) { s.InputSchema = nil }, "input schema"},
		{"schema is not an object", func(s *opcatalog.OperationSpec) {
			s.InputSchema = map[string]any{"type": "array", "properties": map[string]any{}}
		}, "want \"object\""},
		{"id is not a wire identifier", func(s *opcatalog.OperationSpec) { s.ID = "Example-Read" }, "wire identifier"},
		{"http resource is not a path", func(s *opcatalog.OperationSpec) { s.HTTPResource = "Analyze/Example Read" }, "HTTP resource"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := honestContribution()
			tc.mutate(&c.Spec)
			assertFails(t, conformance.VerifyContribution(context.Background(), c),
				conformance.CheckSurfaceMetadata, tc.want)
		})
	}
}

// TestAX10_AProjectorRenderingTheWrongOperationFails is the check that a
// structural comparison alone would miss: the right SHAPE for the wrong
// operation is exactly what a copy-paste in a dispatch table produces.
func TestAX10_AProjectorRenderingTheWrongOperationFails(t *testing.T) {
	c := honestContribution()
	c.Projectors = []conformance.Projector{{
		Surface: "mcp",
		Render: func(spec opcatalog.OperationSpec) (map[string]any, error) {
			return map[string]any{
				"name":        "some_other_operation",
				"description": "[labs] " + spec.Description,
				"inputSchema": spec.InputSchema,
			}, nil
		},
	}}
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckSurfaceProjection, "want \"example_read\"")
}

// TestAX10_AProjectorDroppingTheLabsMarkerFails: the tier is a projection, and a
// surface that forgets to apply it advertises a Labs operation as if it were not.
func TestAX10_AProjectorDroppingTheLabsMarkerFails(t *testing.T) {
	c := honestContribution()
	c.Projectors = []conformance.Projector{{
		Surface: "mcp",
		Render: func(spec opcatalog.OperationSpec) (map[string]any, error) {
			return map[string]any{
				"name": spec.ID, "description": spec.Description, "inputSchema": spec.InputSchema,
			}, nil
		},
	}}
	assertFails(t, conformance.VerifyContribution(context.Background(), c),
		conformance.CheckSurfaceProjection, "lacks the [labs] marker")
}

// TestAX10_AContributionWithoutFixturesOrHandlerFails: a spec with nothing
// behind it, and a handler with nothing to run, are both certifiable-looking and
// unproven.
func TestAX10_AContributionWithoutFixturesOrHandlerFails(t *testing.T) {
	t.Run("no handler", func(t *testing.T) {
		c := honestContribution()
		c.Invoke = nil
		assertFails(t, conformance.VerifyContribution(context.Background(), c),
			conformance.CheckSpec, "no handler")
	})
	t.Run("no fixtures", func(t *testing.T) {
		c := honestContribution()
		c.Fixtures = nil
		assertFails(t, conformance.VerifyContribution(context.Background(), c),
			conformance.CheckSpec, "declares no fixtures")
	})
	t.Run("unaudited ports", func(t *testing.T) {
		c := honestContribution()
		c.Spec.Ports = []opcatalog.Port{opcatalog.PortsUnaudited}
		c.Spec.Permissions = opcatalog.PermissionsFor(c.Spec.Ports)
		c.Spec.Determinism = opcatalog.DeterminismUnaudited
		assertFails(t, conformance.VerifyContribution(context.Background(), c),
			conformance.CheckSpec, "states its ports or it does not ship")
	})
}

// TestAX10_AnEmptyReportIsNotAPass closes the one outcome that would otherwise
// read as success while proving nothing.
func TestAX10_AnEmptyReportIsNotAPass(t *testing.T) {
	var empty conformance.Report
	if empty.OK() {
		t.Error("an empty report reports OK")
	}
	if err := empty.Err(); err == nil {
		t.Error("an empty report has no error; a subject that was never examined must not read as certified")
	}
}

// TestAX10_FixtureFailureClassesAreDeterministicToo: a fixture may declare that
// its outcome IS an error, and the harness holds that error to the same
// reproducibility bar as a success.
func TestAX10_FixtureFailureClassesAreDeterministicToo(t *testing.T) {
	t.Run("a stable error passes", func(t *testing.T) {
		c := honestContribution()
		c.Fixtures = append(c.Fixtures, conformance.Fixture{Name: "refused", Arguments: json.RawMessage(`{"limit":-1}`), WantError: true})
		c.Invoke = func(_ context.Context, host conformance.Host, args json.RawMessage) ([]byte, error) {
			if _, err := host.Use(opcatalog.PortGraphQuery); err != nil {
				return nil, err
			}
			if strings.Contains(string(args), "-1") {
				return nil, fmt.Errorf("example_read: limit must not be negative")
			}
			return []byte(`{"ok":true}`), nil
		}
		if err := conformance.VerifyContribution(context.Background(), c).Err(); err != nil {
			t.Fatalf("a deterministic failure class did not pass: %v", err)
		}
	})
	t.Run("an unstable error fails", func(t *testing.T) {
		var calls atomic.Int64
		c := honestContribution()
		c.Fixtures = []conformance.Fixture{{Name: "refused", WantError: true}}
		c.Invoke = func(_ context.Context, host conformance.Host, _ json.RawMessage) ([]byte, error) {
			if _, err := host.Use(opcatalog.PortGraphQuery); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("example_read: attempt %d failed", calls.Add(1))
		}
		assertFails(t, conformance.VerifyContribution(context.Background(), c),
			conformance.CheckDeterminism, "error text differs")
	})
}

// assertFails requires the report to fail, to fail on the named check, and for
// that failure's detail to contain want. Asserting the CHECK as well as the
// outcome is what stops a case from passing for an unrelated reason.
func assertFails(t *testing.T, report conformance.Report, check, want string) {
	t.Helper()
	if report.OK() {
		t.Fatalf("the report passed; it had to fail on %q\n%s", check, report)
	}
	for _, f := range report.Failures() {
		if f.Check != check {
			continue
		}
		if want != "" && !strings.Contains(f.Detail, want) {
			t.Fatalf("%q failed, but not for the stated reason:\n  detail: %s\n  want it to contain: %s",
				check, f.Detail, want)
		}
		return
	}
	t.Fatalf("the report failed, but not on %q:\n%s", check, report)
}

func ran(report conformance.Report, check string) bool {
	for _, r := range report.Results {
		if r.Check == check {
			return true
		}
	}
	return false
}
