package conformance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/samibel/graphi/engine/extpack"
	"github.com/samibel/graphi/engine/opcatalog"
)

// HostAPIVersion is the rule-pack/extension API version this build of graphi
// speaks. It is extpack's, not a second constant: a pack and a compiled
// contribution negotiate against the SAME host API, and two numbers that could
// drift apart would make "compatible" mean different things on the two tiers.
const HostAPIVersion = extpack.APIVersion

// Handler runs one operation contribution.
//
// It receives raw JSON arguments — the transport's shape, not a Go struct — and
// returns the operation's canonical result bytes. Returning bytes rather than a
// value is what makes the determinism check exact: the harness compares what a
// client would receive, not a struct that a serializer might still render two
// ways.
type Handler func(ctx context.Context, host Host, args json.RawMessage) ([]byte, error)

// Fixture is one recorded invocation.
//
// WantError declares that this fixture's outcome IS an error — a failure class,
// which a contribution has as much duty to produce deterministically as a
// success. Without the field the harness could not tell "this fixture proves the
// unavailable path" from "this contribution is broken".
type Fixture struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	WantError bool            `json:"want_error,omitempty"`
}

// Projection is one surface's rendered metadata for a spec.
type Projection struct {
	// Surface is the surface's name, e.g. "mcp" or "http".
	Surface string
	// Document is the rendered metadata.
	Document map[string]any
}

// Projector renders a spec into one surface's metadata.
//
// It is supplied by the CALLER rather than implemented here. The harness sits at
// engine rank and the real projections live in surfaces/mcp and surfaces/http;
// re-implementing them here would create the second projection site graphi's
// standards exist to prevent, and would then be verifying the copy. What the
// harness owns is the CONTRACT the rendered document must satisfy.
type Projector struct {
	Surface string
	Render  func(opcatalog.OperationSpec) (map[string]any, error)
}

// Contribution is one operation an extension author offers, with everything
// needed to prove it.
type Contribution struct {
	// Spec is the canonical statement of what the operation IS.
	Spec opcatalog.OperationSpec
	// API is the closed range of host API versions this contribution was written
	// for. The zero value declares nothing and is rejected: a contribution that
	// never stated which host it targets cannot be told it is on the wrong one.
	API extpack.APIRange
	// Ports maps each declared port to the handle the host would provide.
	Ports Ports
	// Invoke runs the operation.
	Invoke Handler
	// Fixtures are the recorded invocations the determinism and port checks run
	// over. At least one is required.
	Fixtures []Fixture
	// Projectors are the surface renderers to verify against. Optional: the
	// metadata CONTRACT (CheckSurfaceMetadata) always runs, and supplying a
	// projector additionally proves a real surface accepts the spec.
	Projectors []Projector
}

// readOnlyPermissions is the envelope ADR 0013 fixes for V1 extensions: they
// read the graph and the working tree through host ports, and they write
// nothing and leave the machine never (I2, I3).
var readOnlyPermissions = map[opcatalog.Permission]bool{
	opcatalog.PermissionGraphRead:     true,
	opcatalog.PermissionSourceRead:    true,
	opcatalog.PermissionHistoryRead:   true,
	opcatalog.PermissionStateRead:     true,
	opcatalog.PermissionSourceWrite:   false,
	opcatalog.PermissionStateWrite:    false,
	opcatalog.PermissionNetworkEgress: false,
}

// VerifyContribution runs every contract check over one operation contribution
// and returns the report.
//
// It never returns an error separately from the report: a setup problem is a
// FAILED CHECK, not an exception, because a caller that had to handle both would
// eventually handle one of them by ignoring it.
func VerifyContribution(ctx context.Context, c Contribution) Report {
	rec := &recorder{subject: contributionSubject(c)}

	specOK := rec.record(CheckSpec, specDetail(c.Spec), checkSpec(c))
	rec.record(CheckPermissions, permissionDetail(c.Spec), checkPermissions(c.Spec))
	rec.record(CheckAPIVersion,
		fmt.Sprintf("declares api %s..%s; this host speaks %s", c.API.Min, c.API.Max, HostAPIVersion),
		checkAPIVersion(c.API))
	rec.record(CheckSurfaceMetadata, "renderable as MCP and HTTP metadata", checkSurfaceMetadata(c.Spec))
	rec.record(CheckSurfaceProjection, projectionDetail(c.Projectors), checkProjections(c.Spec, c.Projectors))

	if !specOK {
		// Running fixtures against a spec that does not validate would report
		// consequences of a defect the author already has to fix, and would do it
		// in the two checks whose failure is most alarming. Say so instead.
		rec.fail(CheckDeterminism, "not run: the spec did not validate")
		rec.fail(CheckPortHonesty, "not run: the spec did not validate")
		return rec.report()
	}
	runs, err := runFixtures(ctx, c)
	if err != nil {
		rec.fail(CheckDeterminism, "%v", err)
		rec.fail(CheckPortHonesty, "not run: the fixture suite could not be executed")
		return rec.report()
	}
	rec.record(CheckDeterminism,
		fmt.Sprintf("%d fixture(s), 2 runs each, byte-identical", len(c.Fixtures)),
		runs.determinism())
	rec.record(CheckPortHonesty,
		fmt.Sprintf("used exactly the declared port(s): %s", portList(c.Spec.Ports)),
		runs.observations.verdict(c.Spec.Ports))
	return rec.report()
}

func contributionSubject(c Contribution) string {
	if c.Spec.ID == "" {
		return "contribution (no operation id)"
	}
	return "operation " + c.Spec.ID
}

func specDetail(spec opcatalog.OperationSpec) string {
	return fmt.Sprintf("%s@%s, tier %s, determinism %s, ports [%s]",
		spec.ID, spec.Version, spec.Tier, spec.Determinism, portList(spec.Ports))
}

func permissionDetail(spec opcatalog.OperationSpec) string {
	perms := opcatalog.PermissionsFor(spec.Ports)
	names := make([]string, 0, len(perms))
	for _, p := range perms {
		names = append(names, string(p))
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "read-only: the port set implies no permission at all"
	}
	return "read-only: " + strings.Join(names, ", ")
}

func projectionDetail(projectors []Projector) string {
	if len(projectors) == 0 {
		return "no projector supplied; the metadata contract was checked instead"
	}
	names := make([]string, 0, len(projectors))
	for _, p := range projectors {
		names = append(names, p.Surface)
	}
	sort.Strings(names)
	return "rendered by: " + strings.Join(names, ", ")
}

// checkSpec validates the spec itself plus the two obligations a CONTRIBUTION
// has that a first-party catalog entry does not.
func checkSpec(c Contribution) error {
	if c.Invoke == nil {
		return fmt.Errorf("%s: the contribution has no handler; a spec with nothing behind it is a "+
			"descriptor, not a contribution", registryName)
	}
	if len(c.Fixtures) == 0 {
		return fmt.Errorf("%s: the contribution declares no fixtures; determinism and port honesty "+
			"cannot be observed on an operation that is never run", registryName)
	}
	seen := map[string]bool{}
	for i, f := range c.Fixtures {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("%s: fixture %d has no name", registryName, i)
		}
		if seen[f.Name] {
			return fmt.Errorf("%s: fixture %q is declared twice", registryName, f.Name)
		}
		seen[f.Name] = true
	}
	if err := c.Spec.Validate(); err != nil {
		return err
	}
	if !c.Spec.PortsAudited() {
		return fmt.Errorf("%s: %q declares %q; a contribution states its ports or it does not ship — "+
			"the marker exists for first-party entries whose seam has not been read yet",
			registryName, c.Spec.ID, opcatalog.PortsUnaudited)
	}
	if c.Spec.Determinism == opcatalog.DeterminismUnaudited {
		return fmt.Errorf("%s: %q declares determinism %q; the harness measures determinism, so a "+
			"contribution must claim a class it can be held to", registryName, c.Spec.ID, opcatalog.DeterminismUnaudited)
	}
	return nil
}

// checkPermissions holds a contribution inside the read-only envelope.
func checkPermissions(spec opcatalog.OperationSpec) error {
	for _, perm := range opcatalog.PermissionsFor(spec.Ports) {
		allowed, known := readOnlyPermissions[perm]
		if !known {
			return fmt.Errorf("%s: %q implies permission %q, which is outside the vocabulary this "+
				"harness can reason about", registryName, spec.ID, perm)
		}
		if !allowed {
			return fmt.Errorf("%s: %q implies permission %q; V1 extension capability is READ-ONLY "+
				"(ADR 0013 I2/I3: no writes, no egress) and a port implying it may not be declared",
				registryName, spec.ID, perm)
		}
	}
	return nil
}

// checkAPIVersion refuses a contribution written for a host API this build does
// not speak.
func checkAPIVersion(r extpack.APIRange) error {
	if r.Min == "" || r.Max == "" {
		return fmt.Errorf("%s: the contribution declares no api range; an unstated target host API "+
			"cannot be found incompatible, which is the same as not checking", registryName)
	}
	return r.Validate()
}

// wireIdentifier is the grammar an advertised operation id has to obey. It is
// the shape every id in the live catalog already has; stating it lets the
// harness refuse one that would need quoting on a wire.
func wireIdentifier(id string) bool {
	if id == "" {
		return false
	}
	if id[0] < 'a' || id[0] > 'z' {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_':
		default:
			return false
		}
	}
	return true
}

// checkSurfaceMetadata is the projectability contract: everything a surface
// projection reads out of a spec, checked here so a failure names the missing
// field instead of surfacing as a panic inside a projector.
func checkSurfaceMetadata(spec opcatalog.OperationSpec) error {
	if !wireIdentifier(spec.ID) {
		return fmt.Errorf("%s: operation id %q is not a wire identifier ([a-z][a-z0-9_]*)",
			registryName, extpack.Bound(spec.ID))
	}
	if strings.TrimSpace(spec.Description) == "" {
		return fmt.Errorf("%s: %q has no description; every surface advertises one", registryName, spec.ID)
	}
	if strings.HasPrefix(spec.Description, "[labs] ") {
		return fmt.Errorf("%s: %q stores the [labs] marker in its description; the marker is a "+
			"projection of tier and is applied at advertisement time", registryName, spec.ID)
	}
	schema, ok := spec.InputSchema.(map[string]any)
	if !ok {
		return fmt.Errorf("%s: %q has no JSON-object input schema; an operation advertised without "+
			"one cannot have its arguments validated by a client", registryName, spec.ID)
	}
	if schema["type"] != "object" {
		return fmt.Errorf("%s: %q input schema declares type %v, want \"object\"", registryName, spec.ID, schema["type"])
	}
	if _, hasProps := schema["properties"].(map[string]any); !hasProps {
		return fmt.Errorf("%s: %q input schema has no `properties` object", registryName, spec.ID)
	}
	if spec.Annotations != nil {
		if _, ok := spec.Annotations.(map[string]any); !ok {
			return fmt.Errorf("%s: %q annotations are not a JSON object", registryName, spec.ID)
		}
	}
	if spec.HTTPResource != "" && !httpResource(spec.HTTPResource) {
		return fmt.Errorf("%s: %q declares HTTP resource %q, which is not a lowercase slash-separated "+
			"path", registryName, spec.ID, extpack.Bound(spec.HTTPResource))
	}
	return nil
}

func httpResource(resource string) bool {
	for _, segment := range strings.Split(resource, "/") {
		if segment == "" {
			return false
		}
		for i := 0; i < len(segment); i++ {
			c := segment[i]
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
			default:
				return false
			}
		}
	}
	return true
}

// checkProjections renders the spec through every supplied projector and
// validates the result.
func checkProjections(spec opcatalog.OperationSpec, projectors []Projector) error {
	for _, p := range projectors {
		if p.Render == nil {
			return fmt.Errorf("%s: projector %q has no Render function", registryName, p.Surface)
		}
		doc, err := p.Render(spec)
		if err != nil {
			return fmt.Errorf("%s: surface %q refused to render %q: %w", registryName, p.Surface, spec.ID, err)
		}
		if len(doc) == 0 {
			return fmt.Errorf("%s: surface %q rendered %q as an empty document", registryName, p.Surface, spec.ID)
		}
		if err := validateProjection(spec, p.Surface, doc); err != nil {
			return err
		}
	}
	return nil
}

// validateProjection checks a rendered document against the spec it came from.
//
// The identity checks matter more than the shape ones: a projector that rendered
// the RIGHT SHAPE for the WRONG OPERATION would satisfy any structural check,
// and is exactly what a copy-paste in a dispatch table produces.
func validateProjection(spec opcatalog.OperationSpec, surface string, doc map[string]any) error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("%s: surface %q projection of %q: "+format,
			append([]any{registryName, surface, spec.ID}, a...)...)
	}
	switch surface {
	case "mcp":
		if doc["name"] != spec.ID {
			return fail("name is %v, want %q", doc["name"], spec.ID)
		}
		description, _ := doc["description"].(string)
		if strings.TrimSpace(description) == "" {
			return fail("no description")
		}
		wantMarker := spec.Tier != opcatalog.TierStable
		if hasMarker := strings.HasPrefix(description, "[labs] "); hasMarker != wantMarker {
			return fail("tier is %q but the description %s the [labs] marker",
				spec.Tier, markerVerb(hasMarker))
		}
		schema, ok := doc["inputSchema"].(map[string]any)
		if !ok {
			return fail("no inputSchema object")
		}
		if schema["type"] != "object" {
			return fail("inputSchema type is %v, want \"object\"", schema["type"])
		}
	case "http":
		resource, _ := doc["resource"].(string)
		if resource == "" {
			return fail("no resource")
		}
		if spec.HTTPResource != "" && resource != spec.HTTPResource {
			return fail("resource is %q, but the spec declares %q", resource, spec.HTTPResource)
		}
		labs, ok := doc["labs"].(bool)
		if !ok {
			return fail("no `labs` boolean")
		}
		if want := spec.Tier != opcatalog.TierStable; labs != want {
			return fail("labs is %t but the spec is tier %q", labs, spec.Tier)
		}
	default:
		return fail("unknown surface; the harness validates %q and %q", "mcp", "http")
	}
	return nil
}

func markerVerb(has bool) string {
	if has {
		return "carries"
	}
	return "lacks"
}

// fixtureRuns holds the observations of one whole fixture suite.
type fixtureRuns struct {
	observations *portObservations
	divergent    []string
}

func (r fixtureRuns) determinism() error {
	if len(r.divergent) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %d fixture(s) produced different results on two identical runs: %s",
		registryName, len(r.divergent), strings.Join(r.divergent, "; "))
}

// runFixtures invokes every fixture TWICE behind a fresh gate each time.
//
// Fresh gates matter: a handler that memoised its first port lookup would look
// port-honest on the second run for the wrong reason, and the harness would be
// measuring a cache.
func runFixtures(ctx context.Context, c Contribution) (fixtureRuns, error) {
	runs := fixtureRuns{observations: newObservations()}
	for _, f := range c.Fixtures {
		firstBytes, firstErr, err := runOnce(ctx, c, f, runs.observations)
		if err != nil {
			return runs, err
		}
		secondBytes, secondErr, err := runOnce(ctx, c, f, runs.observations)
		if err != nil {
			return runs, err
		}
		if diff := compareOutcomes(f, firstBytes, firstErr, secondBytes, secondErr); diff != "" {
			runs.divergent = append(runs.divergent, diff)
		}
	}
	return runs, nil
}

func runOnce(ctx context.Context, c Contribution, f Fixture, obs *portObservations) ([]byte, error, error) {
	g := newGate(c.Spec.Ports, c.Ports)
	body, invokeErr := c.Invoke(ctx, g, f.Arguments)
	obs.absorb(g)
	switch {
	case f.WantError && invokeErr == nil:
		return nil, nil, fmt.Errorf("%s: fixture %q declares WantError but the handler succeeded",
			registryName, f.Name)
	case !f.WantError && invokeErr != nil:
		return nil, nil, fmt.Errorf("%s: fixture %q failed: %v", registryName, f.Name, invokeErr)
	case !f.WantError && len(body) == 0:
		return nil, nil, fmt.Errorf("%s: fixture %q produced no bytes and no error; a case with no "+
			"observable outcome proves nothing", registryName, f.Name)
	}
	return body, invokeErr, nil
}

// compareOutcomes compares bytes AND error text, in that order of strictness.
// Comparing bytes alone would let a run that failed where the other succeeded
// pass as "both empty".
func compareOutcomes(f Fixture, aBytes []byte, aErr error, bBytes []byte, bErr error) string {
	switch {
	case (aErr == nil) != (bErr == nil):
		return fmt.Sprintf("fixture %q: one run failed and the other did not", f.Name)
	case aErr != nil && bErr != nil && aErr.Error() != bErr.Error():
		return fmt.Sprintf("fixture %q: error text differs (%v vs %v)", f.Name, aErr, bErr)
	case !bytes.Equal(aBytes, bBytes):
		return fmt.Sprintf("fixture %q: %d bytes vs %d bytes", f.Name, len(aBytes), len(bBytes))
	}
	return ""
}
