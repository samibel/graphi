package client

// This file is the SHARED trust-report composition (P1 trust surface): the ONE
// place the contract §2 `graphi trust-report --json` document is assembled and
// canonically serialized, following the explain_symbol template — engine
// serialization conventions -> client.Client method -> Direct canonical bytes —
// so the CLI and MCP emit byte-identical documents by construction.
//
// Governing contracts: docs/plan/2026-08-graphi-p1-trust-contract-v1.md (§1
// terminology, §2 wire shape + contract rules, §4 error model) and
// docs/adr/0006-status-vs-trust-separation.md (trust consumes the shared
// freshness facts and mints no freshness prose or rebuild recommendation of
// its own; snapshot state is a pure derivation; the reader is a strict
// observer — read-only stores, no state-directory creation, no repair).
//
// The composition lives at the surface rank on purpose: the freshness probe
// (internal/freshness/probe) imports engine/ingest, so engine/trust cannot
// perform this composition itself without a cycle — the trust core stays pure
// and the surface rank wires probe + store + trust together.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/samibel/graphi/core/graphstore"
	"github.com/samibel/graphi/core/parse"
	"github.com/samibel/graphi/engine/ingest"
	"github.com/samibel/graphi/engine/link"
	"github.com/samibel/graphi/engine/semantic"
	"github.com/samibel/graphi/engine/trust"
	"github.com/samibel/graphi/internal/freshness"
	"github.com/samibel/graphi/internal/freshness/probe"
	"github.com/samibel/graphi/internal/releaseinfo"
	"github.com/samibel/graphi/internal/state"
)

// trustReportJSONSchemaVersion versions the `graphi trust-report --json`
// document (contract doc §2.4, following the statusJSONSchemaVersion
// convention). Bump only on breaking changes to the shape or value domain; it
// is the single source of the wire field `schema_version`.
const trustReportJSONSchemaVersion = 1

// liveGenerationKey is the graph's full-pass generation nonce in kv_meta — the
// binding DeriveState checks the snapshot against (engine/ingest stamps it;
// engine/trust/state.go documents the equality).
const liveGenerationKey = "index.full_ingest_generation"

// trustReportDoc is the contract §2 wire document. The first twelve properties
// mirror the frozen §2.2 field register verbatim, at the register's nesting and
// order; `findings`, `checks_passed`, `details`, and `scope_evidence` are
// additive v1 fields (contract doc §2.3 rule 7): findings carry the explaining
// policy/resolver observations (PRD §26 — no verdict without findings or an
// explicit all-checks-passed list, which checks_passed provides), details
// carries the bounded evidence samples emitted only on explicit request
// (rule 8), and scope_evidence carries the target scope's persisted evidence
// row exactly as the policy judged it — always present, zero-valued with
// available=false when no usable row backs the scope (fail closed: absence is
// visible, never dressed up as clean facts). Every property is always present;
// empty slices encode as [], never null (rules 1–2); all lists are canonically
// sorted before serialization (rules 4–5); paths are normalized and
// repo-relative by snapshot construction (rule 9).
type trustReportDoc struct {
	SchemaVersion   int                   `json:"schema_version"`
	SnapshotVersion string                `json:"snapshot_version"`
	SnapshotState   trust.State           `json:"snapshot_state"`
	GraphGeneration trustReportGeneration `json:"graph_generation"`
	Freshness       trustReportFreshness  `json:"freshness"`
	Scope           trustReportScope      `json:"scope"`
	Coverage        trustReportCoverage   `json:"coverage"`
	EdgeEvidence    trust.TierCounts      `json:"edge_evidence"`
	Resolution      trustReportResolution `json:"resolution"`
	Boundaries      []trustReportBoundary `json:"boundaries"`
	Policy          trustReportPolicy     `json:"policy"`
	Limitations     []trust.Limitation    `json:"limitations"`
	Findings        []trust.Finding       `json:"findings"`
	ChecksPassed    []string              `json:"checks_passed"`
	Details         trustReportDetails    `json:"details"`
	ScopeEvidence   trust.ScopeFacts      `json:"scope_evidence"`
	Capabilities    []trust.Capability    `json:"capabilities"`
	// Abstention is the W0.g legible-abstention roll-up: what the semantic
	// binders REFUSED to bind, under which named reason, for this generation.
	// It sits beside capabilities and boundaries on purpose — capabilities say
	// what a language CAN express here, boundaries what the graph does not
	// reach, and abstention what the binder declined to claim within its own
	// reach. Additive v1 field (contract §2.3 rule 7); always present, and
	// fail-closed via its own Available flag.
	Abstention AbstentionFacts `json:"abstention"`
}

// trustReportGeneration is the §2.2 graph_generation object. Every value is a
// fact the caller already holds (ADR 0006: nothing is measured twice): id is
// the live full-pass generation nonce, source_commit/profile come from the
// shared freshness facts (the sync stamp and index.profile), and binary_commit
// is the running binary's VCS stamp ("" for unstamped dev builds).
type trustReportGeneration struct {
	ID           string `json:"id"`
	SourceCommit string `json:"source_commit"`
	Profile      string `json:"profile"`
	BinaryCommit string `json:"binary_commit"`
}

// trustReportFreshness is the §2.2 freshness object: the shared probe's facts
// verbatim — counts and currency only, never freshness prose (ADR 0006 D1).
type trustReportFreshness struct {
	Current bool             `json:"current"`
	Drift   trustReportDrift `json:"drift"`
}

type trustReportDrift struct {
	Added   int `json:"added"`
	Changed int `json:"changed"`
	Removed int `json:"removed"`
}

// trustReportScope is the §2.2 scope object: the closed §1.7 kind plus the
// resolved identity string (empty for the repository default and for every
// unresolved target — an unresolved scope stays visibly empty, PRD §27).
type trustReportScope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// trustReportCoverage is the §2.2 coverage object. files_indexed is the shared
// probe's cached-file count, files_skipped the snapshot's parse-skip count, and
// files_discovered their sum — a pure derivation of already-collected facts
// (discovered = indexed + skipped), never a second walk.
type trustReportCoverage struct {
	FilesDiscovered  int `json:"files_discovered"`
	FilesIndexed     int `json:"files_indexed"`
	FilesSkipped     int `json:"files_skipped"`
	PackagesTotal    int `json:"packages_total"`
	PackagesDegraded int `json:"packages_degraded"`
}

// trustReportResolution is the §2.2 resolution object, filled from the
// snapshot's linker and type-resolution facts.
type trustReportResolution struct {
	ResolvedExternal int `json:"resolved_external"`
	Skipped          int `json:"skipped"`
	Ambiguous        int `json:"ambiguous"`
	DroppedIntents   int `json:"dropped_intents"`
}

// trustReportBoundary is one §2.2 boundaries[] entry ({code, severity, count};
// the §2.5 minimum vocabulary).
type trustReportBoundary struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
}

// trustReportPolicy is the §2.2 policy object. When no policy is requested it
// is present with zero values, never omitted (§2.3 presence clarification).
//
// ID is the canonical versioned identifier PRD v1.0 §6 fixes ("review-v1") and
// the token --policy accepts; Name and Version are its decomposition. All three
// come from one PolicyRef, so they cannot disagree (delta doc §A2). The field
// is additive within schema_version 1 (contract §2.3 rule 7).
type trustReportPolicy struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Version int           `json:"version"`
	Verdict trust.Verdict `json:"verdict"`
}

// trustReportDetails carries the snapshot's bounded evidence samples. The
// object is always present (§2.3 rule 1 discipline for additive fields); its
// lists are filled only when details were explicitly requested (rule 8), and
// capped by the caller's limit when limit > 0. The samples are already bounded
// and repo-relative at snapshot construction (trust.MaxParsePaths /
// trust.MaxDegradedUnits; absolute paths are dropped, never leaked).
type trustReportDetails struct {
	ParsePaths    []string             `json:"parse_paths"`
	DegradedUnits []trust.DegradedUnit `json:"degraded_units"`
	TopBoundaries []trust.Boundary     `json:"top_boundaries"`
}

// trustFacts bundles the store-derived inputs of one composition. scopeFacts
// is the target scope's persisted evidence row (zero — absent, fail closed —
// whenever no usable row backs the scope under the snapshot generation).
type trustFacts struct {
	snap       trust.Snapshot
	state      trust.State
	scope      trust.ScopeRef
	resolution []trust.Finding
	scopeFacts trust.ScopeFacts
	generation string
}

// composeTrustReport is the shared composition: probe -> read-only store ->
// trust read path -> optional scope/policy -> canonical bytes. It returns the
// canonical document bytes plus the policy verdict (zero Verdict when no
// policy was requested) and the derived snapshot state, so the CLI maps its
// exit codes without re-parsing JSON. A non-nil error is operational (CLI
// exit 2, typed MCP tool error): an unknown policy wraps
// trust.ErrPolicyUnknown, a store without selective lookups wraps
// trust.ErrSelectiveLookupUnavailable, everything else is a probe/store
// failure. A missing store is NOT an error: it composes the fail-closed
// UNAVAILABLE document (contract doc §1.6; ADR 0006 — the failure direction is
// "no answer", never "wrong answer").
func composeTrustReport(ctx context.Context, opts TrustReportOptions) ([]byte, trust.Verdict, trust.State, error) {
	// Resolve the policy first: an unknown name is an input error regardless
	// of graph state, and failing before any I/O keeps the outcome identical
	// across indexed and unindexed repositories.
	var pol trust.Policy
	policyRequested := opts.Policy != ""
	if policyRequested {
		p, err := trust.PolicyByID(opts.Policy)
		if err != nil {
			return nil, "", "", err
		}
		pol = p
	}

	f, err := probe.Compute(ctx, opts.Root, opts.DBPath, opts.MetaDir)
	if err != nil {
		return nil, "", "", fmt.Errorf("client: trust report freshness probe: %w", err)
	}

	facts, err := readTrustFacts(ctx, f, opts)
	if err != nil {
		return nil, "", "", err
	}

	// The resolver findings are ALWAYS forwarded into the evaluation — never
	// dropped, never rebuilt from the scope shape alone. Dropping them would
	// launder an ambiguous or not-found target into a clean-looking scope
	// (the false-pass hole the policy red-gate tests pin).
	verdict := trust.Verdict("")
	policyRef := trustReportPolicy{}
	findings := make([]trust.Finding, len(facts.resolution))
	copy(findings, facts.resolution)
	trust.SortFindings(findings)
	checksPassed := []string{}
	if policyRequested {
		a := pol.EvaluateWithScopeFacts(facts.snap, facts.state, facts.scope, facts.scopeFacts, facts.resolution...)
		verdict = a.Verdict
		policyRef = trustReportPolicy{ID: a.Policy.ID, Name: a.Policy.Name, Version: a.Policy.Version, Verdict: a.Verdict}
		findings = a.Findings // canonical order; adopts the resolver findings verbatim
		checksPassed = a.ChecksPassed
	}

	// One limitation source: the same snapshot-derived builder the assessment
	// attaches (Assessment.Limitations == LimitationsFromSnapshot(snap) by
	// construction), split by the §2.5 boundary vocabulary into the two wire
	// lists. The builder's canonical order (severity rank, then code) is
	// preserved through the split.
	boundaries, limitations := splitBoundaries(trust.LimitationsFromSnapshot(facts.snap))

	doc := trustReportDoc{
		SchemaVersion:   trustReportJSONSchemaVersion,
		SnapshotVersion: trust.SnapshotVersion,
		SnapshotState:   facts.state,
		GraphGeneration: trustReportGeneration{
			ID:           facts.generation,
			SourceCommit: f.LastSync.Commit,
			Profile:      f.Profile,
			BinaryCommit: releaseinfo.New().Commit(),
		},
		Freshness: trustReportFreshness{
			Current: f.Current,
			Drift:   trustReportDrift{Added: f.Drift.Added, Changed: f.Drift.Changed, Removed: f.Drift.Removed},
		},
		Scope: trustReportScope{Kind: facts.scope.Kind, ID: scopeWireID(facts.scope)},
		Coverage: trustReportCoverage{
			FilesDiscovered:  f.Index.FilesCached + facts.snap.Parse.Skipped,
			FilesIndexed:     f.Index.FilesCached,
			FilesSkipped:     facts.snap.Parse.Skipped,
			PackagesTotal:    facts.snap.TypeResolution.UnitsTotal,
			PackagesDegraded: facts.snap.TypeResolution.UnitsDegraded,
		},
		EdgeEvidence: facts.snap.Graph.EdgesByTier,
		Resolution: trustReportResolution{
			ResolvedExternal: facts.snap.Link.ResolvedExternal,
			Skipped:          facts.snap.Link.Skipped,
			Ambiguous:        facts.snap.Link.Ambiguous,
			DroppedIntents:   facts.snap.TypeResolution.DroppedIntents,
		},
		Boundaries:    boundaries,
		Policy:        policyRef,
		Limitations:   limitations,
		Findings:      findings,
		ChecksPassed:  checksPassed,
		Details:       detailsBlock(facts.snap, opts),
		ScopeEvidence: facts.scopeFacts,
		Capabilities:  languageCapabilities(),
		Abstention:    readAbstentionFacts(ctx, opts.Root, opts.DBPath, opts.MetaDir),
	}
	b, err := encodeTrustReport(doc)
	if err != nil {
		return nil, "", "", err
	}
	return b, verdict, facts.state, nil
}

// languageCapabilities derives the P1 capability matrix (PRD v1.0 §3) from the
// three live registries, each consulted in the package that owns its fact:
// engine/semantic declares what the process can type-check, engine/link which
// languages have a cross-file resolver, and core/parse which languages are
// parseable at all and which of those extract symbols.
//
// It lives at the surface rank for the same reason the rest of this
// composition does: engine/trust is the pure domain core and grades the inputs
// (trust.Capabilities) without importing the registries.
//
// Derived per call rather than persisted in the snapshot, deliberately.
// Capability is a property of THIS BINARY, not of the graph generation: a
// snapshot written by a build with fewer grammars would otherwise keep
// advertising that build's capabilities under a newer one. Persisting it would
// also change trust.Snapshot's canonical Encode bytes — which IS the digest
// contract — and force SnapshotSchemaVersion to 2, contradicting the
// schema_version 1 that PRD v1.0 §6 itself fixes. Recorded in delta doc §B1.
//
// The cost is one registry construction per report; both registries are pure
// in-memory wiring with no I/O, and the report is a once-per-invocation
// document.
// LanguageCapabilities is the exported form of languageCapabilities, so the
// GA-language gate (internal/coverage.CheckGALanguages, driven by
// cmd/coverage -check) binds to the SAME derivation the trust report serves
// instead of re-assembling the registries — the gate and the product cannot
// disagree about a language's capability level (WP-J1, ADR 0007).
func LanguageCapabilities() []trust.Capability { return languageCapabilities() }

func languageCapabilities() []trust.Capability {
	registry := parse.NewDefaultRegistry()
	languages := registry.Languages()

	extraction := make(map[string]bool, len(languages))
	for _, lang := range languages {
		p, err := registry.ParserForLang(lang)
		if err != nil {
			continue
		}
		// An undeclared parser stays ABSENT from the map, and
		// trust.Capabilities omits its row rather than assuming a level.
		// core/parse's UndeclaredSymbolCapability guard keeps that case out of
		// shipped binaries; this is the fail-closed handling if it ever slips.
		if extracts, known := parse.ExtractsSymbols(p); known {
			extraction[lang] = extracts
		}
	}

	return trust.Capabilities(trust.CapabilityInputs{
		Languages:        languages,
		TypeChecked:      semantic.Languages(),
		CrossFileLinked:  link.New().Languages(),
		SymbolExtraction: extraction,
	})
}

// readTrustFacts opens the durable store READ-ONLY (the same
// OpenSQLiteReadOnly discipline the freshness probe uses — a pure observer
// never creates or repairs state) and derives snapshot, state, scope, and the
// target scope's evidence row through the existing trust read path. A
// repository with no store fails closed to the UNAVAILABLE facts — a valid
// document, never an error.
func readTrustFacts(ctx context.Context, f freshness.Report, opts TrustReportOptions) (trustFacts, error) {
	facts := trustFacts{
		state:      trust.StateUnavailable,
		scope:      unresolvedScope(opts.Target),
		resolution: []trust.Finding{},
	}
	if !f.Index.Exists {
		return facts, nil
	}
	store, err := graphstore.OpenSQLiteReadOnly(f.DBPath)
	if err != nil {
		// The store vanished between the freshness probe and this read: fail
		// closed to the UNAVAILABLE document, not an operational error.
		return facts, nil
	}
	defer func() { _ = store.Close() }()

	facts.generation, err = liveGeneration(ctx, store)
	if err != nil {
		return trustFacts{}, err
	}
	snap, state, err := trust.Evaluate(ctx, store, f, facts.generation)
	if err != nil {
		return trustFacts{}, fmt.Errorf("client: trust report snapshot: %w", err)
	}
	facts.snap, facts.state = snap, state

	if strings.TrimSpace(opts.Target) != "" {
		// ONE sidecar handle serves both halves of target handling: confirming
		// a package target during resolution, and fetching the resolved
		// scope's evidence row afterwards. Opening it twice would double the
		// I/O and could straddle a concurrent write.
		snapshotGeneration := facts.snap.Generation.FullPassGeneration
		ro := openEvidenceSidecar(store, opts)
		if ro != nil {
			defer func() { _ = ro.Close() }()
		}

		var lookups []trust.PackageLookup
		if ro != nil && snapshotGeneration != "" {
			lookups = append(lookups, &sidecarPackageLookup{ro: ro, generation: snapshotGeneration})
		}
		scope, resolution, err := trust.ResolveScope(ctx, store, opts.Target, lookups...)
		if err != nil {
			return trustFacts{}, fmt.Errorf("client: trust report target: %w", err)
		}
		facts.scope, facts.resolution = scope, resolution
		facts.scopeFacts = scopeEvidenceFacts(ctx, ro, snapshotGeneration, scope)
	}
	return facts, nil
}

// sidecarPackageLookup implements trust.PackageLookup over the ingest evidence
// sidecar. It answers only the question the resolver asks — "does a package
// evidence row exist for this key under the snapshot's generation?" — and
// answers it fail-closed: a missing row, a sidecar predating the evidence
// tables, or any operational failure all read "not a package", because the
// resolver may only use this to CONFIRM a target, never to invent one.
//
// Generation-keyed on purpose: a row from another pass describes a repository
// state this snapshot does not, and confirming a target against stale evidence
// would resolve a package that may no longer exist.
type sidecarPackageLookup struct {
	ro         *ingest.Ingester
	generation string
}

func (l *sidecarPackageLookup) LookupPackage(ctx context.Context, key string) (bool, error) {
	if l == nil || l.ro == nil || l.generation == "" {
		return false, nil
	}
	if _, err := l.ro.PackageEvidence(ctx, l.generation, key); err != nil {
		return false, nil
	}
	return true, nil
}

// openEvidenceSidecar opens the ingest meta sidecar READ-ONLY, or returns nil
// when it cannot be opened at all (no meta dir, a store predating the evidence
// tables, an operational failure). nil is not an error: every caller degrades
// to "no scope evidence", which the policy reports as
// SCOPE_EVIDENCE_UNAVAILABLE — "no answer", never "wrong answer" (ADR 0006).
//
// The freshness probe closed its own observer before returning, so one fresh
// mode=ro open is unavoidable here. When opts.MetaDir is empty the
// auto-managed location is resolved WITHOUT Ensure via state.Resolve, exactly
// as the probe resolves it — same inputs, same path, no second location
// semantics, and nothing is created.
func openEvidenceSidecar(store graphstore.Graphstore, opts TrustReportOptions) *ingest.Ingester {
	metaDir := opts.MetaDir
	if metaDir == "" {
		p, err := state.Resolve(opts.Root)
		if err != nil {
			return nil
		}
		metaDir = p.Meta
	}
	ro, err := ingest.NewReadOnly(store, ingest.NewNotebookParser(parse.NewDefaultRegistry()), metaDir)
	if err != nil {
		return nil
	}
	return ro
}

// scopeEvidenceFacts fetches the resolved target scope's persisted evidence
// row and maps it into the policy input:
//
//   - a file scope reads its own row; a resolved symbol scope reads the owning
//     file's row (PRD §27 scope expansion);
//   - a CONFIRMED package scope reads its own package row — the row that
//     confirmed it during resolution, so this fetch always succeeds for a
//     scope that resolved.
//
// The lookup is generation-checked against the SNAPSHOT generation — the facts
// the policy judges must describe the same pass the snapshot describes; a row
// of any other generation is stale evidence and reads not-found.
//
// EVERY failure fails closed to the absent ScopeFacts (zero value): no sidecar,
// row not found, sidecar predating the evidence tables
// (ingest.ErrTrustEvidenceUnavailable), or an operational sidecar error. The
// policy then fires SCOPE_EVIDENCE_UNAVAILABLE exactly as it did before scope
// facts existed ("no answer", never zero-valued clean-looking facts).
func scopeEvidenceFacts(ctx context.Context, ro *ingest.Ingester, snapshotGeneration string, scope trust.ScopeRef) trust.ScopeFacts {
	if ro == nil || snapshotGeneration == "" {
		return trust.ScopeFacts{}
	}
	if scope.Kind == trust.ScopePackage {
		if scope.ID == "" {
			return trust.ScopeFacts{}
		}
		pe, err := ro.PackageEvidence(ctx, snapshotGeneration, scope.ID)
		if err != nil {
			return trust.ScopeFacts{}
		}
		// Package-only facts: no File claim. The rules gate on the presence of
		// the claim they judge, so the per-file checks stay SILENT rather than
		// passing on evidence that was never consulted.
		return trust.ScopeFacts{
			Available: true,
			Package: trust.ScopePackageFacts{
				State:          pe.State,
				DegradedReason: pe.DegradedReason,
				TypeErrors:     pe.TypeErrors,
				DroppedIntents: pe.DroppedIntents,
				ConfirmedEdges: pe.ConfirmedEdges,
				SkippedFiles:   pe.SkippedFiles,
			},
		}
	}

	path := scopeEvidencePath(scope)
	if path == "" {
		return trust.ScopeFacts{}
	}
	fe, err := ro.FileEvidence(ctx, snapshotGeneration, path)
	if err != nil {
		return trust.ScopeFacts{}
	}
	return trust.ScopeFacts{
		Available: true,
		File: trust.ScopeFileFacts{
			ParseStatus:       fe.ParseStatus,
			ParseReason:       fe.ParseReason,
			ResolvedDerived:   fe.ResolvedDerived,
			ResolvedHeuristic: fe.ResolvedHeuristic,
			ResolvedExternal:  fe.ResolvedExternal,
			Skipped:           fe.Skipped,
			Ambiguous:         fe.Ambiguous,
		},
	}
}

// scopeEvidencePath maps a resolved FILE-backed scope to the repo-relative
// file whose evidence row backs it: a file scope's own path, or a resolved
// symbol scope's owning file. Package scopes are handled by their own branch
// above; everything else — unresolved shapes (empty-ID symbol), result-set —
// has no fetchable row and returns "" (the caller fails closed to absent
// facts).
func scopeEvidencePath(scope trust.ScopeRef) string {
	switch scope.Kind {
	case trust.ScopeFile:
		return scope.Path
	case trust.ScopeSymbol:
		if scope.ID != "" {
			return scope.Path
		}
	}
	return ""
}

// liveGeneration reads the graph's full-pass generation nonce; a store no full
// pass ever certified has none ("" — DeriveState fails it closed to
// UNAVAILABLE). Only a genuine store failure is an error.
func liveGeneration(ctx context.Context, store graphstore.Graphstore) (string, error) {
	gen, err := store.Metadata(ctx, liveGenerationKey)
	if errors.Is(err, graphstore.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("client: trust report live generation: %w", err)
	}
	return gen, nil
}

// unresolvedScope shapes the scope for a target that could not be handed to
// the resolver (no store to resolve against): the repository default for an
// empty target, otherwise the asked shape with an empty ID — visibly
// unresolved (PRD §27), mirroring the resolver's own package-looking rule, and
// WITHOUT minting any resolution finding (there is no evidence to claim
// not-found from; the policy rules fail such a scope closed to UNKNOWN and
// explain it via SCOPE_EVIDENCE_UNAVAILABLE).
func unresolvedScope(target string) trust.ScopeRef {
	target = strings.TrimSpace(target)
	switch {
	case target == "":
		return trust.ScopeRef{Kind: trust.ScopeRepository}
	case strings.Contains(target, "/"):
		return trust.ScopeRef{Kind: trust.ScopePackage, Package: target}
	default:
		return trust.ScopeRef{Kind: trust.ScopeSymbol, Symbol: target}
	}
}

// scopeWireID picks the §2.2 scope.id string: the resolved node ID when one
// exists, else the resolved file path (a file scope carries no NodeId), else
// empty — an unresolved scope stays visibly empty (fail closed, PRD §27).
func scopeWireID(s trust.ScopeRef) string {
	if s.ID != "" {
		return s.ID
	}
	return s.Path
}

// splitBoundaries partitions the snapshot-derived limitation list into the
// §2.2 wire pair: entries whose code belongs to the §2.5 boundary vocabulary
// become boundaries[] ({code, severity, count}); everything else stays a
// limitation ({code, severity, count, action}). Both lists keep the builder's
// canonical order and are never nil.
func splitBoundaries(ls []trust.Limitation) ([]trustReportBoundary, []trust.Limitation) {
	bounds := []trustReportBoundary{}
	rest := []trust.Limitation{}
	for _, l := range ls {
		switch l.Code {
		case trust.LimitationExternalNotNavigable,
			trust.LimitationCrossRepositoryUnavailable,
			trust.LimitationDependencyInternalsUnknown,
			trust.LimitationDynamicRuntimeUnknown:
			bounds = append(bounds, trustReportBoundary{Code: l.Code, Severity: l.Severity, Count: l.Count})
		default:
			rest = append(rest, l)
		}
	}
	return bounds, rest
}

// detailsBlock fills the always-present details object: empty lists unless
// details were explicitly requested, each list capped at limit when limit > 0.
func detailsBlock(snap trust.Snapshot, opts TrustReportOptions) trustReportDetails {
	det := trustReportDetails{
		ParsePaths:    []string{},
		DegradedUnits: []trust.DegradedUnit{},
		TopBoundaries: []trust.Boundary{},
	}
	if !opts.Details {
		return det
	}
	det.ParsePaths = capList(snap.Parse.Paths, opts.Limit)
	det.DegradedUnits = capList(snap.TypeResolution.DegradedUnits, opts.Limit)
	det.TopBoundaries = capList(snap.External.TopBoundaries, opts.Limit)
	return det
}

// capList copies in (never aliasing the snapshot's slices) and caps the copy
// at limit when limit > 0. The result is never nil.
func capList[T any](in []T, limit int) []T {
	out := make([]T, 0, len(in))
	out = append(out, in...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// encodeTrustReport is THE canonical trust-report encoder — one encoder for
// every surface, same byte conventions as the trust core's canonical
// documents (engine/trust/serialize.go): encoding/json with HTML escaping
// disabled, no indentation, trailing newline stripped. Field order follows
// the struct declaration and every list is pre-sorted and non-nil, so
// identical facts always encode to identical bytes.
func encodeTrustReport(doc trustReportDoc) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("client: encode trust report: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// TrustReport runs the shared trust-report composition without a constructed
// client — the CLI's entry point. Direct.TrustReport and the MCP surface ride
// the same function, so every surface emits byte-identical documents for the
// same options (the parity-by-construction seam the contract §2 requires).
func TrustReport(ctx context.Context, opts TrustReportOptions) ([]byte, trust.Verdict, trust.State, error) {
	return composeTrustReport(ctx, opts)
}
