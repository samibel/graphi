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
//	         SHIPPED DEFAULT: AX-06 shipped it, SW-228 withdrew it, SW-244
//	         restored it once the record became readable — see the three dated
//	         sections below, in that order.
//	active   Only the executor runs, and its result is what the caller receives.
//
// Rolling back is a value change and nothing else: no schema, no persisted
// state, no cached artifact and no wire identifier is keyed on the position, so
// `active` → `legacy` is complete the moment the next call starts (AC-4).
//
// # Why shadow was the shipped default (SUPERSEDED by SW-228)
//
// The argument below is kept because it is still HALF right, and the half that
// is right is what makes the SW-228 correction narrow rather than a reversal:
// `active` is still forbidden as a default for exactly this reason. What it got
// wrong was assuming the recording it pays for is retrievable.
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
//
// ---------------------------------------------------------------------------
//
// # SW-228 (AX-08) — what this story changed, and why the default moved
//
// AX-08 widens the seam from one operation to ten (see migratedOperations
// below). The mechanism is unchanged: same three positions, same dual-run
// comparison, same recorder. What changed is that the fitness argument above is
// no longer prose — migrationCriteria checks the five criteria against every
// migrated operation's SPEC, and surfaces/client/migration_test.go adds the one
// criterion AX-06 could only assert, that the fixture's answer actually MOVES
// when the arguments move.
//
// The kill switch became PER OPERATION, which is what its environment variable
// always claimed to be: GRAPHI_CANARY_DEAD_CODE now means the dead_code
// position and nothing else, and each migrated operation has the same variable
// under its own name. A single global position across ten operations would have
// meant that rolling one back rolled back nine that were fine.
//
// # Why the shipped default became `legacy` (SUPERSEDED by SW-244)
//
// AX-06 shipped `shadow` as the default and gave a good reason: a canary that
// never flies teaches nothing. The reason did not survive contact with the
// recorder's scope. A shadow divergence is recorded in a PROCESS-GLOBAL
// counter that is never persisted and never read outside the test binary — and
// the two processes that dispatch through this seam are long-running servers
// (`graphi mcp`, `graphi serve`), while every local diagnostic that could show
// an operator the counter (`graphi doctor`, `graphi status`) is a DIFFERENT,
// short-lived process. A doctor check reading it would print zero for a server
// that had diverged on every call. So `shadow` was paying 1.88x latency and
// +70 allocations per call for evidence that, on a live system, nobody could
// retrieve — and AX-08 would have multiplied that by ten.
//
// The evidence that actually gates activation is the fixture parity suite: it
// runs per operation, in CI, compares bytes AND error class, and can be read.
// Production shadow was always the secondary signal, so the position that
// costs nothing is the right default and `shadow` is now something an operator
// turns ON while investigating — where the in-process counter is exactly the
// right scope for the question they are asking. `graphi doctor` reports which
// position each operation is in, so the switch is auditable without being paid
// for. (Backlog entry "PRECONDITION FOR SW-228", 2026-08-27.)
//
// ---------------------------------------------------------------------------
//
// # SW-232 (AX-12a) — the record became durable, the default did not move
//
// The paragraph above is the diagnosis; this story is half the cure. The
// process-global counter is still here and still has exactly the scope it
// always had, but every dual-run outcome — agreement as well as divergence — is
// now ALSO handed to an installed DivergenceRecorder, which the composition
// root backs with a per-process segment file under the graphi state directory.
// A restart no longer erases the evidence, and `graphi doctor -divergence`
// reads it without starting a server.
//
// Two things this story deliberately did NOT do. The kill-switch default stays
// `legacy`: making shadow the default is a release decision with a latency
// price, taken separately. And nothing about the legacy dispatch path changed —
// the recorder is consulted only after a comparison has already happened, which
// on the shipped position never occurs.
//
// ---------------------------------------------------------------------------
//
// # SW-244 (AX-12b) — why the shipped default is `shadow` again
//
// This is the release decision SW-232 left open, and it is a one-constant
// change: canaryModeDefault moves from CanaryModeLegacy to CanaryModeShadow.
// Nothing else about the seam moves — same three positions, same per-operation
// switch, same comparison, same recorder, same closed migratedOperations set,
// and no Stable operation joins it.
//
// SW-228's objection is answered rather than overruled. Its objection was not
// "shadow is expensive", it was "shadow is expensive AND its evidence is
// unretrievable", and TestCanary_ShippedDefaultIsLegacy wrote the condition for
// reversing it into the test itself: moving back to `shadow` is legitimate
// "only together with a way to READ a divergence outside the test binary". That
// way exists as of SW-232 — the durable segment file under the state directory,
// and `graphi doctor -divergence [--json]`, which reads it without starting a
// server. The condition is met, so the position that pays for evidence is worth
// paying for again.
//
// What forced the timing is that the evidence gate for migrating STABLE
// operations (SW-238) requires "at least one release line with the shadow
// catalog live", and with `legacy` compiled in that precondition was not merely
// unmet but unreachable: `graphi doctor` reports 10 legacy / 0 shadow / 0 active
// on tip, so a release cut today would accrue exactly zero shadow evidence, and
// so would the next one. Nothing else in the backlog moves that row.
//
// The price is stated as a measurement, not as an adjective. Under the AX-06
// method recalibrated by SW-242 — same-run A/A control, median AND tail — the
// shadow arm's cost over the legacy baseline is recorded in
// docs/rc/ax06-canary-latency.md §6, and TestSW244_ShadowDefaultCostIsAccounted
// checks the part of that cost which is NOT explained by "both paths ran". Two
// things follow deliberately from how that check is written. It reuses the AX-06
// bar exactly as SW-242 fixed it — the same 10 %/250 µs fixed term, the same
// 3x same-run noise term, the same 4x ceiling — because a story that introduces
// a cost does not get to choose the budget that judges it. And it judges the
// UNACCOUNTED part only: ~2x legacy is shadow's correct behaviour by
// construction (§3), so gating shadow's total would have been either vacuous or
// a standing invitation to weaken the comparison the position exists to perform.
//
// What did NOT change, and is the reason this is affordable at all: in `shadow`
// the caller still receives the LEGACY result, byte for byte. The executor's
// answer is compared and recorded and is never returned. So the default flip
// moves latency and allocation, and moves no bytes — Stable wire names, request
// schemas, canonical result bytes, error codes and the default MCP profile are
// untouched, which is why the AX-00 goldens are unmodified by this story.
//
// Rolling it back is unchanged and still free: GRAPHI_CANARY_ALL=legacy for the
// whole seam, GRAPHI_CANARY_<OP>=legacy for one operation, and the round trip
// (unset everything) now returns to `shadow` rather than to `legacy` — which is
// the one operator-visible change docs/executor-seam-rollback.md had to be
// corrected for, since a page that names the wrong starting position is worse
// than no page during an incident.

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

// CanaryOperation is the operation AX-06 moved onto the executor path first.
// It is no longer the whole allow list — see migratedOperations — but it stays
// named because the AX-06 evidence, the latency gate and the AX-10 worked
// example are all written against this one operation.
const CanaryOperation = "dead_code"

// migratedOperations is the closed set of operation ids whose SURFACE DISPATCH
// reaches the executor. DispatchOperation rejects anything else by name.
//
// It is a list and not a predicate on purpose. "Everything Labs, deterministic
// and read-only" would have quietly enrolled every future operation that
// happened to match, including ones with no argument-fidelity evidence on the
// fixture — the exact failure mode migration_test.go exists to catch. Adding an
// id here is a deliberate act that has to bring its own parity case and its own
// fidelity pair, or the build fails.
//
// The set is the SIMPLE READ-ONLY class the plan puts first in its risk order:
// one Client method, a small argument struct, tier labs, determinism
// "deterministic", permissions {graph.read}. Deliberately absent, each for a
// reason recorded in TestAX08_ExcludedOperationsAreRejectedByName:
//
//   - every STABLE operation (the ten structural queries, search, impact,
//     explain_symbol, related_files, change_risk, agent_brief) — AX-12 owns
//     Stable migration, and it is gated on release evidence this story does not
//     have;
//   - the five LABS structural queries (implementers, implements, overrides,
//     subtypes, supertypes) — they are fit on every catalog criterion, but they
//     share ONE dispatch arm with the five Stable structural queries on both
//     surfaces (the MCP tools/call fallthrough, HTTP's /query/{op}). Migrating
//     them means putting a branch inside the Stable dispatch path to sort the
//     two apart, which is a change to Stable dispatch made for a Labs reason.
//     They are the natural first batch for AX-12, when that path is being
//     opened anyway;
//   - memory and search_semantic — no argument-fidelity evidence on the AX-04
//     fixture (backlog, SW-226 review);
//   - agent_brief — Client.Brief returns two byte slices and the executor
//     transports one; migrating it would silently drop the Markdown the MCP
//     surface concatenates (BriefArgs in executor_adapters.go);
//   - analyze, savings, hotspots, symbol_context, task_context, change_impact,
//     strict_query, graph_health — determinism "environment-dependent". A
//     dual-run whose two halves may legitimately disagree cannot prove parity;
//   - distill, skillgen — the catalog declares NO ports for them, so criterion
//     one (a migrated operation states what it needs) fails;
//   - everything in the edit/apply and forge families — write paths and
//     network-adjacent behaviour, both out of scope by ADR 0013 I3.
var migratedOperations = []string{
	"architecture",
	"architecture_violations",
	"compound",
	"dead_code",
	"find_clones",
	"framework_map",
	"repo_overview",
	"search_ast",
	"search_hybrid",
	"test_impact",
}

// migratedSet is migratedOperations as a lookup, built once.
var migratedSet = func() map[string]bool {
	set := make(map[string]bool, len(migratedOperations))
	for _, id := range migratedOperations {
		set[id] = true
	}
	return set
}()

// MigratedOperations returns the ids that dispatch through the executor, in
// canonical order. Surfaces, `graphi doctor` and the runtime's kill-switch
// wiring all enumerate the set from here rather than re-listing it.
func MigratedOperations() []string {
	return append([]string(nil), migratedOperations...)
}

// isMigratedOperation reports whether op dispatches through the executor.
func isMigratedOperation(op string) bool { return migratedSet[op] }

// IsMigratedOperation is isMigratedOperation for callers outside this package
// — the surfaces' generic dispatch branch, which must not route an operation
// this package would refuse.
func IsMigratedOperation(op string) bool { return isMigratedOperation(op) }

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
//
// SW-228 moved it from `shadow` to `legacy` because shadow's evidence was
// recorded in a process-local counter no operator could read on a live server.
// SW-232 removed that reason by persisting the record, and SW-244 moves it back
// to `shadow` — see the package comment above ("Why the shipped default is
// `shadow` again"). The short form: the condition SW-228's own test wrote down
// ("only together with a way to READ a divergence outside the test binary") is
// met, and the dual-run cost is now measured under the recalibrated AX-06
// method rather than assumed.
//
// It applies to the migrated operations and to those only: CanaryModeFor
// short-circuits to `legacy` for every id outside migratedOperations, so a
// non-migrated operation is untouched by this constant.
const canaryModeDefault = CanaryModeShadow

// canaryModeSelected holds the positions installed by the composition root
// (cmd/internal/runtime, from the GRAPHI_CANARY_* environment). It stores an
// IMMUTABLE map, replaced wholesale on each write, so reads stay lock-free on
// the dispatch path while startup installs overrides that may race with
// requests already in flight on other goroutines. A plain map behind a mutex
// would put a lock in front of every call to buy nothing: the map is written
// only at startup and in tests.
var canaryModeSelected atomic.Value // map[string]CanaryMode

// canaryModeDefaultSelected holds an override for the position used by any
// operation with no per-operation override of its own.
var canaryModeDefaultSelected atomic.Value // CanaryMode

// canaryModeWriteMu serialises the read-modify-write of the override map. The
// map itself is never mutated after publication.
var canaryModeWriteMu sync.Mutex

// CanaryModeDefault returns the position used by any migrated operation that
// has no override of its own.
func CanaryModeDefault() CanaryMode {
	if m, ok := canaryModeDefaultSelected.Load().(CanaryMode); ok && m.Valid() {
		return m
	}
	return canaryModeDefault
}

// CanaryModeFor returns the position dispatch will use for one operation.
//
// It answers for ANY id, including one that is not migrated: a caller asking
// "what would happen to this operation" gets `legacy`, which is the truth —
// a non-migrated operation is served by its legacy method and nothing else.
func CanaryModeFor(operation string) CanaryMode {
	if !isMigratedOperation(operation) {
		return CanaryModeLegacy
	}
	if modes, ok := canaryModeSelected.Load().(map[string]CanaryMode); ok {
		if m, set := modes[operation]; set && m.Valid() {
			return m
		}
	}
	return CanaryModeDefault()
}

// SetCanaryModeDefault installs the position for every migrated operation that
// has no override of its own. An unrecognised position is REJECTED rather than
// falling back to a default: a typo in an operator's environment must not
// quietly select a behaviour they did not ask for.
func SetCanaryModeDefault(m CanaryMode) error {
	if !m.Valid() {
		return canaryModeError(string(m))
	}
	canaryModeDefaultSelected.Store(m)
	return nil
}

// SetCanaryModeFor installs the position for ONE operation.
//
// An operation that does not dispatch through the executor is rejected rather
// than accepted-and-ignored. Accepting it would let an operator set a switch
// that does nothing and believe they had changed something — the same failure
// as accepting a misspelled position.
func SetCanaryModeFor(operation string, m CanaryMode) error {
	if !isMigratedOperation(operation) {
		return registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "SetMode", operation,
			"%s: %q does not dispatch through the executor, so it has no kill switch to set "+
				"(migrated: %v)", canaryRegistry, operation, migratedOperations)
	}
	if !m.Valid() {
		return canaryModeError(string(m))
	}
	canaryModeWriteMu.Lock()
	defer canaryModeWriteMu.Unlock()
	next := make(map[string]CanaryMode, len(migratedOperations))
	if current, ok := canaryModeSelected.Load().(map[string]CanaryMode); ok {
		for id, mode := range current {
			next[id] = mode
		}
	}
	next[operation] = m
	canaryModeSelected.Store(next)
	return nil
}

// ResetCanaryModes drops every installed override, returning the process to the
// compiled-in default. It exists for tests, which need a clean starting point,
// and for the runtime's own re-application path.
func ResetCanaryModes() {
	canaryModeWriteMu.Lock()
	defer canaryModeWriteMu.Unlock()
	canaryModeSelected.Store(map[string]CanaryMode{})
	// An INVALID value, not the compiled-in default: atomic.Value cannot store
	// nil, and storing the default would make a reset indistinguishable from an
	// operator who explicitly asked for the default. The readout reports the
	// source of a position, so that distinction has to survive.
	canaryModeDefaultSelected.Store(CanaryMode(""))
}

// CanaryPosition is one operation's kill-switch position, for a diagnostic
// readout. It carries the source so a reader can tell an explicit override from
// the compiled-in default without re-deriving the precedence rule.
type CanaryPosition struct {
	// Operation is the catalog id.
	Operation string
	// Mode is the position dispatch will use.
	Mode CanaryMode
	// Overridden is true when an explicit per-operation or default override
	// selected this position, false when it is the compiled-in default.
	Overridden bool
}

// CanaryPositions returns every migrated operation's position in canonical
// order. It is the readout `graphi doctor` renders: the seam's configuration is
// local, static and free to read, which is the part of the canary an operator
// can actually act on.
func CanaryPositions() []CanaryPosition {
	overrides, _ := canaryModeSelected.Load().(map[string]CanaryMode)
	installed, ok := canaryModeDefaultSelected.Load().(CanaryMode)
	defaultOverridden := ok && installed.Valid()
	out := make([]CanaryPosition, 0, len(migratedOperations))
	for _, id := range migratedOperations {
		mode, overridden := overrides[id]
		if !overridden || !mode.Valid() {
			mode, overridden = CanaryModeDefault(), defaultOverridden
		}
		out = append(out, CanaryPosition{Operation: id, Mode: mode, Overridden: overridden})
	}
	return out
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
	return OperationContribution(c, CanaryOperation)
}

// OperationContribution resolves ONE migrated operation's contribution against
// the frozen shadow catalog and the executor's adapter table (SW-228). It is
// the generalisation of CanaryContribution: the five conditions below are not
// facts about dead_code, they are the criteria a migrated operation has to meet,
// and every one of the ten is checked against them by
// TestAX08_EveryMigratedOperationMeetsTheCriteria.
func OperationContribution(c Client, operation string) (Contribution, error) {
	catalog, err := opcatalog.Shadow()
	if err != nil {
		return Contribution{}, fmt.Errorf("client: canary needs the operation catalog: %w", err)
	}
	return contributionFor(c, catalog, operation)
}

// canaryContribution is CanaryContribution over an explicit catalog. It exists
// so canary_test.go can feed it a catalog that violates each of the five
// conditions in turn — a check that has never been shown to fail is not a check.
func canaryContribution(c Client, catalog *opcatalog.Catalog) (Contribution, error) {
	return contributionFor(c, catalog, CanaryOperation)
}

// contributionFor is the contribution resolver over an explicit catalog and an
// explicit operation.
func contributionFor(c Client, catalog *opcatalog.Catalog, operation string) (Contribution, error) {
	spec, ok := catalog.Lookup(operation)
	if !ok {
		return Contribution{}, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Contribution", operation,
			"%s: the operation catalog does not declare %q", canaryRegistry, operation)
	}
	executor, err := NewExecutorWithCatalog(c, catalog)
	if err != nil {
		return Contribution{}, err
	}
	if _, handled := executor.adapters[operation]; !handled {
		return Contribution{}, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Contribution", operation,
			"%s: %q has a catalog spec but no executor handler", canaryRegistry, operation)
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

// DivergenceRecorder receives every dual-run OBSERVATION the seam makes — the
// agreements as well as the disagreements — so the record can distinguish "the
// two paths were compared and matched" from "nothing was ever compared".
//
// # Why an interface, and why primitives
//
// The durable record lives in internal/divergence, which this package does not
// import. It cannot: internal/divergence is where the state directory, the
// segment layout and the file writing live, and surfaces/client is a surface —
// it composes capabilities, it does not own on-disk state. The composition root
// (cmd/internal/runtime) imports both and installs one into the other, exactly
// as it already does for the kill-switch positions and for the doctor readout.
//
// The method takes primitives rather than a CanaryMismatch for the same reason:
// with a struct in the signature, internal/divergence would have to import this
// package to implement the interface, and the dependency this design avoids
// would reappear pointing the other way.
//
// # Why observations and not only mismatches
//
// SW-232 AC-3. A record that only counted mismatches would read zero both when
// the paths agreed on ten thousand calls and when the seam had never run at
// all, and a reader cannot tell those apart. Counting observations is what lets
// the read path say UNKNOWN honestly.
type DivergenceRecorder interface {
	// RecordDivergence records one dual-run comparison. mismatch reports
	// whether the two paths disagreed; kind, legacy and executor describe the
	// disagreement and are empty when there was none.
	RecordDivergence(operation string, mismatch bool, kind, legacy, executor string)
}

// divergenceRecorder holds the installed recorder, or a nil one. It is an
// atomic.Value read on the shadow path only: `legacy`, the shipped position,
// returns before it is ever consulted, so the default dispatch cost is
// unchanged (SW-232 AC-6).
var divergenceRecorder atomic.Value // holds divergenceRecorderHolder

// divergenceRecorderHolder makes "no recorder installed" storable in an
// atomic.Value, which rejects a nil interface value.
type divergenceRecorderHolder struct{ recorder DivergenceRecorder }

// SetDivergenceRecorder installs the durable recorder. Passing nil uninstalls
// it, which is what tests do to keep their observations out of the real state
// directory.
//
// The default is UNINSTALLED. A library embedding surfaces/client keeps today's
// behaviour — an in-process counter and no file I/O — and only a graphi process
// that went through the composition root persists anything.
func SetDivergenceRecorder(r DivergenceRecorder) {
	divergenceRecorder.Store(divergenceRecorderHolder{recorder: r})
}

// installedDivergenceRecorder returns the recorder, or nil.
func installedDivergenceRecorder() DivergenceRecorder {
	holder, ok := divergenceRecorder.Load().(divergenceRecorderHolder)
	if !ok {
		return nil
	}
	return holder.recorder
}

// observeCanary is the single place a dual-run outcome is recorded. It keeps
// the in-process counter the AX-06 fixture suite asserts on AND feeds the
// durable record, so the two can never disagree about what happened.
func observeCanary(operation string, mismatch CanaryMismatch, differs bool) {
	if differs {
		recordCanaryMismatch(mismatch)
	}
	if recorder := installedDivergenceRecorder(); recorder != nil {
		recorder.RecordDivergence(operation, differs, mismatch.Kind, mismatch.Legacy, mismatch.Executor)
	}
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

// DispatchOperation runs ONE migrated operation in whichever position that
// operation's kill switch selects, and returns what the CALLER should receive.
//
// It is the single composition for migrated dispatch, in the surfaces/client
// package where graphi puts a capability that two surfaces share, so MCP and
// HTTP cannot end up in different kill-switch positions or comparing different
// things (standards: one composition per capability, not one per surface).
//
// args must belong to a MIGRATED operation. Anything else is rejected by name
// rather than silently executed: the migrated set is closed (migratedOperations)
// and letting an unlisted operation through would migrate it without any of the
// evidence a migration owes — the exact accident AX-06's one-operation guard was
// written to prevent, now generalised instead of dropped.
func DispatchOperation(ctx context.Context, c Client, args Arguments) ([]byte, error) {
	if args == nil {
		return nil, fmt.Errorf("client: canary: nil arguments")
	}
	operation := args.Operation()
	if !isMigratedOperation(operation) {
		return nil, registry.Errorf(registry.ErrMissingDependency, canaryRegistry, "Dispatch", operation,
			"%s: %q does not dispatch through the executor (migrated: %v); migrating it needs "+
				"its own catalog-criteria, byte-parity and argument-fidelity evidence",
			canaryRegistry, operation, migratedOperations)
	}

	mode := CanaryModeFor(operation)
	if mode == CanaryModeLegacy {
		// Byte-for-byte the pre-AX-06 call. invoke IS the legacy Client method
		// with no wrapping, so this position adds nothing at all — not even an
		// error check — to the path it replaces. It is the shipped default
		// (SW-228), which is why this early return is the hot path and why the
		// mode lookup above must stay lock-free.
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
		observeCanary(operation, CanaryMismatch{
			Operation: operation,
			Kind:      "executor-unavailable",
			Legacy:    "(ran)",
			Executor:  err.Error(),
		}, true)
		return args.invoke(ctx, c)
	}

	if mode == CanaryModeActive {
		return executeCanary(ctx, executor, args)
	}

	// Shadow: legacy first, so the value the caller receives is decided before
	// the experiment runs and cannot be influenced by it.
	legacyBytes, legacyErr := args.invoke(ctx, c)
	executorBytes, executorErr := executeCanary(ctx, executor, args)
	mismatch, differs := compareCanaryOutcomes(operation, legacyBytes, legacyErr, executorBytes, executorErr)
	observeCanary(operation, mismatch, differs)
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
func compareCanaryOutcomes(operation string, legacyBytes []byte, legacyErr error, executorBytes []byte, executorErr error) (CanaryMismatch, bool) {
	mismatch := func(kind, legacy, executor string) (CanaryMismatch, bool) {
		return CanaryMismatch{Operation: operation, Kind: kind, Legacy: legacy, Executor: executor}, true
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
