package exthost

// AC-4/AC-5 — the example extension is judged by SW-230's contract-test harness.
//
// The point of running engine/extpack/conformance over a tier-C contribution is
// that the harness was designed for tier B, and whether it TRANSFERS is one of
// the questions the go/no-go has to answer. Two of its checks become materially
// stronger across a process boundary and one stops applying; all three findings
// are recorded in the decision document.
//
//   - determinism becomes a two-PROCESS comparison. The harness runs each
//     fixture twice; because Invoke starts a fresh extension each time, the two
//     runs are separate OS processes with separate address spaces. A tier-B
//     contribution can be accidentally deterministic by reusing a warm map; this
//     one cannot.
//   - port honesty becomes double-entry. The harness's gate records what the
//     handler asked for, and the HOST separately records what the subprocess
//     asked for (PortViolations). Two independent observers of the same fact.
//   - surface projection cannot run here. The real projections live in
//     surfaces/mcp and surfaces/http, above this package's layer rank, which is
//     exactly why the harness takes Projectors from the caller. A tier-C
//     contribution would have to be verified from a surfaces-rank test.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/extpack/conformance"
	"github.com/samibel/graphi/engine/opcatalog"
)

func TestSW231_AC4_ExampleExtensionPassesTheSW230ConformanceHarness(t *testing.T) {
	descriptor := stageExtension(t, descriptorOptions{})
	port := &searchPort{}

	report := conformance.VerifyContribution(context.Background(), conformance.Contribution{
		Spec: exampleSpec(),
		API:  extpack.APIRange{Min: "1.0", Max: "1.0"},
		Ports: conformance.Ports{
			// The handle a tier-C contribution receives IS a PortHandler: the
			// host cannot hand a subprocess a Go service value, so the seam it
			// authorises is the proxy function. That substitution is what makes
			// the harness usable across a process boundary at all.
			opcatalog.PortGraphSearch: PortHandler(port.handle),
		},
		Invoke: func(ctx context.Context, host conformance.Host, args json.RawMessage) ([]byte, error) {
			handle, err := host.Use(opcatalog.PortGraphSearch)
			if err != nil {
				return nil, err
			}
			handler, ok := handle.(PortHandler)
			if !ok {
				t.Fatalf("harness handed back %T, want exthost.PortHandler", handle)
			}
			ext, err := Start(ctx, Config{
				Activated:      true,
				DescriptorPath: descriptor,
				Ports:          map[opcatalog.Port]PortHandler{opcatalog.PortGraphSearch: handler},
			})
			if err != nil {
				return nil, err
			}
			defer func() { _ = ext.Close() }()
			res, err := ext.Call(ctx, exampleOperation, args)
			if err != nil {
				return nil, err
			}
			return res.CanonicalJSON()
		},
		Fixtures: []conformance.Fixture{
			{Name: "census of a symbol prefix", Arguments: json.RawMessage(`{"symbol":"Hel"}`)},
			{Name: "a blank symbol is a deterministic failure",
				Arguments: json.RawMessage(`{"symbol":"   "}`), WantError: true},
			{Name: "a missing symbol is a deterministic failure",
				Arguments: json.RawMessage(`{}`), WantError: true},
		},
	})

	if !report.OK() {
		t.Fatalf("the example extension does not conform:\n%s", report.String())
	}
	for _, want := range []string{
		conformance.CheckSpec, conformance.CheckPermissions, conformance.CheckAPIVersion,
		conformance.CheckSurfaceMetadata, conformance.CheckDeterminism, conformance.CheckPortHonesty,
	} {
		if !reportRan(report, want) {
			t.Errorf("check %q did not run; a passing report with a missing check is not evidence", want)
		}
	}
}

// AC-5 evidence, recorded as data rather than prose: the operation-id grammar
// the harness enforces has NO NAMESPACE.
//
// `wireIdentifier` accepts [a-z][a-z0-9_]* — no dot, no slash, no vendor prefix.
// So a third-party operation lands in the same flat namespace as graphi's own
// twelve, with nothing in the mechanism to stop two extensions from claiming the
// same id. The example above is therefore called `example_symbol_census` and not
// `example.symbol_census`, and this test pins WHY, in the SW-157 spirit: assert
// the known limitation as an assertion the suite makes, not as a sentence in a
// document that can drift away from the code.
func TestSW231_AC5_TierCOperationIdsHaveNoNamespace(t *testing.T) {
	spec := exampleSpec()
	spec.ID = "example.symbol_census"
	report := conformance.VerifyContribution(context.Background(), conformance.Contribution{
		Spec:     spec,
		API:      extpack.APIRange{Min: "1.0", Max: "1.0"},
		Ports:    conformance.Ports{opcatalog.PortGraphSearch: PortHandler((&searchPort{}).handle)},
		Invoke:   func(context.Context, conformance.Host, json.RawMessage) ([]byte, error) { return []byte(`{}`), nil },
		Fixtures: []conformance.Fixture{{Name: "unused"}},
	})
	if report.OK() {
		t.Fatal("a dotted operation id was accepted; if the grammar has grown a namespace, this " +
			"finding is stale and the decision document's id-collision risk should be revisited")
	}
	if !strings.Contains(report.String(), "wire identifier") {
		t.Fatalf("expected the surface-metadata check to reject the namespaced id:\n%s", report.String())
	}
}

// exampleSpec is the OperationSpec a tier-C author would have to write. It is
// the SAME type a tier-B module declares — which is the useful half of the
// answer: the catalog vocabulary transfers to the process tier unchanged.
func exampleSpec() opcatalog.OperationSpec {
	ports := []opcatalog.Port{opcatalog.PortGraphSearch}
	return opcatalog.OperationSpec{
		ID:      exampleOperation,
		Version: "1",
		Tier:    opcatalog.TierLabs,
		Advertisement: opcatalog.Advertisement{
			Description: "Count symbol-search matches per file path, computed by an external " +
				"process extension (trusted local code, not a sandbox).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Symbol name or prefix to census.",
					},
				},
				"required": []any{"symbol"},
			},
		},
		Ports:       ports,
		Permissions: opcatalog.PermissionsFor(ports),
		Determinism: opcatalog.DeterminismDeterministic,
		PortsEvidence: "SW-231 spike: extensions/example-analyzer/main.go issues exactly one " +
			"port_call, for graph.search; engine/exthost/host.go refuses any other port with " +
			"registry.ErrMissingDependency and records it in Extension.PortViolations.",
	}
}

// reportRan reports whether a named check appears in the report at all.
func reportRan(r conformance.Report, check string) bool {
	for _, res := range r.Results {
		if res.Check == check {
			return true
		}
	}
	return false
}
