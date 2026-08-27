package client

// SW-226 (AX-06) — the dead_code CANARY: one Labs operation whose surface
// dispatch reaches the AX-04 executor, behind a three-position kill switch.
//
// # Why dead_code and not one of the other three candidates
//
// The plan named four candidates from the live Labs registry. The canary
// criteria are: read-only, no network or edit side effects, deterministic
// output, good fixtures, no Stable contract. dead_code is the only one that
// satisfies all five without an argument:
//
//   - hotspots is REJECTED on determinism. Its catalog spec says
//     determinism: "environment-dependent" and permissions
//     ["graph.read","history.read"] — it shells out to a bounded local `git log`.
//     A canary whose two paths can legitimately disagree because the repository's
//     history changed underneath them cannot prove byte parity; it can only
//     produce arguments about whether a diff was real.
//   - framework_map is REJECTED on fixture strength. It is deterministic and
//     read-only, but the shared Go fixture records no framework annotations, so
//     the only outcome the fixture suite can compare is its honest EMPTY answer.
//     Byte parity on an empty envelope is exactly the "two empty results look
//     like agreement" failure the AX-04 parity tests were written to avoid.
//   - repo_overview is REJECTED as the more expensive of two otherwise equal
//     options. It is deterministic and well-fixtured, but it carries the
//     `communities` opt-in — a full-graph Louvain pass that is the tool's only
//     non-aggregate read — so a dual-run doubles the heaviest analysis in the
//     Labs set. It is the right SECOND canary, and SW-228 can take it cheaply
//     once this seam is proven.
//   - dead_code is CHOSEN: catalog tier labs, determinism "deterministic",
//     permissions ["graph.read"] only, ports [graph.query, graph.search], one
//     integer argument, and three fixture outcomes that already exist and are
//     already asserted byte-stable — the populated answer
//     (surfaces/agentintel_golden_test.go), the typed `unavailable` outcome when
//     the graph dependencies are absent, and the typed `empty` outcome on an
//     empty graph. Its surfaces take exactly one argument on all three
//     transports, so the argument-fidelity evidence is real rather than
//     structural.
//
// Two operations were excluded before the candidate list was even considered,
// and the reasons are recorded so they are not rediscovered:
//
//   - agent_brief could not be a canary at all. Client.Brief returns TWO byte
//     slices and the executor transports only the canonical one; migrating it
//     would silently drop the Markdown rendering the MCP surface concatenates
//     today (see BriefArgs in executor_adapters.go).
//   - memory and search_semantic have no argument-fidelity evidence in the AX-04
//     fixture: with no memory store wired, Direct.Memory short-circuits before it
//     reads its arguments, so a parity pass there proves the sentinel and not the
//     arguments. That gap is filed in the backlog and must close before either
//     migrates.
//
// # What the three kill-switch positions mean
//
//	legacy   Only Client.DeadCode runs. Byte-for-byte the AX-00 behaviour, and
//	         the position to move to if anything at all goes wrong.
//	shadow   BOTH run. The caller receives the LEGACY result; the executor result
//	         is compared against it and a divergence is RECORDED. This is the
//	         shipped default.
//	active   Only the executor runs, and its result is what the caller receives.
//
// Rolling back is a value change and nothing else: no schema, no persisted
// state, no cached artifact and no wire identifier is keyed on the position, so
// `active` → `legacy` is complete the moment the next call starts (AC-4).
//
// # Why shadow is the shipped default
//
// `legacy` would ship a canary that never flies: the new path would be exercised
// only inside the test binary, and AX-06's whole purpose is to learn whether the
// seam holds on real calls. `active` would make the executor the source of truth
// on day one, which is precisely the ordering the strangler design forbids — the
// legacy path stays authoritative until parity is proven, not until it is
// implemented. `shadow` is the only position where the executor runs on every
// real call AND cannot change a single byte the caller sees, because the bytes
// the caller receives are literally the legacy method's return value.
//
// Its cost is stated rather than hidden: in shadow, dead_code runs TWICE. That is
// the deliberate price of the evidence, it applies to one Labs operation, and it
// is one constant away from zero.
//
// # Why a shadow mismatch does not fail the caller's request
//
// A mismatch in shadow mode means the NEW path is wrong. The caller already holds
// the legacy answer, which is the correct one. Failing their request because an
// experiment disagreed would convert a bug in unreleased plumbing into a
// user-visible outage — strictly worse than the status quo the canary is measured
// against.
//
// "Never swallowed" (AC-2) is honoured a different way, and a checkable one: the
// divergence is recorded in-process, the fixture suite asserts the recorder is
// empty after every canary case, and canary_test.go proves in BOTH directions
// that the recorder fires when the two paths really do differ. A comparison that
// cannot fail is the thing this design had to avoid, so it is tested against a
// deliberately broken executor rather than asserted in a comment.
//
// Recording is a counter and a value. No file, no log line, no network: the
// zero-egress posture is a hard invariant and observability may not be the
// exception to it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/samibel/graphi/core/registry"
	"github.com/samibel/graphi/engine/opcatalog"
)

// canaryRegistry is the short name the typed lifecycle errors carry.
const canaryRegistry = "canary"

// CanaryOperation is the one operation AX-06 moves onto the executor path.
// SW-228 migrates the rest; until then this constant is the whole allow list,
// and DispatchCanary rejects anything else by name.
const CanaryOperation = "dead_code"

// CanaryMode is the kill switch position for the canary operation.
type CanaryMode string

const (
	// CanaryModeLegacy runs only the legacy Client method.
	CanaryModeLegacy CanaryMode = "legacy"
	// CanaryModeShadow runs both paths, compares them, and returns the LEGACY
	// result.
	CanaryModeShadow CanaryMode = "shadow"
	// CanaryModeActive runs only the executor path and returns ITS result.
	CanaryModeActive CanaryMode = "active"
)

// Valid reports whether m is one of the three declared positions. There is no
// fourth position and no empty default: an unrecognised value is rejected at the
// point it is set, so dispatch never has to guess what an operator meant.
func (m CanaryMode) Valid() bool {
	switch m {
	case CanaryModeLegacy, CanaryModeShadow, CanaryModeActive:
		return true
	}
	return false
}

// CanaryModes returns the three positions in their escalation order. It exists
// so tests and `graphi doctor`-style callers enumerate the switch instead of
// re-listing it.
func CanaryModes() []CanaryMode {
	return []CanaryMode{CanaryModeLegacy, CanaryModeShadow, CanaryModeActive}
}

// canaryModeDefault is the compiled-in position of record — the one a release
// ships with, changed in a diff like every other behaviour change.
const canaryModeDefault = CanaryModeShadow

// canaryModeSelected holds an override installed by the composition root
// (cmd/internal/runtime, from GRAPHI_CANARY_DEAD_CODE). It is atomic because the
// override is installed once at startup while requests may already be in flight
// on other goroutines; an unsynchronised package var would be a data race the
// race detector would find on the first MCP session test.
var canaryModeSelected atomic.Value // CanaryMode

// CanaryModeSetting returns the position dispatch will use.
func CanaryModeSetting() CanaryMode {
	if m, ok := canaryModeSelected.Load().(CanaryMode); ok && m.Valid() {
		return m
	}
	return canaryModeDefault
}

// SetCanaryMode installs a kill-switch position. An unrecognised position is
// REJECTED rather than falling back to a default: a typo in an operator's
// environment must not quietly select a behaviour they did not ask for.
func SetCanaryMode(m CanaryMode) error {
	if !m.Valid() {
		return canaryModeError(string(m))
	}
	canaryModeSelected.Store(m)
	return nil
}

// ParseCanaryMode turns an operator-supplied string into a position, failing
// closed on anything else.
func ParseCanaryMode(s string) (CanaryMode, error) {
	m := CanaryMode(s)
	if !m.Valid() {
		return "", canaryModeError(s)
	}
	return m, nil
}

// canaryModeError is the one rejection message for an unrecognised position, so
// the environment-variable path and the programmatic path cannot describe the
// same mistake differently. It is a plain error, not a registry lifecycle error:
// core/registry's four kinds describe registration failures (duplicate,
// unsupported override, missing dependency, frozen), and "this string is not one
// of three positions" is none of them. Inventing a fifth kind to make it fit
// would be the parallel error vocabulary SW-222 exists to prevent.
func canaryModeError(s string) error {
	return fmt.Errorf("%s: %q is not a kill-switch position (want %q, %q or %q)",
		canaryRegistry, s, CanaryModeLegacy, CanaryModeShadow, CanaryModeActive)
}

// Contribution is one operation's registration into the new path: the catalog
// SPEC that gives it identity, the executor HANDLER that runs it, and the PORTS
// the spec declares it needs. AC-1 asks for that triple to be a registered
// thing rather than three facts that happen to line up, so resolving it is a
// checked operation with typed failures.
//
// It is intentionally a VALUE derived from the catalog and the adapter table,
// not a second place where an operation is described. Nothing here can be
// declared; everything is looked up, and a lookup that comes back empty is an
// error.
type Contribution struct {
	// Operation is the catalog id.
	Operation string
	// Version is the contract version the catalog declares.
	Version string
	// Tier is the catalog's stability tier.
	Tier opcatalog.Tier
	// Ports are the host ports the spec requires.
	Ports []opcatalog.Port
	// Permissions are the permissions those ports imply.
	Permissions []opcatalog.Permission
	// Determinism is the spec's declared determinism class.
	Determinism opcatalog.Determinism
}

// CanaryContribution resolves the canary's contribution against the frozen
// shadow catalog and the executor's adapter table.
//
// Five things must hold, and each is a way the triple could be incomplete
// without anyone noticing:
//
//  1. the catalog declares the operation (spec exists);
//  2. the executor has a handler for it (handler exists);
//  3. the spec declares at least one port, and every port is one the catalog
//     vocabulary knows (required ports exist and are real);
//  4. the tier is LABS — a Stable canary is forbidden by this story's scope and
//     by ADR 0013 I1, and a tier change that made it Stable must break the build
//     here rather than be discovered on the wire;
//  5. the operation is read-only and deterministic — the canary criteria
//     themselves, checked against the spec instead of remembered from a plan.
func CanaryContribution(c Client) (Contribution, error) {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		return Contribution{}, fmt.Errorf("client: canary needs the operation catalog: %w", err)
	}
	return canaryContribution(c, catalog)
}

// canaryContribution is CanaryContribution over an explicit catalog. It exists
// so canary_test.go can feed it a catalog that violates each of the five
// conditions in turn — a check that has never been shown to fail is not a check.
func canaryContribution(c Client, catalog *opcatalog.Catalog) (Contribution, error) {
	spec, ok := catalog.Lookup(CanaryOperation)
	if !ok {
		return Contribution{}, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Contribution", CanaryOperation,
			"%s: the operation catalog does not declare %q", canaryRegistry, CanaryOperation)
	}
	executor, err := NewExecutorWithCatalog(c, catalog)
	if err != nil {
		return Contribution{}, err
	}
	if _, handled := executor.adapters[CanaryOperation]; !handled {
		return Contribution{}, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Contribution", CanaryOperation,
			"%s: %q has a catalog spec but no executor handler", canaryRegistry, CanaryOperation)
	}
	if err := canaryCriteria(spec); err != nil {
		return Contribution{}, err
	}
	return Contribution{
		Operation:   spec.ID,
		Version:     spec.Version,
		Tier:        spec.Tier,
		Ports:       append([]opcatalog.Port(nil), spec.Ports...),
		Permissions: opcatalog.PermissionsFor(spec.Ports),
		Determinism: spec.Determinism,
	}, nil
}

// canaryCriteria checks a spec against the canary criteria themselves: declared
// real ports, Labs tier, deterministic output, and read-only permissions.
//
// The permission clause is defence in depth rather than a reachable branch
// today. OperationSpec.Validate already refuses to pair a non-graph-read port
// with determinism "deterministic" (every such port is in
// opcatalog's nonDeterministicPorts), so a spec that reaches here through
// Catalog.Add cannot fail the permission check without failing the determinism
// one first. It is written and tested anyway because Catalog.Validate exists for
// precisely the case its own doc names — "a catalog assembled by some other
// route (a future rule pack, a merged module set)" — and a canary criterion that
// depends on another package's validation order for its enforcement is not
// enforced, it is fortunate.
func canaryCriteria(spec opcatalog.OperationSpec) error {
	if len(spec.Ports) == 0 {
		return registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Contribution", spec.ID,
			"%s: %q declares no required ports", canaryRegistry, spec.ID)
	}
	for _, port := range spec.Ports {
		if !opcatalog.ValidPort(port) {
			return fmt.Errorf("%s: %q requires unknown port %q", canaryRegistry, spec.ID, string(port))
		}
	}
	if spec.Tier != opcatalog.TierLabs {
		return fmt.Errorf("%s: %q is tier %q — no Stable operation may be the canary (ADR 0013 I1)",
			canaryRegistry, spec.ID, string(spec.Tier))
	}
	for _, permission := range opcatalog.PermissionsFor(spec.Ports) {
		if permission != opcatalog.PermissionGraphRead {
			return fmt.Errorf("%s: %q requires permission %q — the canary must be read-only "+
				"with no network or edit side effects", canaryRegistry, spec.ID, string(permission))
		}
	}
	if spec.Determinism != opcatalog.DeterminismDeterministic {
		return fmt.Errorf("%s: %q declares determinism %q — a canary whose two paths may "+
			"legitimately disagree cannot prove byte parity",
			canaryRegistry, spec.ID, string(spec.Determinism))
	}
	return nil
}

// CanaryMismatch is one recorded divergence between the two paths.
//
// Legacy and Executor hold RENDERED values (an error string, or a byte length
// and a bounded prefix) rather than the raw results: a mismatch record is
// diagnostic, and holding whole result documents in a process-global would turn
// a comparison into a memory leak.
type CanaryMismatch struct {
	// Operation is the canary operation id.
	Operation string
	// Kind is what disagreed: "error-presence", "error", "bytes", or
	// "executor-unavailable" (the executor path could not be constructed at all).
	Kind string
	// Legacy renders what the legacy path produced.
	Legacy string
	// Executor renders what the executor path produced.
	Executor string
}

// String renders the mismatch for a test failure message.
func (m CanaryMismatch) String() string {
	return fmt.Sprintf("%s canary %s mismatch:\n  legacy:   %s\n  executor: %s",
		m.Operation, m.Kind, m.Legacy, m.Executor)
}

var (
	canaryMismatchMu    sync.Mutex
	canaryMismatchCount int
	canaryMismatchLast  CanaryMismatch
)

// recordCanaryMismatch stores a divergence. It performs no I/O by design — see
// the package comment above: observability may not become the exception to the
// zero-egress posture.
func recordCanaryMismatch(m CanaryMismatch) {
	canaryMismatchMu.Lock()
	defer canaryMismatchMu.Unlock()
	canaryMismatchCount++
	canaryMismatchLast = m
}

// CanaryMismatches returns how many divergences have been recorded in this
// process and the most recent one. The fixture suite asserts the count is zero
// after every canary case; canary_test.go asserts it is NOT zero when the paths
// genuinely differ, so the check is non-vacuous in both directions.
func CanaryMismatches() (int, CanaryMismatch) {
	canaryMismatchMu.Lock()
	defer canaryMismatchMu.Unlock()
	return canaryMismatchCount, canaryMismatchLast
}

// ResetCanaryMismatches clears the recorder. It exists for tests, which need a
// per-case zero point, and it is exported because the fixture suite that asserts
// on it lives in surfaces_test rather than in this package.
func ResetCanaryMismatches() {
	canaryMismatchMu.Lock()
	defer canaryMismatchMu.Unlock()
	canaryMismatchCount = 0
	canaryMismatchLast = CanaryMismatch{}
}

// canaryComparableSentinels are the typed sentinels an error on this path may
// carry, across every Client implementation the canary can sit behind: Direct's
// capability sentinels, the daemon client's ErrAgentIntelUnavailable, and the
// HTTP client's transport classes.
//
// Comparing error TEXT alone would let a path that returns a differently-typed
// error with the same message pass as identical — and the type is what surfaces
// branch on (errors.Is), so the CLASS is compared too. The list is deliberately
// wider than the errors dead_code can produce today: a sentinel that never fires
// costs one pointer comparison, and one that fires unlisted would be a silent
// class change.
var canaryComparableSentinels = []error{
	ErrAgentIntelUnavailable,
	ErrAgentToolsUnavailable,
	ErrAnalysisUnavailable,
	ErrSearchUnavailable,
	ErrSavingsUnavailable,
	ErrMemoryUnavailable,
	ErrUnavailable,
	ErrUnreachable,
	ErrSchemaMismatch,
}

// DispatchCanary runs the canary operation in whichever position the kill switch
// selects and returns what the CALLER should receive.
//
// It is the single composition for the canary's dispatch, in the surfaces/client
// package where graphi puts a capability that two surfaces share, so MCP and
// HTTP cannot end up in different kill-switch positions or comparing different
// things (standards: one composition per capability, not one per surface).
//
// args must be the canary's arguments. Anything else is rejected by name rather
// than silently executed: this is a one-operation seam, and letting a second
// operation through it would make the bulk migration (SW-228) happen by accident.
func DispatchCanary(ctx context.Context, c Client, args Arguments) ([]byte, error) {
	if args == nil {
		return nil, fmt.Errorf("client: canary: nil arguments")
	}
	if args.Operation() != CanaryOperation {
		return nil, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Dispatch", args.Operation(),
			"%s: %q is not the AX-06 canary operation (%q); migrating further operations is SW-228",
			canaryRegistry, args.Operation(), CanaryOperation)
	}

	mode := CanaryModeSetting()
	if mode == CanaryModeLegacy {
		// Byte-for-byte the pre-AX-06 call. invoke IS the legacy Client method
		// with no wrapping, so this position adds nothing at all — not even an
		// error check — to the path it replaces.
		return args.invoke(ctx, c)
	}

	executor, err := NewExecutor(c)
	if err != nil {
		if mode == CanaryModeActive {
			// The selected path could not be built. Fail closed rather than
			// quietly serving the other one: an `active` canary that silently
			// answers from legacy is a kill switch that lies about its position.
			return nil, err
		}
		recordCanaryMismatch(CanaryMismatch{
			Operation: CanaryOperation,
			Kind:      "executor-unavailable",
			Legacy:    "(ran)",
			Executor:  err.Error(),
		})
		return args.invoke(ctx, c)
	}

	if mode == CanaryModeActive {
		return executeCanary(ctx, executor, args)
	}

	// Shadow: legacy first, so the value the caller receives is decided before
	// the experiment runs and cannot be influenced by it.
	legacyBytes, legacyErr := args.invoke(ctx, c)
	executorBytes, executorErr := executeCanary(ctx, executor, args)
	if mismatch, differs := compareCanaryOutcomes(legacyBytes, legacyErr, executorBytes, executorErr); differs {
		recordCanaryMismatch(mismatch)
	}
	return legacyBytes, legacyErr
}

// executeCanary drives one request through the executor: catalog lookup,
// argument marshal, typed decode, adapter call.
func executeCanary(ctx context.Context, executor *Executor, args Arguments) ([]byte, error) {
	req, err := executor.NewRequest(args)
	if err != nil {
		return nil, err
	}
	return executor.Execute(ctx, req)
}

// compareCanaryOutcomes is the dual-run comparison: canonical bytes AND error
// outcome, in that order of strictness.
//
// Error comparison is deliberately three-part. Presence first (one path failing
// where the other succeeded is the worst kind of divergence and must not be
// reported as a byte difference); then the message, which is what a user sees;
// then the CLASS, via errors.Is against the declared sentinels, which is what a
// surface branches on. Two errors with the same text but different types would
// pass a text-only comparison and still change how a surface behaves.
func compareCanaryOutcomes(legacyBytes []byte, legacyErr error, executorBytes []byte, executorErr error) (CanaryMismatch, bool) {
	mismatch := func(kind, legacy, executor string) (CanaryMismatch, bool) {
		return CanaryMismatch{Operation: CanaryOperation, Kind: kind, Legacy: legacy, Executor: executor}, true
	}
	switch {
	case legacyErr == nil && executorErr != nil:
		return mismatch("error-presence", "ok", executorErr.Error())
	case legacyErr != nil && executorErr == nil:
		return mismatch("error-presence", legacyErr.Error(), "ok")
	case legacyErr != nil && executorErr != nil:
		if legacyErr.Error() != executorErr.Error() {
			return mismatch("error", legacyErr.Error(), executorErr.Error())
		}
		for _, sentinel := range canaryComparableSentinels {
			if errors.Is(legacyErr, sentinel) != errors.Is(executorErr, sentinel) {
				return mismatch("error",
					fmt.Sprintf("%v (is %v: %t)", legacyErr, sentinel, errors.Is(legacyErr, sentinel)),
					fmt.Sprintf("%v (is %v: %t)", executorErr, sentinel, errors.Is(executorErr, sentinel)))
			}
		}
		return CanaryMismatch{}, false
	}
	if !bytes.Equal(legacyBytes, executorBytes) {
		return mismatch("bytes", renderCanaryBytes(legacyBytes), renderCanaryBytes(executorBytes))
	}
	return CanaryMismatch{}, false
}

// canaryMismatchPrefix bounds how much of a diverging document is retained. The
// record is a pointer to an investigation, not the evidence itself.
const canaryMismatchPrefix = 200

func renderCanaryBytes(b []byte) string {
	if len(b) <= canaryMismatchPrefix {
		return fmt.Sprintf("%d bytes: %s", len(b), b)
	}
	return fmt.Sprintf("%d bytes: %s…", len(b), b[:canaryMismatchPrefix])
}
